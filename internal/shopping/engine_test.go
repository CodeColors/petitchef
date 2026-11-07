package shopping

import (
	"slices"
	"strings"
	"testing"

	"github.com/codecolors/petitchef/internal/catalog"
	"github.com/codecolors/petitchef/internal/platform/calendar"
	"github.com/codecolors/petitchef/internal/platform/qty"
	"github.com/codecolors/petitchef/internal/stock"
)

const today = calendar.Date("2026-11-07")

var products = map[int64]catalog.Product{
	1: {ID: 1, Name: "Crème fraîche", Unit: qty.Gram, AisleID: 6, Packages: []catalog.Package{{Qty: 200}, {Qty: 400}}},
	2: {ID: 2, Name: "Courgette", Unit: qty.Gram, AisleID: 1, Packages: []catalog.Package{{Qty: 1000}}},
	3: {ID: 3, Name: "Dentifrice", Unit: qty.Milli, AisleID: 16, Packages: []catalog.Package{{Qty: 75}}},
	4: {ID: 4, Name: "Œufs", Unit: qty.Piece, AisleID: 6, Packages: []catalog.Package{{Qty: 6000}, {Qty: 12000}}},
	5: {ID: 5, Name: "Sel fin", Unit: qty.Gram, AisleID: 10, Staple: true, Packages: []catalog.Package{{Qty: 1000}}},
	6: {ID: 6, Name: "Liquide vaisselle", Unit: qty.Milli, AisleID: 17, Packages: []catalog.Package{{Qty: 500}}},
	7: {ID: 7, Name: "Citron", Unit: qty.Piece, AisleID: 1},
}

func find(lines []Line, id int64) (Line, bool) {
	for _, l := range lines {
		if l.ProductID == id {
			return l, true
		}
	}
	return Line{}, false
}

func TestMenuNeedsMinusStock(t *testing.T) {
	in := Input{Today: today, Until: today.AddDays(7), Products: products,
		Needs: []MenuNeed{
			{ProductID: 1, Qty: 400, Date: "2026-11-10", Label: "le gratin de mardi"},
			{ProductID: 1, Qty: 200, Date: "2026-11-12", Label: "la quiche de jeudi"},
			{ProductID: 2, Qty: 600, Date: "2026-11-09", Label: "la ratatouille de lundi"},
			{ProductID: 4, Qty: 3000, Date: "2026-11-11", Label: "l'omelette de mercredi"},
			{ProductID: 7, Qty: 500, Date: "2026-11-11", Label: "le poisson de mercredi"},
		},
		Lots: map[int64][]stock.Lot{
			// 300 g de crème : 200 g bons jusqu'au 11, 100 g périmés le 9.
			1: {{ID: 1, Qty: 200, BestBefore: "2026-11-11"}, {ID: 2, Qty: 100, BestBefore: "2026-11-09"}},
			2: {{ID: 3, Qty: 1000, BestBefore: "2026-11-20"}},
			4: {{ID: 4, Qty: 2000, BestBefore: "2026-11-30"}},
		},
		Order: map[int64]int{6: 1, 1: 2}}
	lines := Compute(in)
	cream, ok := find(lines, 1)
	// Gratin : 200 g en stock (le lot périmé le 9 ne compte pas) → 200 g à
	// acheter ; quiche du 12 : le lot du 11 est déjà réservé → 200 g.
	if !ok || cream.Need != 400 || cream.Buy != 400 || cream.Count != 1 || cream.Sources != FromMenu {
		t.Fatalf("crème : %+v", cream)
	}
	if !strings.Contains(strings.Join(cream.Details, " + "), "400 g pour le gratin de mardi + 200 g pour la quiche de jeudi") {
		t.Errorf("détail : %v", cream.Details)
	}
	if _, ok := find(lines, 2); ok {
		t.Error("courgettes en stock : rien à acheter")
	}
	if eggs, _ := find(lines, 4); eggs.Need != 1000 || eggs.Buy != 6000 {
		t.Errorf("œufs : %+v", eggs)
	}
	if lemon, _ := find(lines, 7); lemon.Buy != 1000 || lemon.Count != 1 {
		t.Errorf("demi-citron arrondi à un citron : %+v", lemon)
	}
	// Tri dans l'ordre du magasin : crèmerie avant fruits et légumes.
	if lines[0].AisleID != 6 || lines[len(lines)-1].AisleID != 1 {
		t.Errorf("ordre des rayons : %v", lines)
	}
}

