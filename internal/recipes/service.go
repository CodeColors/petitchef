package recipes

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/codecolors/petitchef/internal/catalog"
	"github.com/codecolors/petitchef/internal/platform/calendar"
	"github.com/codecolors/petitchef/internal/platform/db"
	"github.com/codecolors/petitchef/internal/platform/qty"
	"github.com/codecolors/petitchef/internal/platform/validation"
)

// Service gère les recettes.
type Service struct {
	db      *db.DB
	catalog *catalog.Service
}

// NewService crée le service des recettes.
func NewService(d *db.DB, c *catalog.Service) *Service { return &Service{db: d, catalog: c} }

const recipeColumns = `r.id, r.title, r.types, r.servings, r.prep_min, r.cook_min, r.rest_min, r.difficulty, r.utensils, r.source, r.notes,
	r.photo, coalesce(r.variant_of, 0), r.allergens, r.traits, r.season, r.balance_score, r.balance_grade, coalesce(r.balance_manual, ''),
	r.protein, coalesce(r.starch_id, 0), r.heavy, r.veg_share, r.cooked_count, coalesce(r.last_cooked, ''), r.origin, r.modified`

func scanRecipe(row db.Scanner) (Recipe, error) {
	var r Recipe
	err := row.Scan(&r.ID, &r.Title, &r.Types, &r.Servings, &r.PrepMin, &r.CookMin, &r.RestMin, &r.Difficulty, &r.Utensils, &r.Source,
		&r.Notes, &r.Photo, &r.VariantOf, &r.Allergens, &r.Traits, &r.Season, &r.BalanceScore, &r.BalanceGrade, &r.BalanceManual,
		&r.Protein, &r.StarchID, &r.Heavy, &r.VegShare, &r.CookedCount, &r.LastCooked, &r.Origin, &r.Modified)
	return r, err
}

