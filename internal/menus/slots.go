// Package menus gère les créneaux de repas, les semaines types, les menus
// planifiés et leur édition ; il prépare les données du générateur
// (internal/planner) et enregistre ses propositions.
package menus

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/codecolors/petitchef/internal/catalog"
	"github.com/codecolors/petitchef/internal/household"
	"github.com/codecolors/petitchef/internal/planner"
	"github.com/codecolors/petitchef/internal/platform/calendar"
	"github.com/codecolors/petitchef/internal/platform/db"
	"github.com/codecolors/petitchef/internal/platform/validation"
	"github.com/codecolors/petitchef/internal/recipes"
	"github.com/codecolors/petitchef/internal/stock"
)

// Service gère les menus.
type Service struct {
	db        *db.DB
	catalog   *catalog.Service
	recipes   *recipes.Service
	household *household.Service
	stock     *stock.Service
}

// NewService crée le service des menus.
func NewService(d *db.DB, c *catalog.Service, r *recipes.Service, h *household.Service, st *stock.Service) *Service {
	return &Service{db: d, catalog: c, recipes: r, household: h, stock: st}
}

// MealSlot est un créneau de repas.
type MealSlot struct {
	ID            int64
	Slug          string
	Name          string
	Position      int
	Enabled       bool
	MaxMinWeekday int
	MaxMinWeekend int
}

// MaxMin renvoie le temps maximum du créneau un jour donné (0 = lundi).
func (s MealSlot) MaxMin(weekday int) int {
	if weekday >= 5 {
		return s.MaxMinWeekend
	}
	return s.MaxMinWeekday
}

// Slots renvoie les créneaux (activés seulement si enabledOnly).
func (s *Service) Slots(ctx context.Context, enabledOnly bool) ([]MealSlot, error) {
	return db.Collect(ctx, s.db.R, func(r db.Scanner) (MealSlot, error) {
		var m MealSlot
		return m, r.Scan(&m.ID, &m.Slug, &m.Name, &m.Position, &m.Enabled, &m.MaxMinWeekday, &m.MaxMinWeekend)
	}, `SELECT id, slug, name, position, enabled, max_min_weekday, max_min_weekend FROM meal_slots WHERE ? = 0 OR enabled ORDER BY position`, enabledOnly)
}

// SaveSlot enregistre un créneau (activation, nom, temps maximum).
func (s *Service) SaveSlot(ctx context.Context, m MealSlot) error {
	m.Name = strings.TrimSpace(m.Name)
	if m.Name == "" || m.MaxMinWeekday < 0 || m.MaxMinWeekend < 0 || m.MaxMinWeekday > 600 || m.MaxMinWeekend > 600 {
		return validation.Error("Nom obligatoire et temps maximum entre 0 et 600 minutes.")
	}
	_, err := s.db.W.ExecContext(ctx, `UPDATE meal_slots SET name = ?, enabled = ?, max_min_weekday = ?, max_min_weekend = ? WHERE id = ?`,
		m.Name, m.Enabled, m.MaxMinWeekday, m.MaxMinWeekend, m.ID)
	return err
}

// Template est une semaine type.
type Template struct {
	ID        int64
	Name      string
	IsDefault bool
}

// Cell est le réglage d'un créneau d'un jour dans une semaine type.
type Cell struct {
	Weekday    int
	SlotID     int64
	Format     planner.Format
	AllMembers bool
	Members    []int64
	Guests     int
}

// Templates renvoie les semaines types, la semaine par défaut d'abord.
func (s *Service) Templates(ctx context.Context) ([]Template, error) {
	return db.Collect(ctx, s.db.R, func(r db.Scanner) (Template, error) {
		var t Template
		return t, r.Scan(&t.ID, &t.Name, &t.IsDefault)
	}, `SELECT id, name, is_default FROM week_templates ORDER BY is_default DESC, name`)
}

// Cells renvoie les réglages d'une semaine type, indexés par jour et
// créneau.
func (s *Service) Cells(ctx context.Context, templateID int64) (map[[2]int64]Cell, error) {
	cells, err := db.Collect(ctx, s.db.R, func(r db.Scanner) (Cell, error) {
		var c Cell
		return c, r.Scan(&c.Weekday, &c.SlotID, &c.Format, &c.AllMembers, &c.Guests)
	}, `SELECT weekday, slot_id, format, all_members, guests FROM week_template_slots WHERE template_id = ?`, templateID)
	if err != nil {
		return nil, err
	}
	members, err := db.Collect(ctx, s.db.R, func(r db.Scanner) ([3]int64, error) {
		var x [3]int64
		return x, r.Scan(&x[0], &x[1], &x[2])
	}, `SELECT weekday, slot_id, member_id FROM week_template_members WHERE template_id = ?`, templateID)
	if err != nil {
		return nil, err
	}
	out := make(map[[2]int64]Cell, len(cells))
	for _, c := range cells {
		out[[2]int64{int64(c.Weekday), c.SlotID}] = c
	}
	for _, m := range members {
		k := [2]int64{m[0], m[1]}
		c := out[k]
		c.Members = append(c.Members, m[2])
		out[k] = c
	}
	return out, nil
}

