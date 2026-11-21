package menus

import (
	"testing"

	"github.com/codecolors/petitchef/internal/planner"
)

func TestDeleteMealsAndRecipes(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	pref := DefaultPreferences()
	pref.Seed = 5
	pref.Leftovers = false
	if _, err := e.s.Generate(ctx, Request{From: monday, Weeks: 1, Preferences: pref}); err != nil {
		t.Fatal(err)
	}
	meals, _ := e.s.Range(ctx, monday, monday.AddDays(7))
	var dinner, lunch, cooked Meal
	for _, m := range meals {
		switch {
		case m.Date == monday && m.SlotName == "Dîner":
			dinner = m
		case m.Date == monday.AddDays(1) && m.SlotName == "Déjeuner":
			lunch = m
		case m.Date == monday.AddDays(2) && m.SlotName == "Dîner":
			cooked = m
		}
	}
	var main Entry
	for _, x := range dinner.Entries {
		if x.Role == planner.Main {
			main = x
		}
	}
	// Restes du dîner prévus le lendemain midi ; un plat déjà cuisiné.
	if err := e.s.CookDouble(ctx, main.ID, lunch.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.d.W.ExecContext(ctx, `UPDATE menu_entries SET status = 'cuisine' WHERE id = ?`, cooked.Entries[0].ID); err != nil {
		t.Fatal(err)
	}
	removed, kept, err := e.s.DeleteMeals(ctx, []int64{dinner.ID, cooked.ID})
	if err != nil || removed != 1 || kept != 1 {
		t.Fatalf("retirés %d, conservés %d, %v", removed, kept, err)
	}
	if _, err := e.s.Meal(ctx, dinner.ID); err == nil {
		t.Error("le dîner existe encore")
	}
	if c, _ := e.s.Meal(ctx, cooked.ID); len(c.Entries) != 1 || c.Entries[0].Status != Cooked {
		t.Errorf("seul le plat cuisiné reste : %+v", c.Entries)
	}
	l, _ := e.s.Meal(ctx, lunch.ID)
	for _, x := range l.Entries {
		if x.LeftoversFrom == main.ID || (x.RecipeID == main.RecipeID && x.Role == planner.Main && x.IsLeftover()) {
			t.Errorf("restes d'un repas supprimé encore prévus : %+v", x)
		}
	}

	// Une recette supprimée disparaît de tous les menus, y compris des
	// plats cuisinés et des menus enregistrés.
	if _, err := e.s.SaveModel(ctx, "Semaine", monday, 7); err != nil {
		t.Fatal(err)
	}
	target := cooked.Entries[0].RecipeID
	var also int64
	rest, _ := e.s.Range(ctx, monday, monday.AddDays(7))
	for _, m := range rest {
		for _, x := range m.Entries {
			if x.RecipeID > 0 && x.RecipeID != target {
				also = x.RecipeID
			}
		}
	}
	if target == 0 || also == 0 {
		t.Fatal("recettes de test introuvables")
	}
	if _, err := e.r.DeleteMany(ctx, []int64{target, also}); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := e.d.R.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM menu_entries WHERE recipe_id IN (?1, ?2))
		+ (SELECT count(*) FROM saved_menu_entries WHERE recipe_id IN (?1, ?2))`, target, also).Scan(&n); err != nil || n != 0 {
		t.Errorf("%d plats renvoient encore aux recettes supprimées (%v)", n, err)
	}
	if _, err := e.r.Get(ctx, target); err == nil {
		t.Error("recette non supprimée")
	}
}

func TestAddAndDropCourse(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	pref := DefaultPreferences()
	pref.Seed = 7
	if _, err := e.s.Generate(ctx, Request{From: monday, Weeks: 1, Preferences: pref}); err != nil {
		t.Fatal(err)
	}
	meals, _ := e.s.Range(ctx, monday, monday.AddDays(7))
	var m Meal
	for _, x := range meals {
		if x.Format == planner.MainOnly && len(x.Entries) == 1 {
			m = x
			break
		}
	}
	if m.ID == 0 {
		t.Fatal("aucun repas « plat seul »")
	}
	// Ajouter un dessert : le format s'élargit et une recette est placée.
	title, err := e.s.AddCourse(ctx, m.ID, planner.Dessert, true)
	if err != nil || title == "" {
		t.Fatalf("ajout du dessert : %q, %v", title, err)
	}
	got, _ := e.s.Meal(ctx, m.ID)
	if got.Format != planner.MainDessert || len(got.Entries) != 2 {
		t.Fatalf("après ajout : %s, %d plats", got.Format, len(got.Entries))
	}
	// Ajouter une entrée à choisir soi-même : format élargi, rien de placé.
	if _, err := e.s.AddCourse(ctx, m.ID, planner.Starter, false); err != nil {
		t.Fatal(err)
	}
	if got, _ = e.s.Meal(ctx, m.ID); got.Format != planner.FullMeal || len(got.Entries) != 2 {
		t.Fatalf("après choix manuel : %s, %d plats", got.Format, len(got.Entries))
	}
	// Retirer le dessert : il sort aussi du format.
	var dessert Entry
	for _, x := range got.Entries {
		if x.Role == planner.Dessert {
			dessert = x
		}
	}
	if err := e.s.DropCourse(ctx, dessert.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ = e.s.Meal(ctx, m.ID); got.Format != planner.StarterMain || len(got.Entries) != 1 {
		t.Fatalf("après retrait : %s, %d plats", got.Format, len(got.Entries))
	}
	if _, err := e.s.AddCourse(ctx, m.ID, "apéritif", true); err == nil {
		t.Error("un rôle inconnu doit être refusé")
	}
}
