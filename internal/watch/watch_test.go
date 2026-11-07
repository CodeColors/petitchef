package watch

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/codecolors/petitchef/internal/alerts"
	"github.com/codecolors/petitchef/internal/catalog"
	"github.com/codecolors/petitchef/internal/household"
	"github.com/codecolors/petitchef/internal/menus"
	"github.com/codecolors/petitchef/internal/platform/calendar"
	"github.com/codecolors/petitchef/internal/platform/db"
	"github.com/codecolors/petitchef/internal/recipes"
	"github.com/codecolors/petitchef/internal/shopping"
	"github.com/codecolors/petitchef/internal/stats"
	"github.com/codecolors/petitchef/internal/stock"
)

func TestAlertsAndStats(t *testing.T) {
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
	st := stock.NewService(d, cat)
	mn := menus.NewService(d, cat, rec, household.NewService(d), st)
	sh := shopping.NewService(d, cat, rec, mn, st)
	a := alerts.NewService(d, "")
	Register(a, st, mn, sh)
	today := calendar.Today()
	cream, _ := cat.Resolve(ctx, "crème fraîche épaisse")
	milk, _ := cat.Resolve(ctx, "lait")
	paste, _ := cat.Resolve(ctx, "dentifrice")
	if _, _, err := st.Add(ctx, stock.AddInput{ProductID: cream, Qty: 200, BestBefore: today.AddDays(2), PriceCents: 180, Date: today}); err != nil {
		t.Fatal(err)
	}
	old, _, err := st.Add(ctx, stock.AddInput{ProductID: milk, Qty: 1000, BestBefore: today.AddDays(-2), DateKind: catalog.DLC, PriceCents: 120, Date: today})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SaveSettings(ctx, stock.Settings{ProductID: paste, Min: 75, Target: 150}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Evaluate(ctx, today); err != nil {
		t.Fatal(err)
	}
	open, _ := a.Open(ctx)
	kinds := map[alerts.Kind]string{}
	for _, x := range open {
		kinds[x.Kind] = x.Title + " " + x.Detail
	}
	if !strings.Contains(kinds[alerts.ExpiringSoon], "Crème fraîche épaisse") || !strings.Contains(kinds[alerts.Expired], "Lait") ||
		!strings.Contains(kinds[alerts.BelowMinimum], "Dentifrice") {
		t.Errorf("alertes : %v", kinds)
	}
	if !strings.Contains(kinds[alerts.NoMenu], "semaine prochaine") && today.DaysUntil(menus.Monday(today).AddDays(7)) <= 2 {
		t.Errorf("menu non planifié : %v", kinds)
	}
	// Une deuxième évaluation le même jour ne duplique rien.
	if n, _ := a.Evaluate(ctx, today); n != 0 {
		t.Errorf("%d alertes en double", n)
	}
	// Statistiques : le lait périmé jeté compte dans le gaspillage, avec sa valeur.
	if _, err := st.Empty(ctx, old, stock.Waste, today, 0); err != nil {
		t.Fatal(err)
	}
	rep, err := stats.NewService(d).Compute(ctx, today.AddDays(-30), today)
	if err != nil {
		t.Fatal(err)
	}
	if rep.WasteCount != 1 || len(rep.WasteProducts) != 1 || rep.WasteProducts[0].Value != 120 {
		t.Errorf("gaspillage : %+v", rep.WasteProducts)
	}
	var spent int64
	for _, p := range rep.SpendByMonth {
		spent += p.Value
	}
	if spent != 300 {
		t.Errorf("dépenses : %d", spent)
	}
}