// Get renvoie une recette complète (ingrédients, étapes, étiquettes).
func (s *Service) Get(ctx context.Context, id int64) (Recipe, error) {
	r, err := scanRecipe(s.db.R.QueryRowContext(ctx, `SELECT `+recipeColumns+` FROM recipes r WHERE r.id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return r, db.ErrNotFound
	}
	if err != nil {
		return r, err
	}
	if r.Ingredients, err = s.ingredients(ctx, s.db.R, id); err != nil {
		return r, err
	}
	if r.Steps, err = db.Collect(ctx, s.db.R, func(row db.Scanner) (Step, error) {
		var st Step
		return st, row.Scan(&st.Position, &st.Text, &st.TimerSec)
	}, `SELECT position, text, timer_sec FROM recipe_steps WHERE recipe_id = ? ORDER BY position`, id); err != nil {
		return r, err
	}
	r.Tags, err = db.Collect(ctx, s.db.R, func(row db.Scanner) (string, error) {
		var t string
		return t, row.Scan(&t)
	}, `SELECT tag FROM recipe_tags WHERE recipe_id = ? ORDER BY tag`, id)
	return r, err
}

func (s *Service) ingredients(ctx context.Context, q *sql.DB, id int64) ([]Ingredient, error) {
	return db.Collect(ctx, q, func(row db.Scanner) (Ingredient, error) {
		var in Ingredient
		err := row.Scan(&in.ID, &in.Position, &in.Group, &in.ProductID, &in.SubRecipe, &in.Name, &in.Qty, &in.InputQty, &in.InputUnit,
			&in.Note, &in.Optional, &in.ToTaste, &in.Unit, &in.Staple, &in.Group2)
		in.PieceFrac = in.InputUnit == "piece" && in.InputQty%1000 != 0
		in.Convertible = in.Qty > 0 || in.InputQty == 0
		return in, err
	}, `SELECT i.id, i.position, i.group_name, coalesce(i.product_id, 0), coalesce(i.sub_recipe_id, 0),
			coalesce(p.name, r.title), i.qty, i.input_qty, i.input_unit, i.note, i.optional, i.to_taste,
			coalesce(p.unit, ''), coalesce(p.staple, 0), coalesce(p.food_group, '')
		FROM recipe_ingredients i LEFT JOIN products p ON p.id = i.product_id LEFT JOIN recipes r ON r.id = i.sub_recipe_id
		WHERE i.recipe_id = ? ORDER BY i.position`, id)
}

// loader renvoie un lecteur de recettes avec cache, pour Expand.
func (s *Service) loader(ctx context.Context) Loader {
	cache := map[int64]Recipe{}
	return func(id int64) (Recipe, error) {
		if r, ok := cache[id]; ok {
			return r, nil
		}
		r, err := s.Get(ctx, id)
		if err == nil {
			cache[id] = r
		}
		return r, err
	}
}

// Loader renvoie un lecteur de recettes avec cache (moteurs de menus et de
// courses).
func (s *Service) Loader(ctx context.Context) Loader { return s.loader(ctx) }

// Needs déroule une recette pour un nombre de portions (millièmes) en
// besoins de produits, recettes composées comprises.
func (s *Service) Needs(ctx context.Context, id int64, portionsMilli int64) ([]Need, error) {
	load := s.loader(ctx)
	r, err := load(id)
	if err != nil {
		return nil, err
	}
	return Expand(r, portionsMilli, load)
}

// Input est la saisie d'une recette : les ingrédients sont des lignes de
// texte, rapprochées du catalogue au moment de l'enregistrement.
type Input struct {
	Title         string
	Types         Type
	Servings      int
	PrepMin       int
	CookMin       int
	RestMin       int
	Difficulty    int
	Utensils      string
	Source        string
	Notes         string
	Tags          []string
	Ingredients   []Resolved
	Steps         []string
	BalanceManual string
	VariantOf     int64
	SeedKey       string // base de départ
}

// Resolved est une ligne d'ingrédient rapprochée du catalogue.
type Resolved struct {
	Line
	ProductID   int64
	SubRecipe   int64
	Confidence  Confidence
	Suggestions []catalog.Hit
}

// Confidence qualifie un rapprochement.
type Confidence int

const (
	NoMatch   Confidence = iota // aucun produit trouvé
	Suggested                   // proposition à valider
	Partial                     // nom raccourci (« oignon émincé » → « oignon »)
	Exact                       // nom ou synonyme exact
)

// Resolve rapproche des lignes analysées du catalogue (et des recettes,
// pour les noms entre crochets).
func (s *Service) Resolve(ctx context.Context, lines []Line) ([]Resolved, error) {
	out := make([]Resolved, len(lines))
	for i, l := range lines {
		r := Resolved{Line: l}
		if l.SubRecipe {
			id, err := s.ByTitle(ctx, l.Name)
			if err != nil && !errors.Is(err, db.ErrNotFound) {
				return nil, err
			}
			if id > 0 {
				r.SubRecipe, r.Confidence = id, Exact
			}
			out[i] = r
			continue
		}
		// « 4 filets de saumon », « 1 gousse de vanille » : l'unité propre
		// fait partie du nom d'un produit.
		if _, _, generic := qty.Generic(l.Unit); l.Unit != "" && !generic && l.UnitText != "" {
			for _, cand := range []string{l.UnitText + " de " + l.Name, l.UnitText + " " + l.Name} {
				if id, err := s.catalog.Resolve(ctx, cand); err == nil {
					r.ProductID, r.Confidence, r.Unit, r.Name = id, Exact, "", cand
					break
				}
			}
			if r.ProductID > 0 {
				out[i] = r
				continue
			}
		}
		if id, err := s.catalog.Resolve(ctx, l.Name); err == nil {
			r.ProductID, r.Confidence = id, Exact
		} else if !errors.Is(err, db.ErrNotFound) {
			return nil, err
		} else {
			words := strings.Fields(l.Name)
			for n := len(words) - 1; n >= 1 && r.ProductID == 0; n-- {
				for _, cand := range []string{strings.Join(words[:n], " "), strings.Join(words[len(words)-n:], " ")} {
					if id, err := s.catalog.Resolve(ctx, cand); err == nil {
						r.ProductID, r.Confidence = id, Partial
						rest := strings.TrimSpace(strings.Replace(l.Name, cand, "", 1))
						if r.Note == "" {
							r.Note = rest
						}
						break
					}
				}
			}
		}
		if r.ProductID == 0 {
			hits, err := s.catalog.Search(ctx, l.Name, 5)
			if err != nil {
				return nil, err
			}
			r.Suggestions = hits
			if len(hits) > 0 {
				r.ProductID, r.Confidence = hits[0].ID, Suggested
			}
		}
		out[i] = r
	}
	return out, nil
}

// ByTitle renvoie la recette d'un titre exact (forme normalisée).
func (s *Service) ByTitle(ctx context.Context, title string) (int64, error) {
	var id int64
	err := s.db.R.QueryRowContext(ctx, `SELECT id FROM recipes WHERE search = ? ORDER BY id LIMIT 1`, catalog.Normalize(title)).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, db.ErrNotFound
	}
	return id, err
}

var timerRe = regexp.MustCompile(`(?i)(\d+)\s*(min\b|minutes?|h\b|heures?)`)

// stepTimer détecte une durée dans le texte d'une étape (« 20 min »).
func stepTimer(text string) int {
	m := timerRe.FindStringSubmatch(text)
	if m == nil {
		return 0
	}
	n, _ := strconv.Atoi(m[1])
	if strings.HasPrefix(strings.ToLower(m[2]), "h") {
		return n * 3600
	}
	return n * 60
}

func validateInput(in *Input) error {
	in.Title = strings.TrimSpace(in.Title)
	switch {
	case in.Title == "":
		return validation.Error("Le titre de la recette est obligatoire.")
	case utf8.RuneCountInString(in.Title) > 150:
		return validation.Error("Titre trop long : 150 caractères au plus.")
	case in.Types == 0:
		return validation.Error("Choisissez au moins un type (entrée, plat, dessert…).")
	case in.Servings < 1 || in.Servings > 100:
		return validation.Error("Nombre de portions entre 1 et 100.")
	case in.PrepMin < 0 || in.CookMin < 0 || in.RestMin < 0 || in.PrepMin > 10000 || in.CookMin > 10000 || in.RestMin > 10000:
		return validation.Error("Temps invalide.")
	case in.BalanceManual != "" && (len(in.BalanceManual) != 1 || in.BalanceManual[0] < 'A' || in.BalanceManual[0] > 'E'):
		return validation.Error("Indice d'équilibre : A à E.")
	}
	if in.Difficulty < 1 || in.Difficulty > 3 {
		in.Difficulty = 1
	}
	for _, r := range in.Ingredients {
		if r.ProductID == 0 && r.SubRecipe == 0 {
			return validation.Errorf("Ingrédient « %s » : choisissez le produit correspondant.", r.Raw)
		}
	}
	return nil
}

// Save crée (id nul) ou enregistre une recette ; renvoie son identifiant.
func (s *Service) Save(ctx context.Context, id int64, in Input) (int64, error) {
	if err := validateInput(&in); err != nil {
		return 0, err
	}
	load := s.loader(ctx)
	for _, r := range in.Ingredients {
		if r.SubRecipe > 0 && id > 0 {
			if err := CheckCycle(id, r.SubRecipe, load); err != nil {
				if errors.Is(err, ErrCycle) {
					return 0, validation.Errorf("« %s » utilise déjà cette recette : une recette composée ne peut pas boucler.", r.Name)
				}
				return 0, err
			}
		}
	}
	ids := make([]int64, 0, len(in.Ingredients))
	for _, r := range in.Ingredients {
		if r.ProductID > 0 {
			ids = append(ids, r.ProductID)
		}
	}
	products, err := s.catalog.Many(ctx, ids)
	if err != nil {
		return 0, err
	}
	err = s.db.Tx(ctx, func(tx *sql.Tx) error {
		manual := any(nil)
		if in.BalanceManual != "" {
			manual = in.BalanceManual
		}
		search := catalog.Normalize(in.Title)
		if id == 0 {
			origin, seedKey := "utilisateur", any(nil)
			if in.SeedKey != "" {
				origin, seedKey = "seed", in.SeedKey
			}
			res, err := tx.ExecContext(ctx, `INSERT INTO recipes (title, search, types, servings, prep_min, cook_min, rest_min, difficulty,
				utensils, source, notes, balance_manual, variant_of, origin, seed_key) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				in.Title, search, in.Types, in.Servings, in.PrepMin, in.CookMin, in.RestMin, in.Difficulty, in.Utensils, in.Source, in.Notes,
				manual, nullID(in.VariantOf), origin, seedKey)
			if err != nil {
				return err
			}
			if id, err = res.LastInsertId(); err != nil {
				return err
			}
		} else {
			res, err := tx.ExecContext(ctx, `UPDATE recipes SET title = ?, search = ?, types = ?, servings = ?, prep_min = ?, cook_min = ?,
				rest_min = ?, difficulty = ?, utensils = ?, source = ?, notes = ?, balance_manual = ?, modified = 1 WHERE id = ?`,
				in.Title, search, in.Types, in.Servings, in.PrepMin, in.CookMin, in.RestMin, in.Difficulty, in.Utensils, in.Source, in.Notes,
				manual, id)
			if err != nil {
				return err
			}
			if n, _ := res.RowsAffected(); n == 0 {
				return db.ErrNotFound
			}
		}
		for _, q := range []string{`DELETE FROM recipe_ingredients WHERE recipe_id = ?`, `DELETE FROM recipe_steps WHERE recipe_id = ?`,
			`DELETE FROM recipe_tags WHERE recipe_id = ?`} {
			if _, err := tx.ExecContext(ctx, q, id); err != nil {
				return err
			}
		}
		for i, r := range in.Ingredients {
			base := int64(0)
			if r.SubRecipe > 0 {
				base = r.Milli
				if base == 0 {
					var servings int64
					if err := tx.QueryRowContext(ctx, `SELECT servings FROM recipes WHERE id = ?`, r.SubRecipe).Scan(&servings); err != nil {
						return err
					}
					base = servings * 1000
				}
			} else if r.Milli > 0 {
				base = ToBase(products[r.ProductID], r.Milli, r.Unit)
			}
			_, err := tx.ExecContext(ctx, `INSERT INTO recipe_ingredients (recipe_id, position, group_name, product_id, sub_recipe_id, qty,
				input_qty, input_unit, note, optional, to_taste) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				id, i+1, r.Group, nullID(r.ProductID), nullID(r.SubRecipe), base, r.Milli, unitOrBase(r.Unit, r.Milli), r.Note, r.Optional, r.ToTaste)
			if err != nil {
				return err
			}
		}
		for i, st := range in.Steps {
			st = strings.TrimSpace(st)
			if st == "" {
				continue
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO recipe_steps (recipe_id, position, text, timer_sec) VALUES (?, ?, ?, ?)`,
				id, i+1, st, stepTimer(st)); err != nil {
				return err
			}
		}
		for _, t := range in.Tags {
			t = strings.ToLower(strings.TrimSpace(t))
			if t == "" {
				continue
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO recipe_tags (recipe_id, tag) VALUES (?, ?) ON CONFLICT DO NOTHING`, id, t); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return id, s.Refresh(ctx, id)
}

func unitOrBase(u string, milli int64) string {
	if u == "" && milli > 0 {
		return "piece"
	}
	return u
}

// ToBase convertit une quantité saisie en unités de base du produit ; les
// contenants (boîte, paquet, sachet, pot…) sans conversion propre valent
// le plus petit conditionnement. 0 si la conversion est impossible.
func ToBase(p catalog.Product, milli int64, unit string) int64 {
	if unit == "" {
		unit = "piece"
		if p.Unit != qty.Piece && p.PieceG == 0 {
			unit = ""
		}
	}
	c := p.Conv()
	if v, ok := c.ToBase(milli, unit); ok {
		return v
	}
	switch unit {
	case "boite", "paquet", "sachet", "pot", "brique", "bouteille", "tablette", "rouleau", "botte", "bouquet":
		if len(p.Packages) > 0 {
			return p.Packages[0].Qty * milli / 1000
		}
	}
	return 0
}

func nullID(v int64) any {
	if v <= 0 {
		return nil
	}
	return v
}

// Refresh recalcule les champs dérivés d'une recette (allergènes, régimes,
// saison, indice d'équilibre, protéine principale…), puis de celles qui
// l'utilisent, et met à jour l'index de recherche.
func (s *Service) Refresh(ctx context.Context, id int64) error {
	grid, err := s.Grid(ctx)
	if err != nil {
		return err
	}
	return s.refresh(ctx, id, grid, map[int64]bool{})
}

func (s *Service) refresh(ctx context.Context, id int64, grid Grid, done map[int64]bool) error {
	if done[id] {
		return nil
	}
	done[id] = true
	r, err := s.Get(ctx, id)
	if err != nil {
		return err
	}
	a, err := s.Analyze(ctx, r, grid)
	if err != nil {
		return err
	}
	var names []string
	for _, in := range r.Ingredients {
		names = append(names, in.Name)
	}
	err = s.db.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE recipes SET allergens = ?, traits = ?, season = ?, balance_score = ?, balance_grade = ?,
			protein = ?, starch_id = ?, heavy = ?, veg_share = ? WHERE id = ?`,
			a.Allergens, a.Traits, a.Season, a.Balance.Score, a.Balance.Grade, a.Protein, nullID(a.StarchID), a.Heavy, a.VegShare, id); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM recipes_fts WHERE rowid = ?`, id); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO recipes_fts (rowid, title, ingredients, tags) VALUES (?, ?, ?, ?)`,
			id, r.Title, strings.Join(names, " "), strings.Join(r.Tags, " "))
		return err
	})
	if err != nil {
		return err
	}
	parents, err := db.Collect(ctx, s.db.R, func(row db.Scanner) (int64, error) {
		var p int64
		return p, row.Scan(&p)
	}, `SELECT DISTINCT recipe_id FROM recipe_ingredients WHERE sub_recipe_id = ?`, id)
	if err != nil {
		return err
	}
	for _, p := range parents {
		if err := s.refresh(ctx, p, grid, done); err != nil {
			return err
		}
	}
	return nil
}

