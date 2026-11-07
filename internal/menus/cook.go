package menus

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/codecolors/petitchef/internal/catalog"
	"github.com/codecolors/petitchef/internal/platform/calendar"
	"github.com/codecolors/petitchef/internal/platform/qty"
	"github.com/codecolors/petitchef/internal/platform/validation"
	"github.com/codecolors/petitchef/internal/recipes"
	"github.com/codecolors/petitchef/internal/stock"
)

// Use est un produit utilisé par un plat cuisiné (écran de confirmation).
type Use struct {
	ProductID int64
	Name      string
	Unit      qty.Base
	Qty       int64 // unités de base
	InStock   int64
	Via       string
}

// PlannedUses renvoie ce qu'un plat doit décompter du stock : ingrédients
// de la recette mis à l'échelle des portions (recettes composées
// déroulées, produits de base et « au goût » exclus), ou une unité du
// produit simple.
func (s *Service) PlannedUses(ctx context.Context, entryID int64) (Entry, []Use, error) {
	e, _, err := s.Entry(ctx, entryID)
	if err != nil {
		return e, nil, err
	}
	var uses []Use
	switch {
	case e.StockItemID > 0:
		it, err := s.stock.Get(ctx, e.StockItemID)
		if err == nil {
			q := min(it.Qty, e.Portions)
			if it.Unit != qty.Piece {
				q = it.Qty
			}
			uses = append(uses, Use{ProductID: it.ProductID, Name: it.Product, Unit: it.Unit, Qty: q})
		}
	case e.RecipeID > 0:
		needs, err := s.recipes.Needs(ctx, e.RecipeID, e.Portions)
		if err != nil {
			return e, nil, err
		}
		merged := map[int64]int{}
		for _, n := range needs {
			if n.Staple || n.ToTaste && n.Qty == 0 || n.Optional || n.Qty <= 0 {
				continue
			}
			if i, ok := merged[n.ProductID]; ok {
				uses[i].Qty += n.Qty
				continue
			}
			merged[n.ProductID] = len(uses)
			uses = append(uses, Use{ProductID: n.ProductID, Qty: n.Qty, Via: n.Via})
		}
	case e.ProductID > 0:
		uses = append(uses, Use{ProductID: e.ProductID, Qty: 0})
	}
	ids := make([]int64, len(uses))
	for i, u := range uses {
		ids[i] = u.ProductID
	}
	products, err := s.catalog.Many(ctx, ids)
	if err != nil {
		return e, nil, err
	}
	today := calendar.Today()
	for i := range uses {
		p := products[uses[i].ProductID]
		uses[i].Name, uses[i].Unit = p.Name, p.Unit
		if uses[i].Qty == 0 {
			uses[i].Qty = 1000
			if p.Unit != qty.Piece && len(p.Packages) > 0 {
				uses[i].Qty = p.Packages[0].Qty
			}
		}
		if uses[i].InStock, err = s.stock.Total(ctx, uses[i].ProductID, today); err != nil {
			return e, nil, err
		}
	}
	return e, uses, nil
}

// Cook marque un plat cuisiné et décompte du stock les quantités
// confirmées (modifiables : « j'ai mis 300 g », « j'ai pris du lait ») dans
// l'ordre FEFO, en une action annulable. leftovers (millièmes de portion)
// range des restes en stock comme plat « fait maison ». Renvoie la liste
// des produits qui manquaient.
func (s *Service) Cook(ctx context.Context, entryID int64, uses []Use, leftovers int64, userID int64) ([]string, error) {
	e, m, err := s.Entry(ctx, entryID)
	if err != nil {
		return nil, err
	}
	if e.Status == Cooked {
		return nil, validation.Error("Ce plat est déjà marqué cuisiné.")
	}
	today := calendar.Today()
	date := m.Date
	if date > today {
		date = today
	}
	var leftoverProduct int64
	if leftovers > 0 && e.RecipeID > 0 {
		if leftoverProduct, err = s.homeMadeProduct(ctx, e.RecipeID, e.Title); err != nil {
			return nil, err
		}
	}
	var missing []string
	err = s.db.Tx(ctx, func(tx *sql.Tx) error {
		batch, err := stock.NextBatch(ctx, tx)
		if err != nil {
			return err
		}
		for _, u := range uses {
			if u.Qty <= 0 {
				continue
			}
			short, err := stock.ConsumeFEFO(ctx, tx, u.ProductID, u.Qty, stock.Recipe, date, batch, entryID, userID)
			if err != nil {
				return err
			}
			if short > 0 {
				missing = append(missing, u.Name)
			}
		}
		if leftoverProduct > 0 {
			if _, err := s.stock.AddTx(ctx, tx, stock.AddInput{ProductID: leftoverProduct, Qty: leftovers, BestBefore: today.AddDays(3),
				DateKind: catalog.DLC, PriceCents: -1, Origin: stock.Leftovers, RecipeID: e.RecipeID, Date: today, UserID: userID}, batch); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, `UPDATE menu_entries SET status = 'cuisine', cooked_batch = ?, cooked_on = ? WHERE id = ?`, batch, today, entryID); err != nil {
			return err
		}
		if e.RecipeID > 0 {
			if err := recipes.MarkCooked(ctx, tx, e.RecipeID, m.Date, 1); err != nil {
				return err
			}
		}
		return nil
	})
	return missing, err
}

