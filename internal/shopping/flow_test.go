package shopping

import (
	"path/filepath"
	"testing"

	"github.com/codecolors/petitchef/internal/catalog"
	"github.com/codecolors/petitchef/internal/household"
	"github.com/codecolors/petitchef/internal/menus"
	"github.com/codecolors/petitchef/internal/planner"
	"github.com/codecolors/petitchef/internal/platform/calendar"
	"github.com/codecolors/petitchef/internal/platform/db"
	"github.com/codecolors/petitchef/internal/recipes"
	"github.com/codecolors/petitchef/internal/stock"
)

// TestFullFlow : semaine type → génération → liste de courses → rangement
// des courses → cuisiné → stock mis à jour → liste suivante.
func TestFullFlow(t *testing.T) {
	ctx := t.Context()
	d, err := db.Open(ctx, filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = d.Close() }()
	if err := catalog.SyncSeed(ctx, d); err != nil {
		t.Fatal(err)
	}
	cat := catalog.NewService(d)
	rec := recipes.NewService(d, cat)
	if err := rec.SyncSeed(ctx); err != nil {
		t.Fatal(err)
	}
	h := household.NewService(d)
	st := stock.NewService(d, cat)
	mn := menus.NewService(d, cat, rec, h, st)
	sh := NewService(d, cat, rec, mn, st)
	for _, m := range []household.Member{{Name: "Léa", Portion: 1000}, {Name: "Hugo", Portion: 1000}} {
		if _, err := h.Save(ctx, m); err != nil {
			t.Fatal(err)
		}
	}
	// Courses chaque jour de la semaine suivant aujourd'hui : la liste
	// couvre 7 jours à partir d'aujourd'hui.
	today := calendar.Today()
	wd := (int(today.Time().Weekday()) + 6) % 7
	if err := sh.SaveSchedule(ctx, Schedule{Weekday: wd, Every: 7}); err != nil {
		t.Fatal(err)
	}
	pref := menus.DefaultPreferences()
	pref.Seed = 11
	pref.Leftovers = false
	if _, err := mn.Generate(ctx, menus.Request{From: today, Weeks: 1, Preferences: pref}); err != nil {
		t.Fatal(err)
	}
	salt, _ := cat.Resolve(ctx, "sel")
	if err := st.SetLow(ctx, salt, true, today); err != nil {
		t.Fatal(err)
	}
	list, err := sh.Recompute(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Items) < 10 {
		t.Fatalf("liste : %d lignes", len(list.Items))
	}
	foundSalt := false
	for _, it := range list.Items {
		if it.ProductID == salt && it.Sources.Has(FromLow) {
			foundSalt = true
		}
		if it.Buy < it.Need {
			t.Errorf("%s : achat %d < besoin %d", it.Name, it.Buy, it.Need)
		}
	}
	if !foundSalt {
		t.Error("le sel presque vide manque à la liste")
	}
	// Ajout manuel, puis on coche tout sauf une ligne (non trouvée).
	choc, _ := cat.Resolve(ctx, "chocolat au lait")
	if err := sh.AddManual(ctx, choc, 0); err != nil {
		t.Fatal(err)
	}
	list, _ = sh.Current(ctx)
	var away []PutAway
	var notFound Item
	for i, it := range list.Items {
		if i == 0 {
			notFound = it
			continue
		}
		price := int64(-1)
		if i == 1 {
			price = 349
		}
		if err := sh.Toggle(ctx, it.ID, true, price); err != nil {
			t.Fatal(err)
		}
		away = append(away, PutAway{ItemID: it.ID, Qty: it.Buy, PriceCents: price})
	}
	// Recalcul : les cases cochées et l'ajout manuel sont conservés.
	again, err := sh.Recompute(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, it := range again.Items {
		if it.Checked {
			checked++
		}
	}
	if checked != len(away) {
		t.Errorf("cases cochées après recalcul : %d, attendu %d", checked, len(away))
	}
	n, err := sh.Store(ctx, away, 0)
	if err != nil || n != len(away) {
		t.Fatalf("rangement : %d, %v", n, err)
	}
	after, _ := sh.Current(ctx)
	if len(after.Items) != 1 || after.Items[0].ProductID != notFound.ProductID {
		t.Errorf("après rangement, seule la ligne non trouvée reste : %+v", after.Items)
	}
	// Le menu est maintenant couvert par le stock : la liste suivante ne
	// contient plus que ce qui n'a pas été trouvé.
	next, err := sh.Recompute(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range next.Items {
		if it.ProductID != notFound.ProductID && it.Sources.Has(FromMenu) {
			t.Errorf("encore à acheter après les courses : %s (%v)", it.Name, it.Details)
		}
	}
	// Cuisiné : le stock est décompté, puis restauré par l'annulation.
	meals, _ := mn.Range(ctx, today, today.AddDays(7))
	var entry menus.Entry
	for _, m := range meals {
		for _, e := range m.Entries {
			if entry.ID == 0 && e.RecipeID > 0 && e.Role == planner.Main && !e.IsLeftover() {
				entry = e
			}
		}
	}
	_, uses, err := mn.PlannedUses(ctx, entry.ID)
	if err != nil || len(uses) == 0 {
		t.Fatalf("ingrédients à décompter : %v, %v", uses, err)
	}
	before, _ := st.Total(ctx, uses[0].ProductID, today)
	if _, err := mn.Cook(ctx, entry.ID, uses, 1000, 0); err != nil {
		t.Fatal(err)
	}
	cooked, _ := st.Total(ctx, uses[0].ProductID, today)
	if cooked != max(before-uses[0].Qty, 0) {
		t.Errorf("décompte : %d → %d (−%d attendu)", before, cooked, uses[0].Qty)
	}
	if r, _ := rec.Get(ctx, entry.RecipeID); r.CookedCount != 1 {
		t.Errorf("historique : %d", r.CookedCount)
	}
	if err := mn.Uncook(ctx, entry.ID); err != nil {
		t.Fatal(err)
	}
	if restored, _ := st.Total(ctx, uses[0].ProductID, today); restored != before {
		t.Errorf("annulation : %d, attendu %d", restored, before)
	}
}
