package web

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/codecolors/petitchef/internal/platform/db"
	"github.com/codecolors/petitchef/internal/platform/qty"
	"github.com/codecolors/petitchef/internal/recipes"
)

// Saisie structurée des ingrédients d'une recette : une ligne par
// ingrédient (quantité, unité de la liste ou « autre », ingrédient choisi
// dans le catalogue, une autre recette ou « autre »).

// Nature de l'ingrédient d'une ligne.
const (
	kindProduct = "produit" // produit du catalogue
	kindRecipe  = "recette" // autre recette (recette composée)
	kindOther   = "autre"   // ingrédient hors catalogue, créé à la volée
	unitOther   = "autre"   // unité hors liste
)

// ingRow est une ligne de saisie d'ingrédient.
type ingRow struct {
	Index     int
	Qty       string
	Unit      string // code d'unité, ou « autre »
	UnitOther string
	Kind      string
	Product   string // nom d'un produit du catalogue
	RecipeID  int64
	Other     string // nom d'un ingrédient hors catalogue
	Note      string
	Group     string
	Optional  bool
	ToTaste   bool
	Error     string
}

// blank indique une ligne laissée vide (ignorée à l'enregistrement).
func (r ingRow) blank() bool {
	return strings.TrimSpace(r.Qty) == "" && strings.TrimSpace(r.Product) == "" && strings.TrimSpace(r.Other) == "" &&
		r.RecipeID == 0 && strings.TrimSpace(r.Note) == ""
}

func newRow(i int) ingRow { return ingRow{Index: i, Unit: "g", Kind: kindProduct} }

// parseRows lit les lignes d'ingrédients d'un formulaire (champs ing_*_N),
// dans l'ordre de leur numéro ; la ligne remove est retirée.
func parseRows(form url.Values, remove int) []ingRow {
	var idx []int
	for key := range form {
		if n, ok := strings.CutPrefix(key, "ing_kind_"); ok {
			if i, err := strconv.Atoi(n); err == nil && i >= 0 && i < 500 {
				idx = append(idx, i)
			}
		}
	}
	slices.Sort(idx)
	var rows []ingRow
	for _, i := range idx {
		if i == remove {
			continue
		}
		get := func(name string) string { return strings.TrimSpace(form.Get(fmt.Sprintf("ing_%s_%d", name, i))) }
		rows = append(rows, ingRow{
			Qty: get("qty"), Unit: get("unit"), UnitOther: get("unit_other"), Kind: get("kind"),
			Product: get("product"), RecipeID: formInt(get("recipe"), 0), Other: get("other"), Note: get("note"),
			Group: get("group"), Optional: get("optional") != "", ToTaste: get("taste") != "",
		})
	}
	return renumber(rows)
}

func renumber(rows []ingRow) []ingRow {
	for i := range rows {
		rows[i].Index = i
	}
	return rows
}

// rowsFrom convertit les ingrédients d'une recette en lignes de saisie.
func rowsFrom(list []recipes.Ingredient) []ingRow {
	rows := make([]ingRow, 0, len(list))
	for _, in := range list {
		r := ingRow{Kind: kindProduct, Product: in.Name, Note: in.Note, Group: in.Group, Optional: in.Optional, ToTaste: in.ToTaste, Unit: "piece"}
		if in.SubRecipe > 0 {
			r.Kind, r.Product, r.RecipeID = kindRecipe, "", in.SubRecipe
		}
		if in.InputQty > 0 {
			r.Qty = qty.FormatDecimal(in.InputQty)
		}
		switch {
		case in.InputUnit == "":
			r.Unit = "piece"
		case qty.IsKitchenUnit(in.InputUnit):
			r.Unit = in.InputUnit
		default:
			r.Unit, r.UnitOther = unitOther, in.InputUnit
		}
		rows = append(rows, r)
	}
	return renumber(rows)
}

// rowsFromLines convertit une liste collée, analysée et rapprochée du
// catalogue, en lignes de saisie ; un nom non reconnu reste à choisir.
func rowsFromLines(list []recipes.Resolved, names map[int64]string) []ingRow {
	rows := make([]ingRow, 0, len(list))
	for _, x := range list {
		r := ingRow{Kind: kindProduct, Note: x.Note, Group: x.Group, Optional: x.Optional, ToTaste: x.ToTaste, Unit: x.Unit}
		if x.Milli > 0 {
			r.Qty = qty.FormatDecimal(x.Milli)
		}
		if r.Unit == "" {
			r.Unit = "piece"
		}
		switch {
		case x.SubRecipe > 0:
			r.Kind, r.RecipeID = kindRecipe, x.SubRecipe
		case x.Confidence >= recipes.Partial:
			r.Product = names[x.ProductID]
		default:
			r.Product = x.Name
			r.Error = "Ingrédient non reconnu : choisissez-le dans le catalogue, ou « Autre »."
		}
		rows = append(rows, r)
	}
	return renumber(rows)
}

// ingredientChecker fournit au contrôle des lignes l'accès au catalogue.
type ingredientChecker interface {
	resolveName(ctx context.Context, name string) (int64, error)
	baseQty(ctx context.Context, productID, milli int64, unit string) (int64, error)
	createOther(ctx context.Context, name string) (int64, error)
}

