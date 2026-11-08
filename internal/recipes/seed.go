package recipes

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"log/slog"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/codecolors/petitchef/internal/catalog"
)

// yamlRecipe est une recette décrite en YAML (jeu d'essai des tests, voir
// recipetest/recettes/LISEZMOI.md).
type yamlRecipe struct {
	Title       string   `yaml:"titre"`
	Types       string   `yaml:"types"`
	Servings    int      `yaml:"portions"`
	Prep        int      `yaml:"preparation"`
	Cook        int      `yaml:"cuisson"`
	Rest        int      `yaml:"repos"`
	Difficulty  int      `yaml:"difficulte"`
	Tags        string   `yaml:"etiquettes"`
	Utensils    string   `yaml:"ustensiles"`
	Ingredients string   `yaml:"ingredients"`
	Steps       []string `yaml:"etapes"`
}

// LoadYAML enregistre les recettes des fichiers YAML de fsys (dans
// l'ordre de leur nom) ; chaque ingrédient doit désigner exactement un
// produit du catalogue. Renvoie le nombre de recettes créées.
func (s *Service) LoadYAML(ctx context.Context, fsys fs.FS) (int, error) {
	files, err := fs.Glob(fsys, "*.yaml")
	if err != nil {
		return 0, err
	}
	n := 0
	for _, f := range files {
		raw, err := fs.ReadFile(fsys, f)
		if err != nil {
			return n, err
		}
		var list []yamlRecipe
		if err := yaml.Unmarshal(raw, &list); err != nil {
			return n, fmt.Errorf("%s : %w", f, err)
		}
		for _, yr := range list {
			in, err := s.yamlInput(ctx, yr)
			if err != nil {
				return n, err
			}
			if _, err := s.Save(ctx, 0, in); err != nil {
				return n, fmt.Errorf("%s : %w", yr.Title, err)
			}
			n++
		}
	}
	return n, nil
}

func (s *Service) yamlInput(ctx context.Context, yr yamlRecipe) (Input, error) {
	in := Input{Title: yr.Title, Servings: yr.Servings, PrepMin: yr.Prep, CookMin: yr.Cook, RestMin: yr.Rest, Difficulty: yr.Difficulty,
		Utensils: yr.Utensils, Steps: yr.Steps}
	for t := range strings.SplitSeq(yr.Types, ",") {
		bit, ok := TypeByCode(strings.TrimSpace(t))
		if !ok {
			return in, fmt.Errorf("%s : type %q inconnu", yr.Title, t)
		}
		in.Types |= bit
	}
	for t := range strings.SplitSeq(yr.Tags, ",") {
		if t = strings.TrimSpace(t); t != "" {
			in.Tags = append(in.Tags, t)
		}
	}
	resolved, err := s.Resolve(ctx, ParseLines(yr.Ingredients))
	if err != nil {
		return in, err
	}
	for _, r := range resolved {
		if r.Confidence != Exact {
			return in, fmt.Errorf("%s : ingrédient « %s » non reconnu exactement (%q)", yr.Title, r.Raw, r.Name)
		}
	}
	in.Ingredients = resolved
	return in, nil
}

// PurgeSeed retire les recettes de l'ancien jeu de départ (versions
// antérieures de PetitChef), sauf celles que l'utilisateur a modifiées ou
// qui servent encore : menus, menus enregistrés, autres recettes. Appelée à
// chaque démarrage ; sans effet une fois le ménage fait.
func (s *Service) PurgeSeed(ctx context.Context) (int, error) {
	total := 0
	for {
		var ids []int64
		rows, err := s.db.R.QueryContext(ctx, `SELECT r.id FROM recipes r WHERE r.origin = 'seed' AND NOT r.modified
			AND NOT EXISTS (SELECT 1 FROM menu_entries e WHERE e.recipe_id = r.id)
			AND NOT EXISTS (SELECT 1 FROM saved_menu_entries e WHERE e.recipe_id = r.id)
			AND NOT EXISTS (SELECT 1 FROM recipe_ingredients i WHERE i.sub_recipe_id = r.id)`)
		if err != nil {
			return total, err
		}
		for rows.Next() {
			var id int64
			if err := rows.Scan(&id); err != nil {
				_ = rows.Close()
				return total, err
			}
			ids = append(ids, id)
		}
		if err := rows.Close(); err != nil {
			return total, err
		}
		if len(ids) == 0 {
			break
		}
		err = s.db.Tx(ctx, func(tx *sql.Tx) error {
			for _, id := range ids {
				if _, err := tx.ExecContext(ctx, `DELETE FROM recipes_fts WHERE rowid = ?`, id); err != nil {
					return err
				}
				if _, err := tx.ExecContext(ctx, `DELETE FROM recipes WHERE id = ?`, id); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			return total, err
		}
		total += len(ids) // une recette de base libérée sera retirée au tour suivant
	}
	if total > 0 {
		slog.InfoContext(ctx, "recettes de départ retirées", "recettes", total)
	}
	return total, nil
}

var _ = catalog.Normalize
