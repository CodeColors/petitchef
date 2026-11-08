package menus

import (
	"path/filepath"
	"slices"
	"testing"

	"github.com/codecolors/petitchef/internal/catalog"
	"github.com/codecolors/petitchef/internal/household"
	"github.com/codecolors/petitchef/internal/planner"
	"github.com/codecolors/petitchef/internal/platform/calendar"
	"github.com/codecolors/petitchef/internal/platform/db"
	"github.com/codecolors/petitchef/internal/recipes"
	"github.com/codecolors/petitchef/internal/recipes/recipetest"
	"github.com/codecolors/petitchef/internal/stock"
)

type env struct {
	t   *testing.T
	d   *db.DB
	s   *Service
	h   *household.Service
	r   *recipes.Service
	cat *catalog.Service
	st  *stock.Service
}

func newEnv(t *testing.T) *env {
	t.Helper()
	d, err := db.Open(t.Context(), filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	if err := catalog.SyncSeed(t.Context(), d); err != nil {
		t.Fatal(err)
	}
	c := catalog.NewService(d)
	r := recipes.NewService(d, c)
	if err := recipetest.Load(t.Context(), r); err != nil {
		t.Fatal(err)
	}
	h := household.NewService(d)
	st := stock.NewService(d, c)
	return &env{t: t, d: d, s: NewService(d, c, r, h, st), h: h, r: r, cat: c, st: st}
}

const monday = calendar.Date("2026-11-09")

func TestGenerateRespectsTemplateAndConstraints(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	peanut, _ := e.h.Save(ctx, household.Member{Name: "Léa", Portion: 1000, Diets: catalog.Vegetarian})
	if _, err := e.h.Save(ctx, household.Member{Name: "Tom", Portion: 500, Allergens: catalog.Gluten}); err != nil {
		t.Fatal(err)
	}
	_ = peanut
	pref := DefaultPreferences()
	pref.Seed = 99
	res, err := e.s.Generate(ctx, Request{From: monday, Weeks: 2, Preferences: pref})
	if err != nil {
		t.Fatal(err)
	}
	if res.Dishes == 0 {
		t.Fatal("aucun plat généré")
	}
	meals, _ := e.s.Range(ctx, monday, monday.AddDays(14))
	all, _ := e.r.All(ctx)
	for _, m := range meals {
		if m.Format == planner.NoMeal && len(m.Entries) > 0 {
			t.Errorf("%s : plats servis alors qu'il n'y a pas de repas", m.Date)
		}
		for _, en := range m.Entries {
			r := all[en.RecipeID]
			if r.Traits&catalog.Meat != 0 || r.Allergens&catalog.Gluten != 0 {
				t.Errorf("%s : « %s » ne convient pas (végétarien, sans gluten)", m.Date, r.Title)
			}
			if en.Portions != 1500 && !en.CookDouble {
				t.Errorf("portions : %d, attendu 1 500 (adulte + enfant)", en.Portions)
			}
			if m.SlotName == "Dîner" && weekday(m.Date) < 5 && r.ActiveMin() > 40 && !en.IsLeftover() {
				t.Errorf("dîner de semaine trop long : %s (%d min)", r.Title, r.ActiveMin())
			}
		}
		roles := m.Format.Roles()
		if len(m.Entries) > len(roles) {
			t.Errorf("%s %s : %d plats pour le format %s", m.Date, m.SlotName, len(m.Entries), m.Format)
		}
	}
	// Verrouiller un plat puis régénérer : il est conservé, et la même
	// graine donne le même menu.
	first := meals[0].Entries[0]
	if err := e.s.ToggleLock(ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	before := titles(t, e, monday)
	if _, err := e.s.Generate(ctx, Request{From: monday, Weeks: 2, Preferences: pref}); err != nil {
		t.Fatal(err)
	}
	after := titles(t, e, monday)
	if !slices.Equal(before, after) {
		t.Errorf("même graine, menus différents :\n%v\n%v", before, after)
	}
	again, _ := e.s.Range(ctx, monday, monday.AddDays(1))
	if again[0].Entries[0].RecipeID != first.RecipeID || !again[0].Entries[0].Locked {
		t.Error("le plat verrouillé n'a pas été conservé")
	}
}

func titles(t *testing.T, e *env, from calendar.Date) []string {
	t.Helper()
	meals, err := e.s.Range(t.Context(), from, from.AddDays(14))
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, m := range meals {
		for _, en := range m.Entries {
			out = append(out, string(m.Date)+" "+string(en.Role)+" "+en.Title)
		}
	}
	return out
}

func TestEditing(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	pref := DefaultPreferences()
	pref.Seed = 3
	pref.Leftovers = false
	if _, err := e.s.Generate(ctx, Request{From: monday, Weeks: 1, Preferences: pref}); err != nil {
		t.Fatal(err)
	}
	meals, _ := e.s.Range(ctx, monday, monday.AddDays(7))
	var dinner, lunch Meal
	for _, m := range meals {
		if m.Date == monday && m.SlotName == "Dîner" {
			dinner = m
		}
		if m.Date == monday.AddDays(1) && m.SlotName == "Déjeuner" {
			lunch = m
		}
	}
	main := dinner.Entries[0]
	for _, x := range dinner.Entries {
		if x.Role == planner.Main {
			main = x
		}
	}
	alts, err := e.s.Alternatives(ctx, main.ID, 5)
	if err != nil || len(alts) < 3 {
		t.Fatalf("alternatives : %d, %v", len(alts), err)
	}
	if _, err := e.s.SetDish(ctx, dinner.ID, planner.Main, alts[0].RecipeID, 0, 0, nil); err != nil {
		t.Fatal(err)
	}
	// Restes du dîner le lendemain midi.
	updated, _ := e.s.Meal(ctx, dinner.ID)
	var newMain Entry
	for _, x := range updated.Entries {
		if x.Role == planner.Main {
			newMain = x
		}
	}
	if err := e.s.CookDouble(ctx, newMain.ID, 0); err != nil {
		t.Fatal(err)
	}
	l, _ := e.s.Meal(ctx, lunch.ID)
	found := false
	for _, x := range l.Entries {
		if x.Role == planner.Main && x.LeftoversFrom == newMain.ID && x.RecipeID == newMain.RecipeID {
			found = true
		}
	}
	if !found {
		t.Errorf("restes non placés : %+v", l.Entries)
	}
	// Remplacer un plat par un simple produit, échanger, reporter.
	pizza, _ := e.cat.Resolve(ctx, "pizza surgelée")
	if _, err := e.s.SetDish(ctx, meals[2].ID, planner.Main, 0, pizza, 0, nil); err != nil {
		t.Fatal(err)
	}
	m2, _ := e.s.Meal(ctx, meals[2].ID)
	m3, _ := e.s.Meal(ctx, meals[3].ID)
	if err := e.s.Swap(ctx, m2.Entries[len(m2.Entries)-1].ID, m3.Entries[len(m3.Entries)-1].ID); err != nil {
		t.Fatal(err)
	}
	if err := e.s.Postpone(ctx, m3.Entries[0].ID, true); err != nil {
		t.Fatal(err)
	}
	// Changer le format retire les plats devenus sans objet.
	if err := e.s.UpdateMeal(ctx, dinner.ID, MealInput{Format: planner.MainOnly, AllMembers: true, MaxMin: -1}); err != nil {
		t.Fatal(err)
	}
	if d, _ := e.s.Meal(ctx, dinner.ID); len(d.Entries) != 1 || d.Entries[0].Role != planner.Main {
		t.Errorf("format plat seul : %+v", d.Entries)
	}
	// Menu enregistré, puis appliqué une autre semaine.
	if _, err := e.s.SaveModel(ctx, "Semaine test", monday, 7); err != nil {
		t.Fatal(err)
	}
	models, _ := e.s.Models(ctx)
	if n, err := e.s.ApplyModel(ctx, models[0].ID, monday.AddDays(14)); err != nil || n == 0 {
		t.Errorf("menu enregistré appliqué : %d, %v", n, err)
	}
	// « Que cuisiner ce soir » avec le stock d'une recette.
	ratatouille, _ := e.r.ByTitle(ctx, "ratatouille")
	r, _ := e.r.Get(ctx, ratatouille)
	for _, in := range r.Ingredients {
		if _, _, err := e.st.Add(ctx, stock.AddInput{ProductID: in.ProductID, Qty: 5000, PriceCents: -1, Date: calendar.Today()}); err != nil {
			t.Fatal(err)
		}
	}
	tonight, err := e.s.Tonight(ctx, planner.Main, 0, 2000, 5)
	if err != nil || len(tonight) == 0 || tonight[0].Title != "Ratatouille" || len(tonight[0].Missing) != 0 {
		t.Errorf("ce soir : %+v, %v", tonight, err)
	}
}

func BenchmarkGenerateSeed(b *testing.B) {
	t := &testing.T{}
	_ = t
	d, err := db.Open(b.Context(), filepath.Join(b.TempDir(), "t.db"))
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = d.Close() }()
	if err := catalog.SyncSeed(b.Context(), d); err != nil {
		b.Fatal(err)
	}
	c := catalog.NewService(d)
	r := recipes.NewService(d, c)
	if err := recipetest.Load(b.Context(), r); err != nil {
		b.Fatal(err)
	}
	s := NewService(d, c, r, household.NewService(d), stock.NewService(d, c))
	pref := DefaultPreferences()
	pref.Seed = 1
	for b.Loop() {
		if _, err := s.Generate(b.Context(), Request{From: monday, Weeks: 4, Preferences: pref}); err != nil {
			b.Fatal(err)
		}
	}
}
