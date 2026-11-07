package recipes

import (
	"context"

	"github.com/codecolors/petitchef/internal/platform/db"
)

// All charge toutes les recettes avec leurs ingrédients en trois requêtes
// (moteurs : générateur de menus, « que cuisiner ce soir »), indexées par
// identifiant. Les étapes et étiquettes ne sont pas chargées, sauf le
// repère « batch cooking » (Tags).
func (s *Service) All(ctx context.Context) (map[int64]Recipe, error) {
	list, err := db.Collect(ctx, s.db.R, scanRecipe, `SELECT `+recipeColumns+` FROM recipes r`)
	if err != nil {
		return nil, err
	}
	out := make(map[int64]Recipe, len(list))
	for _, r := range list {
		out[r.ID] = r
	}
	type ing struct {
		recipe int64
		in     Ingredient
	}
	ings, err := db.Collect(ctx, s.db.R, func(row db.Scanner) (ing, error) {
		var x ing
		in := &x.in
		err := row.Scan(&x.recipe, &in.Position, &in.ProductID, &in.SubRecipe, &in.Qty, &in.InputQty, &in.InputUnit, &in.Optional,
			&in.ToTaste, &in.Unit, &in.Staple)
		in.PieceFrac = in.InputUnit == "piece" && in.InputQty%1000 != 0
		return x, err
	}, `SELECT i.recipe_id, i.position, coalesce(i.product_id, 0), coalesce(i.sub_recipe_id, 0), i.qty, i.input_qty, i.input_unit,
			i.optional, i.to_taste, coalesce(p.unit, ''), coalesce(p.staple, 0)
		FROM recipe_ingredients i LEFT JOIN products p ON p.id = i.product_id ORDER BY i.recipe_id, i.position`)
	if err != nil {
		return nil, err
	}
	for _, x := range ings {
		r := out[x.recipe]
		r.Ingredients = append(r.Ingredients, x.in)
		out[x.recipe] = r
	}
	tags, err := db.Collect(ctx, s.db.R, func(row db.Scanner) ([2]any, error) {
		var id int64
		var tag string
		err := row.Scan(&id, &tag)
		return [2]any{id, tag}, err
	}, `SELECT recipe_id, tag FROM recipe_tags`)
	if err != nil {
		return nil, err
	}
	for _, t := range tags {
		id := t[0].(int64)
		r := out[id]
		r.Tags = append(r.Tags, t[1].(string))
		out[id] = r
	}
	return out, nil
}

// MemLoader lit les recettes dans une table chargée par All.
func MemLoader(all map[int64]Recipe) Loader {
	return func(id int64) (Recipe, error) {
		r, ok := all[id]
		if !ok {
			return r, db.ErrNotFound
		}
		return r, nil
	}
}
