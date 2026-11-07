// Package household gère les membres du foyer (distincts des comptes de
// connexion) : portion relative, allergies, régimes et aliments refusés.
// Ce sont les seules informations utilisées par la compatibilité des
// recettes, les contraintes du générateur et la mise à l'échelle.
package household

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/codecolors/petitchef/internal/catalog"
	"github.com/codecolors/petitchef/internal/platform/db"
	"github.com/codecolors/petitchef/internal/platform/validation"
)

// Member est un membre du foyer.
type Member struct {
	ID           int64
	Name         string
	Portion      int64 // millièmes : 1000 = adulte, 500 = enfant
	Allergens    catalog.Allergens
	Diets        catalog.Diet
	Refused      []int64 // produits refusés
	RefusedNames []string
	Archived     bool
}

// Service gère le foyer.
type Service struct{ db *db.DB }

// NewService crée le service du foyer.
func NewService(d *db.DB) *Service { return &Service{db: d} }

// Members renvoie les membres actifs (archivés compris si all).
func (s *Service) Members(ctx context.Context, all bool) ([]Member, error) {
	members, err := db.Collect(ctx, s.db.R, func(r db.Scanner) (Member, error) {
		var m Member
		return m, r.Scan(&m.ID, &m.Name, &m.Portion, &m.Allergens, &m.Diets, &m.Archived)
	}, `SELECT id, name, portion, allergens, diets, archived FROM household_members WHERE ? OR NOT archived ORDER BY archived, id`, all)
	if err != nil {
		return nil, err
	}
	type ref struct {
		member, product int64
		name            string
	}
	refs, err := db.Collect(ctx, s.db.R, func(r db.Scanner) (ref, error) {
		var x ref
		return x, r.Scan(&x.member, &x.product, &x.name)
	}, `SELECT m.member_id, m.product_id, p.name FROM member_refusals m JOIN products p ON p.id = m.product_id ORDER BY p.search`)
	if err != nil {
		return nil, err
	}
	for i := range members {
		for _, r := range refs {
			if r.member == members[i].ID {
				members[i].Refused = append(members[i].Refused, r.product)
				members[i].RefusedNames = append(members[i].RefusedNames, r.name)
			}
		}
	}
	return members, nil
}

// Get renvoie un membre.
func (s *Service) Get(ctx context.Context, id int64) (Member, error) {
	all, err := s.Members(ctx, true)
	if err != nil {
		return Member{}, err
	}
	for _, m := range all {
		if m.ID == id {
			return m, nil
		}
	}
	return Member{}, db.ErrNotFound
}

// Save crée (ID nul) ou enregistre un membre ; renvoie son identifiant.
func (s *Service) Save(ctx context.Context, m Member) (int64, error) {
	m.Name = strings.TrimSpace(m.Name)
	if m.Name == "" || utf8.RuneCountInString(m.Name) > 40 {
		return 0, validation.Error("Prénom obligatoire (40 caractères au plus).")
	}
	if m.Portion < 100 || m.Portion > 5000 {
		return 0, validation.Error("Portion relative entre 0,1 et 5.")
	}
	err := s.db.Tx(ctx, func(tx *sql.Tx) error {
		if m.ID == 0 {
			res, err := tx.ExecContext(ctx, `INSERT INTO household_members (name, portion, allergens, diets) VALUES (?, ?, ?, ?)`,
				m.Name, m.Portion, m.Allergens, m.Diets)
			if err != nil {
				return err
			}
			m.ID, err = res.LastInsertId()
			if err != nil {
				return err
			}
		} else {
			res, err := tx.ExecContext(ctx, `UPDATE household_members SET name = ?, portion = ?, allergens = ?, diets = ?, archived = ? WHERE id = ?`,
				m.Name, m.Portion, m.Allergens, m.Diets, m.Archived, m.ID)
			if err != nil {
				return err
			}
			if n, _ := res.RowsAffected(); n == 0 {
				return db.ErrNotFound
			}
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM member_refusals WHERE member_id = ?`, m.ID); err != nil {
			return err
		}
		for _, p := range m.Refused {
			if _, err := tx.ExecContext(ctx, `INSERT INTO member_refusals (member_id, product_id) VALUES (?, ?) ON CONFLICT DO NOTHING`, m.ID, p); err != nil {
				return err
			}
		}
		return nil
	})
	return m.ID, err
}

// Delete supprime un membre ; s'il figure dans des menus, il est archivé.
func (s *Service) Delete(ctx context.Context, id int64) error {
	_, err := s.db.W.ExecContext(ctx, `DELETE FROM household_members WHERE id = ?`, id)
	if db.IsForeignKeyViolation(err) {
		_, err = s.db.W.ExecContext(ctx, `UPDATE household_members SET archived = 1 WHERE id = ?`, id)
	}
	return err
}

// Constraints sont les contraintes strictes d'un groupe de convives.
type Constraints struct {
	Allergens catalog.Allergens
	Diets     catalog.Diet
	Refused   map[int64]bool
	Portions  int64 // millièmes de portion
}

// ConstraintsFor cumule les contraintes des membres présents, plus des
// invités (portion d'adulte, sans contrainte connue).
func ConstraintsFor(members []Member, present []int64, guests int) Constraints {
	c := Constraints{Refused: map[int64]bool{}, Portions: int64(guests) * 1000}
	for _, m := range members {
		for _, id := range present {
			if m.ID == id {
				c.Allergens |= m.Allergens
				c.Diets |= m.Diets
				c.Portions += m.Portion
				for _, p := range m.Refused {
					c.Refused[p] = true
				}
			}
		}
	}
	return c
}

// ErrNoMember signale un foyer sans membre.
var ErrNoMember = errors.New("aucun membre du foyer")
