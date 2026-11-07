package recipes

import (
	"context"
	"strings"

	"github.com/codecolors/petitchef/internal/platform/db"
	"github.com/codecolors/petitchef/internal/platform/validation"
)

// Rating est l'avis d'un membre sur une recette.
type Rating struct {
	MemberID int64
	Member   string
	Stars    int
	Favorite bool
	Banned   bool
	Comment  string
}

// Ratings renvoie l'avis de chaque membre actif (vide s'il n'en a pas
// donné).
func (s *Service) Ratings(ctx context.Context, recipeID int64) ([]Rating, error) {
	return db.Collect(ctx, s.db.R, func(row db.Scanner) (Rating, error) {
		var r Rating
		return r, row.Scan(&r.MemberID, &r.Member, &r.Stars, &r.Favorite, &r.Banned, &r.Comment)
	}, `SELECT m.id, m.name, coalesce(rt.stars, 0), coalesce(rt.favorite, 0), coalesce(rt.banned, 0), coalesce(rt.comment, '')
		FROM household_members m LEFT JOIN recipe_ratings rt ON rt.member_id = m.id AND rt.recipe_id = ?
		WHERE NOT m.archived ORDER BY m.id`, recipeID)
}

// Rate enregistre l'avis d'un membre.
func (s *Service) Rate(ctx context.Context, recipeID int64, r Rating) error {
	if r.Stars < 0 || r.Stars > 5 {
		return validation.Error("Note entre 1 et 5.")
	}
	_, err := s.db.W.ExecContext(ctx, `INSERT INTO recipe_ratings (recipe_id, member_id, stars, favorite, banned, comment) VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT (recipe_id, member_id) DO UPDATE SET stars = excluded.stars, favorite = excluded.favorite, banned = excluded.banned,
		comment = excluded.comment`, recipeID, r.MemberID, r.Stars, r.Favorite, r.Banned, strings.TrimSpace(r.Comment))
	if db.IsForeignKeyViolation(err) {
		return db.ErrNotFound
	}
	return err
}

// Opinion résume les avis du foyer sur une recette (générateur).
type Opinion struct {
	Stars    int // moyenne × 10, 0 sans note
	Favorite bool
	BannedBy map[int64]bool // membres qui ne veulent plus la voir
}

// Opinions renvoie l'avis du foyer sur chaque recette notée.
func (s *Service) Opinions(ctx context.Context) (map[int64]Opinion, error) {
	type row struct {
		recipe, member   int64
		stars            int
		favorite, banned bool
	}
	rows, err := db.Collect(ctx, s.db.R, func(r db.Scanner) (row, error) {
		var x row
		return x, r.Scan(&x.recipe, &x.member, &x.stars, &x.favorite, &x.banned)
	}, `SELECT recipe_id, member_id, stars, favorite, banned FROM recipe_ratings`)
	if err != nil {
		return nil, err
	}
	out := map[int64]Opinion{}
	sums, counts := map[int64]int{}, map[int64]int{}
	for _, x := range rows {
		o := out[x.recipe]
		if o.BannedBy == nil {
			o.BannedBy = map[int64]bool{}
		}
		if x.banned {
			o.BannedBy[x.member] = true
		}
		o.Favorite = o.Favorite || x.favorite
		if x.stars > 0 {
			sums[x.recipe] += x.stars
			counts[x.recipe]++
		}
		out[x.recipe] = o
	}
	for id, n := range counts {
		o := out[id]
		o.Stars = (sums[id]*10 + n/2) / n
		out[id] = o
	}
	return out, nil
}
