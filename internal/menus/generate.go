package menus

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/codecolors/petitchef/internal/catalog"
	"github.com/codecolors/petitchef/internal/household"
	"github.com/codecolors/petitchef/internal/planner"
	"github.com/codecolors/petitchef/internal/platform/calendar"
	"github.com/codecolors/petitchef/internal/platform/db"
	"github.com/codecolors/petitchef/internal/platform/qty"
	"github.com/codecolors/petitchef/internal/platform/validation"
	"github.com/codecolors/petitchef/internal/recipes"
	"github.com/codecolors/petitchef/internal/stock"
)

// Preferences sont les réglages du générateur, mémorisés d'une génération
// à l'autre.
type Preferences struct {
	Preset     string          `json:"preset"`
	Weights    planner.Weights `json:"poids"`
	Rules      planner.Rules   `json:"regles"`
	Leftovers  bool            `json:"restes"`
	Weeks      int             `json:"semaines"`
	TemplateID int64           `json:"semaine_type"`
	Seed       uint64          `json:"graine"`
}

// DefaultPreferences sont les réglages initiaux.
func DefaultPreferences() Preferences {
	return Preferences{Preset: "equilibre", Weights: planner.Presets[0].Weights, Rules: planner.DefaultRules, Weeks: 1, Leftovers: true}
}

