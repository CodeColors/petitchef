// Package shopping est le moteur unique de la liste de courses : besoins
// du menu moins le stock disponible à la date de chaque repas, produits qui
// passeront sous leur minimum avant les courses suivantes, ajouts manuels ;
// fusion par produit, arrondi au conditionnement et tri dans l'ordre des
// rayons du magasin.
package shopping

import (
	"cmp"
	"fmt"
	"slices"

	"github.com/codecolors/petitchef/internal/catalog"
	"github.com/codecolors/petitchef/internal/platform/calendar"
	"github.com/codecolors/petitchef/internal/platform/qty"
	"github.com/codecolors/petitchef/internal/stock"
)

// Source est l'origine d'une ligne (bits).
type Source int

const (
	FromMenu    Source = 1 << iota // besoins du menu
	FromMinimum                    // stock minimum ou consommation prévue
	FromManual                     // ajout manuel
	FromLow                        // produit de base signalé presque vide
)

// Has indique une origine.
func (s Source) Has(b Source) bool { return s&b != 0 }

// MenuNeed est le besoin d'un plat du menu en un produit.
type MenuNeed struct {
	ProductID int64
	Qty       int64
	Date      calendar.Date
	Label     string // « le gratin de mardi »
}

// Manual est un ajout manuel.
type Manual struct {
	ProductID int64
	Qty       int64 // 0 : quantité non précisée
}

// Input est l'entrée du moteur.
type Input struct {
	Today    calendar.Date
	Until    calendar.Date // courses suivantes : fin de la période
	Needs    []MenuNeed
	Lots     map[int64][]stock.Lot
	Watched  []stock.Watched
	Manual   []Manual
	Products map[int64]catalog.Product
	Order    map[int64]int // rayon → position dans le magasin
}

// Line est une ligne de la liste calculée.
type Line struct {
	ProductID int64
	Need      int64 // quantité réellement nécessaire (unité de base)
	Buy       int64 // quantité à acheter, arrondie au conditionnement
	Count     int64 // nombre de conditionnements
	Size      int64 // taille du conditionnement
	Sources   Source
	Details   []string
	AisleID   int64
	Name      string
}

// Compute calcule la liste de courses.
func Compute(in Input) []Line {
	lines := map[int64]*Line{}
	get := func(id int64) *Line {
		l, ok := lines[id]
		if !ok {
			p := in.Products[id]
			l = &Line{ProductID: id, AisleID: p.AisleID, Name: p.Name}
			lines[id] = l
		}
		return l
	}
	lots := make(map[int64][]stock.Lot, len(in.Lots))
	for id, l := range in.Lots {
		lots[id] = slices.Clone(l)
	}
	// 1. Besoins du menu, dans l'ordre des repas : chaque repas puise dans
	//    le stock encore disponible à sa date (lots non périmés, déjà
	//    réservés par les repas précédents déduits).
	needs := slices.Clone(in.Needs)
	slices.SortStableFunc(needs, func(a, b MenuNeed) int { return cmp.Compare(a.Date, b.Date) })
	type origin struct {
		qty   int64
		label string
	}
	origins := map[int64][]origin{}
	short := map[int64]int64{}
	inStock := map[int64]int64{}
	var order []int64
	for _, n := range needs {
		if n.Qty <= 0 {
			continue
		}
		takes, missing := stock.PlanFEFO(lots[n.ProductID], n.Qty, n.Date)
		stock.Consume(lots[n.ProductID], takes)
		if _, ok := origins[n.ProductID]; !ok {
			order = append(order, n.ProductID)
		}
		origins[n.ProductID] = append(origins[n.ProductID], origin{n.Qty, n.Label})
		short[n.ProductID] += missing
		inStock[n.ProductID] += n.Qty - missing
	}
	for _, id := range order {
		if short[id] <= 0 {
			continue
		}
		p := in.Products[id]
		l := get(id)
		l.Need += short[id]
		l.Sources |= FromMenu
		for _, o := range origins[id] {
			l.Details = append(l.Details, fmt.Sprintf("%s pour %s", qty.Format(o.qty, p.Unit), o.label))
		}
		if inStock[id] > 0 {
			l.Details = append(l.Details, fmt.Sprintf("− %s déjà en stock", qty.Format(inStock[id], p.Unit)))
		}
	}
	// 2. Stock minimum et consommation prévue : stock restant aux courses
	//    suivantes, après le menu, moins la consommation estimée.
	days := max(in.Today.DaysUntil(in.Until), 0)
	for _, w := range in.Watched {
		p, ok := in.Products[w.ProductID]
		if !ok {
			continue
		}
		var left int64
		for _, lt := range lots[w.ProductID] {
			if lt.Qty > 0 && lt.Usable(in.Until) {
				left += lt.Qty
			}
		}
		projected := stock.Projected(left, w.DailyUse, days)
		switch {
		case w.Low:
			l := get(w.ProductID)
			q := max(w.Target-projected, 0)
			if q == 0 {
				q = smallest(p)
			}
			l.Need += q
			l.Sources |= FromLow
			l.Details = append(l.Details, "signalé presque vide")
		case w.Min > 0 && projected < w.Min:
			l := get(w.ProductID)
			target := max(w.Target, w.Min)
			l.Need += target - projected
			l.Sources |= FromMinimum
			if left < w.Min {
				l.Details = append(l.Details, fmt.Sprintf("stock minimum : %s restant, cible %s", qty.Format(left, p.Unit), qty.Format(target, p.Unit)))
			} else if d, ok := stock.RunOutDate(left, w.Min, w.DailyUse, in.Today); ok {
				l.Details = append(l.Details, fmt.Sprintf("passera sous le minimum vers le %s", d.Format()))
			}
		}
	}
	// 3. Ajouts manuels.
	for _, m := range in.Manual {
		p, ok := in.Products[m.ProductID]
		if !ok {
			continue
		}
		l := get(m.ProductID)
		q := m.Qty
		if q <= 0 {
			q = smallest(p)
		}
		l.Need += q
		l.Sources |= FromManual
		l.Details = append(l.Details, "ajout manuel")
	}
	// 4. Arrondi au conditionnement et tri par rayon.
	out := make([]Line, 0, len(lines))
	for _, l := range lines {
		p := in.Products[l.ProductID]
		l.Buy, l.Count, l.Size = catalog.RoundToPackages(l.Need, p.Unit, p.BuyUnits())
		out = append(out, *l)
	}
	Sort(out, in.Order)
	return out
}

// smallest renvoie la plus petite quantité achetable : un conditionnement,
// ou une pièce.
func smallest(p catalog.Product) int64 {
	if len(p.Packages) > 0 {
		m := p.Packages[0].Qty
		for _, pk := range p.Packages {
			m = min(m, pk.Qty)
		}
		return m
	}
	if p.Unit == qty.Piece {
		return 1000
	}
	return 0
}

// Sort range des lignes par rayon, dans l'ordre du magasin (rayons non
// ordonnés à la fin, dans l'ordre par défaut), puis par nom.
func Sort(lines []Line, order map[int64]int) {
	pos := func(aisle int64) int {
		if p, ok := order[aisle]; ok {
			return p
		}
		return 1000 + int(aisle)
	}
	slices.SortStableFunc(lines, func(a, b Line) int {
		if c := cmp.Compare(pos(a.AisleID), pos(b.AisleID)); c != 0 {
			return c
		}
		return cmp.Compare(catalog.Normalize(a.Name), catalog.Normalize(b.Name))
	})
}
