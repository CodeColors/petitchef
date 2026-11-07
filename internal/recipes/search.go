package recipes

import (
	"context"
	"fmt"
	"strings"

	"github.com/codecolors/petitchef/internal/catalog"
	"github.com/codecolors/petitchef/internal/platform/db"
	"github.com/codecolors/petitchef/internal/platform/text"
)

// Filter sélectionne des recettes.
type Filter struct {
	Query      string
	Type       Type
	MaxMinutes int    // temps total maximum
	Tag        string // étiquette
	MaxGrade   string // indice au plus (« B » : A ou B)
	Month      int    // de saison ce mois-là
	Diet       catalog.Diet
	MinStars   int // note moyenne minimale
	Favorites  bool
	Page       int
	PageSize   int // 0 : 30
}

// Summary est une recette dans une liste.
type Summary struct {
	ID        int64
	Title     string
	Types     Type
	TotalMin  int
	ActiveMin int
	Grade     string
	Photo     string
	Stars     int // note moyenne × 10
	Favorite  bool
	Servings  int
}

// Page est une page de recettes.
type Page struct {
	Items []Summary
	Total int
	Page  int
	More  bool
}

// ftsQuery transforme une saisie en requête FTS5 : chaque mot devient un
// préfixe obligatoire, les caractères spéciaux sont neutralisés.
func ftsQuery(q string) string {
	var terms []string
	for _, w := range strings.Fields(text.Fold(q)) {
		w = strings.Map(func(r rune) rune {
			if r == '"' || r == '*' || r == '(' || r == ')' || r == ':' || r == '^' || r == '-' || r == '+' || r == '\'' {
				return ' '
			}
			return r
		}, w)
		for _, part := range strings.Fields(w) {
			terms = append(terms, `"`+part+`"*`)
		}
	}
	return strings.Join(terms, " ")
}

// Search renvoie une page de recettes filtrées, les plus pertinentes (ou
// les mieux notées) d'abord.
func (s *Service) Search(ctx context.Context, f Filter) (Page, error) {
	size := f.PageSize
	if size <= 0 {
		size = 30
	}
	where := []string{"1 = 1"}
	var args []any
	order := "coalesce(rt.stars, 0) DESC, r.title"
	if q := ftsQuery(f.Query); q != "" {
		where = append(where, "r.id IN (SELECT rowid FROM recipes_fts WHERE recipes_fts MATCH ?)")
		args = append(args, q)
	}
	if f.Type != 0 {
		where, args = append(where, "r.types & ? != 0"), append(args, f.Type)
	}
	if f.MaxMinutes > 0 {
		where, args = append(where, "r.prep_min + r.cook_min + r.rest_min <= ?"), append(args, f.MaxMinutes)
	}
	if f.Tag != "" {
		where, args = append(where, "r.id IN (SELECT recipe_id FROM recipe_tags WHERE tag = ?)"), append(args, strings.ToLower(f.Tag))
	}
	if f.MaxGrade != "" {
		where, args = append(where, "coalesce(r.balance_manual, r.balance_grade) <= ?"), append(args, f.MaxGrade)
	}
	if f.Month >= 1 && f.Month <= 12 {
		where, args = append(where, "r.season & ? != 0"), append(args, 1<<(f.Month-1))
	}
	if f.Diet != 0 {
		t, a := DietExclusions(f.Diet)
		where, args = append(where, "r.traits & ? = 0 AND r.allergens & ? = 0"), append(args, t, a)
	}
	if f.MinStars > 0 {
		where, args = append(where, "coalesce(rt.stars, 0) >= ?"), append(args, f.MinStars*10)
	}
	if f.Favorites {
		where = append(where, "coalesce(rt.favorite, 0) = 1")
	}
	from := ` FROM recipes r LEFT JOIN (SELECT recipe_id, CAST(round(avg(CASE WHEN stars > 0 THEN stars END) * 10) AS INTEGER) AS stars,
		max(favorite) AS favorite FROM recipe_ratings GROUP BY recipe_id) rt ON rt.recipe_id = r.id WHERE ` + strings.Join(where, " AND ")
	p := Page{Page: max(f.Page, 0)}
	if err := s.db.R.QueryRowContext(ctx, `SELECT count(*)`+from, args...).Scan(&p.Total); err != nil {
		return p, err
	}
	var err error
	p.Items, err = db.Collect(ctx, s.db.R, func(row db.Scanner) (Summary, error) {
		var x Summary
		return x, row.Scan(&x.ID, &x.Title, &x.Types, &x.TotalMin, &x.ActiveMin, &x.Grade, &x.Photo, &x.Stars, &x.Favorite, &x.Servings)
	}, `SELECT r.id, r.title, r.types, r.prep_min + r.cook_min + r.rest_min, r.prep_min + r.cook_min, coalesce(r.balance_manual, r.balance_grade),
		r.photo, coalesce(rt.stars, 0), coalesce(rt.favorite, 0), r.servings`+from+fmt.Sprintf(` ORDER BY %s LIMIT %d OFFSET %d`, order, size, p.Page*size), args...)
	p.More = (p.Page+1)*size < p.Total
	return p, err
}

// DietExclusions renvoie les traits et allergènes interdits par un
// ensemble de régimes.
func DietExclusions(d catalog.Diet) (catalog.Traits, catalog.Allergens) {
	var t catalog.Traits
	var a catalog.Allergens
	if d&catalog.Vegetarian != 0 {
		t |= catalog.Meat
	}
	if d&catalog.Vegan != 0 {
		t |= catalog.Meat | catalog.Animal
	}
	if d&catalog.PorkFree != 0 {
		t |= catalog.Pork
	}
	if d&catalog.AlcoholFree != 0 {
		t |= catalog.Alcohol
	}
	if d&catalog.GlutenFree != 0 {
		a |= catalog.Gluten
	}
	if d&catalog.LactoseFree != 0 {
		a |= catalog.Milk
	}
	return t, a
}

// Tag est une étiquette et son nombre de recettes.
type Tag struct {
	Name  string
	Count int
}

// Tags renvoie les étiquettes utilisées, les plus fréquentes d'abord.
func (s *Service) Tags(ctx context.Context) ([]Tag, error) {
	return db.Collect(ctx, s.db.R, func(row db.Scanner) (Tag, error) {
		var t Tag
		return t, row.Scan(&t.Name, &t.Count)
	}, `SELECT tag, count(*) FROM recipe_tags GROUP BY tag ORDER BY count(*) DESC, tag LIMIT 40`)
}

// Titles renvoie les titres des recettes (choix d'une recette à la main,
// recettes composées).
func (s *Service) Titles(ctx context.Context, t Type) ([]Summary, error) {
	return db.Collect(ctx, s.db.R, func(row db.Scanner) (Summary, error) {
		var x Summary
		return x, row.Scan(&x.ID, &x.Title, &x.Types)
	}, `SELECT id, title, types FROM recipes WHERE ? = 0 OR types & ? != 0 ORDER BY search`, t, t)
}