// Preferences renvoie les réglages mémorisés du générateur.
func (s *Service) Preferences(ctx context.Context) (Preferences, error) {
	p := DefaultPreferences()
	var raw string
	err := s.db.R.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = 'generator'`).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return p, nil
	}
	if err != nil {
		return p, err
	}
	_ = json.Unmarshal([]byte(raw), &p)
	return p, nil
}

func (s *Service) savePreferences(ctx context.Context, p Preferences) error {
	raw, err := json.Marshal(p)
	if err != nil {
		return err
	}
	_, err = s.db.W.ExecContext(ctx, `INSERT INTO settings (key, value) VALUES ('generator', ?) ON CONFLICT (key) DO UPDATE SET value = excluded.value`, string(raw))
	return err
}

// Request est une demande de génération.
type Request struct {
	From  calendar.Date
	Weeks int // 1 à 8
	Preferences
}

// Result est le résultat d'une génération.
type Result struct {
	Summary  planner.Summary
	Dishes   int
	Empty    int // cases restées vides (aucune recette admissible)
	Duration time.Duration
}

// data rassemble ce que le générateur doit savoir, chargé en peu de
// requêtes.
type data struct {
	members []household.Member
	slots   map[int64]MealSlot
	cands   []planner.Candidate
	byID    map[int64]planner.Candidate
	stock   map[int64][]stock.Lot
	names   planner.ProductNames
	ready   []planner.Ready
}

// load prépare les recettes candidates (besoins par portion, avis, coût),
// le stock, les membres et les créneaux.
func (s *Service) load(ctx context.Context) (*data, error) {
	d := &data{slots: map[int64]MealSlot{}, byID: map[int64]planner.Candidate{}, names: planner.ProductNames{}}
	var err error
	if d.members, err = s.household.Members(ctx, false); err != nil {
		return nil, err
	}
	slotList, err := s.Slots(ctx, false)
	if err != nil {
		return nil, err
	}
	for _, sl := range slotList {
		d.slots[sl.ID] = sl
	}
	all, err := s.recipes.All(ctx)
	if err != nil {
		return nil, err
	}
	opinions, err := s.recipes.Opinions(ctx)
	if err != nil {
		return nil, err
	}
	load := recipes.MemLoader(all)
	needs := map[int64][]recipes.Need{}
	var productIDs []int64
	seen := map[int64]bool{}
	ids := make([]int64, 0, len(all))
	for id := range all {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	for _, id := range ids {
		r := all[id]
		if r.Types&(recipes.Starter|recipes.Main|recipes.Dessert) == 0 {
			continue
		}
		n, err := recipes.Expand(r, 1000, load) // une portion
		if err != nil {
			continue // recette en boucle : ignorée
		}
		needs[id] = n
		for _, x := range n {
			if !seen[x.ProductID] {
				seen[x.ProductID] = true
				productIDs = append(productIDs, x.ProductID)
			}
		}
	}
	products, err := s.catalog.Many(ctx, productIDs)
	if err != nil {
		return nil, err
	}
	prices, err := s.catalog.UnitPrices(ctx, productIDs)
	if err != nil {
		return nil, err
	}
	for _, id := range ids {
		n, ok := needs[id]
		if !ok {
			continue
		}
		r := all[id]
		op := opinions[id]
		c := planner.Candidate{ID: id, Title: r.Title, Types: r.Types, ActiveMin: r.ActiveMin(), Allergens: r.Allergens, Traits: r.Traits,
			Season: r.Season, Grade: r.Grade(), Protein: r.Protein, StarchID: r.StarchID, Heavy: r.Heavy, VegShare: r.VegShare,
			Stars: op.Stars, Favorite: op.Favorite, BannedBy: op.BannedBy, LastCooked: r.LastCooked,
			BatchFriendly: slices.ContainsFunc(r.Tags, func(t string) bool { return strings.Contains(t, "batch") || t == "mijoté" }),
			Fish:          r.Protein == catalog.Fish, RedMeat: r.Protein == catalog.RedMeat || r.Protein == catalog.ColdCuts,
			Vegetarian: r.Traits&catalog.Meat == 0}
		var cost int64
		costKnown := true
		for _, x := range n {
			if x.Optional || x.ToTaste && x.Qty == 0 {
				continue
			}
			p := products[x.ProductID]
			g, _ := p.Conv().Grams(x.Qty)
			c.Uses = append(c.Uses, planner.Use{ProductID: x.ProductID, PerPortion: x.Qty * 1000, Grams: g, Staple: x.Staple || p.Staple})
			if up, ok := prices[x.ProductID]; ok {
				cost += catalog.Cost(up, x.Qty)
			} else if !p.Staple {
				costKnown = false
			}
		}
		c.CostPerPortion = -1
		if costKnown {
			c.CostPerPortion = cost
		}
		d.cands = append(d.cands, c)
		d.byID[id] = c
	}
	for id, p := range products {
		d.names[id] = p.Name
	}
	if d.stock, err = s.stock.Lots(ctx, nil); err != nil {
		return nil, err
	}
	d.ready, err = db.Collect(ctx, s.db.R, func(r db.Scanner) (planner.Ready, error) {
		var x planner.Ready
		var unit qty.Base
		var q int64
		err := r.Scan(&x.StockItemID, &x.RecipeID, &q, &unit, &x.BestBefore)
		x.Portions = q
		if unit != qty.Piece {
			x.Portions = 0 // portions inconnues
		}
		return x, err
	}, `SELECT s.id, s.recipe_id, s.qty, p.unit, coalesce(s.best_before, '') FROM stock_items s JOIN products p ON p.id = s.product_id
		WHERE s.qty > 0 AND s.recipe_id IS NOT NULL ORDER BY s.best_before IS NULL, s.best_before, s.id`)
	return d, err
}

// slotFor convertit un repas en créneau du générateur.
func (d *data) slotFor(m Meal) planner.Slot {
	sl := d.slots[m.SlotID]
	c := household.ConstraintsFor(d.members, Present(m, d.members), m.Guests)
	maxMin := sl.MaxMin(weekday(m.Date))
	if m.MaxMin >= 0 {
		maxMin = m.MaxMin
	}
	return planner.Slot{MealID: m.ID, Date: m.Date, SlotID: m.SlotID, SlotName: sl.Name, Lunch: sl.Slug == "dejeuner", Dinner: sl.Slug == "diner",
		Format: m.Format, Portions: Portions(m, d.members), MaxMin: maxMin,
		Constraints: planner.Constraints{Allergens: c.Allergens, Diets: c.Diets, Refused: c.Refused, Members: Present(m, d.members)},
		Fixed:       map[planner.Role]planner.Fixed{}}
}

// history renvoie les plats des 30 jours précédant from (variété).
func (s *Service) history(ctx context.Context, d *data, from calendar.Date) ([]planner.Past, error) {
	past, err := s.Range(ctx, from.AddDays(-30), from)
	if err != nil {
		return nil, err
	}
	var out []planner.Past
	for _, m := range past {
		for _, e := range m.Entries {
			if e.RecipeID > 0 {
				c := d.byID[e.RecipeID]
				out = append(out, planner.Past{Date: m.Date, RecipeID: e.RecipeID, Protein: c.Protein, StarchID: c.StarchID})
			}
		}
	}
	return out, nil
}

// Generate génère les menus de req.Weeks semaines à partir de req.From :
// repas créés d'après la semaine type, plats verrouillés ou cuisinés
// conservés, autres plats remplacés.
func (s *Service) Generate(ctx context.Context, req Request) (Result, error) {
	start := time.Now()
	var res Result
	if req.Weeks < 1 || req.Weeks > 8 {
		return res, validation.Error("Période de 1 à 8 semaines.")
	}
	if req.From == "" {
		req.From = calendar.Today()
	}
	if req.TemplateID == 0 {
		var err error
		if req.TemplateID, err = s.DefaultTemplate(ctx); err != nil {
			return res, err
		}
	}
	if req.Seed == 0 {
		req.Seed = uint64(time.Now().UnixNano())
	}
	days := req.Weeks * 7
	to := req.From.AddDays(days)
	if err := s.EnsureMeals(ctx, req.From, days, req.TemplateID); err != nil {
		return res, err
	}
	d, err := s.load(ctx)
	if err != nil {
		return res, err
	}
	meals, err := s.Range(ctx, req.From, to)
	if err != nil {
		return res, err
	}
	in := planner.Input{Candidates: d.cands, Stock: d.stock, Ready: d.ready, Names: d.names, Weights: req.Weights, Rules: req.Rules,
		Leftovers: req.Leftovers, Seed: req.Seed, Today: calendar.Today()}
	if in.History, err = s.history(ctx, d, req.From); err != nil {
		return res, err
	}
	var slotMeals []Meal
	for _, m := range meals {
		if m.Format == planner.NoMeal {
			continue
		}
		sl := d.slotFor(m)
		for _, e := range m.Entries {
			if e.Locked || e.Status == Cooked {
				sl.Fixed[e.Role] = planner.Fixed{RecipeID: e.RecipeID, ProductID: e.ProductID, Leftover: e.IsLeftover()}
			}
		}
		in.Slots = append(in.Slots, sl)
		slotMeals = append(slotMeals, m)
	}
	plan, sum := planner.Generate(in)
	ids := make([]int64, len(plan))
	err = s.db.Tx(ctx, func(tx *sql.Tx) error {
		if err := clearRange(ctx, tx, req.From, to); err != nil {
			return err
		}
		for i, p := range plan {
			if p.Fixed {
				continue
			}
			sl := in.Slots[p.Slot]
			portions := sl.Portions
			reasons, _ := json.Marshal(p.Reasons)
			var leftover any
			if p.LeftoverOf > 0 {
				leftover = ids[p.LeftoverOf-1]
			}
			r, err := tx.ExecContext(ctx, `INSERT INTO menu_entries (meal_id, role, recipe_id, stock_item_id, portions, leftovers_from, cook_double, reasons)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, sl.MealID, p.Role, nullID(p.RecipeID), nullID(p.ReadyItem), portions, leftover, p.CookDouble, string(reasons))
			if err != nil {
				return err
			}
			if ids[i], err = r.LastInsertId(); err != nil {
				return err
			}
			if p.LeftoverOf > 0 {
				if _, err := tx.ExecContext(ctx, `UPDATE menu_entries SET portions = portions + ? WHERE id = ?`, portions, ids[p.LeftoverOf-1]); err != nil {
					return err
				}
			}
			res.Dishes++
		}
		return nil
	})
	if err != nil {
		return res, err
	}
	for k, m := range slotMeals {
		_ = k
		res.Empty += len(m.Format.Roles())
	}
	res.Empty -= len(plan)
	res.Summary = sum
	if err := s.savePreferences(ctx, req.Preferences); err != nil {
		return res, err
	}
	res.Duration = time.Since(start)
	return res, nil
}

