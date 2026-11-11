package menus

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"

	"github.com/codecolors/petitchef/internal/household"
	"github.com/codecolors/petitchef/internal/planner"
	"github.com/codecolors/petitchef/internal/platform/calendar"
	"github.com/codecolors/petitchef/internal/platform/db"
	"github.com/codecolors/petitchef/internal/platform/validation"
)

// Status est l'état d'un plat du menu.
type Status string

const (
	Planned   Status = "prevu"
	Cooked    Status = "cuisine"
	Postponed Status = "reporte"
)

// Meal est un repas planifié.
type Meal struct {
	ID         int64
	Date       calendar.Date
	SlotID     int64
	SlotName   string
	SlotPos    int
	Format     planner.Format
	AllMembers bool
	Members    []int64
	Guests     int
	MaxMin     int // -1 : celui du créneau
	Entries    []Entry
}

// Entry est un plat du menu.
type Entry struct {
	ID            int64
	MealID        int64
	Role          planner.Role
	RecipeID      int64
	ProductID     int64
	StockItemID   int64
	Title         string
	Grade         string
	ActiveMin     int
	Portions      int64
	Locked        bool
	Status        Status
	LeftoversFrom int64
	CookDouble    bool
	Reasons       []string
	CookedBatch   int64
}

// IsLeftover indique un plat de restes (rien à cuisiner).
func (e Entry) IsLeftover() bool { return e.LeftoversFrom > 0 }

// Range renvoie les repas planifiés du jour from inclus à to exclu, avec
// leurs plats.
func (s *Service) Range(ctx context.Context, from, to calendar.Date) ([]Meal, error) {
	meals, err := db.Collect(ctx, s.db.R, func(r db.Scanner) (Meal, error) {
		var m Meal
		return m, r.Scan(&m.ID, &m.Date, &m.SlotID, &m.SlotName, &m.SlotPos, &m.Format, &m.AllMembers, &m.Guests, &m.MaxMin)
	}, `SELECT m.id, m.date, m.slot_id, s.name, s.position, m.format, m.all_members, m.guests, m.max_min
		FROM meals m JOIN meal_slots s ON s.id = m.slot_id WHERE m.date >= ? AND m.date < ? ORDER BY m.date, s.position`, from, to)
	if err != nil || len(meals) == 0 {
		return meals, err
	}
	idx := make(map[int64]int, len(meals))
	for i, m := range meals {
		idx[m.ID] = i
	}
	members, err := db.Collect(ctx, s.db.R, func(r db.Scanner) ([2]int64, error) {
		var x [2]int64
		return x, r.Scan(&x[0], &x[1])
	}, `SELECT mm.meal_id, mm.member_id FROM meal_members mm JOIN meals m ON m.id = mm.meal_id WHERE m.date >= ? AND m.date < ?`, from, to)
	if err != nil {
		return nil, err
	}
	for _, x := range members {
		meals[idx[x[0]]].Members = append(meals[idx[x[0]]].Members, x[1])
	}
	entries, err := s.entries(ctx, `m.date >= ? AND m.date < ?`, from, to)
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		i := idx[e.MealID]
		meals[i].Entries = append(meals[i].Entries, e)
	}
	for i := range meals {
		slices.SortFunc(meals[i].Entries, func(a, b Entry) int { return roleOrder(a.Role) - roleOrder(b.Role) })
	}
	return meals, nil
}

func roleOrder(r planner.Role) int { return slices.Index(planner.Roles, r) }