// homeMadeProduct renvoie le produit « fait maison » d'une recette (une
// pièce = une portion), créé au besoin dans le rayon traiteur.
func (s *Service) homeMadeProduct(ctx context.Context, recipeID int64, title string) (int64, error) {
	name := "Fait maison : " + title
	if id, err := s.catalog.Resolve(ctx, name); err == nil {
		return id, nil
	}
	var aisle, location int64
	if err := s.db.R.QueryRowContext(ctx, `SELECT id FROM aisles WHERE slug = 'charcuterie-traiteur'`).Scan(&aisle); err != nil {
		return 0, err
	}
	_ = s.db.R.QueryRowContext(ctx, `SELECT id FROM locations WHERE slug = 'refrigerateur'`).Scan(&location)
	r, err := s.recipes.Get(ctx, recipeID)
	if err != nil {
		return 0, err
	}
	return s.catalog.Create(ctx, catalog.Product{Name: name, Universe: catalog.Food, AisleID: aisle, LocationID: location, Unit: qty.Piece,
		PieceG: 300, ShelfClosed: 3, ShelfOpened: 1, ShelfFrozen: 90, DateKind: catalog.DLC, Group: catalog.Processed,
		Allergens: r.Allergens, Traits: r.Traits, Nutrition: catalog.Nutrition{Kcal: -1, Proteins: -1, Carbs: -1, Fat: -1, Fibres: -1}})
}

// Uncook annule « cuisiné » : le stock décompté est restauré.
func (s *Service) Uncook(ctx context.Context, entryID int64) error {
	e, m, err := s.Entry(ctx, entryID)
	if err != nil {
		return err
	}
	if e.Status != Cooked {
		return validation.Error("Ce plat n'est pas marqué cuisiné.")
	}
	if e.CookedBatch > 0 {
		if err := s.stock.Undo(ctx, e.CookedBatch); err != nil {
			var msg string
			if v, ok := validation.Message(err); ok {
				msg = v
			}
			if msg != "Rien à annuler." {
				return err
			}
		}
	}
	return s.db.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE menu_entries SET status = 'prevu', cooked_batch = NULL, cooked_on = NULL WHERE id = ?`, entryID); err != nil {
			return err
		}
		if e.RecipeID > 0 {
			if err := recipes.MarkCooked(ctx, tx, e.RecipeID, m.Date, -1); err != nil {
				return err
			}
			// La date de dernière cuisine redevient la précédente.
			_, err := tx.ExecContext(ctx, `UPDATE recipes SET last_cooked = (SELECT max(m.date) FROM menu_entries x JOIN meals m ON m.id = x.meal_id
				WHERE x.recipe_id = ? AND x.status = 'cuisine') WHERE id = ?`, e.RecipeID, e.RecipeID)
			return err
		}
		return nil
	})
}

// TodayEntry trouve, pour le mode cuisine, le plat d'aujourd'hui qui
// correspond à une recette (0 s'il n'y en a pas).
func (s *Service) TodayEntry(ctx context.Context, recipeID int64) (int64, error) {
	var id int64
	today := calendar.Today()
	err := s.db.R.QueryRowContext(ctx, `SELECT e.id FROM menu_entries e JOIN meals m ON m.id = e.meal_id
		WHERE e.recipe_id = ? AND e.status != 'cuisine' AND e.leftovers_from IS NULL AND m.date BETWEEN ? AND ?
		ORDER BY abs(julianday(m.date) - julianday(?)) LIMIT 1`, recipeID, today.AddDays(-1), today.AddDays(1), today).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return id, err
}

// describe résume les manques pour un message.
func describe(missing []string) string {
	if len(missing) == 0 {
		return ""
	}
	return fmt.Sprintf(" Manquait en stock : %s.", strings.Join(missing, ", "))
}

// Describe est exporté pour l'interface.
func Describe(missing []string) string { return describe(missing) }
