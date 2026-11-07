package catalog

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/codecolors/petitchef/internal/platform/db"
	"github.com/codecolors/petitchef/internal/platform/qty"
)

func TestSeedIsConsistent(t *testing.T) {
	seed, err := LoadSeed()
	if err != nil {
		t.Fatal(err)
	}
	if len(seed) < 800 {
		t.Errorf("base de départ : %d produits, 800 au moins attendus", len(seed))
	}
	aisles := []string{"fruits-legumes", "boulangerie", "boucherie", "poissonnerie", "charcuterie-traiteur", "cremerie", "fromages",
		"surgeles", "epicerie-salee", "condiments", "epicerie-sucree", "petit-dejeuner", "monde", "boissons", "bebe", "hygiene",
		"entretien", "maison", "animaux"}
	locations := []string{"refrigerateur", "congelateur", "placard", "cave", "fruits", "salle-de-bain", "buanderie", "garage"}
	for _, p := range SeedProblems(seed, aisles, locations) {
		t.Error(p)
	}
	perAisle := map[string]int{}
	for _, p := range seed {
		perAisle[p.Aisle]++
		if p.Nutrition != (Nutrition{}) || len(p.Barcodes) > 0 {
			t.Errorf("%s : valeurs nutritionnelles ou code-barres inventés", p.Name)
		}
	}
	for _, a := range aisles {
		if perAisle[a] == 0 {
			t.Errorf("rayon %s sans produit", a)
		}
	}
}

func TestSeedProblemsDetectsDuplicates(t *testing.T) {
	seed := []SeedProduct{
		{Key: "a", Aisle: "x", Product: Product{Name: "Patate", Search: "patate", Unit: qty.Gram, Universe: Food, Group: Vegetables}},
		{Key: "b", Aisle: "x", Product: Product{Name: "Pomme de terre", Search: "pomme de terre", Unit: qty.Gram, Universe: Food, Group: Vegetables, Synonyms: []string{"patates"}}},
		{Key: "b", Aisle: "y", Product: Product{Name: "Œuf", Search: "oeuf", Unit: qty.Piece, Universe: Food}},
	}
	got := SeedProblems(seed, []string{"x"}, nil)
	if len(got) != 5 { // synonyme pris, clé en double, rayon inconnu, pièce sans poids, groupe manquant
		t.Errorf("problèmes : %q", got)
	}
}

func TestNormalize(t *testing.T) {
	for in, want := range map[string]string{
		"Pommes de terre":       "pomme de terre",
		"pomme de terre":        "pomme de terre",
		"Œufs":                  "oeuf",
		"Choux de Bruxelles":    "chou de bruxelle",
		"Poireaux":              "poireau",
		"Crème fraîche épaisse": "creme fraiche epaisse",
		"Jus d'orange":          "jus d orange",
		"  Riz   basmati ":      "riz basmati",
	} {
		if got := Normalize(in); got != want {
			t.Errorf("Normalize(%q) = %q ; attendu %q", in, got, want)
		}
	}
}

func TestConversions(t *testing.T) {
	oil := Conv{Base: qty.Milli, Density: 915, Units: map[string]int64{"cs": 15}}
	sugar := Conv{Base: qty.Gram, Units: map[string]int64{"cs": 12}}
	onion := Conv{Base: qty.Gram, PieceG: 120}
	egg := Conv{Base: qty.Piece, PieceG: 60}
	lemon := Conv{Base: qty.Piece, PieceG: 120, Units: map[string]int64{"cs": 330}}
	flour := Conv{Base: qty.Gram}
	for _, c := range []struct {
		name string
		conv Conv
		in   int64
		unit string
		want int64
		ok   bool
	}{
		{"2 c. à soupe d'huile", oil, 2000, "cs", 30, true},
		{"1 L d'huile", oil, 1000, "l", 1000, true},
		{"100 g d'huile", oil, 100_000, "g", 109, true},
		{"3 c. à soupe de sucre", sugar, 3000, "cs", 36, true},
		{"1 oignon", onion, 1000, "piece", 120, true},
		{"½ oignon", onion, 500, "piece", 60, true},
		{"0,5 g d'oignon", onion, 500, "", 1, true}, // unité vide : unité de base (g)
		{"1,5 kg de farine", flour, 1500, "kg", 1500, true},
		{"2 œufs", egg, 2000, "piece", 2000, true},
		{"120 g d'œufs", egg, 120_000, "g", 2000, true},
		{"1 c. à soupe de citron", lemon, 1000, "cs", 330, true},
		{"1 pincée de farine", flour, 1000, "pincee", 1, true}, // 0,5 g arrondi
		{"1 gousse de farine", flour, 1000, "gousse", 0, false},
		{"1 pièce de farine", flour, 1000, "piece", 0, false},
		{"10 cl de crème (densité par défaut)", Conv{Base: qty.Gram}, 10_000, "cl", 100, true},
	} {
		got, ok := c.conv.ToBase(c.in, c.unit)
		if got != c.want || ok != c.ok {
			t.Errorf("%s : %d, %v ; attendu %d, %v", c.name, got, ok, c.want, c.ok)
		}
	}
	if g, ok := egg.Grams(1500); g != 90 || !ok {
		t.Errorf("1,5 œuf = %d g, %v", g, ok)
	}
	if g, _ := oil.Grams(1000); g != 915 {
		t.Errorf("1 L d'huile = %d g", g)
	}
}

