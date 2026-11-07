package recipes

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/codecolors/petitchef/internal/catalog"
	"github.com/codecolors/petitchef/internal/household"
	"github.com/codecolors/petitchef/internal/platform/db"
	"github.com/codecolors/petitchef/internal/platform/qty"
)

func TestParseLine(t *testing.T) {
	for _, c := range []struct {
		in    string
		milli int64
		unit  string
		name  string
		note  string
		opt   bool
		taste bool
		sub   bool
	}{
		{"200 g de farine", 200_000, "g", "farine", "", false, false, false},
		{"200g farine", 200_000, "g", "farine", "", false, false, false},
		{"3 œufs", 3000, "", "œufs", "", false, false, false},
		{"1 pincée de sel", 1000, "pincee", "sel", "", false, false, false},
		{"2 c. à soupe d'huile d'olive", 2000, "cs", "huile d'olive", "", false, false, false},
		{"2 cuillères à café de cumin", 2000, "cc", "cumin", "", false, false, false},
		{"½ citron", 500, "", "citron", "", false, false, false},
		{"1/2 botte de persil", 500, "botte", "persil", "", false, false, false},
		{"1,5 kg de pommes de terre", 1500, "kg", "pommes de terre", "", false, false, false},
		{"20 cl de crème liquide", 20_000, "cl", "crème liquide", "", false, false, false},
		{"1 oignon (émincé)", 1000, "", "oignon", "émincé", false, false, false},
		{"Sel, poivre", 0, "", "Sel", "poivre", false, true, false},
		{"Poivre noir moulu", 0, "", "Poivre noir moulu", "", false, true, false},
		{"50 g de noix (facultatif)", 50_000, "g", "noix", "", true, false, false},
		{"1 c. à café de sucre (au goût)", 1000, "cc", "sucre", "", false, true, false},
		{"1 [Pâte brisée maison]", 1000, "", "Pâte brisée maison", "", false, false, true},
		{"1 laitue", 1000, "", "laitue", "", false, false, false},
		{"2 gousses d'ail", 2000, "gousse", "ail", "", false, false, false},
	} {
		l := ParseLine(c.in)
		if l.Milli != c.milli || l.Unit != c.unit || l.Name != c.name || l.Note != c.note || l.Optional != c.opt || l.ToTaste != c.taste || l.SubRecipe != c.sub {
			t.Errorf("ParseLine(%q) = %+v", c.in, l)
		}
	}
}

func TestParseLinesSplitsCommasAndGroups(t *testing.T) {
	lines := ParseLines("200 g de farine, 3 œufs, 1 pincée de sel")
	if len(lines) != 3 || lines[1].Milli != 3000 || lines[2].Unit != "pincee" {
		t.Errorf("liste en ligne : %+v", lines)
	}
	lines = ParseLines("Pour la pâte :\n250 g farine\n1,5 cl d'eau\n## Garniture\n- 3 pommes")
	if len(lines) != 3 || lines[0].Group != "Pour la pâte" || lines[1].Milli != 1500 || lines[2].Group != "Garniture" {
		t.Errorf("regroupements : %+v", lines)
	}
}

func TestScaleQty(t *testing.T) {
	egg := Ingredient{ProductID: 1, Qty: 3000, Unit: qty.Piece}
	if got := ScaleQty(egg, 4, 6000); got != 5000 { // 4,5 œufs → 5 (pas de demi-œuf)
		t.Errorf("œufs pour 6 : %d", got)
	}
	lemon := Ingredient{ProductID: 2, Qty: 500, Unit: qty.Piece, PieceFrac: true}
	if got := ScaleQty(lemon, 4, 6000); got != 1000 { // ¾ → 1 (demi-pièces)
		t.Errorf("citron pour 6 : %d", got)
	}
	if got := ScaleQty(lemon, 4, 2500); got != 500 {
		t.Errorf("citron pour 2,5 : %d", got)
	}
	flour := Ingredient{ProductID: 3, Qty: 250, Unit: qty.Gram}
	if got := ScaleQty(flour, 4, 2500); got != 156 {
		t.Errorf("farine pour 2,5 : %d", got)
	}
}