// SaveCell enregistre le réglage d'un créneau d'une semaine type.
func (s *Service) SaveCell(ctx context.Context, templateID int64, c Cell) error {
	if !c.Format.Valid() || c.Weekday < 0 || c.Weekday > 6 || c.Guests < 0 || c.Guests > 50 {
		return validation.Error("Réglage du créneau invalide.")
	}
	return s.db.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO week_template_slots (template_id, weekday, slot_id, format, all_members, guests)
			VALUES (?, ?, ?, ?, ?, ?) ON CONFLICT DO UPDATE SET format = excluded.format, all_members = excluded.all_members, guests = excluded.guests`,
			templateID, c.Weekday, c.SlotID, c.Format, c.AllMembers, c.Guests); err != nil {
			if db.IsForeignKeyViolation(err) {
				return db.ErrNotFound
			}
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM week_template_members WHERE template_id = ? AND weekday = ? AND slot_id = ?`,
			templateID, c.Weekday, c.SlotID); err != nil {
			return err
		}
		if c.AllMembers {
			return nil
		}
		for _, m := range c.Members {
			if _, err := tx.ExecContext(ctx, `INSERT INTO week_template_members (template_id, weekday, slot_id, member_id) VALUES (?, ?, ?, ?)
				ON CONFLICT DO NOTHING`, templateID, c.Weekday, c.SlotID, m); err != nil {
				return err
			}
		}
		return nil
	})
}

// CreateTemplate crée une semaine type, copiée d'une autre (from) si
// demandé ; renvoie son identifiant.
func (s *Service) CreateTemplate(ctx context.Context, name string, from int64) (int64, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return 0, validation.Error("Nom de la semaine type obligatoire.")
	}
	var id int64
	err := s.db.Tx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `INSERT INTO week_templates (name) VALUES (?)`, name)
		if err != nil {
			return err
		}
		if id, err = res.LastInsertId(); err != nil {
			return err
		}
		if from == 0 {
			_, err = tx.ExecContext(ctx, `INSERT INTO week_template_slots (template_id, weekday, slot_id, format)
				SELECT ?, d.value, s.id, 'plat' FROM meal_slots s, json_each('[0,1,2,3,4,5,6]') d`, id)
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO week_template_slots (template_id, weekday, slot_id, format, all_members, guests)
			SELECT ?, weekday, slot_id, format, all_members, guests FROM week_template_slots WHERE template_id = ?`, id, from); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO week_template_members (template_id, weekday, slot_id, member_id)
			SELECT ?, weekday, slot_id, member_id FROM week_template_members WHERE template_id = ?`, id, from)
		return err
	})
	if db.IsUniqueViolation(err) {
		return 0, validation.Errorf("La semaine type « %s » existe déjà.", name)
	}
	return id, err
}

// SetDefaultTemplate fait d'une semaine type celle proposée par défaut.
func (s *Service) SetDefaultTemplate(ctx context.Context, id int64) error {
	return s.db.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE week_templates SET is_default = (id = ?)`, id); err != nil {
			return err
		}
		return nil
	})
}

// DeleteTemplate supprime une semaine type (sauf la dernière).
func (s *Service) DeleteTemplate(ctx context.Context, id int64) error {
	return s.db.Tx(ctx, func(tx *sql.Tx) error {
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM week_templates`).Scan(&n); err != nil {
			return err
		}
		if n <= 1 {
			return validation.Error("Gardez au moins une semaine type.")
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM week_templates WHERE id = ?`, id); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `UPDATE week_templates SET is_default = 1 WHERE id = (SELECT min(id) FROM week_templates)
			AND NOT EXISTS (SELECT 1 FROM week_templates WHERE is_default)`)
		return err
	})
}

// DefaultTemplate renvoie la semaine type par défaut.
func (s *Service) DefaultTemplate(ctx context.Context) (int64, error) {
	var id int64
	err := s.db.R.QueryRowContext(ctx, `SELECT id FROM week_templates ORDER BY is_default DESC, id LIMIT 1`).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, db.ErrNotFound
	}
	return id, err
}

// weekday renvoie le jour de la semaine d'une date (0 = lundi).
func weekday(d calendar.Date) int { return (int(d.Time().Weekday()) + 6) % 7 }

// Monday renvoie le lundi de la semaine d'une date.
func Monday(d calendar.Date) calendar.Date { return d.AddDays(-weekday(d)) }