func (s *Service) entries(ctx context.Context, where string, args ...any) ([]Entry, error) {
	return db.Collect(ctx, s.db.R, func(r db.Scanner) (Entry, error) {
		var e Entry
		var reasons string
		err := r.Scan(&e.ID, &e.MealID, &e.Role, &e.RecipeID, &e.ProductID, &e.StockItemID, &e.Title, &e.Grade, &e.ActiveMin, &e.Portions,
			&e.Locked, &e.Status, &e.LeftoversFrom, &e.CookDouble, &reasons, &e.CookedBatch)
		if err == nil {
			_ = json.Unmarshal([]byte(reasons), &e.Reasons)
		}
		return e, err
	}, `SELECT e.id, e.meal_id, e.role, coalesce(e.recipe_id, 0), coalesce(e.product_id, 0), coalesce(e.stock_item_id, 0),
			coalesce(r.title, p.name, ''), coalesce(r.balance_manual, r.balance_grade, ''), coalesce(r.prep_min + r.cook_min, 0), e.portions,
			e.locked, e.status, coalesce(e.leftovers_from, 0), e.cook_double, e.reasons, coalesce(e.cooked_batch, 0)
		FROM menu_entries e JOIN meals m ON m.id = e.meal_id LEFT JOIN recipes r ON r.id = e.recipe_id LEFT JOIN products p ON p.id = e.product_id
		WHERE `+where, args...)
}

// Entry renvoie un plat du menu et son repas.
func (s *Service) Entry(ctx context.Context, id int64) (Entry, Meal, error) {
	list, err := s.entries(ctx, `e.id = ?`, id)
	if err != nil {
		return Entry{}, Meal{}, err
	}
	if len(list) == 0 {
		return Entry{}, Meal{}, db.ErrNotFound
	}
	m, err := s.Meal(ctx, list[0].MealID)
	return list[0], m, err
}

// Meal renvoie un repas et ses plats.
func (s *Service) Meal(ctx context.Context, id int64) (Meal, error) {
	var d calendar.Date
	if err := s.db.R.QueryRowContext(ctx, `SELECT date FROM meals WHERE id = ?`, id).Scan(&d); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Meal{}, db.ErrNotFound
		}
		return Meal{}, err
	}
	meals, err := s.Range(ctx, d, d.AddDays(1))
	if err != nil {
		return Meal{}, err
	}
	for _, m := range meals {
		if m.ID == id {
			return m, nil
		}
	}
	return Meal{}, db.ErrNotFound
}

// Portions calcule les portions d'un repas (millièmes) : membres présents
// selon leur portion relative, plus les invités ; 2 portions par défaut si
// le foyer n'a pas de membre.
func Portions(m Meal, members []household.Member) int64 {
	var p int64
	for _, mb := range members {
		if mb.Archived {
			continue
		}
		if m.AllMembers || slices.Contains(m.Members, mb.ID) {
			p += mb.Portion
		}
	}
	p += int64(m.Guests) * 1000
	if p == 0 {
		p = 2000
	}
	return p
}

// Present renvoie les membres présents à un repas.
func Present(m Meal, members []household.Member) []int64 {
	var out []int64
	for _, mb := range members {
		if !mb.Archived && (m.AllMembers || slices.Contains(m.Members, mb.ID)) {
			out = append(out, mb.ID)
		}
	}
	return out
}

// EnsureMeals crée, d'après une semaine type, les repas des jours from à
// from+days qui n'existent pas encore (un repas existant garde ses
// réglages ponctuels). Les créneaux désactivés sont ignorés.
func (s *Service) EnsureMeals(ctx context.Context, from calendar.Date, days int, templateID int64) error {
	slotList, err := s.Slots(ctx, true)
	if err != nil {
		return err
	}
	cells, err := s.Cells(ctx, templateID)
	if err != nil {
		return err
	}
	return s.db.Tx(ctx, func(tx *sql.Tx) error {
		for d := range days {
			date := from.AddDays(d)
			wd := weekday(date)
			for _, sl := range slotList {
				c, ok := cells[[2]int64{int64(wd), sl.ID}]
				if !ok {
					c = Cell{Format: planner.NoMeal, AllMembers: true}
				}
				res, err := tx.ExecContext(ctx, `INSERT INTO meals (date, slot_id, format, all_members, guests) VALUES (?, ?, ?, ?, ?)
					ON CONFLICT (date, slot_id) DO NOTHING`, date, sl.ID, c.Format, c.AllMembers, c.Guests)
				if err != nil {
					return err
				}
				if n, _ := res.RowsAffected(); n == 0 || c.AllMembers {
					continue
				}
				id, err := res.LastInsertId()
				if err != nil {
					return err
				}
				for _, m := range c.Members {
					if _, err := tx.ExecContext(ctx, `INSERT INTO meal_members (meal_id, member_id) VALUES (?, ?) ON CONFLICT DO NOTHING`, id, m); err != nil {
						return err
					}
				}
			}
		}
		return nil
	})
}