// RefreshProduct recalcule les recettes qui utilisent un produit (après la
// modification de sa fiche).
func (s *Service) RefreshProduct(ctx context.Context, productID int64) error {
	ids, err := db.Collect(ctx, s.db.R, func(row db.Scanner) (int64, error) {
		var id int64
		return id, row.Scan(&id)
	}, `SELECT DISTINCT recipe_id FROM recipe_ingredients WHERE product_id = ?`, productID)
	if err != nil {
		return err
	}
	grid, err := s.Grid(ctx)
	if err != nil {
		return err
	}
	done := map[int64]bool{}
	for _, id := range ids {
		if err := s.refresh(ctx, id, grid, done); err != nil {
			return err
		}
	}
	return nil
}

// RefreshAll recalcule toutes les recettes (changement de grille).
func (s *Service) RefreshAll(ctx context.Context) error {
	ids, err := db.Collect(ctx, s.db.R, func(row db.Scanner) (int64, error) {
		var id int64
		return id, row.Scan(&id)
	}, `SELECT id FROM recipes`)
	if err != nil {
		return err
	}
	grid, err := s.Grid(ctx)
	if err != nil {
		return err
	}
	done := map[int64]bool{}
	for _, id := range ids {
		if err := s.refresh(ctx, id, grid, done); err != nil {
			return err
		}
	}
	return nil
}

