// Package stock tient l'inventaire du foyer : lots datés par produit et
// emplacement, journal unique des mouvements (achat, consommation, perte,
// correction, transfert…), annulation, stock minimum et estimation de la
// consommation.
package stock

import (
	"cmp"
	"slices"

	"github.com/codecolors/petitchef/internal/platform/calendar"
)

// Lot est un lot de stock vu par les moteurs de calcul.
type Lot struct {
	ID         int64
	ProductID  int64
	Qty        int64
	BestBefore calendar.Date // vide : pas de date limite
	AddedOn    calendar.Date
}

// Usable indique si le lot est utilisable à la date at (non périmé).
func (l Lot) Usable(at calendar.Date) bool { return l.BestBefore == "" || l.BestBefore >= at }

// Take est une quantité prise dans un lot.
type Take struct {
	LotID int64
	Qty   int64
}

// SortFEFO range les lots dans l'ordre de consommation : date limite la
// plus proche d'abord (« premier périmé, premier sorti »), lots sans date
// en dernier, puis le plus ancien.
func SortFEFO(lots []Lot) {
	slices.SortStableFunc(lots, func(a, b Lot) int {
		switch {
		case a.BestBefore == "" && b.BestBefore != "":
			return 1
		case a.BestBefore != "" && b.BestBefore == "":
			return -1
		}
		if c := cmp.Compare(a.BestBefore, b.BestBefore); c != 0 {
			return c
		}
		if c := cmp.Compare(a.AddedOn, b.AddedOn); c != 0 {
			return c
		}
		return cmp.Compare(a.ID, b.ID)
	})
}

// PlanFEFO choisit les prélèvements couvrant need à la date at, parmi les
// lots utilisables à cette date, dans l'ordre FEFO ; renvoie aussi la
// quantité manquante. Les lots ne sont pas modifiés.
func PlanFEFO(lots []Lot, need int64, at calendar.Date) (takes []Take, shortage int64) {
	sorted := slices.Clone(lots)
	SortFEFO(sorted)
	for _, l := range sorted {
		if need <= 0 {
			break
		}
		if l.Qty <= 0 || !l.Usable(at) {
			continue
		}
		q := min(l.Qty, need)
		takes = append(takes, Take{LotID: l.ID, Qty: q})
		need -= q
	}
	return takes, max(need, 0)
}

// Consume applique des prélèvements à des lots (copie modifiée en place).
func Consume(lots []Lot, takes []Take) {
	for _, t := range takes {
		for i := range lots {
			if lots[i].ID == t.LotID {
				lots[i].Qty -= t.Qty
				break
			}
		}
	}
}

// Event est une consommation datée (quantité positive, unité de base).
type Event struct {
	Date calendar.Date
	Qty  int64
}

// Estimation de la consommation : fenêtre d'observation et exigences
// minimales pour qu'une estimation soit proposée.
const (
	estimateWindow  = 180 // jours examinés
	estimateMinDays = 14  // durée d'observation minimale
	estimateMinUses = 2   // nombre minimal de consommations
)

// EstimateDailyUse estime la consommation moyenne, en millièmes d'unité
// de base par jour, d'après les consommations des 180 derniers jours :
// total consommé divisé par la durée écoulée depuis la première
// consommation observée (au moins 14 jours). ok vaut false si les données
// sont insuffisantes.
func EstimateDailyUse(events []Event, today calendar.Date) (perDayMilli int64, ok bool) {
	from := today.AddDays(-estimateWindow)
	var total int64
	first := calendar.Date("")
	uses := 0
	for _, e := range events {
		if e.Date < from || e.Date > today || e.Qty <= 0 {
			continue
		}
		total += e.Qty
		uses++
		if first == "" || e.Date < first {
			first = e.Date
		}
	}
	if uses < estimateMinUses {
		return 0, false
	}
	span := first.DaysUntil(today)
	if span < estimateMinDays {
		return 0, false
	}
	return total * 1000 / int64(span), true
}

// RunOutDate prévoit la date à laquelle un stock tombera sous un seuil au
// rythme de consommation donné (millièmes d'unité par jour) ; ok vaut
// false sans consommation.
func RunOutDate(current, threshold, perDayMilli int64, today calendar.Date) (calendar.Date, bool) {
	if perDayMilli <= 0 {
		return "", false
	}
	if current < threshold {
		return today, true
	}
	days := (current - threshold) * 1000 / perDayMilli
	return today.AddDays(int(days)), true
}

// Projected renvoie le stock prévu après days jours de consommation.
func Projected(current, perDayMilli int64, days int) int64 {
	return max(current-perDayMilli*int64(days)/1000, 0)
}
