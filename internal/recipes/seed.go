package recipes

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/codecolors/petitchef/internal/catalog"
	"github.com/codecolors/petitchef/internal/platform/db"
)

// SeedVersion est la version du jeu de départ de recettes ; à augmenter à
// chaque modification de seed/.
const SeedVersion = 1

//go:embed seed/*.yaml
var seedFS embed.FS

// seedRecipe est une recette du jeu de départ (voir seed/LISEZMOI.md).
type seedRecipe struct {
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

// SeedRecipes lit le jeu de départ, dans l'ordre des fichiers (les bases
// utilisées par d'autres recettes d'abord).
func SeedRecipes() ([]seedRecipe, error) {
	files, err := fs.Glob(seedFS, "seed/*.yaml")
	if err != nil {
		return nil, err
	}
	var out []seedRecipe
	for _, f := range files {
		raw, err := fs.ReadFile(seedFS, f)
		if err != nil {
			return nil, err
		}
		var list []seedRecipe
		if err := yaml.Unmarshal(raw, &list); err != nil {
			return nil, fmt.Errorf("%s : %w", f, err)
		}
		out = append(out, list...)
	}
	return out, nil
}

// input convertit une recette du jeu de départ en saisie, ingrédients
// rapprochés du catalogue ; tout rapprochement inexact est une erreur.
func (s *Service) seedInput(ctx context.Context, sr seedRecipe) (Input, error) {
	in := Input{Title: sr.Title, Servings: sr.Servings, PrepMin: sr.Prep, CookMin: sr.Cook, RestMin: sr.Rest, Difficulty: sr.Difficulty,
		Utensils: sr.Utensils, Steps: sr.Steps, SeedKey: strings.ReplaceAll(catalog.Normalize(sr.Title), " ", "-")}
	for t := range strings.SplitSeq(sr.Types, ",") {
		bit, ok := TypeByCode(strings.TrimSpace(t))
		if !ok {
			return in, fmt.Errorf("%s : type %q inconnu", sr.Title, t)
		}
		in.Types |= bit
	}
	for t := range strings.SplitSeq(sr.Tags, ",") {
		if t = strings.TrimSpace(t); t != "" {
			in.Tags = append(in.Tags, t)
		}
	}
	resolved, err := s.Resolve(ctx, ParseLines(sr.Ingredients))
	if err != nil {
		return in, err
	}
	for _, r := range resolved {
		if r.Confidence != Exact {
			return in, fmt.Errorf("%s : ingrédient « %s » non reconnu exactement (%q)", sr.Title, r.Raw, r.Name)
		}
	}
	in.Ingredients = resolved
	return in, nil
}

// SyncSeed charge le jeu de départ de recettes (après celui du
// catalogue) : recettes nouvelles ajoutées, recettes non modifiées mises à
// jour, recettes modifiées par l'utilisateur laissées telles quelles.
func (s *Service) SyncSeed(ctx context.Context) error {
	var current int
	err := s.db.W.QueryRowContext(ctx, `SELECT CAST(value AS INTEGER) FROM settings WHERE key = 'recipes_seed_version'`).Scan(&current)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if current >= SeedVersion {
		return nil
	}
	list, err := SeedRecipes()
	if err != nil {
		return err
	}
	added := 0
	for _, sr := range list {
		in, err := s.seedInput(ctx, sr)
		if err != nil {
			return fmt.Errorf("jeu de départ des recettes : %w", err)
		}
		var id int64
		var modified bool
		err = s.db.R.QueryRowContext(ctx, `SELECT id, modified FROM recipes WHERE seed_key = ?`, in.SeedKey).Scan(&id, &modified)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			if _, err := s.Save(ctx, 0, in); err != nil {
				return fmt.Errorf("%s : %w", sr.Title, err)
			}
			added++
		case err != nil:
			return err
		case !modified:
			if _, err := s.Save(ctx, id, in); err != nil {
				return fmt.Errorf("%s : %w", sr.Title, err)
			}
			if _, err := s.db.W.ExecContext(ctx, `UPDATE recipes SET modified = 0 WHERE id = ?`, id); err != nil {
				return err
			}
		}
	}
	if _, err := s.db.W.ExecContext(ctx, `INSERT INTO settings (key, value) VALUES ('recipes_seed_version', ?)
		ON CONFLICT (key) DO UPDATE SET value = excluded.value`, strconv.Itoa(SeedVersion)); err != nil {
		return err
	}
	slog.InfoContext(ctx, "jeu de départ des recettes chargé", "version", SeedVersion, "ajoutées", added)
	return nil
}

var _ = db.ErrNotFound