func TestMinimumForecastManualAndMerge(t *testing.T) {
	in := Input{Today: today, Until: today.AddDays(7), Products: products,
		Lots: map[int64][]stock.Lot{
			3: {{ID: 1, Qty: 50}},  // dentifrice sous le minimum
			6: {{ID: 2, Qty: 320}}, // liquide vaisselle : au-dessus du minimum aujourd'hui…
			4: {{ID: 3, Qty: 6000}},
		},
		Watched: []stock.Watched{
			{Settings: stock.Settings{ProductID: 3, Min: 75, Target: 225}},
			// … mais 1 bouteille de 500 ml en 5 semaines : il passera sous
			// 250 ml avant les courses suivantes.
			{Settings: stock.Settings{ProductID: 6, Min: 250, Target: 1000, DailyUse: 500 * 1000 / 35}},
			{Settings: stock.Settings{ProductID: 5, Low: true}},
			{Settings: stock.Settings{ProductID: 4, Min: 6000, Target: 12000}},
		},
		Needs:  []MenuNeed{{ProductID: 4, Qty: 4000, Date: "2026-11-09", Label: "le clafoutis de lundi"}},
		Manual: []Manual{{ProductID: 4, Qty: 0}, {ProductID: 2, Qty: 1500}},
	}
	lines := Compute(in)
	if l, _ := find(lines, 3); l.Need != 175 || l.Buy != 225 || l.Count != 3 || !l.Sources.Has(FromMinimum) {
		t.Errorf("dentifrice : %+v", l)
	}
	if l, ok := find(lines, 6); !ok || l.Need != 1000-stock.Projected(320, 500*1000/35, 7) || l.Buy != 1000 || !strings.Contains(l.Details[0], "passera sous le minimum") {
		t.Errorf("liquide vaisselle (rupture prévue) : %+v", l)
	}
	if l, _ := find(lines, 5); l.Buy != 1000 || !l.Sources.Has(FromLow) {
		t.Errorf("sel presque vide : %+v", l)
	}
	// Œufs : menu (6 en stock, 4 utilisés, rien à acheter) + minimum (2
	// restants < 6 → cible 12, soit 10) + ajout manuel sans quantité (une
	// boîte de 6) : une seule ligne fusionnée.
	eggs, _ := find(lines, 4)
	if eggs.Need != 10000+6000 || eggs.Sources != FromMinimum|FromManual || eggs.Buy != 18000 {
		t.Errorf("œufs fusionnés : %+v", eggs)
	}
	if l, _ := find(lines, 2); l.Buy != 2000 || l.Count != 2 {
		t.Errorf("ajout manuel 1,5 kg de courgettes : %+v", l)
	}
	names := make([]string, len(lines))
	for i, l := range lines {
		names[i] = l.Name
	}
	if !slices.IsSortedFunc(lines, func(a, b Line) int { return int(a.AisleID - b.AisleID) }) {
		t.Errorf("ordre par défaut des rayons : %v", names)
	}
}

func BenchmarkCompute(b *testing.B) {
	in := Input{Today: today, Until: today.AddDays(28), Products: map[int64]catalog.Product{}, Lots: map[int64][]stock.Lot{}}
	for id := int64(1); id <= 300; id++ {
		in.Products[id] = catalog.Product{ID: id, Name: "Produit", Unit: qty.Gram, AisleID: id % 19, Packages: []catalog.Package{{Qty: 250}, {Qty: 1000}}}
	}
	for k := range 1000 {
		id := int64(1 + k%300)
		in.Lots[id] = append(in.Lots[id], stock.Lot{ID: int64(k), Qty: 300, BestBefore: today.AddDays(k % 40)})
	}
	for d := range 28 {
		for k := range 30 {
			in.Needs = append(in.Needs, MenuNeed{ProductID: int64(1 + (d*31+k*7)%300), Qty: 150, Date: today.AddDays(d), Label: "un plat"})
		}
	}
	for b.Loop() {
		Compute(in)
	}
}