// Analysis est le résultat de l'analyse d'une recette.
type Analysis struct {
	Allergens catalog.Allergens
	Traits    catalog.Traits
	Season    catalog.Months
	Balance   Balance
	Protein   catalog.Group
	StarchID  int64
	Heavy     bool
	VegShare  int
	Nutrition *Nutrition // par portion, si toutes les valeurs sont connues
	SeasonIn  []string   // ingrédients de saison ce mois-ci (affichage)
}

// Analyze calcule les indicateurs d'une recette à partir de ses
// ingrédients (recettes composées déroulées, pour une fournée).
func (s *Service) Analyze(ctx context.Context, r Recipe, grid Grid) (Analysis, error) {
	var a Analysis
	needs, err := Expand(r, int64(r.Servings)*1000, s.loader(ctx))
	if err != nil {
		return a, err
	}
	ids := make([]int64, 0, len(needs))
	for _, n := range needs {
		ids = append(ids, n.ProductID)
	}
	products, err := s.catalog.Many(ctx, ids)
	if err != nil {
		return a, err
	}
	grams := map[catalog.Group]int64{}
	weightByProduct := map[int64]int64{}
	a.Season = catalog.AllYear
	allNutrition := len(needs) > 0
	var nut Nutrition
	for _, n := range needs {
		p := products[n.ProductID]
		a.Allergens |= p.Allergens
		a.Traits |= p.Traits
		if p.Season != 0 && p.Season != catalog.AllYear && !n.Optional && !n.ToTaste {
			a.Season &= p.Season
		}
		g, ok := p.Conv().Grams(n.Qty)
		if !ok {
			if !n.ToTaste {
				a.Balance.Unweighed = append(a.Balance.Unweighed, p.Name)
			}
			continue
		}
		if n.Optional {
			continue
		}
		grams[p.Group] += g
		weightByProduct[p.ID] += g
		if p.Nutrition.Known() {
			nut.Kcal += p.Nutrition.Kcal * g / 100
			nut.Proteins += p.Nutrition.Proteins * g / 100
			nut.Carbs += p.Nutrition.Carbs * g / 100
			nut.Fat += p.Nutrition.Fat * g / 100
			nut.Fibres += p.Nutrition.Fibres * g / 100
		} else if g > 0 && !neutral(p.Group) {
			allNutrition = false
		}
	}
	if a.Season == 0 {
		a.Season = catalog.AllYear // ingrédients jamais de saison en même temps : pas d'indication
	}
	a.Balance = ComputeBalance(grams, grid)
	a.Balance.Unweighed = slices.Compact(a.Balance.Unweighed)
	total := a.Balance.Weighed
	if total > 0 {
		var proteinW int64
		for _, grp := range catalog.Groups {
			if grp.IsProtein() && grams[grp] > proteinW && grams[grp]*10 >= total {
				a.Protein, proteinW = grp, grams[grp]
			}
		}
		rich := grams[catalog.Fats] + grams[catalog.Sweets] + grams[catalog.Cheese] + grams[catalog.ColdCuts] + grams[catalog.Processed]
		a.Heavy = rich*100/total >= 30 || a.Balance.Score < -15
		a.VegShare = int((grams[catalog.Vegetables] + grams[catalog.Fruits] + grams[catalog.Pulses]) * 100 / total)
	}
	var starchW int64
	for id, w := range weightByProduct {
		if products[id].Group.IsStarch() && (w > starchW || (w == starchW && id < a.StarchID)) {
			a.StarchID, starchW = id, w
		}
	}
	if allNutrition && r.Servings > 0 {
		div := int64(r.Servings)
		a.Nutrition = &Nutrition{Kcal: nut.Kcal / div, Proteins: nut.Proteins / div, Carbs: nut.Carbs / div, Fat: nut.Fat / div, Fibres: nut.Fibres / div}
	}
	return a, nil
}