// MealInput est la modification ponctuelle d'un repas.
type MealInput struct {
	Format     planner.Format
	AllMembers bool
	Members    []int64
	Guests     int
	MaxMin     int // -1 : celui du créneau
}

// UpdateMeal modifie un repas : format (les plats devenus sans objet sont
// retirés), convives, temps maximum ; les portions des plats suivent.
func (s *Service) UpdateMeal(ctx context.Context, id int64, in MealInput) error {
	if !in.Format.Valid() || in.Guests < 0 || in.Guests > 50 {
		return validation.Error("Réglage du repas invalide.")
	}
	members, err := s.household.Members(ctx, false)
	if err != nil {
		return err
	}
	m := Meal{AllMembers: in.AllMembers, Members: in.Members, Guests: in.Guests}
	portions := Portions(m, members)
	return s.db.Tx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE meals SET format = ?, all_members = ?, guests = ?, max_min = ? WHERE id = ?`,
			in.Format, in.AllMembers, in.Guests, in.MaxMin, id)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return db.ErrNotFound
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM meal_members WHERE meal_id = ?`, id); err != nil {
			return err
		}
		if !in.AllMembers {
			for _, mb := range in.Members {
				if _, err := tx.ExecContext(ctx, `INSERT INTO meal_members (meal_id, member_id) VALUES (?, ?) ON CONFLICT DO NOTHING`, id, mb); err != nil {
					return err
				}
			}
		}
		roles := in.Format.Roles()
		list := make([]string, len(roles))
		for i, r := range roles {
			list[i] = `"` + string(r) + `"`
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM menu_entries WHERE meal_id = ? AND status != 'cuisine'
			AND role NOT IN (SELECT value FROM json_each(?))`, id, "["+joinComma(list)+"]"); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE menu_entries SET portions = ? WHERE meal_id = ? AND status != 'cuisine' AND NOT cook_double`, portions, id)
		return err
	})
}

func joinComma(list []string) string {
	out := ""
	for i, s := range list {
		if i > 0 {
			out += ","
		}
		out += s
	}
	return out
}

// SetDish place une recette, un produit simple ou un plat prêt dans un
// repas, pour un rôle ; le plat précédent (non cuisiné) est remplacé.
func (s *Service) SetDish(ctx context.Context, mealID int64, role planner.Role, recipeID, productID, stockItemID int64, reasons []string) (int64, error) {
	if recipeID == 0 && productID == 0 {
		return 0, validation.Error("Choisissez une recette ou un produit.")
	}
	m, err := s.Meal(ctx, mealID)
	if err != nil {
		return 0, err
	}
	if !slices.Contains(m.Format.Roles(), role) {
		return 0, validation.Errorf("Ce repas n'a pas de %s : changez d'abord son format.", lowerRole(role))
	}
	members, err := s.household.Members(ctx, false)
	if err != nil {
		return 0, err
	}
	raw, _ := json.Marshal(reasons)
	if reasons == nil {
		raw = []byte("[]")
	}
	var id int64
	err = s.db.Tx(ctx, func(tx *sql.Tx) error {
		var status string
		err := tx.QueryRowContext(ctx, `SELECT status FROM menu_entries WHERE meal_id = ? AND role = ?`, mealID, role).Scan(&status)
		if err == nil && status == string(Cooked) {
			return validation.Error("Ce plat est déjà cuisiné : annulez d'abord « cuisiné ».")
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM menu_entries WHERE meal_id = ? AND role = ?`, mealID, role); err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, `INSERT INTO menu_entries (meal_id, role, recipe_id, product_id, stock_item_id, portions, reasons)
			VALUES (?, ?, ?, ?, ?, ?, ?)`, mealID, role, nullID(recipeID), nullID(productID), nullID(stockItemID), Portions(m, members), string(raw))
		if err != nil {
			if db.IsForeignKeyViolation(err) {
				return db.ErrNotFound
			}
			return err
		}
		id, err = res.LastInsertId()
		return err
	})
	return id, err
}

func lowerRole(r planner.Role) string {
	switch r {
	case planner.Starter:
		return "entrée"
	case planner.Dessert:
		return "dessert"
	}
	return "plat"
}

func nullID(v int64) any {
	if v <= 0 {
		return nil
	}
	return v
}

// RemoveDish retire un plat non cuisiné (ses restes éventuels aussi).
func (s *Service) RemoveDish(ctx context.Context, entryID int64) error {
	return s.db.Tx(ctx, func(tx *sql.Tx) error {
		var status string
		if err := tx.QueryRowContext(ctx, `SELECT status FROM menu_entries WHERE id = ?`, entryID).Scan(&status); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return db.ErrNotFound
			}
			return err
		}
		if status == string(Cooked) {
			return validation.Error("Ce plat est déjà cuisiné : annulez d'abord « cuisiné ».")
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM menu_entries WHERE leftovers_from = ? AND status != 'cuisine'`, entryID); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `DELETE FROM menu_entries WHERE id = ?`, entryID)
		return err
	})
}

