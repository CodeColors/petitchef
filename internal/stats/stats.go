// Package stats calcule les statistiques du foyer à partir des briques
// existantes, sans données parallèles : le journal des mouvements de stock
// (gaspillage, dépenses, consommation) et les menus (recettes cuisinées,
// équilibre).
package stats

import (
	"context"

	"github.com/codecolors/petitchef/internal/catalog"
	"github.com/codecolors/petitchef/internal/platform/calendar"
	"github.com/codecolors/petitchef/internal/platform/db"
	"github.com/codecolors/petitchef/internal/platform/qty"
)

// Service calcule les statistiques.
type Service struct{ db *db.DB }

// NewService crée le service des statistiques.
func NewService(d *db.DB) *Service { return &Service{db: d} }

// Point est une valeur d'une série (mois, semaine…).
type Point struct {
	Label string
	Value int64
}

// Ranked est une ligne de classement.
type Ranked struct {
	ID    int64
	Name  string
	Count int64
	Value int64 // centimes
	Qty   int64
	Unit  qty.Base
}

// Report rassemble les statistiques d'une période.
type Report struct {
	From, To        calendar.Date
	TopRecipes      []Ranked
	Balance         []Point // indice moyen par semaine (0 = E … 100 = A)
	WasteByMonth    []Point // centimes
	WasteProducts   []Ranked
	WasteCount      int64
	SpendByMonth    []Point // centimes
	SpendByAisle    []Ranked
	SpendByUniverse []Ranked
	Household       []Ranked // consommation hygiène et entretien
}

var monthsShort = [...]string{"janv.", "févr.", "mars", "avr.", "mai", "juin", "juil.", "août", "sept.", "oct.", "nov.", "déc."}

func monthLabel(ym string) string {
	if len(ym) != 7 {
		return ym
	}
	m := int(ym[5]-'0')*10 + int(ym[6]-'0')
	return monthsShort[m-1] + " " + ym[2:4]
}

// months renvoie les mois (AAAA-MM) de la période, dans l'ordre.
func months(from, to calendar.Date) []string {
	var out []string
	for d := calendar.Date(string(from)[:8] + "01"); d <= to; {
		out = append(out, string(d)[:7])
		t := d.Time().AddDate(0, 1, 0)
		d = calendar.FromTime(t)
	}
	return out
}

func series(ctx context.Context, q *db.DB, all []string, query string, args ...any) ([]Point, error) {
	rows, err := db.Collect(ctx, q.R, func(r db.Scanner) (Point, error) {
		var p Point
		return p, r.Scan(&p.Label, &p.Value)
	}, query, args...)
	if err != nil {
		return nil, err
	}
	byMonth := map[string]int64{}
	for _, p := range rows {
		byMonth[p.Label] = p.Value
	}
	out := make([]Point, len(all))
	for i, m := range all {
		out[i] = Point{Label: monthLabel(m), Value: byMonth[m]}
	}
	return out, nil
}