// Grid renvoie la grille de l'indice d'équilibre (réglage, ou défaut).
func (s *Service) Grid(ctx context.Context) (Grid, error) {
	g := DefaultGrid()
	var raw string
	err := s.db.R.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = 'balance_grid'`).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return g, nil
	}
	if err != nil {
		return g, err
	}
	var saved Grid
	if json.Unmarshal([]byte(raw), &saved) != nil || saved.Validate() != nil {
		saved = g // réglage illisible : grille par défaut
	}
	for k, v := range saved.Coef {
		g.Coef[k] = v
	}
	g.Thresholds = saved.Thresholds
	return g, nil
}

// SaveGrid enregistre la grille et recalcule toutes les recettes.
func (s *Service) SaveGrid(ctx context.Context, g Grid) error {
	if err := g.Validate(); err != nil {
		return validation.Error(err.Error())
	}
	raw, err := json.Marshal(g)
	if err != nil {
		return err
	}
	if _, err := s.db.W.ExecContext(ctx, `INSERT INTO settings (key, value) VALUES ('balance_grid', ?)
		ON CONFLICT (key) DO UPDATE SET value = excluded.value`, string(raw)); err != nil {
		return err
	}
	return s.RefreshAll(ctx)
}

// Delete supprime une recette ; voir DeleteMany.
func (s *Service) Delete(ctx context.Context, id int64) error {
	_, err := s.DeleteMany(ctx, []int64{id})
	return err
}

// DeleteMany supprime des recettes et les retire de tous les menus, passés
// comme à venir (plats prévus, cuisinés, restes, menus enregistrés). Une
// recette utilisée comme ingrédient par une recette conservée ne peut pas
// être supprimée. Renvoie les photos à effacer.
func (s *Service) DeleteMany(ctx context.Context, ids []int64) ([]string, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	list, err := json.Marshal(ids)
	if err != nil {
		return nil, err
	}
	var photos []string
	err = s.db.Tx(ctx, func(tx *sql.Tx) error {
		var title, used string
		err := tx.QueryRowContext(ctx, `SELECT r.title, u.title FROM recipe_ingredients i
			JOIN recipes r ON r.id = i.recipe_id JOIN recipes u ON u.id = i.sub_recipe_id
			WHERE i.sub_recipe_id IN (SELECT value FROM json_each(?1)) AND i.recipe_id NOT IN (SELECT value FROM json_each(?1)) LIMIT 1`,
			string(list)).Scan(&title, &used)
		if err == nil {
			return validation.Errorf("« %s » est utilisée par « %s » : retirez-la d'abord de cette recette, ou supprimez les deux.", used, title)
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		var raw string
		if err := tx.QueryRowContext(ctx, `SELECT json_group_array(photo) FROM recipes WHERE photo != ''
			AND id IN (SELECT value FROM json_each(?))`, string(list)).Scan(&raw); err != nil {
			return err
		}
		if err := json.Unmarshal([]byte(raw), &photos); err != nil {
			return err
		}
		for _, q := range []string{
			// Restes prévus à partir de ces plats, puis les plats eux-mêmes.
			`DELETE FROM menu_entries WHERE leftovers_from IN (SELECT id FROM menu_entries WHERE recipe_id IN (SELECT value FROM json_each(?)))`,
			`DELETE FROM menu_entries WHERE recipe_id IN (SELECT value FROM json_each(?))`,
			`DELETE FROM recipes_fts WHERE rowid IN (SELECT value FROM json_each(?))`,
		} {
			if _, err := tx.ExecContext(ctx, q, string(list)); err != nil {
				return err
			}
		}
		res, err := tx.ExecContext(ctx, `DELETE FROM recipes WHERE id IN (SELECT value FROM json_each(?))`, string(list))
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return db.ErrNotFound
		}
		return nil
	})
	return photos, err
}

// InputFrom reconstitue la saisie d'une recette existante (duplication,
// formulaire de modification).
func InputFrom(r Recipe) Input {
	in := Input{Title: r.Title, Types: r.Types, Servings: r.Servings, PrepMin: r.PrepMin, CookMin: r.CookMin, RestMin: r.RestMin,
		Difficulty: r.Difficulty, Utensils: r.Utensils, Source: r.Source, Notes: r.Notes, Tags: r.Tags, BalanceManual: r.BalanceManual}
	for _, i := range r.Ingredients {
		in.Ingredients = append(in.Ingredients, Resolved{Line: Line{Group: i.Group, Milli: i.InputQty, Unit: i.InputUnit, Name: i.Name,
			Note: i.Note, Optional: i.Optional, ToTaste: i.ToTaste, SubRecipe: i.SubRecipe > 0},
			ProductID: i.ProductID, SubRecipe: i.SubRecipe, Confidence: Exact})
	}
	for _, st := range r.Steps {
		in.Steps = append(in.Steps, st.Text)
	}
	return in
}

// Duplicate crée une variante d'une recette ; renvoie son identifiant.
func (s *Service) Duplicate(ctx context.Context, id int64) (int64, error) {
	r, err := s.Get(ctx, id)
	if err != nil {
		return 0, err
	}
	in := InputFrom(r)
	in.Title = "Variante de " + r.Title
	if utf8.RuneCountInString(in.Title) > 150 {
		in.Title = string([]rune(in.Title)[:150])
	}
	in.VariantOf = id
	nid, err := s.Save(ctx, 0, in)
	if err != nil {
		return 0, err
	}
	_, err = s.db.W.ExecContext(ctx, `UPDATE recipes SET photo = ? WHERE id = ?`, r.Photo, nid)
	return nid, err
}

// SetPhoto enregistre le nom du fichier photo d'une recette ; renvoie
// l'ancien.
func (s *Service) SetPhoto(ctx context.Context, id int64, name string) (string, error) {
	var old string
	err := s.db.Tx(ctx, func(tx *sql.Tx) error {
		if err := tx.QueryRowContext(ctx, `SELECT photo FROM recipes WHERE id = ?`, id).Scan(&old); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return db.ErrNotFound
			}
			return err
		}
		_, err := tx.ExecContext(ctx, `UPDATE recipes SET photo = ? WHERE id = ?`, name, id)
		return err
	})
	return old, err
}

// FormatLine écrit un ingrédient sous la forme de saisie, relisible par
// ParseLine : « 200 g Farine de blé », « 2 c. à soupe Huile d'olive
// (facultatif) », « 1 [Pâte brisée maison] ».
func FormatLine(in Ingredient) string {
	var b strings.Builder
	if in.InputQty > 0 {
		b.WriteString(qty.FormatDecimal(in.InputQty))
		b.WriteByte(' ')
		if in.InputUnit != "" && in.InputUnit != "piece" {
			b.WriteString(qty.UnitLabel(in.InputUnit, in.InputQty))
			b.WriteByte(' ')
		}
	}
	if in.SubRecipe > 0 {
		b.WriteString("[" + in.Name + "]")
	} else {
		b.WriteString(in.Name)
	}
	if in.Note != "" {
		b.WriteString(" (" + in.Note + ")")
	}
	if in.Optional {
		b.WriteString(" (facultatif)")
	}
	if in.ToTaste && in.InputQty > 0 {
		b.WriteString(" (au goût)")
	}
	return b.String()
}

// FormatLines écrit la liste des ingrédients pour le formulaire, avec les
// regroupements.
func FormatLines(list []Ingredient) string {
	var b strings.Builder
	group := ""
	for _, in := range list {
		if in.Group != group {
			group = in.Group
			if group != "" {
				fmt.Fprintf(&b, "%s :\n", group)
			}
		}
		b.WriteString(FormatLine(in))
		b.WriteByte('\n')
	}
	return b.String()
}

// MarkCooked inscrit une recette comme cuisinée à une date (historique,
// variété du générateur), dans une transaction ; delta vaut -1 pour une
// annulation.
func MarkCooked(ctx context.Context, tx *sql.Tx, recipeID int64, date calendar.Date, delta int) error {
	_, err := tx.ExecContext(ctx, `UPDATE recipes SET cooked_count = max(cooked_count + ?, 0),
		last_cooked = CASE WHEN ? > 0 AND (last_cooked IS NULL OR last_cooked < ?) THEN ? ELSE last_cooked END WHERE id = ?`,
		delta, delta, date, date, recipeID)
	return err
}

// PhotoUses compte les recettes qui utilisent un fichier photo.
func (s *Service) PhotoUses(ctx context.Context, name string) (int, error) {
	var n int
	err := s.db.R.QueryRowContext(ctx, `SELECT count(*) FROM recipes WHERE photo = ?`, name).Scan(&n)
	return n, err
}
