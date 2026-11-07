package planner

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/codecolors/petitchef/internal/catalog"
	"github.com/codecolors/petitchef/internal/platform/calendar"
	"github.com/codecolors/petitchef/internal/recipes"
	"github.com/codecolors/petitchef/internal/stock"
)

const start = calendar.Date("2026-11-09") // un lundi

// synthetic fabrique n recettes variées : types, temps, protéines,
// allergènes et ingrédients répartis de façon déterministe.
func synthetic(n int) []Candidate {
	proteins := []catalog.Group{catalog.Poultry, catalog.Fish, catalog.RedMeat, catalog.Pulses, catalog.Eggs, ""}
	grades := []string{"A", "B", "C", "D", "E"}
	out := make([]Candidate, n)
	for i := range out {
		c := Candidate{ID: int64(i + 1), Title: fmt.Sprintf("Recette %d", i+1), ActiveMin: 10 + i%7*10, Grade: grades[i%5],
			Protein: proteins[i%6], StarchID: int64(900 + i%4), Season: catalog.AllYear, CostPerPortion: int64(100 + i%9*40),
			Stars: i % 6 * 10, BannedBy: map[int64]bool{}}
		switch i % 4 {
		case 0, 1:
			c.Types = recipes.Main
		case 2:
			c.Types = recipes.Starter
		default:
			c.Types = recipes.Dessert
		}
		if i%10 == 0 {
			c.Types |= recipes.Starter
		}
		c.Fish = c.Protein == catalog.Fish
		c.RedMeat = c.Protein == catalog.RedMeat
		c.Vegetarian = c.Protein == catalog.Pulses || c.Protein == catalog.Eggs || c.Protein == ""
		if !c.Vegetarian {
			c.Traits = catalog.Meat | catalog.Animal
		}
		if i%7 == 0 {
			c.Allergens = catalog.Peanuts
		}
		if i%5 == 0 {
			c.Allergens |= catalog.Gluten
		}
		for k := range 6 {
			c.Uses = append(c.Uses, Use{ProductID: int64(1 + (i*7+k*13)%200), PerPortion: 100_000, Grams: 100})
		}
		c.BatchFriendly = i%3 == 0
		out[i] = c
	}
	return out
}

func slots(weeks int, format Format) []Slot {
	var out []Slot
	for d := range weeks * 7 {
		date := start.AddDays(d)
		out = append(out,
			Slot{Date: date, SlotID: 1, SlotName: "Déjeuner", Lunch: true, Format: format, Portions: 4000, Constraints: Constraints{Refused: map[int64]bool{}}},
			Slot{Date: date, SlotID: 2, SlotName: "Dîner", Dinner: true, Format: format, Portions: 4000, MaxMin: 40, Constraints: Constraints{Refused: map[int64]bool{}}})
	}
	return out
}

func baseInput(n, weeks int, format Format) Input {
	return Input{Slots: slots(weeks, format), Candidates: synthetic(n), Stock: map[int64][]stock.Lot{}, Names: ProductNames{},
		Weights: Presets[0].Weights, Rules: DefaultRules, Seed: 42, Today: start}
}

func index(cands []Candidate) map[int64]Candidate {
	m := map[int64]Candidate{}
	for _, c := range cands {
		m[c.ID] = c
	}
	return m
}

func TestFormatsAndHardConstraints(t *testing.T) {
	in := baseInput(200, 2, FullMeal)
	for i := range in.Slots {
		in.Slots[i].Allergens = catalog.Peanuts | catalog.Gluten
		in.Slots[i].Diets = catalog.Vegetarian
		in.Slots[i].Members = []int64{7}
	}
	in.Slots[3].Format = MainOnly
	in.Slots[4].Format = NoMeal
	in.Slots[5].Refused = map[int64]bool{}
	in.Candidates[1].BannedBy = map[int64]bool{7: true} // ne plus proposer la recette 2
	plan, _ := Generate(in)
	byID := index(in.Candidates)
	count := map[int]int{}
	for _, p := range plan {
		count[p.Slot]++
		c := byID[p.RecipeID]
		s := in.Slots[p.Slot]
		switch {
		case !c.Types.Has(p.Role.Type()):
			t.Errorf("%s : type %v pour le rôle %s", c.Title, c.Types, p.Role)
		case c.Allergens&(catalog.Peanuts|catalog.Gluten) != 0:
			t.Errorf("%s : allergène servi", c.Title)
		case !c.Vegetarian:
			t.Errorf("%s : non végétarien servi", c.Title)
		case s.MaxMin > 0 && c.ActiveMin > s.MaxMin:
			t.Errorf("%s : %d min au dîner (max %d)", c.Title, c.ActiveMin, s.MaxMin)
		case c.ID == 2:
			t.Error("recette « à ne plus proposer » servie")
		}
	}
	if count[3] != 1 || count[4] != 0 || count[0] != 3 {
		t.Errorf("formats : plat seul %d, aucun %d, complet %d", count[3], count[4], count[0])
	}
}