func TestExpandAndCycles(t *testing.T) {
	recipes := map[int64]Recipe{
		1: {ID: 1, Title: "Quiche", Servings: 6, Ingredients: []Ingredient{
			{SubRecipe: 2, Qty: 1000}, {ProductID: 10, Qty: 200, Unit: qty.Gram},
		}},
		2: {ID: 2, Title: "Pâte brisée", Servings: 1, Ingredients: []Ingredient{{ProductID: 20, Qty: 250, Unit: qty.Gram}}},
		3: {ID: 3, Title: "A", Servings: 1, Ingredients: []Ingredient{{SubRecipe: 4, Qty: 1000}}},
		4: {ID: 4, Title: "B", Servings: 1, Ingredients: []Ingredient{{SubRecipe: 3, Qty: 1000}}},
	}
	load := func(id int64) (Recipe, error) { return recipes[id], nil }
	needs, err := Expand(recipes[1], 12000, load) // double de la recette
	if err != nil || len(needs) != 2 || needs[0].ProductID != 20 || needs[0].Qty != 500 || needs[0].Via != "Pâte brisée" || needs[1].Qty != 400 {
		t.Errorf("déroulé : %+v, %v", needs, err)
	}
	if _, err := Expand(recipes[3], 1000, load); !errors.Is(err, ErrCycle) {
		t.Errorf("boucle non détectée : %v", err)
	}
	if err := CheckCycle(2, 1, load); !errors.Is(err, ErrCycle) {
		t.Errorf("utiliser la quiche dans la pâte doit boucler : %v", err)
	}
	if err := CheckCycle(1, 2, load); err != nil {
		t.Errorf("pâte dans la quiche : %v", err)
	}
}

func TestComputeBalance(t *testing.T) {
	g := DefaultGrid()
	ratatouille := ComputeBalance(map[catalog.Group]int64{catalog.Vegetables: 1800, catalog.Fats: 75, catalog.Condiments: 10}, g)
	if ratatouille.Grade != "A" || ratatouille.Contributions[0].Group != catalog.Vegetables {
		t.Errorf("ratatouille : %+v", ratatouille)
	}
	cake := ComputeBalance(map[catalog.Group]int64{catalog.Sweets: 500, catalog.Fats: 300, catalog.RefinedStarch: 250, catalog.Eggs: 200}, g)
	if cake.Grade != "E" {
		t.Errorf("gâteau : %+v", cake)
	}
	if ComputeBalance(nil, g).Grade != "C" {
		t.Error("recette sans poids : C attendu")
	}
	if g.Letter(35) != "A" || g.Letter(34) != "B" || g.Letter(-26) != "E" {
		t.Error("seuils")
	}
	bad := g
	bad.Thresholds = [4]int{10, 20, 0, -10}
	if bad.Validate() == nil {
		t.Error("seuils croissants acceptés")
	}
}

