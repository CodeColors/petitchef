package stock

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/codecolors/petitchef/internal/catalog"
	"github.com/codecolors/petitchef/internal/platform/calendar"
	"github.com/codecolors/petitchef/internal/platform/db"
)

type env struct {
	t   *testing.T
	d   *db.DB
	cat *catalog.Service
	s   *Service
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
	return &env{t: t, d: d, cat: c, s: NewService(d, c)}
}

func (e *env) product(name string) catalog.Product {
	e.t.Helper()
	id, err := e.cat.Resolve(e.t.Context(), name)
	if err != nil {
		e.t.Fatalf("%s : %v", name, err)
	}
	p, err := e.cat.Get(e.t.Context(), id)
	if err != nil {
		e.t.Fatal(err)
	}
	return p
}

func (e *env) location(slug string) int64 {
	var id int64
	if err := e.d.R.QueryRowContext(e.t.Context(), `SELECT id FROM locations WHERE slug = ?`, slug).Scan(&id); err != nil {
		e.t.Fatal(err)
	}
	return id
}

func (e *env) qty(item int64) int64 {
	it, err := e.s.Get(e.t.Context(), item)
	if err != nil {
		e.t.Fatal(err)
	}
	return it.Qty
}

const today = calendar.Date("2026-11-07")

func TestStockLifecycle(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	cream := e.product("crème fraîche épaisse")
	fridge := e.location("refrigerateur")
	best := SuggestBestBefore(cream, "frais", today)
	if best != today.AddDays(21) {
		t.Fatalf("date proposée : %s", best)
	}
	item, batch, err := e.s.Add(ctx, AddInput{ProductID: cream.ID, LocationID: fridge, Qty: 400, BestBefore: best, PriceCents: 240, Origin: FromShopping, Date: today, UserID: 0})
	if err != nil || batch == 0 {
		t.Fatal(err)
	}
	if up, _ := e.cat.UnitPrices(ctx, []int64{cream.ID}); catalog.Cost(up[cream.ID], 200) != 120 {
		t.Errorf("prix relevé : %v", up)
	}
	// Ouvert aujourd'hui : la date limite passe à la conservation après
	// ouverture (5 jours).
	openBatch, err := e.s.Open(ctx, item, today, 0)
	if err != nil {
		t.Fatal(err)
	}
	it, _ := e.s.Get(ctx, item)
	if it.BestBefore != today.AddDays(5) || it.OpenedOn != today {
		t.Errorf("après ouverture : %s, %s", it.BestBefore, it.OpenedOn)
	}
	if err := e.s.Undo(ctx, openBatch); err != nil {
		t.Fatal(err)
	}
	if it, _ := e.s.Get(ctx, item); it.BestBefore != best || it.OpenedOn != "" {
		t.Errorf("ouverture annulée : %s, %s", it.BestBefore, it.OpenedOn)
	}
	// −1 conditionnement, puis jeté : valeur de la perte d'après le prix payé.
	if _, err := e.s.Adjust(ctx, item, -200, Consumption, today, 0); err != nil {
		t.Fatal(err)
	}
	wasteBatch, err := e.s.Empty(ctx, item, Waste, today, 0)
	if err != nil || e.qty(item) != 0 {
		t.Fatalf("jeté : %v, reste %d", err, e.qty(item))
	}
	var value int64
	if err := e.d.R.QueryRowContext(ctx, `SELECT value_cents FROM stock_movements WHERE batch = ?`, wasteBatch).Scan(&value); err != nil || value != 120 {
		t.Errorf("valeur jetée : %d, %v", value, err)
	}
	if err := e.s.Undo(ctx, wasteBatch); err != nil || e.qty(item) != 200 {
		t.Errorf("annulation : %v, %d", err, e.qty(item))
	}
	if err := e.s.Undo(ctx, wasteBatch); err == nil {
		t.Error("double annulation acceptée")
	}
	// Déplacement d'une partie au congélateur : nouvelle date limite.
	moveBatch, err := e.s.Move(ctx, item, e.location("congelateur"), 100, "", today, 0)
	if err != nil {
		t.Fatal(err)
	}
	page, _ := e.s.List(ctx, Filter{ProductID: cream.ID})
	if page.Total != 2 {
		t.Fatalf("lots après déplacement : %d", page.Total)
	}
	for _, l := range page.Items {
		if l.Location == "Congélateur" && (l.Qty != 100 || l.BestBefore != today.AddDays(90)) {
			t.Errorf("lot congelé : %+v", l)
		}
	}
	if err := e.s.Undo(ctx, moveBatch); err != nil {
		t.Fatal(err)
	}
	if page, _ := e.s.List(ctx, Filter{ProductID: cream.ID}); page.Total != 1 || page.Items[0].Qty != 200 {
		t.Errorf("déplacement annulé : %+v", page.Items)
	}
}

