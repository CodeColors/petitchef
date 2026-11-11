package web

import (
	"context"
	"net/url"
	"strings"
	"testing"

	"github.com/codecolors/petitchef/internal/platform/db"
	"github.com/codecolors/petitchef/internal/recipes"
)

// fakeCatalog : « Farine » (id 1, en grammes) et « Œuf » (id 2, à la pièce,
// poids inconnu).
type fakeCatalog struct{ created []string }

func (f *fakeCatalog) resolveName(_ context.Context, name string) (int64, error) {
	switch strings.ToLower(name) {
	case "farine":
		return 1, nil
	case "œuf":
		return 2, nil
	}
	return 0, db.ErrNotFound
}

func (f *fakeCatalog) baseQty(_ context.Context, id, milli int64, unit string) (int64, error) {
	switch {
	case id == 1 && (unit == "g" || unit == "cs"):
		return milli / 1000, nil
	case id == 2 && unit == "piece":
		return milli, nil
	}
	return 0, nil
}

func (f *fakeCatalog) createOther(_ context.Context, name string) (int64, error) {
	f.created = append(f.created, name)
	return 100 + int64(len(f.created)), nil
}

func TestParseRows(t *testing.T) {
	form := url.Values{
		"ing_kind_3": {"produit"}, "ing_qty_3": {"3"}, "ing_unit_3": {"piece"}, "ing_product_3": {"Œuf"},
		"ing_kind_0": {"produit"}, "ing_qty_0": {"200"}, "ing_unit_0": {"g"}, "ing_product_0": {" Farine "},
		"ing_kind_1": {"autre"}, "ing_qty_1": {"1"}, "ing_unit_1": {"autre"}, "ing_unit_other_1": {"cube"}, "ing_other_1": {"Bouillon"},
		"ing_kind_x": {"produit"},
	}
	rows := parseRows(form, -1)
	if len(rows) != 3 || rows[0].Product != "Farine" || rows[1].Other != "Bouillon" || rows[2].Product != "Œuf" {
		t.Fatalf("lignes = %+v", rows)
	}
	for i, r := range rows {
		if r.Index != i {
			t.Errorf("index %d = %d", i, r.Index)
		}
	}
	if rows = parseRows(form, 1); len(rows) != 2 || rows[1].Product != "Œuf" {
		t.Fatalf("retrait : %+v", rows)
	}
}

func TestCheckRows(t *testing.T) {
	ctx := context.Background()
	row := func(q, unit, kind, name string) ingRow {
		r := ingRow{Qty: q, Unit: unit, Kind: kind}
		switch kind {
		case kindOther:
			r.Other = name
		default:
			r.Product = name
		}
		return r
	}
	t.Run("valide", func(t *testing.T) {
		f := &fakeCatalog{}
		other := row("1", unitOther, kindOther, "Bouillon de légumes")
		other.UnitOther = "Cube"
		rows := []ingRow{row("200", "g", kindProduct, "Farine"), row("", "piece", kindProduct, ""), other,
			{Unit: "piece", Kind: kindProduct, Product: "Œuf", ToTaste: true},
			{Qty: "1", Unit: "piece", Kind: kindRecipe, RecipeID: 7}}
		out, ok, err := checkRows(ctx, f, rows)
		if err != nil || !ok {
			t.Fatalf("ok=%v err=%v lignes=%+v", ok, err, rows)
		}
		if len(out) != 4 {
			t.Fatalf("ligne vide non ignorée : %d", len(out))
		}
		if out[0].ProductID != 1 || out[0].Milli != 200000 || out[0].Unit != "g" {
			t.Errorf("farine = %+v", out[0])
		}
		if out[1].ProductID != 101 || out[1].Unit != "cube" || len(f.created) != 1 {
			t.Errorf("autre = %+v, créés %v", out[1], f.created)
		}
		if !out[2].ToTaste || out[2].Milli != 0 || out[2].Unit != "" {
			t.Errorf("au goût = %+v", out[2])
		}
		if out[3].SubRecipe != 7 || out[3].Milli != 1000 || out[3].Unit != "" {
			t.Errorf("recette = %+v", out[3])
		}
	})
	cases := []struct {
		name string
		row  ingRow
		want string
	}{
		{"quantité illisible", row("deux", "g", kindProduct, "Farine"), "illisible"},
		{"quantité manquante", row("", "g", kindProduct, "Farine"), "au goût"},
		{"unité inconnue", row("2", "brouette", kindProduct, "Farine"), "unité"},
		{"autre unité vide", row("2", unitOther, kindProduct, "Farine"), "Précisez"},
		{"hors catalogue", row("2", "g", kindProduct, "Farine de lin"), "pas dans le catalogue"},
		{"sans ingrédient", row("2", "g", kindProduct, ""), "Choisissez un ingrédient"},
		{"autre sans nom", row("2", "g", kindOther, ""), "Nommez"},
		{"unité incompatible", row("2", "g", kindProduct, "Œuf"), "ne convient pas"},
		{"recette sans choix", row("2", "piece", kindRecipe, ""), "Choisissez la recette"},
		{"recette en grammes", ingRow{Qty: "2", Unit: "g", Kind: kindRecipe, RecipeID: 3}, "portions"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := &fakeCatalog{}
			rows := []ingRow{c.row, row("1", "g", kindOther, "Sucre vanillé")}
			out, ok, err := checkRows(ctx, f, rows)
			if err != nil || ok || out != nil {
				t.Fatalf("ok=%v err=%v", ok, err)
			}
			if !strings.Contains(rows[0].Error, c.want) {
				t.Errorf("erreur = %q, attendu %q", rows[0].Error, c.want)
			}
			if rows[1].Error != "" || len(f.created) != 0 {
				t.Errorf("rien ne doit être créé tant qu'une ligne est fausse : %v", f.created)
			}
		})
	}
}

func TestRowsFrom(t *testing.T) {
	rows := rowsFrom([]recipes.Ingredient{
		{Name: "Farine", InputQty: 1500, InputUnit: "g"},
		{Name: "Bouillon", InputQty: 1000, InputUnit: "cube"},
		{SubRecipe: 4, InputQty: 2000},
		{Name: "Sel", ToTaste: true},
	})
	if rows[0].Qty != "1,5" || rows[0].Unit != "g" || rows[1].Unit != unitOther || rows[1].UnitOther != "cube" ||
		rows[2].Kind != kindRecipe || rows[2].RecipeID != 4 || rows[3].Qty != "" || !rows[3].ToTaste {
		t.Fatalf("%+v", rows)
	}
}
