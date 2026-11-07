package menus

import (
	"context"
	"database/sql"
	"strings"

	"github.com/codecolors/petitchef/internal/planner"
	"github.com/codecolors/petitchef/internal/platform/calendar"
	"github.com/codecolors/petitchef/internal/platform/db"
	"github.com/codecolors/petitchef/internal/platform/validation"
)

// Model est un menu enregistré comme modèle réutilisable.
type Model struct {
	ID     int64
	Name   string
	Days   int
	Dishes int
}

// Models renvoie les menus enregistrés.
func (s *Service) Models(ctx context.Context) ([]Model, error) {
	return db.Collect(ctx, s.db.R, func(r db.Scanner) (Model, error) {
		var m Model
		return m, r.Scan(&m.ID, &m.Name, &m.Days, &m.Dishes)
	}, `SELECT m.id, m.name, m.days, (SELECT count(*) FROM saved_menu_entries e WHERE e.saved_id = m.id) FROM saved_menus m ORDER BY m.name`)
}

// SaveModel enregistre les plats des jours from à from+days comme modèle.
func (s *Service) SaveModel(ctx context.Context, name string, from calendar.Date, days int) (int64, error) {
	name = strings.TrimSpace(name)
	if name == "" || days < 1 || days > 56 {
		return 0, validation.Error("Nom obligatoire, de 1 à 56 jours.")
	}
	meals, err := s.Range(ctx, from, from.AddDays(days))
	if err != nil {
		return 0, err
	}
	var id int64
	err = s.db.Tx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `INSERT INTO saved_menus (name, days) VALUES (?, ?)`, name, days)
		if err != nil {
			return err
		}
		if id, err = res.LastInsertId(); err != nil {
			return err
		}
		for _, m := range meals {
			for _, e := range m.Entries {
				if e.IsLeftover() {
					continue
				}
				if _, err := tx.ExecContext(ctx, `INSERT INTO saved_menu_entries (saved_id, day, slot_id, format, role, recipe_id, product_id)
					VALUES (?, ?, ?, ?, ?, ?, ?)`, id, from.DaysUntil(m.Date), m.SlotID, m.Format, e.Role, nullID(e.RecipeID), nullID(e.ProductID)); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if db.IsUniqueViolation(err) {
		return 0, validation.Errorf("Un menu enregistré s'appelle déjà « %s ».", name)
	}
	return id, err
}

// ApplyModel applique un menu enregistré à partir d'une date : formats et
// plats des repas (les plats cuisinés ou verrouillés sont conservés).
func (s *Service) ApplyModel(ctx context.Context, id int64, from calendar.Date) (int, error) {
	type row struct {
		day             int
		slot            int64
		format, role    string
		recipe, product int64
	}
	rows, err := db.Collect(ctx, s.db.R, func(r db.Scanner) (row, error) {
		var x row
		return x, r.Scan(&x.day, &x.slot, &x.format, &x.role, &x.recipe, &x.product)
	}, `SELECT day, slot_id, format, role, coalesce(recipe_id, 0), coalesce(product_id, 0) FROM saved_menu_entries WHERE saved_id = ? ORDER BY day`, id)
	if err != nil {
		return 0, err
	}
	if len(rows) == 0 {
		return 0, db.ErrNotFound
	}
	tpl, err := s.DefaultTemplate(ctx)
	if err != nil {
		return 0, err
	}
	if err := s.EnsureMeals(ctx, from, rows[len(rows)-1].day+1, tpl); err != nil {
		return 0, err
	}
	n := 0
	for _, x := range rows {
		date := from.AddDays(x.day)
		var mealID int64
		if err := s.db.R.QueryRowContext(ctx, `SELECT id FROM meals WHERE date = ? AND slot_id = ?`, date, x.slot).Scan(&mealID); err != nil {
			continue // créneau désactivé depuis
		}
		m, err := s.Meal(ctx, mealID)
		if err != nil {
			return n, err
		}
		if m.Format != planner.Format(x.format) {
			if err := s.UpdateMeal(ctx, mealID, MealInput{Format: planner.Format(x.format), AllMembers: m.AllMembers, Members: m.Members, Guests: m.Guests, MaxMin: m.MaxMin}); err != nil {
				return n, err
			}
		}
		keep := false
		for _, e := range m.Entries {
			if e.Role == planner.Role(x.role) && (e.Locked || e.Status == Cooked) {
				keep = true
			}
		}
		if keep {
			continue
		}
		if _, err := s.SetDish(ctx, mealID, planner.Role(x.role), x.recipe, x.product, 0, []string{"menu enregistré"}); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

// DeleteModel supprime un menu enregistré.
func (s *Service) DeleteModel(ctx context.Context, id int64) error {
	_, err := s.db.W.ExecContext(ctx, `DELETE FROM saved_menus WHERE id = ?`, id)
	return err
}

// Weeks renvoie les lundis des semaines qui ont des plats, les plus
// récentes d'abord (historique).
func (s *Service) Weeks(ctx context.Context, limit int) ([]calendar.Date, error) {
	dates, err := db.Collect(ctx, s.db.R, func(r db.Scanner) (calendar.Date, error) {
		var d calendar.Date
		return d, r.Scan(&d)
	}, `SELECT DISTINCT m.date FROM meals m JOIN menu_entries e ON e.meal_id = m.id ORDER BY m.date DESC LIMIT 400`)
	if err != nil {
		return nil, err
	}
	var out []calendar.Date
	for _, d := range dates {
		mon := Monday(d)
		if len(out) == 0 || out[len(out)-1] != mon {
			out = append(out, mon)
		}
		if len(out) == limit {
			break
		}
	}
	return out, nil
}
