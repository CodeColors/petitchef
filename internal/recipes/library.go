package recipes

import (
	"bufio"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"strconv"
	"strings"

	"github.com/codecolors/petitchef/internal/catalog"
)

// Bibliothèque de recettes embarquée (paquet library) : un format texte
// compact, une recette par bloc.
//
//	= Titre
//	types | portions | préparation | cuisson[/repos] | étiquettes | difficulté
//	200 g Farine de blé          (une ligne par ingrédient, format de saisie)
//	> Première étape.            (une ligne par étape)
//
// Les lignes vides et celles qui commencent par « # » sont ignorées. Les
// deux derniers champs de la ligne d'en-tête sont facultatifs.

// ParseLibrary lit un fichier de la bibliothèque.
func ParseLibrary(raw string) ([]yamlRecipe, error) {
	var out []yamlRecipe
	var cur *yamlRecipe
	var ingredients []string
	flush := func() {
		if cur != nil {
			cur.Ingredients = strings.Join(ingredients, "\n")
			out = append(out, *cur)
		}
		cur, ingredients = nil, nil
	}
	sc := bufio.NewScanner(strings.NewReader(raw))
	n := 0
	header := false
	for sc.Scan() {
		n++
		line := strings.TrimSpace(sc.Text())
		switch {
		case line == "" || strings.HasPrefix(line, "#"):
			continue
		case strings.HasPrefix(line, "="):
			flush()
			cur = &yamlRecipe{Title: strings.TrimSpace(line[1:]), Difficulty: 1}
			header = true
		case cur == nil:
			return nil, fmt.Errorf("ligne %d : recette sans titre", n)
		case header:
			header = false
			if err := parseLibraryHeader(cur, line); err != nil {
				return nil, fmt.Errorf("ligne %d (%s) : %w", n, cur.Title, err)
			}
		case strings.HasPrefix(line, ">"):
			cur.Steps = append(cur.Steps, strings.TrimSpace(line[1:]))
		default:
			ingredients = append(ingredients, line)
		}
	}
	flush()
	return out, sc.Err()
}

func parseLibraryHeader(r *yamlRecipe, line string) error {
	f := strings.Split(line, "|")
	if len(f) < 4 {
		return errors.New("en-tête incomplet (types | portions | préparation | cuisson)")
	}
	for i := range f {
		f[i] = strings.TrimSpace(f[i])
	}
	r.Types = f[0]
	var err error
	if r.Servings, err = strconv.Atoi(f[1]); err != nil {
		return fmt.Errorf("portions %q", f[1])
	}
	if r.Prep, err = strconv.Atoi(f[2]); err != nil {
		return fmt.Errorf("préparation %q", f[2])
	}
	cook, rest, _ := strings.Cut(f[3], "/")
	if r.Cook, err = strconv.Atoi(strings.TrimSpace(cook)); err != nil {
		return fmt.Errorf("cuisson %q", f[3])
	}
	if rest != "" {
		if r.Rest, err = strconv.Atoi(strings.TrimSpace(rest)); err != nil {
			return fmt.Errorf("repos %q", f[3])
		}
	}
	if len(f) > 4 {
		r.Tags = f[4]
	}
	if len(f) > 5 && f[5] != "" {
		if r.Difficulty, err = strconv.Atoi(f[5]); err != nil || r.Difficulty < 1 || r.Difficulty > 3 {
			return fmt.Errorf("difficulté %q", f[5])
		}
	}
	return nil
}

// LibraryRecipes lit tous les fichiers *.txt d'une bibliothèque, dans
// l'ordre de leur nom (les bases d'abord).
func LibraryRecipes(fsys fs.FS) ([]yamlRecipe, error) {
	files, err := fs.Glob(fsys, "*.txt")
	if err != nil {
		return nil, err
	}
	var all []yamlRecipe
	for _, f := range files {
		raw, err := fs.ReadFile(fsys, f)
		if err != nil {
			return nil, err
		}
		list, err := ParseLibrary(string(raw))
		if err != nil {
			return nil, fmt.Errorf("%s : %w", f, err)
		}
		all = append(all, list...)
	}
	return all, nil
}

// LibraryInput prépare l'enregistrement d'une recette de la bibliothèque :
// chaque ingrédient doit désigner exactement un produit du catalogue (ou
// une recette déjà présente).
func (s *Service) LibraryInput(ctx context.Context, r yamlRecipe) (Input, error) {
	in, err := s.yamlInput(ctx, r)
	in.SeedKey = LibraryKey(r.Title)
	return in, err
}

// LibraryKey est l'identifiant stable d'une recette de la bibliothèque.
func LibraryKey(title string) string { return "lib:" + catalog.Normalize(title) }

// LibraryTitle renvoie le titre d'une recette lue (tests).
func LibraryTitle(r yamlRecipe) string { return r.Title }

// SyncLibrary ajoute les recettes de la bibliothèque embarquée qui n'ont
// jamais été proposées : une recette supprimée par l'utilisateur ne revient
// pas, une recette de même titre déjà présente est laissée telle quelle.
// Sans effet si la bibliothèque est à jour (version). Une recette
// impossible à enregistrer (base retirée, produit supprimé du catalogue)
// est ignorée et signalée dans le journal.
func (s *Service) SyncLibrary(ctx context.Context, fsys fs.FS, version int) (int, error) {
	var current int
	err := s.db.R.QueryRowContext(ctx, `SELECT CAST(value AS INTEGER) FROM settings WHERE key = 'recipe_library_version'`).Scan(&current)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	if current >= version {
		return 0, nil
	}
	list, err := LibraryRecipes(fsys)
	if err != nil {
		return 0, err
	}
	added := 0
	for _, r := range list {
		key := LibraryKey(r.Title)
		var seen int
		if err := s.db.R.QueryRowContext(ctx, `SELECT count(*) FROM recipe_library WHERE key = ?`, key).Scan(&seen); err != nil {
			return added, err
		}
		if seen > 0 {
			continue
		}
		if _, err := s.ByTitle(ctx, r.Title); err == nil {
			if err := s.markLibrary(ctx, key); err != nil {
				return added, err
			}
			continue
		}
		in, err := s.LibraryInput(ctx, r)
		if err == nil {
			_, err = s.Save(ctx, 0, in)
		}
		if err != nil {
			slog.WarnContext(ctx, "recette de la bibliothèque ignorée", "recette", r.Title, "err", err)
			continue
		}
		if err := s.markLibrary(ctx, key); err != nil {
			return added, err
		}
		added++
	}
	if _, err := s.db.W.ExecContext(ctx, `INSERT INTO settings (key, value) VALUES ('recipe_library_version', ?)
		ON CONFLICT (key) DO UPDATE SET value = excluded.value`, strconv.Itoa(version)); err != nil {
		return added, err
	}
	slog.InfoContext(ctx, "bibliothèque de recettes chargée", "version", version, "ajoutées", added)
	return added, nil
}

func (s *Service) markLibrary(ctx context.Context, key string) error {
	_, err := s.db.W.ExecContext(ctx, `INSERT INTO recipe_library (key) VALUES (?) ON CONFLICT DO NOTHING`, key)
	return err
}