// ToggleLock verrouille ou déverrouille un plat (la régénération le garde).
func (s *Service) ToggleLock(ctx context.Context, entryID int64) error {
	res, err := s.db.W.ExecContext(ctx, `UPDATE menu_entries SET locked = NOT locked WHERE id = ?`, entryID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return db.ErrNotFound
	}
	return nil
}

// Swap échange le contenu de deux plats non cuisinés (glisser-déposer ou
// bouton « échanger »).
func (s *Service) Swap(ctx context.Context, a, b int64) error {
	ea, ma, err := s.Entry(ctx, a)
	if err != nil {
		return err
	}
	eb, mb, err := s.Entry(ctx, b)
	if err != nil {
		return err
	}
	if ea.Status == Cooked || eb.Status == Cooked {
		return validation.Error("Un plat cuisiné ne s'échange pas.")
	}
	if ea.IsLeftover() || eb.IsLeftover() {
		return validation.Error("Des restes ne s'échangent pas : déplacez le plat d'origine.")
	}
	_ = ma
	_ = mb
	return s.db.Tx(ctx, func(tx *sql.Tx) error {
		set := func(id int64, e Entry) error {
			raw, _ := json.Marshal(append([]string{"échangé"}, e.Reasons...))
			_, err := tx.ExecContext(ctx, `UPDATE menu_entries SET recipe_id = ?, product_id = ?, stock_item_id = ?, reasons = ?, cook_double = ? WHERE id = ?`,
				nullID(e.RecipeID), nullID(e.ProductID), nullID(e.StockItemID), string(raw), e.CookDouble, id)
			return err
		}
		if err := set(a, eb); err != nil {
			return err
		}
		if err := set(b, ea); err != nil {
			return err
		}
		// Les restes suivent le plat d'origine.
		if _, err := tx.ExecContext(ctx, `UPDATE menu_entries SET leftovers_from = CASE leftovers_from WHEN ? THEN ? ELSE ? END
			WHERE leftovers_from IN (?, ?)`, a, b, a, a, b); err != nil {
			return err
		}
		return nil
	})
}

// Postpone marque un plat « pas cuisiné / reporté » : il reste au menu
// (ses ingrédients restent réservés) jusqu'à ce qu'on le replace.
func (s *Service) Postpone(ctx context.Context, entryID int64, postponed bool) error {
	status := Planned
	if postponed {
		status = Postponed
	}
	res, err := s.db.W.ExecContext(ctx, `UPDATE menu_entries SET status = ? WHERE id = ? AND status != 'cuisine'`, status, entryID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return validation.Error("Plat introuvable ou déjà cuisiné.")
	}
	return nil
}