// Alternative est une recette proposée pour remplacer un plat.
type Alternative struct {
	planner.Alternative
	Title string
	Grade string
	Min   int
}

// Alternatives propose n recettes pour remplacer un plat, le reste de la
// semaine étant conservé.
func (s *Service) Alternatives(ctx context.Context, entryID int64, n int) ([]Alternative, error) {
	e, m, err := s.Entry(ctx, entryID)
	if err != nil {
		return nil, err
	}
	return s.alternativesFor(ctx, m, e.Role, n)
}

// AlternativesForMeal propose n recettes pour un rôle d'un repas.
func (s *Service) AlternativesForMeal(ctx context.Context, mealID int64, role planner.Role, n int) ([]Alternative, error) {
	m, err := s.Meal(ctx, mealID)
	if err != nil {
		return nil, err
	}
	return s.alternativesFor(ctx, m, role, n)
}

func (s *Service) alternativesFor(ctx context.Context, m Meal, role planner.Role, n int) ([]Alternative, error) {
	d, err := s.load(ctx)
	if err != nil {
		return nil, err
	}
	pref, err := s.Preferences(ctx)
	if err != nil {
		return nil, err
	}
	from, to := m.Date.AddDays(-3), m.Date.AddDays(4)
	meals, err := s.Range(ctx, from, to)
	if err != nil {
		return nil, err
	}
	in := planner.Input{Candidates: d.cands, Stock: d.stock, Names: d.names, Weights: pref.Weights, Rules: pref.Rules, Seed: pref.Seed, Today: calendar.Today()}
	var plan []planner.Placement
	target := -1
	for _, x := range meals {
		if x.Format == planner.NoMeal {
			continue
		}
		in.Slots = append(in.Slots, d.slotFor(x))
		idx := len(in.Slots) - 1
		if x.ID == m.ID {
			target = idx
		}
		for _, e := range x.Entries {
			if e.RecipeID > 0 {
				plan = append(plan, planner.Placement{Slot: idx, Role: e.Role, RecipeID: e.RecipeID, LeftoverOf: boolInt(e.IsLeftover())})
			}
		}
	}
	if target < 0 {
		return nil, validation.Error("Ce repas n'est pas à la maison.")
	}
	alts := planner.Alternatives(in, plan, target, role, n)
	out := make([]Alternative, len(alts))
	for i, a := range alts {
		c := d.byID[a.RecipeID]
		out[i] = Alternative{Alternative: a, Title: c.Title, Grade: c.Grade, Min: c.ActiveMin}
	}
	return out, nil
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// Tonight est une recette proposée pour ce soir.
type Tonight struct {
	planner.Feasible
	Title   string
	Grade   string
	Min     int
	Missing []string
}

// Tonight classe les recettes par part des ingrédients disponibles, en
// favorisant ce qui périme bientôt (même moteur de score que le
// générateur) ; productID restreint aux recettes qui utilisent ce produit.
func (s *Service) Tonight(ctx context.Context, role planner.Role, productID int64, portions int64, n int) ([]Tonight, error) {
	d, err := s.load(ctx)
	if err != nil {
		return nil, err
	}
	if portions <= 0 {
		portions = Portions(Meal{AllMembers: true}, d.members)
	}
	today := calendar.Today()
	present := Present(Meal{AllMembers: true}, d.members)
	c := household.ConstraintsFor(d.members, present, 0)
	slot := planner.Slot{Date: today, Format: planner.MainOnly, Portions: portions, Dinner: true,
		Constraints: planner.Constraints{Allergens: c.Allergens, Diets: c.Diets, Refused: c.Refused, Members: present}}
	w := planner.Presets[2].Weights // anti-gaspi
	in := planner.Input{Candidates: d.cands, Stock: d.stock, Names: d.names, Weights: w, Rules: planner.Rules{}, Today: today, Seed: 1}
	ranked := planner.RankTonight(in, slot, role, productID, n)
	out := make([]Tonight, len(ranked))
	for i, f := range ranked {
		cand := d.byID[f.RecipeID]
		t := Tonight{Feasible: f, Title: cand.Title, Grade: cand.Grade, Min: cand.ActiveMin}
		for _, u := range cand.Uses {
			if u.Staple {
				continue
			}
			need := u.PerPortion * portions / 1_000_000
			var avail int64
			for _, l := range d.stock[u.ProductID] {
				if l.Usable(today) {
					avail += l.Qty
				}
			}
			if avail < need {
				t.Missing = append(t.Missing, d.names[u.ProductID])
			}
		}
		out[i] = t
	}
	return out, nil
}

// AddToday ajoute une recette au menu d'un jour, au premier repas du jour
// (à partir de l'heure courante) qui a un plat libre, sinon au dîner.
func (s *Service) AddToMeal(ctx context.Context, date calendar.Date, slotID int64, role planner.Role, recipeID int64) (int64, error) {
	tpl, err := s.DefaultTemplate(ctx)
	if err != nil {
		return 0, err
	}
	if err := s.EnsureMeals(ctx, date, 1, tpl); err != nil {
		return 0, err
	}
	meals, err := s.Range(ctx, date, date.AddDays(1))
	if err != nil {
		return 0, err
	}
	for _, m := range meals {
		if m.SlotID != slotID {
			continue
		}
		if !slices.Contains(m.Format.Roles(), role) {
			in := MealInput{Format: formatWith(m.Format, role), AllMembers: m.AllMembers, Members: m.Members, Guests: m.Guests, MaxMin: m.MaxMin}
			if err := s.UpdateMeal(ctx, m.ID, in); err != nil {
				return 0, err
			}
		}
		return s.SetDish(ctx, m.ID, role, recipeID, 0, 0, []string{"choisi à la main"})
	}
	return 0, validation.Error("Ce créneau n'est pas activé.")
}

// formatWith renvoie le plus petit format qui contient un rôle en plus
// de ceux du format actuel.
func formatWith(f planner.Format, role planner.Role) planner.Format {
	roles := append(f.Roles(), role)
	has := func(r planner.Role) bool { return slices.Contains(roles, r) }
	switch {
	case has(planner.Starter) && has(planner.Dessert):
		return planner.FullMeal
	case has(planner.Starter):
		return planner.StarterMain
	case has(planner.Dessert):
		return planner.MainDessert
	}
	return planner.MainOnly
}

// WeekSummary dresse le bilan des menus d'une période : indice moyen,
// répartition des protéines, coût estimé, part des ingrédients en stock.
func (s *Service) WeekSummary(ctx context.Context, meals []Meal) (planner.Summary, error) {
	d, err := s.load(ctx)
	if err != nil {
		return planner.Summary{}, err
	}
	in := planner.Input{Candidates: d.cands, Stock: d.stock, Names: d.names}
	var plan []planner.Placement
	for _, m := range meals {
		if m.Format == planner.NoMeal {
			continue
		}
		in.Slots = append(in.Slots, d.slotFor(m))
		for _, e := range m.Entries {
			if e.RecipeID > 0 && e.Status != Cooked {
				plan = append(plan, planner.Placement{Slot: len(in.Slots) - 1, Role: e.Role, RecipeID: e.RecipeID,
					LeftoverOf: boolInt(e.IsLeftover()), ReadyItem: e.StockItemID})
			}
		}
	}
	return planner.Summarize(in, plan), nil
}