func newService(t testing.TB) (*Service, *db.DB) {
	t.Helper()
	d, err := db.Open(t.Context(), filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	if err := catalog.SyncSeed(t.Context(), d); err != nil {
		t.Fatal(err)
	}
	return NewService(d, catalog.NewService(d)), d
}

func TestSeedRecipes(t *testing.T) {
	s, d := newService(t)
	ctx := t.Context()
	if err := s.SyncSeed(ctx); err != nil {
		t.Fatal(err)
	}
	var n, veg int
	if err := d.R.QueryRowContext(ctx, `SELECT count(*), sum(traits & 1 = 0) FROM recipes`).Scan(&n, &veg); err != nil || n < 60 || veg < 15 {
		t.Fatalf("recettes : %d dont %d végétariennes, %v", n, veg, err)
	}
	for _, typ := range []Type{Starter, Main, Dessert} {
		if p, _ := s.Search(ctx, Filter{Type: typ}); p.Total < 10 {
			t.Errorf("type %d : %d recettes", typ, p.Total)
		}
	}
	// Recherche insensible aux accents, recette composée et indicateurs calculés.
	page, err := s.Search(ctx, Filter{Query: "quiche lorr"})
	if err != nil || page.Total != 1 {
		t.Fatalf("recherche : %+v, %v", page, err)
	}
	q, _ := s.Get(ctx, page.Items[0].ID)
	if !q.Allergens.Has(catalog.Gluten) || !q.Allergens.Has(catalog.Milk) || q.Traits&catalog.Pork == 0 || q.Grade() > "C" && q.Grade() < "D" {
		t.Errorf("quiche : allergènes %v, traits %v, indice %s", q.Allergens.Labels(), q.Traits, q.Grade())
	}
	needs, err := s.Needs(ctx, q.ID, 3000) // demi-quiche
	if err != nil {
		t.Fatal(err)
	}
	flour, _ := s.catalog.Resolve(ctx, "farine")
	found := false
	for _, nd := range needs {
		if nd.ProductID == flour && nd.Qty == 125 && nd.Via == "Pâte brisée maison" {
			found = true
		}
	}
	if !found {
		t.Errorf("farine de la pâte (125 g attendus) : %+v", needs)
	}
	if p, _ := s.Search(ctx, Filter{Query: "creme", Diet: catalog.Vegetarian}); p.Total == 0 {
		t.Error("recherche « creme » végétarienne vide")
	}
	if p, _ := s.Search(ctx, Filter{Diet: catalog.Vegan, Type: Main}); p.Total < 3 {
		t.Errorf("plats végans : %d", p.Total)
	}
	rat, _ := s.ByTitle(ctx, "ratatouille")
	r, _ := s.Get(ctx, rat)
	if r.Grade() != "A" || r.Protein != "" || r.Season.Has(1) || !r.Season.Has(7) {
		t.Errorf("ratatouille : indice %s, protéine %q, saison %s", r.Grade(), r.Protein, r.Season.Label())
	}
	// Une deuxième synchronisation ne change rien.
	if err := s.SyncSeed(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestSaveFormatAndRatings(t *testing.T) {
	s, d := newService(t)
	ctx := t.Context()
	lines := ParseLines("3 œufs\n2 c. à soupe d'huile d'olive\n1 oignon émincé\nsel")
	res, err := s.Resolve(ctx, lines)
	if err != nil {
		t.Fatal(err)
	}
	if res[2].Confidence != Partial || res[2].Note != "émincé" {
		t.Errorf("oignon émincé : %+v", res[2])
	}
	id, err := s.Save(ctx, 0, Input{Title: "Omelette à l'oignon", Types: Main, Servings: 2, Ingredients: res, Steps: []string{"Cuire 10 min."}})
	if err != nil {
		t.Fatal(err)
	}
	r, _ := s.Get(ctx, id)
	if r.Ingredients[1].Qty != 30 || r.Ingredients[0].Qty != 3000 || r.Steps[0].TimerSec != 600 {
		t.Errorf("enregistrée : %+v", r.Ingredients)
	}
	again, _ := s.Resolve(ctx, ParseLines(FormatLines(r.Ingredients)))
	for i, x := range again {
		if x.ProductID != r.Ingredients[i].ProductID || x.Milli != r.Ingredients[i].InputQty {
			t.Errorf("relecture de « %s » : %+v", FormatLine(r.Ingredients[i]), x)
		}
	}
	m, err := household.NewService(d).Save(ctx, household.Member{Name: "Léa", Portion: 1000})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Rate(ctx, id, Rating{MemberID: m, Stars: 4, Favorite: true}); err != nil {
		t.Fatal(err)
	}
	ops, _ := s.Opinions(ctx)
	if ops[id].Stars != 40 || !ops[id].Favorite {
		t.Errorf("avis : %+v", ops[id])
	}
	dup, err := s.Duplicate(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := s.Get(ctx, dup); v.VariantOf != id || len(v.Ingredients) != 4 {
		t.Errorf("variante : %+v", v)
	}
	if err := s.Delete(ctx, dup); err != nil {
		t.Fatal(err)
	}
}

func TestImportExport(t *testing.T) {
	s, _ := newService(t)
	ctx := t.Context()
	if err := s.SyncSeed(ctx); err != nil {
		t.Fatal(err)
	}
	quiche, _ := s.ByTitle(ctx, "quiche lorraine")
	var buf strings.Builder
	if err := s.Export(ctx, &buf, []int64{quiche}); err != nil {
		t.Fatal(err)
	}
	var f File
	if err := json.Unmarshal([]byte(buf.String()), &f); err != nil || len(f.Recipes) != 2 || f.Recipes[0].Title != "Pâte brisée maison" {
		t.Fatalf("export (la pâte d'abord) : %+v, %v", f.Recipes, err)
	}
	// Réimport dans une base vide (sans le jeu de départ des recettes).
	s2, _ := newService(t)
	res, err := s2.Import(ctx, strings.NewReader(buf.String()))
	if err != nil || res.Created != 2 || res.Products != 0 {
		t.Fatalf("import : %+v, %v", res, err)
	}
	if res, _ := s2.Import(ctx, strings.NewReader(buf.String())); res.Created != 0 || len(res.Skipped) != 2 {
		t.Errorf("réimport : %+v", res)
	}
	ld := `{"@context":"https://schema.org","@graph":[{"@type":"WebPage"},{"@type":["Recipe"],"name":"Soupe express",
		"recipeYield":["2","2 bols"],"prepTime":"PT10M","cookTime":"PT1H5M","recipeCategory":"Entrée",
		"recipeIngredient":["2 carottes","1 l de bouillon de légumes","1 pincée de truc mystère"],
		"recipeInstructions":[{"@type":"HowToSection","itemListElement":[{"@type":"HowToStep","text":"Couper."},{"@type":"HowToStep","text":"Cuire."}]}]}]}`
	res, err = s2.Import(ctx, strings.NewReader(ld))
	if err != nil || res.Created != 1 || res.Products != 1 {
		t.Fatalf("import schema.org : %+v, %v", res, err)
	}
	id, _ := s2.ByTitle(ctx, "Soupe express")
	r, _ := s2.Get(ctx, id)
	if r.Servings != 2 || r.CookMin != 65 || !r.Types.Has(Starter) || len(r.Steps) != 2 || len(r.Ingredients) != 3 {
		t.Errorf("recette importée : %+v", r)
	}
	if out := SchemaOrg(r); out["cookTime"] != "PT1H5M" || out["@type"] != "Recipe" {
		t.Errorf("schema.org : %+v", out)
	}
}