// Compute calcule les statistiques de la période [from, to].
func (s *Service) Compute(ctx context.Context, from, to calendar.Date) (Report, error) {
	r := Report{From: from, To: to}
	ms := months(from, to)
	var err error
	scanRanked := func(row db.Scanner) (Ranked, error) {
		var x Ranked
		return x, row.Scan(&x.ID, &x.Name, &x.Count, &x.Value, &x.Qty, &x.Unit)
	}
	if r.TopRecipes, err = db.Collect(ctx, s.db.R, scanRanked, `
		SELECT r.id, r.title, count(*), 0, 0, 'piece' FROM menu_entries e JOIN meals m ON m.id = e.meal_id JOIN recipes r ON r.id = e.recipe_id
		WHERE e.status = 'cuisine' AND e.leftovers_from IS NULL AND m.date BETWEEN ? AND ?
		GROUP BY r.id ORDER BY count(*) DESC, r.title LIMIT 10`, from, to); err != nil {
		return r, err
	}
	weeks, err := db.Collect(ctx, s.db.R, func(row db.Scanner) (Point, error) {
		var p Point
		return p, row.Scan(&p.Label, &p.Value)
	}, `SELECT date(m.date, '-' || ((CAST(strftime('%w', m.date) AS INTEGER) + 6) % 7) || ' days') AS week,
			CAST(round(avg(100 - (unicode(coalesce(r.balance_manual, r.balance_grade)) - 65) * 25)) AS INTEGER)
		FROM menu_entries e JOIN meals m ON m.id = e.meal_id JOIN recipes r ON r.id = e.recipe_id
		WHERE m.date BETWEEN ? AND ? AND e.leftovers_from IS NULL
		GROUP BY week ORDER BY week`, from, to)
	if err != nil {
		return r, err
	}
	for _, w := range weeks {
		r.Balance = append(r.Balance, Point{Label: calendar.Date(w.Label).Format()[:5], Value: w.Value})
	}
	if r.WasteByMonth, err = series(ctx, s.db, ms, `SELECT substr(date, 1, 7), coalesce(sum(value_cents), 0) FROM stock_movements
		WHERE kind = 'perte' AND NOT undone AND date BETWEEN ? AND ? GROUP BY 1`, from, to); err != nil {
		return r, err
	}
	if r.WasteProducts, err = db.Collect(ctx, s.db.R, scanRanked, `
		SELECT p.id, p.name, count(*), coalesce(sum(m.value_cents), 0), -sum(m.qty), p.unit FROM stock_movements m JOIN products p ON p.id = m.product_id
		WHERE m.kind = 'perte' AND NOT m.undone AND m.date BETWEEN ? AND ?
		GROUP BY p.id ORDER BY 4 DESC, 3 DESC LIMIT 10`, from, to); err != nil {
		return r, err
	}
	if err := s.db.R.QueryRowContext(ctx, `SELECT count(*) FROM stock_movements WHERE kind = 'perte' AND NOT undone AND date BETWEEN ? AND ?`,
		from, to).Scan(&r.WasteCount); err != nil {
		return r, err
	}
	if r.SpendByMonth, err = series(ctx, s.db, ms, `SELECT substr(date, 1, 7), coalesce(sum(value_cents), 0) FROM stock_movements
		WHERE kind IN ('achat', 'ajout') AND value_cents IS NOT NULL AND NOT undone AND date BETWEEN ? AND ? GROUP BY 1`, from, to); err != nil {
		return r, err
	}
	if r.SpendByAisle, err = db.Collect(ctx, s.db.R, scanRanked, `
		SELECT a.id, a.name, count(*), coalesce(sum(m.value_cents), 0), 0, 'piece' FROM stock_movements m
		JOIN products p ON p.id = m.product_id JOIN aisles a ON a.id = p.aisle_id
		WHERE m.kind IN ('achat', 'ajout') AND NOT m.undone AND m.value_cents IS NOT NULL AND m.date BETWEEN ? AND ?
		GROUP BY a.id ORDER BY 4 DESC`, from, to); err != nil {
		return r, err
	}
	univ, err := db.Collect(ctx, s.db.R, scanRanked, `
		SELECT 0, p.universe, count(*), coalesce(sum(m.value_cents), 0), 0, 'piece' FROM stock_movements m JOIN products p ON p.id = m.product_id
		WHERE m.kind IN ('achat', 'ajout') AND NOT m.undone AND m.value_cents IS NOT NULL AND m.date BETWEEN ? AND ?
		GROUP BY p.universe ORDER BY 4 DESC`, from, to)
	if err != nil {
		return r, err
	}
	for _, u := range univ {
		u.Name = catalog.Universe(u.Name).Label()
		r.SpendByUniverse = append(r.SpendByUniverse, u)
	}
	r.Household, err = db.Collect(ctx, s.db.R, scanRanked, `
		SELECT p.id, p.name, count(*), 0, -sum(m.qty), p.unit FROM stock_movements m JOIN products p ON p.id = m.product_id
		WHERE m.kind IN ('consommation', 'recette') AND NOT m.undone AND p.universe IN ('hygiene', 'entretien', 'maison')
		AND m.date BETWEEN ? AND ? GROUP BY p.id ORDER BY count(*) DESC, p.name LIMIT 15`, from, to)
	return r, err
}