// checkRows contrôle strictement chaque ligne et la convertit en
// ingrédient : quantité lisible (sauf « au goût »), unité de la liste ou
// précisée, produit existant du catalogue (ou ingrédient « autre » nommé),
// unité compatible avec le produit. Les erreurs sont portées par les
// lignes ; ok vaut false s'il y en a.
func checkRows(ctx context.Context, c ingredientChecker, rows []ingRow) ([]recipes.Resolved, bool, error) {
	var out []recipes.Resolved
	ok := true
	fail := func(r *ingRow, msg string) {
		r.Error = msg
		ok = false
	}
	for i := range rows {
		r := &rows[i]
		r.Error = ""
		if r.blank() {
			continue
		}
		res := recipes.Resolved{Confidence: recipes.Exact, Line: recipes.Line{Group: r.Group, Note: r.Note, Optional: r.Optional, ToTaste: r.ToTaste}}
		if utf8.RuneCountInString(r.Note) > 80 || utf8.RuneCountInString(r.Group) > 60 {
			fail(r, "Précision ou groupe trop long.")
			continue
		}
		// Quantité.
		if r.Qty != "" {
			m, err := qty.ParseDecimal(r.Qty)
			if err != nil {
				fail(r, "Quantité illisible : écrivez par exemple 200, 1,5 ou ½.")
				continue
			}
			res.Milli = m
		} else if !r.ToTaste && !r.Optional {
			fail(r, "Indiquez une quantité, ou cochez « au goût ».")
			continue
		}
		// Unité.
		switch {
		case r.Unit == unitOther:
			if r.UnitOther == "" || utf8.RuneCountInString(r.UnitOther) > 30 {
				fail(r, "Précisez l'unité (30 caractères au plus).")
				continue
			}
			res.Unit = strings.ToLower(r.UnitOther)
			if code := qty.Canonical(r.UnitOther); code != "" {
				res.Unit = code // « c. à s. » saisi à la main : unité connue
			}
		case qty.IsKitchenUnit(r.Unit):
			res.Unit = r.Unit
		default:
			fail(r, "Choisissez une unité.")
			continue
		}
		if res.Milli == 0 {
			res.Unit = ""
		}
		// Ingrédient.
		switch r.Kind {
		case kindRecipe:
			if r.RecipeID <= 0 {
				fail(r, "Choisissez la recette utilisée.")
				continue
			}
			res.SubRecipe, res.Line.SubRecipe = r.RecipeID, true
			if res.Milli > 0 && res.Unit != "piece" {
				fail(r, "Une recette s'utilise en portions : choisissez l'unité « pièce(s) ».")
				continue
			}
			res.Unit = ""
		case kindOther:
			if r.Other == "" || utf8.RuneCountInString(r.Other) > 120 {
				fail(r, "Nommez l'ingrédient (120 caractères au plus).")
				continue
			}
			res.Name = r.Other // créé une fois toutes les lignes valides
		default:
			if r.Product == "" {
				fail(r, "Choisissez un ingrédient du catalogue, ou « Autre ».")
				continue
			}
			id, err := c.resolveName(ctx, r.Product)
			if errors.Is(err, db.ErrNotFound) {
				fail(r, fmt.Sprintf("« %s » n'est pas dans le catalogue : choisissez une suggestion, ou « Autre ».", r.Product))
				continue
			}
			if err != nil {
				return nil, false, err
			}
			res.ProductID, res.Name = id, r.Product
		}
		// Unité compatible avec le produit (unités de la liste seulement).
		if res.ProductID > 0 && res.Milli > 0 && r.Unit != unitOther {
			base, err := c.baseQty(ctx, res.ProductID, res.Milli, res.Unit)
			if err != nil {
				return nil, false, err
			}
			if base == 0 {
				fail(r, "Cette unité ne convient pas à ce produit (son poids ou sa contenance est inconnu) : choisissez-en une autre, ou complétez sa fiche.")
				continue
			}
		}
		out = append(out, res)
	}
	if !ok {
		return nil, false, nil
	}
	// Ingrédients hors catalogue : créés « à compléter » dans le catalogue,
	// référentiel unique des courses et du stock.
	for i := range out {
		if out[i].ProductID == 0 && out[i].SubRecipe == 0 {
			id, err := c.createOther(ctx, out[i].Name)
			if err != nil {
				return nil, false, err
			}
			out[i].ProductID = id
		}
	}
	return out, true, nil
}

// serverChecker relie checkRows aux services.
type serverChecker struct{ s *Server }

func (c serverChecker) resolveName(ctx context.Context, name string) (int64, error) {
	return c.s.Catalog.Resolve(ctx, name)
}

func (c serverChecker) baseQty(ctx context.Context, productID, milli int64, unit string) (int64, error) {
	p, err := c.s.Catalog.Get(ctx, productID)
	if err != nil {
		return 0, err
	}
	return recipes.ToBase(p, milli, unit), nil
}

func (c serverChecker) createOther(ctx context.Context, name string) (int64, error) {
	return c.s.Catalog.QuickCreate(ctx, name)
}
