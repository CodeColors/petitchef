package library_test

import (
	"path/filepath"
	"sort"
	"strconv"
	"testing"

	"github.com/codecolors/petitchef/internal/catalog"
	"github.com/codecolors/petitchef/internal/platform/db"
	"github.com/codecolors/petitchef/internal/recipes"
	"github.com/codecolors/petitchef/internal/recipes/library"
)

func open(t *testing.T) (*db.DB, *catalog.Service, *recipes.Service) {
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
	return d, c, recipes.NewService(d, c)
}

// TestLibrary vérifie toute la bibliothèque : titres uniques, chaque
// ingrédient relié exactement au catalogue, chaque quantité convertible
// dans l'unité de base du produit, chaque produit achetable dans un
// conditionnement du commerce, et une répartition entrées / plats /
// desserts suffisante pour composer des menus variés.
func TestLibrary(t *testing.T) {
	d, c, r := open(t)
	ctx := t.Context()
	list, err := recipes.LibraryRecipes(library.FS())
	if err != nil {
		t.Fatal(err)
	}
	if len(list) < 500 {
		t.Errorf("%d recettes, 500 au moins attendues", len(list))
	}
	seen := map[string]bool{}
	var problems []string
	for _, x := range list {
		title := recipes.LibraryTitle(x)
		key := recipes.LibraryKey(title)
		if seen[key] {
			problems = append(problems, "titre en double : "+title)
		}
		seen[key] = true
		in, err := r.LibraryInput(ctx, x)
		if err == nil {
			_, err = r.Save(ctx, 0, in)
		}
		if err != nil {
			problems = append(problems, err.Error())
		}
	}
	sort.Strings(problems)
	for _, p := range problems {
		t.Error(p)
	}
	if t.Failed() {
		return
	}
	// Quantités converties : rien de saisi qui vaille zéro.
	rows, err := db.Collect(ctx, d.R, func(s db.Scanner) (string, error) {
		var title, product, unit string
		var milli int64
		err := s.Scan(&title, &product, &milli, &unit)
		return title + " : " + product + " (" + unit + ")", err
	}, `SELECT r.title, p.name, i.input_qty, i.input_unit FROM recipe_ingredients i JOIN recipes r ON r.id = i.recipe_id
		JOIN products p ON p.id = i.product_id WHERE i.input_qty > 0 AND i.qty = 0`)
	if err != nil {
		t.Fatal(err)
	}
	for _, x := range rows {
		t.Error("quantité non convertible : " + x)
	}
	// Chaque produit utilisé s'achète : conditionnement, botte ou pièce.
	used, err := db.Collect(ctx, d.R, func(s db.Scanner) (int64, error) {
		var id int64
		return id, s.Scan(&id)
	}, `SELECT DISTINCT product_id FROM recipe_ingredients WHERE product_id IS NOT NULL`)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range used {
		p, err := c.Get(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if p.Unit != "piece" && len(p.BuyUnits()) == 0 {
			t.Errorf("%s : aucun conditionnement du commerce", p.Name)
		}
	}
	// Répartition par type et par indice d'équilibre.
	count := func(q string) int {
		var n int
		if err := d.R.QueryRowContext(ctx, q).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	for _, x := range []struct {
		label string
		bit   recipes.Type
		min   int
	}{{"entrées", recipes.Starter, 80}, {"plats", recipes.Main, 250}, {"desserts", recipes.Dessert, 80}} {
		if n := count("SELECT count(*) FROM recipes WHERE types & " + itoa(int(x.bit)) + " != 0"); n < x.min {
			t.Errorf("%d %s, %d au moins attendus", n, x.label, x.min)
		}
	}
	balanced := count(`SELECT count(*) FROM recipes WHERE balance_grade IN ('A', 'B')`)
	rich := count(`SELECT count(*) FROM recipes WHERE balance_grade IN ('D', 'E')`)
	if balanced < 200 || rich < 40 {
		t.Errorf("équilibrées (A, B) : %d, gourmandes (D, E) : %d", balanced, rich)
	}
	t.Logf("%d recettes ; A-B %d, D-E %d", len(list), balanced, rich)
}

// TestSyncLibraryOnce : la bibliothèque n'est chargée qu'une fois, et une
// recette supprimée ne revient pas.
func TestSyncLibraryOnce(t *testing.T) {
	d, _, r := open(t)
	ctx := t.Context()
	n, err := r.SyncLibrary(ctx, library.FS(), 1)
	if err != nil || n < 500 {
		t.Fatalf("%d recettes chargées, %v", n, err)
	}
	id, err := r.ByTitle(ctx, "Raclette")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.DeleteMany(ctx, []int64{id}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.W.ExecContext(ctx, `DELETE FROM settings WHERE key = 'recipe_library_version'`); err != nil {
		t.Fatal(err)
	}
	if again, err := r.SyncLibrary(ctx, library.FS(), 2); err != nil || again != 0 {
		t.Fatalf("second chargement : %d recettes, %v", again, err)
	}
	if _, err := r.ByTitle(ctx, "Raclette"); err == nil {
		t.Error("une recette supprimée est revenue")
	}
	if p, err := r.PurgeSeed(ctx); err != nil || p != 0 {
		t.Errorf("la purge de l'ancien jeu ne doit pas toucher la bibliothèque : %d, %v", p, err)
	}
}

func itoa(n int) string { return strconv.Itoa(n) }