// Move replace un plat (reporté ou non) dans un autre repas.
func (s *Service) Move(ctx context.Context, entryID, toMeal int64) error {
	e, _, err := s.Entry(ctx, entryID)
	if err != nil {
		return err
	}
	if e.Status == Cooked {
		return validation.Error("Ce plat est déjà cuisiné.")
	}
	if _, err := s.SetDish(ctx, toMeal, e.Role, e.RecipeID, e.ProductID, e.StockItemID, []string{"replacé"}); err != nil {
		return err
	}
	return s.RemoveDish(ctx, entryID)
}

// CookDouble prévoit de cuisiner un plat pour deux repas : ses restes sont
// servis au repas suivant choisi (le lendemain midi par défaut).
func (s *Service) CookDouble(ctx context.Context, entryID, toMeal int64) error {
	e, m, err := s.Entry(ctx, entryID)
	if err != nil {
		return err
	}
	if e.RecipeID == 0 || e.Role != planner.Main {
		return validation.Error("Seul un plat principal cuisiné à partir d'une recette peut faire des restes.")
	}
	if toMeal == 0 {
		meals, err := s.Range(ctx, m.Date.AddDays(1), m.Date.AddDays(2))
		if err != nil {
			return err
		}
		for _, x := range meals {
			if slices.Contains(x.Format.Roles(), planner.Main) && x.SlotPos <= 2 {
				toMeal = x.ID
				break
			}
		}
		if toMeal == 0 {
			return validation.Error("Pas de repas le lendemain midi : choisissez où servir les restes.")
		}
	}
	target, err := s.Meal(ctx, toMeal)
	if err != nil {
		return err
	}
	members, err := s.household.Members(ctx, false)
	if err != nil {
		return err
	}
	return s.db.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM menu_entries WHERE meal_id = ? AND role = 'plat' AND status != 'cuisine'`, toMeal); err != nil {
			return err
		}
		extra := Portions(target, members)
		if _, err := tx.ExecContext(ctx, `INSERT INTO menu_entries (meal_id, role, recipe_id, portions, leftovers_from, reasons)
			VALUES (?, 'plat', ?, ?, ?, ?)`, toMeal, e.RecipeID, extra, entryID, `["restes"]`); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `UPDATE menu_entries SET cook_double = 1, portions = portions + ? WHERE id = ?`, extra, entryID)
		return err
	})
}

// ClearRange retire les plats non verrouillés et non cuisinés d'une
// période (avant régénération).
func clearRange(ctx context.Context, tx *sql.Tx, from, to calendar.Date) error {
	_, err := tx.ExecContext(ctx, `DELETE FROM menu_entries WHERE NOT locked AND status != 'cuisine'
		AND meal_id IN (SELECT id FROM meals WHERE date >= ? AND date < ?)`, from, to)
	return err
}

// DeleteMeals retire des repas du menu, avec leurs plats et les restes
// prévus ailleurs à partir de ces plats. Les plats déjà cuisinés sont
// conservés (le stock a été décompté) : leur repas reste en place. Renvoie
// le nombre de repas retirés et de plats cuisinés conservés.
func (s *Service) DeleteMeals(ctx context.Context, ids []int64) (removed, kept int, err error) {
	if len(ids) == 0 {
		return 0, 0, nil
	}
	list, err := json.Marshal(ids)
	if err != nil {
		return 0, 0, err
	}
	err = s.db.Tx(ctx, func(tx *sql.Tx) error {
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM menu_entries WHERE status = 'cuisine'
			AND meal_id IN (SELECT value FROM json_each(?))`, string(list)).Scan(&kept); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM menu_entries WHERE status != 'cuisine' AND leftovers_from IN (
			SELECT id FROM menu_entries WHERE meal_id IN (SELECT value FROM json_each(?)) AND status != 'cuisine')`, string(list)); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM menu_entries WHERE status != 'cuisine'
			AND meal_id IN (SELECT value FROM json_each(?))`, string(list)); err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, `DELETE FROM meals WHERE id IN (SELECT value FROM json_each(?))
			AND NOT EXISTS (SELECT 1 FROM menu_entries e WHERE e.meal_id = meals.id)`, string(list))
		if err != nil {
			return err
		}
		n, _ := res.RowsAffected()
		removed = int(n)
		return nil
	})
	return removed, kept, err
}