func TestInventoryCorrectionAndFEFO(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	milk := e.product("lait")
	shelf := e.location("placard")
	old, _, _ := e.s.Add(ctx, AddInput{ProductID: milk.ID, LocationID: shelf, Qty: 1000, BestBefore: "2026-11-10", PriceCents: -1, Date: today})
	recent, _, _ := e.s.Add(ctx, AddInput{ProductID: milk.ID, LocationID: shelf, Qty: 6000, BestBefore: "2027-01-10", PriceCents: -1, Date: today})
	expired, _, _ := e.s.Add(ctx, AddInput{ProductID: milk.ID, LocationID: shelf, Qty: 500, BestBefore: "2026-11-01", PriceCents: -1, Date: today})
	// Inventaire : un écart seulement.
	_, changed, err := e.s.Correct(ctx, map[int64]int64{old: 1000, recent: 5000}, today, 0)
	if err != nil || changed != 1 || e.qty(recent) != 5000 {
		t.Fatalf("inventaire : %d écarts, %v", changed, err)
	}
	if total, _ := e.s.Total(ctx, milk.ID, today); total != 6000 {
		t.Errorf("total hors périmés : %d", total)
	}
	// Cuisine : 1,5 L pris d'abord dans le lot le plus proche de sa date.
	var short int64
	err = e.d.Tx(ctx, func(txx *sql.Tx) error {
		b, err := NextBatch(ctx, txx)
		if err != nil {
			return err
		}
		short, err = ConsumeFEFO(ctx, txx, milk.ID, 1500, Recipe, today, b, 0, 0)
		return err
	})
	if err != nil || short != 0 || e.qty(old) != 0 || e.qty(recent) != 4500 || e.qty(expired) != 500 {
		t.Errorf("FEFO : manque %d, lots %d/%d/%d, %v", short, e.qty(old), e.qty(recent), e.qty(expired), err)
	}
	soon, _ := e.s.Expiring(ctx, today.AddDays(3), 10)
	if len(soon) != 1 || soon[0].ID != expired {
		t.Errorf("à consommer en priorité : %+v", soon)
	}
	if exp := soon[0].ExpiryOn(today); exp.Class != "expiry-soon" || exp.Days != -6 {
		t.Errorf("DDM dépassée : %+v", exp)
	}
}

func TestMinimumAndTemplates(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	n, err := e.s.ApplyTemplate(ctx, "salle-de-bain")
	if err != nil || n != len(Templates[0].Items) {
		t.Fatalf("modèle : %d produits réglés, %v", n, err)
	}
	paste := e.product("dentifrice")
	st, _ := e.s.Settings(ctx, paste.ID)
	if st.Min != 75 || st.Target != 225 || !st.Track {
		t.Errorf("réglages : %+v", st)
	}
	// Consommations régulières : 75 ml toutes les 3 semaines.
	item, _, _ := e.s.Add(ctx, AddInput{ProductID: paste.ID, Qty: 300, PriceCents: -1, Date: "2026-08-01"})
	for _, d := range []calendar.Date{"2026-08-22", "2026-09-12", "2026-10-03"} {
		if _, err := e.s.Adjust(ctx, item, -75, Consumption, d, 0); err != nil {
			t.Fatal(err)
		}
	}
	if n, err := e.s.RefreshEstimates(ctx, today); err != nil || n != 1 {
		t.Fatalf("estimations : %d, %v", n, err)
	}
	watched, err := e.s.Watched(ctx, today)
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range watched {
		if w.ProductID == paste.ID && (w.Stock != 75 || w.DailyUse == 0) {
			t.Errorf("dentifrice suivi : %+v", w)
		}
	}
	salt := e.product("sel")
	if err := e.s.SetLow(ctx, salt.ID, true, today); err != nil {
		t.Fatal(err)
	}
	if st, _ := e.s.Settings(ctx, salt.ID); !st.Low || st.LowSince != today {
		t.Errorf("presque vide : %+v", st)
	}
	if _, _, err := e.s.Add(ctx, AddInput{ProductID: salt.ID, Qty: 1000, PriceCents: -1, Date: today}); err != nil {
		t.Fatal(err)
	}
	if st, _ := e.s.Settings(ctx, salt.ID); st.Low {
		t.Error("le réapprovisionnement doit retirer « presque vide »")
	}
}
