package recipes

import (
	"errors"
	"fmt"

	"github.com/codecolors/petitchef/internal/catalog"
	"github.com/codecolors/petitchef/internal/platform/qty"
)

// ScaleQty met une quantité d'ingrédient à l'échelle : portions voulues
// (millièmes) sur portions de base. Les pièces sont arrondies
// intelligemment (pas « 1,37 œuf »).
func ScaleQty(in Ingredient, servings int, portionsMilli int64) int64 {
	if servings <= 0 {
		return in.Qty
	}
	v := roundDiv(in.Qty*portionsMilli, int64(servings)*1000)
	if in.Unit == qty.Piece && in.ProductID > 0 && v > 0 {
		return catalog.ScalePieces(v, in.PieceFrac)
	}
	return v
}

// ErrCycle signale une recette composée qui s'utilise elle-même.
var ErrCycle = errors.New("recette composée en boucle")

// Need est un besoin en produit issu d'une recette mise à l'échelle.
type Need struct {
	ProductID int64
	Qty       int64
	Staple    bool
	Optional  bool
	ToTaste   bool
	Via       string // recette composée d'origine (« pâte brisée »)
}

// Loader lit une recette (ingrédients compris) par son identifiant.
type Loader func(id int64) (Recipe, error)

// Expand déroule une recette mise à l'échelle en besoins de produits, en
// suivant les recettes composées ; une boucle est une erreur.
func Expand(r Recipe, portionsMilli int64, load Loader) ([]Need, error) {
	var out []Need
	err := expand(r, portionsMilli, load, map[int64]bool{}, "", &out)
	return out, err
}

func expand(r Recipe, portionsMilli int64, load Loader, path map[int64]bool, via string, out *[]Need) error {
	if path[r.ID] {
		return fmt.Errorf("%w : « %s »", ErrCycle, r.Title)
	}
	path[r.ID] = true
	defer delete(path, r.ID)
	for _, in := range r.Ingredients {
		if in.SubRecipe > 0 {
			sub, err := load(in.SubRecipe)
			if err != nil {
				return err
			}
			// Portions de la sous-recette demandées par la recette, à l'échelle.
			subPortions := roundDiv(in.Qty*portionsMilli, int64(r.Servings)*1000)
			name := sub.Title
			if via != "" {
				name = via
			}
			if err := expand(sub, subPortions, load, path, name, out); err != nil {
				return err
			}
			continue
		}
		*out = append(*out, Need{ProductID: in.ProductID, Qty: ScaleQty(in, r.Servings, portionsMilli), Staple: in.Staple,
			Optional: in.Optional, ToTaste: in.ToTaste, Via: via})
	}
	return nil
}

// CheckCycle vérifie qu'utiliser sub dans la recette id ne crée pas de
// boucle (sub, ou l'une de ses sous-recettes, ne doit pas être id).
func CheckCycle(id, sub int64, load Loader) error {
	seen := map[int64]bool{}
	var walk func(int64) error
	walk = func(cur int64) error {
		if cur == id {
			return ErrCycle
		}
		if seen[cur] {
			return nil
		}
		seen[cur] = true
		r, err := load(cur)
		if err != nil {
			return err
		}
		for _, in := range r.Ingredients {
			if in.SubRecipe > 0 {
				if err := walk(in.SubRecipe); err != nil {
					return err
				}
			}
		}
		return nil
	}
	return walk(sub)
}