func TestRoundToPackages(t *testing.T) {
	for _, c := range []struct {
		need             int64
		base             qty.Base
		packs            []int64
		buy, count, size int64
	}{
		{300, qty.Gram, []int64{250}, 500, 2, 250},
		{300, qty.Gram, []int64{250, 500}, 500, 1, 500},
		{1000, qty.Gram, []int64{250, 500}, 1000, 2, 500},
		{50, qty.Gram, nil, 50, 0, 0},
		{2500, qty.Piece, nil, 3000, 3, 1000},
		{13000, qty.Piece, []int64{6000, 12000}, 18000, 3, 6000},
		{0, qty.Gram, []int64{250}, 0, 0, 0},
	} {
		buy, count, size := RoundToPackages(c.need, c.base, c.packs)
		if buy != c.buy || count != c.count || size != c.size {
			t.Errorf("RoundToPackages(%d, %v) = %d, %d, %d ; attendu %d, %d, %d", c.need, c.packs, buy, count, size, c.buy, c.count, c.size)
		}
	}
}

func TestScalePieces(t *testing.T) {
	for _, c := range []struct {
		v    int64
		frac bool
		want int64
	}{
		{1370, false, 1000}, {1600, false, 2000}, {200, false, 1000}, {1370, true, 1500}, {200, true, 500}, {3000, false, 3000},
	} {
		if got := ScalePieces(c.v, c.frac); got != c.want {
			t.Errorf("ScalePieces(%d, %v) = %d ; attendu %d", c.v, c.frac, got, c.want)
		}
	}
}

func TestDietsAndSeason(t *testing.T) {
	ham := DefaultTraits(ColdCuts, 0) | Pork
	if Compatible(Vegetarian, ham, 0) || Compatible(PorkFree, ham, 0) || !Compatible(GlutenFree, ham, 0) {
		t.Error("jambon : compatibilités erronées")
	}
	cheese := DefaultTraits(Cheese, Milk)
	if !Compatible(Vegetarian, cheese, Milk) || Compatible(Vegan, cheese, Milk) || Compatible(LactoseFree, cheese, Milk) {
		t.Error("fromage : compatibilités erronées")
	}
	if got := MonthRange(11, 2).Label(); got != "nov. à févr." {
		t.Errorf("saison à cheval : %q", got)
	}
	if got := MonthRange(6, 9).Label(); got != "juin à sept." {
		t.Errorf("saison : %q", got)
	}
	if !MonthRange(11, 2).Has(1) || MonthRange(11, 2).Has(5) {
		t.Error("Has")
	}
}