func TestDeterminismAndVariety(t *testing.T) {
	in := baseInput(300, 4, StarterMain)
	a, sa := Generate(in)
	b, sb := Generate(in)
	if !reflect.DeepEqual(a, b) || !reflect.DeepEqual(sa, sb) {
		t.Fatal("même graine, menus différents")
	}
	in.Seed = 7
	c, _ := Generate(in)
	if reflect.DeepEqual(a, c) {
		t.Error("graines différentes, menus identiques")
	}
	// Pas la même recette avant 10 jours.
	last := map[int64]calendar.Date{}
	for _, p := range a {
		d := in.Slots[p.Slot].Date
		if prev, ok := last[p.RecipeID]; ok && prev.DaysUntil(d) < in.Rules.NoRepeatDays {
			t.Errorf("recette %d servie le %s puis le %s", p.RecipeID, prev, d)
		}
		last[p.RecipeID] = d
	}
	// Règles de la semaine : au moins 2 poissons, au plus 2 viandes rouges.
	byID := index(in.Candidates)
	weeks := map[calendar.Date][2]int{}
	for _, p := range a {
		if p.Role != Main {
			continue
		}
		wk := weekKey(in.Slots[p.Slot].Date)
		x := weeks[wk]
		if byID[p.RecipeID].Fish {
			x[0]++
		}
		if byID[p.RecipeID].RedMeat {
			x[1]++
		}
		weeks[wk] = x
	}
	for wk, x := range weeks {
		if x[0] < 2 || x[1] > 2 {
			t.Errorf("semaine du %s : %d poissons, %d viandes rouges", wk, x[0], x[1])
		}
	}
}

func TestPrefersExpiringStockAndExplains(t *testing.T) {
	in := baseInput(120, 1, MainOnly)
	// La recette 41 utilise le produit 1 + ... ; on met en stock tous ses
	// ingrédients, le premier périmant le mardi.
	var target Candidate
	for _, c := range in.Candidates {
		if c.ID == 41 {
			target = c
		}
	}
	in.Names = ProductNames{}
	for i, u := range target.Uses {
		best := calendar.Date("2026-12-31")
		if i == 0 {
			best = "2026-11-10"
		}
		in.Stock[u.ProductID] = []stock.Lot{{ID: int64(i + 1), ProductID: u.ProductID, Qty: 10_000, BestBefore: best}}
		in.Names[u.ProductID] = fmt.Sprintf("Produit %d", u.ProductID)
	}
	in.Weights = Presets[2].Weights // anti-gaspi
	plan, sum := Generate(in)
	found := false
	for _, p := range plan {
		if p.RecipeID == 41 {
			found = true
			if in.Slots[p.Slot].Date > "2026-11-10" {
				t.Errorf("recette anti-gaspi servie trop tard : %s", in.Slots[p.Slot].Date)
			}
			if len(p.Reasons) == 0 || p.Reasons[0] != "utilise produit "+fmt.Sprint(target.Uses[0].ProductID)+" qui périme mardi" &&
				p.Reasons[0] != "utilise produit "+fmt.Sprint(target.Uses[0].ProductID)+" qui périme le jour même" &&
				p.Reasons[0] != "utilise produit "+fmt.Sprint(target.Uses[0].ProductID)+" qui périme le lendemain" {
				t.Errorf("raisons : %v", p.Reasons)
			}
		}
	}
	if !found {
		t.Error("la recette qui utilise le stock qui périme n'a pas été choisie")
	}
	if sum.StockShare == 0 || sum.Grade == "" || sum.Dishes != len(plan) {
		t.Errorf("bilan : %+v", sum)
	}
}

func TestLeftoversAndFixed(t *testing.T) {
	in := baseInput(100, 1, MainOnly)
	in.Leftovers = true
	in.Slots[0].Fixed = map[Role]Fixed{Main: {RecipeID: 5}}
	plan, _ := Generate(in)
	if plan[0].RecipeID != 5 || !plan[0].Fixed {
		t.Errorf("plat verrouillé : %+v", plan[0])
	}
	leftovers := 0
	for _, p := range plan {
		if p.LeftoverOf > 0 {
			leftovers++
			src := plan[p.LeftoverOf-1]
			if src.RecipeID != p.RecipeID || !src.CookDouble || !in.Slots[src.Slot].Dinner || !in.Slots[p.Slot].Lunch ||
				in.Slots[src.Slot].Date.AddDays(1) != in.Slots[p.Slot].Date {
				t.Errorf("restes mal placés : %+v depuis %+v", p, src)
			}
		}
	}
	if leftovers == 0 {
		t.Error("aucun reste placé")
	}
	alts := Alternatives(in, plan, 3, Main, 5)
	if len(alts) != 5 {
		t.Errorf("alternatives : %d", len(alts))
	}
	for _, a := range alts {
		if a.RecipeID == plan[3].RecipeID {
			t.Error("l'alternative reprend le plat actuel")
		}
	}
}

func TestRankTonight(t *testing.T) {
	in := baseInput(60, 1, MainOnly)
	c := in.Candidates[4] // recette 5, plat
	for _, u := range c.Uses {
		in.Stock[u.ProductID] = []stock.Lot{{ID: u.ProductID, ProductID: u.ProductID, Qty: 100_000}}
	}
	s := in.Slots[1]
	s.MaxMin = 0
	got := RankTonight(in, s, Main, 0, 5)
	if len(got) == 0 || got[0].RecipeID != 5 || got[0].Coverage != 100 || got[0].Missing != 0 {
		t.Errorf("ce soir : %+v", got)
	}
	only := RankTonight(in, s, Main, c.Uses[0].ProductID, 50)
	for _, f := range only {
		if f.RecipeID == 0 {
			t.Error("recette vide")
		}
	}
}

func BenchmarkGenerate4Weeks500Recipes(b *testing.B) {
	in := baseInput(500, 4, StarterMain)
	for k := range 1000 {
		pid := int64(1 + k%200)
		in.Stock[pid] = append(in.Stock[pid], stock.Lot{ID: int64(k + 1), ProductID: pid, Qty: 500, BestBefore: start.AddDays(k % 30)})
	}
	b.ReportAllocs()
	for b.Loop() {
		Generate(in)
	}
}