func openDB(t testing.TB) *db.DB {
	t.Helper()
	d, err := db.Open(t.Context(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return d
}

func TestSyncSeedKeepsUserChanges(t *testing.T) {
	ctx := t.Context()
	d := openDB(t)
	if err := SyncSeed(ctx, d); err != nil {
		t.Fatal(err)
	}
	s := NewService(d)
	var n int
	if err := d.R.QueryRowContext(ctx, `SELECT count(*) FROM products WHERE origin = 'seed'`).Scan(&n); err != nil || n < 800 {
		t.Fatalf("produits chargés : %d, %v", n, err)
	}
	id, err := s.Resolve(ctx, "patates")
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.Get(ctx, id)
	if err != nil || p.Name != "Pomme de terre" || len(p.Packages) == 0 {
		t.Fatalf("pomme de terre : %+v, %v", p, err)
	}
	// L'utilisateur modifie le produit ; une nouvelle version de la base
	// ne doit pas l'écraser, mais doit restaurer un produit non modifié.
	p.Name = "Pomme de terre de mon jardin"
	if err := s.Update(ctx, p); err != nil {
		t.Fatal(err)
	}
	carrot, _ := s.Resolve(ctx, "carotte")
	if _, err := d.W.ExecContext(ctx, `UPDATE products SET name = 'Carotte abîmée' WHERE id = ?`, carrot); err != nil {
		t.Fatal(err)
	}
	if _, err := d.W.ExecContext(ctx, `UPDATE settings SET value = '0' WHERE key = 'catalog_seed_version'`); err != nil {
		t.Fatal(err)
	}
	if err := SyncSeed(ctx, d); err != nil {
		t.Fatal(err)
	}
	if p2, _ := s.Get(ctx, id); p2.Name != "Pomme de terre de mon jardin" || !p2.Modified {
		t.Errorf("produit modifié écrasé : %q", p2.Name)
	}
	if c, _ := s.Get(ctx, carrot); c.Name != "Carotte" {
		t.Errorf("produit de base non restauré : %q", c.Name)
	}
}

func TestSearchAndQuickCreate(t *testing.T) {
	ctx := t.Context()
	d := openDB(t)
	if err := SyncSeed(ctx, d); err != nil {
		t.Fatal(err)
	}
	s := NewService(d)
	hits, err := s.Search(ctx, "patate", 5)
	if err != nil || len(hits) == 0 {
		t.Fatalf("recherche : %v, %v", hits, err)
	}
	names := make([]string, len(hits))
	for i, h := range hits {
		names[i] = h.Name
	}
	if !slices.Contains(names, "Pomme de terre") || !slices.Contains(names, "Patate douce") {
		t.Errorf("recherche « patate » : %v", names)
	}
	if hits, _ := s.Search(ctx, "creme fraiche", 3); len(hits) < 2 || !strings.HasPrefix(hits[0].Name, "Crème fraîche") {
		t.Errorf("recherche sans accents : %+v", hits)
	}
	id, err := s.QuickCreate(ctx, "yaourt à la vanille bourbon")
	if err != nil {
		t.Fatal(err)
	}
	p, _ := s.Get(ctx, id)
	if p.AisleName != "Crèmerie" || !p.Incomplete {
		t.Errorf("produit créé à la volée : rayon %q, à compléter %v", p.AisleName, p.Incomplete)
	}
	if again, _ := s.QuickCreate(ctx, "Yaourt à la vanille bourbon"); again != id {
		t.Error("doublon créé")
	}
	if err := s.AttachBarcode(ctx, id, "3 017620 422003"); err != nil {
		t.Fatal(err)
	}
	if got, err := s.ByBarcode(ctx, "3017620422003"); err != nil || got != id {
		t.Errorf("code-barres : %d, %v", got, err)
	}
	page, err := s.List(ctx, Filter{Incomplete: true})
	if err != nil || page.Total != 1 {
		t.Errorf("produits à compléter : %d, %v", page.Total, err)
	}
}

func TestStoreOrder(t *testing.T) {
	ctx := t.Context()
	d := openDB(t)
	s := NewService(d)
	store, err := s.SaveStore(ctx, 0, "Leclerc", true)
	if err != nil {
		t.Fatal(err)
	}
	aisles, _ := s.StoreAisles(ctx, store)
	if aisles[0].Slug != "fruits-legumes" {
		t.Fatalf("ordre par défaut : %s", aisles[0].Slug)
	}
	if err := s.MoveAisle(ctx, store, aisles[1].ID, -1); err != nil {
		t.Fatal(err)
	}
	aisles, _ = s.StoreAisles(ctx, store)
	if aisles[0].Slug != "boulangerie" || aisles[1].Slug != "fruits-legumes" || len(aisles) != 19 {
		t.Errorf("ordre après déplacement : %s, %s (%d)", aisles[0].Slug, aisles[1].Slug, len(aisles))
	}
}

func BenchmarkSyncSeed(b *testing.B) {
	for b.Loop() {
		d := openDB(b)
		if err := SyncSeed(b.Context(), d); err != nil {
			b.Fatal(err)
		}
	}
}
