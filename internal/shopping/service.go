package shopping

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/codecolors/petitchef/internal/catalog"
	"github.com/codecolors/petitchef/internal/menus"
	"github.com/codecolors/petitchef/internal/platform/calendar"
	"github.com/codecolors/petitchef/internal/platform/db"
	"github.com/codecolors/petitchef/internal/platform/qty"
	"github.com/codecolors/petitchef/internal/platform/validation"
	"github.com/codecolors/petitchef/internal/recipes"
	"github.com/codecolors/petitchef/internal/stock"
)

// Service gère la liste de courses.
type Service struct {
	db      *db.DB
	catalog *catalog.Service
	recipes *recipes.Service
	menus   *menus.Service
	stock   *stock.Service
}

// NewService crée le service de la liste de courses.
func NewService(d *db.DB, c *catalog.Service, r *recipes.Service, m *menus.Service, st *stock.Service) *Service {
	return &Service{db: d, catalog: c, recipes: r, menus: m, stock: st}
}

// Schedule est le rythme des courses.
type Schedule struct {
	Weekday int `json:"jour"`     // 0 = lundi
	Every   int `json:"tous_les"` // jours entre deux courses
}

// Schedule renvoie le rythme des courses.
func (s *Service) Schedule(ctx context.Context) (Schedule, error) {
	sc := Schedule{Weekday: 5, Every: 7}
	var raw string
	err := s.db.R.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = 'shopping'`).Scan(&raw)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return sc, err
	}
	_ = json.Unmarshal([]byte(raw), &sc)
	if sc.Every < 1 || sc.Every > 31 {
		sc.Every = 7
	}
	return sc, nil
}

// SaveSchedule enregistre le rythme des courses.
func (s *Service) SaveSchedule(ctx context.Context, sc Schedule) error {
	if sc.Weekday < 0 || sc.Weekday > 6 || sc.Every < 1 || sc.Every > 31 {
		return validation.Error("Jour des courses invalide.")
	}
	raw, _ := json.Marshal(sc)
	_, err := s.db.W.ExecContext(ctx, `INSERT INTO settings (key, value) VALUES ('shopping', ?) ON CONFLICT (key) DO UPDATE SET value = excluded.value`, string(raw))
	return err
}

// Dates renvoie la prochaine date de courses (aujourd'hui compris) et
// celle des courses suivantes, fin de la période couverte par la liste.
func (sc Schedule) Dates(today calendar.Date) (next, after calendar.Date) {
	wd := (int(today.Time().Weekday()) + 6) % 7
	next = today.AddDays((sc.Weekday - wd + 7) % 7)
	return next, next.AddDays(sc.Every)
}

// List est la liste de courses en cours.
type List struct {
	ID         int64
	StoreID    int64
	Until      calendar.Date
	ComputedAt string
	Items      []Item
}

// Item est une ligne de la liste.
type Item struct {
	ID         int64
	ProductID  int64
	Name       string
	Unit       qty.Base
	AisleID    int64
	Aisle      string
	StoreID    int64 // magasin préféré du produit
	Need       int64
	Buy        int64
	PackCount  int64
	PackSize   int64
	Sources    Source
	Details    []string
	Manual     bool
	ManualQty  int64
	Checked    bool
	PriceCents int64 // -1 : non saisi
	Cost       int64 // coût estimé (centimes), -1 : inconnu
	LocationID int64
	Universe   catalog.Universe
}

// Current renvoie la liste en cours (créée si besoin).
func (s *Service) Current(ctx context.Context) (List, error) {
	var l List
	err := s.db.W.QueryRowContext(ctx, `SELECT id, coalesce(store_id, 0), coalesce(until_date, ''), coalesce(computed_at, '')
		FROM shopping_lists ORDER BY id DESC LIMIT 1`).Scan(&l.ID, &l.StoreID, &l.Until, &l.ComputedAt)
	if errors.Is(err, sql.ErrNoRows) {
		store, err := s.catalog.DefaultStore(ctx)
		if err != nil {
			return l, err
		}
		res, err := s.db.W.ExecContext(ctx, `INSERT INTO shopping_lists (store_id) VALUES (?)`, nullID(store))
		if err != nil {
			return l, err
		}
		l.ID, err = res.LastInsertId()
		l.StoreID = store
		return l, err
	}
	if err != nil {
		return l, err
	}
	l.Items, err = s.items(ctx, l.ID)
	return l, err
}

func nullID(v int64) any {
	if v <= 0 {
		return nil
	}
	return v
}

func (s *Service) items(ctx context.Context, listID int64) ([]Item, error) {
	list, err := db.Collect(ctx, s.db.R, func(r db.Scanner) (Item, error) {
		var it Item
		var detail string
		err := r.Scan(&it.ID, &it.ProductID, &it.Name, &it.Unit, &it.AisleID, &it.Aisle, &it.StoreID, &it.Need, &it.Buy, &it.PackCount,
			&it.PackSize, &it.Sources, &detail, &it.Manual, &it.ManualQty, &it.Checked, &it.PriceCents, &it.LocationID, &it.Universe)
		_ = json.Unmarshal([]byte(detail), &it.Details)
		return it, err
	}, `SELECT i.id, i.product_id, p.name, p.unit, p.aisle_id, a.name, coalesce(p.store_id, 0), i.need, i.buy, i.pack_count, i.pack_size,
			i.sources, i.detail, i.manual, i.manual_qty, i.checked, coalesce(i.price_cents, -1), coalesce(p.location_id, 0), p.universe
		FROM shopping_items i JOIN products p ON p.id = i.product_id JOIN aisles a ON a.id = p.aisle_id
		WHERE i.list_id = ? AND NOT i.dismissed`, listID)
	if err != nil || len(list) == 0 {
		return list, err
	}
	ids := make([]int64, len(list))
	for i, it := range list {
		ids[i] = it.ProductID
	}
	prices, err := s.catalog.UnitPrices(ctx, ids)
	if err != nil {
		return nil, err
	}
	for i := range list {
		list[i].Cost = -1
		if p, ok := prices[list[i].ProductID]; ok {
			list[i].Cost = catalog.Cost(p, list[i].Buy)
		}
	}
	return list, nil
}

// SetStore choisit le magasin de la liste (ordre des rayons).
func (s *Service) SetStore(ctx context.Context, storeID int64) error {
	l, err := s.Current(ctx)
	if err != nil {
		return err
	}
	_, err = s.db.W.ExecContext(ctx, `UPDATE shopping_lists SET store_id = ? WHERE id = ?`, nullID(storeID), l.ID)
	return err
}

// Order renvoie la position de chaque rayon dans le magasin.
func (s *Service) Order(ctx context.Context, storeID int64) (map[int64]int, error) {
	aisles, err := s.catalog.StoreAisles(ctx, storeID)
	if err != nil {
		return nil, err
	}
	out := make(map[int64]int, len(aisles))
	for _, a := range aisles {
		out[a.ID] = a.Position
	}
	return out, nil
}

// MenuNeeds déroule les plats prévus (ou reportés) du jour from au jour to
// exclu en besoins de produits : recettes mises à l'échelle des convives,
// sans les produits de base ni les ingrédients « au goût » ou facultatifs ;
// les restes et les plats prêts du stock n'ont rien à acheter.
func (s *Service) MenuNeeds(ctx context.Context, from, to calendar.Date) ([]MenuNeed, error) {
	meals, err := s.menus.Range(ctx, from, to)
	if err != nil {
		return nil, err
	}
	// Plats reportés d'avant la période : toujours à préparer.
	late, err := s.menus.Range(ctx, from.AddDays(-14), from)
	if err != nil {
		return nil, err
	}
	all, err := s.recipes.All(ctx)
	if err != nil {
		return nil, err
	}
	load := recipes.MemLoader(all)
	var out []MenuNeed
	add := func(m menus.Meal, date calendar.Date, e menus.Entry) error {
		if e.Status == menus.Cooked || e.IsLeftover() || e.StockItemID > 0 {
			return nil
		}
		label := fmt.Sprintf("%s de %s", lowerFirst(e.Title), weekdayName(m.Date))
		if e.RecipeID == 0 {
			if e.ProductID > 0 {
				out = append(out, MenuNeed{ProductID: e.ProductID, Qty: 0, Date: date, Label: label})
			}
			return nil
		}
		r, ok := all[e.RecipeID]
		if !ok {
			return nil
		}
		needs, err := recipes.Expand(r, e.Portions, load)
		if err != nil {
			return err
		}
		for _, n := range needs {
			if n.Staple || n.ToTaste || n.Optional || n.Qty <= 0 {
				continue
			}
			out = append(out, MenuNeed{ProductID: n.ProductID, Qty: n.Qty, Date: date, Label: label})
		}
		return nil
	}
	for _, m := range late {
		for _, e := range m.Entries {
			if e.Status == menus.Postponed {
				if err := add(m, from, e); err != nil {
					return nil, err
				}
			}
		}
	}
	for _, m := range meals {
		for _, e := range m.Entries {
			if err := add(m, m.Date, e); err != nil {
				return nil, err
			}
		}
	}
	return out, nil
}

var weekdays = [...]string{"lundi", "mardi", "mercredi", "jeudi", "vendredi", "samedi", "dimanche"}

func weekdayName(d calendar.Date) string { return weekdays[(int(d.Time().Weekday())+6)%7] }

func lowerFirst(s string) string {
	if s == "" {
		return s
	}
	r := []rune(s)
	if len(r) > 1 && strings.ToUpper(string(r[1])) != string(r[1]) {
		r[0] = []rune(strings.ToLower(string(r[0])))[0]
	}
	return string(r)
}

// Recompute recalcule la liste en cours (menu, stock minimum, ajouts
// manuels) sans perdre les cases cochées ni les ajouts manuels ; until
// vide : jusqu'aux courses suivantes. Les plats simples du menu (« pizza
// surgelée ») comptent une unité.
func (s *Service) Recompute(ctx context.Context, until calendar.Date) (List, error) {
	today := calendar.Today()
	l, err := s.Current(ctx)
	if err != nil {
		return l, err
	}
	sc, err := s.Schedule(ctx)
	if err != nil {
		return l, err
	}
	if until == "" {
		_, until = sc.Dates(today)
	}
	if until <= today {
		return l, validation.Error("La période doit se terminer après aujourd'hui.")
	}
	needs, err := s.MenuNeeds(ctx, today, until)
	if err != nil {
		return l, err
	}
	in := Input{Today: today, Until: until, Needs: needs}
	if in.Lots, err = s.stock.Lots(ctx, nil); err != nil {
		return l, err
	}
	if in.Watched, err = s.stock.Watched(ctx, today); err != nil {
		return l, err
	}
	dismissed := map[int64]bool{}
	for _, it := range l.Items {
		if it.Manual {
			in.Manual = append(in.Manual, Manual{ProductID: it.ProductID, Qty: it.ManualQty})
		}
	}
	rows, err := db.Collect(ctx, s.db.R, func(r db.Scanner) (int64, error) {
		var id int64
		return id, r.Scan(&id)
	}, `SELECT product_id FROM shopping_items WHERE list_id = ? AND dismissed`, l.ID)
	if err != nil {
		return l, err
	}
	for _, id := range rows {
		dismissed[id] = true
	}
	ids := map[int64]bool{}
	for _, n := range needs {
		ids[n.ProductID] = true
	}
	for _, w := range in.Watched {
		ids[w.ProductID] = true
	}
	for _, m := range in.Manual {
		ids[m.ProductID] = true
	}
	list := make([]int64, 0, len(ids))
	for id := range ids {
		list = append(list, id)
	}
	if in.Products, err = s.catalog.Many(ctx, list); err != nil {
		return l, err
	}
	// Un plat simple du menu : une unité (pièce ou conditionnement).
	for i, n := range in.Needs {
		if n.Qty == 0 {
			in.Needs[i].Qty = smallest(in.Products[n.ProductID])
			if in.Needs[i].Qty == 0 {
				in.Needs[i].Qty = 1
			}
		}
	}
	if in.Order, err = s.Order(ctx, l.StoreID); err != nil {
		return l, err
	}
	lines := Compute(in)
	err = s.db.Tx(ctx, func(tx *sql.Tx) error {
		keep := map[int64]bool{}
		for _, ln := range lines {
			if dismissed[ln.ProductID] {
				continue
			}
			keep[ln.ProductID] = true
			detail, _ := json.Marshal(ln.Details)
			if _, err := tx.ExecContext(ctx, `INSERT INTO shopping_items (list_id, product_id, need, buy, pack_count, pack_size, sources, detail)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?)
				ON CONFLICT (list_id, product_id) DO UPDATE SET need = excluded.need, buy = excluded.buy, pack_count = excluded.pack_count,
				pack_size = excluded.pack_size, sources = excluded.sources, detail = excluded.detail`,
				l.ID, ln.ProductID, ln.Need, ln.Buy, ln.Count, ln.Size, ln.Sources, string(detail)); err != nil {
				return err
			}
		}
		// Les lignes calculées devenues inutiles disparaissent, sauf si
		// elles sont cochées (déjà dans le chariot) ou ajoutées à la main.
		var gone []int64
		for _, it := range l.Items {
			if !keep[it.ProductID] && !it.Checked && !it.Manual {
				gone = append(gone, it.ID)
			}
		}
		for _, id := range gone {
			if _, err := tx.ExecContext(ctx, `DELETE FROM shopping_items WHERE id = ?`, id); err != nil {
				return err
			}
		}
		_, err := tx.ExecContext(ctx, `UPDATE shopping_lists SET until_date = ?, computed_at = strftime('%Y-%m-%dT%H:%M:%SZ', 'now') WHERE id = ?`, until, l.ID)
		return err
	})
	if err != nil {
		return l, err
	}
	return s.Current(ctx)
}

// AddManual ajoute un produit à la liste (quantité facultative, en unités
// de base), puis recalcule la ligne.
func (s *Service) AddManual(ctx context.Context, productID, quantity int64) error {
	l, err := s.Current(ctx)
	if err != nil {
		return err
	}
	p, err := s.catalog.Get(ctx, productID)
	if err != nil {
		return err
	}
	q := max(quantity, 0)
	buyQ := q
	if buyQ == 0 {
		buyQ = smallest(p)
	}
	buy, count, size := catalog.RoundToPackages(buyQ, p.Unit, p.BuyUnits())
	_, err = s.db.W.ExecContext(ctx, `INSERT INTO shopping_items (list_id, product_id, need, buy, pack_count, pack_size, sources, detail, manual, manual_qty)
		VALUES (?, ?, ?, ?, ?, ?, ?, '["ajout manuel"]', 1, ?)
		ON CONFLICT (list_id, product_id) DO UPDATE SET manual = 1, manual_qty = manual_qty + excluded.manual_qty, dismissed = 0,
		need = need + excluded.need, buy = max(buy, excluded.buy), sources = sources | excluded.sources`,
		l.ID, productID, buyQ, buy, count, size, FromManual, q)
	return err
}

// Toggle coche ou décoche une ligne ; price (centimes, -1 : inchangé) est
// le prix payé relevé en magasin.
func (s *Service) Toggle(ctx context.Context, itemID int64, checked bool, price int64) error {
	var res sql.Result
	var err error
	if price >= 0 {
		res, err = s.db.W.ExecContext(ctx, `UPDATE shopping_items SET checked = ?, checked_at = CASE WHEN ? THEN strftime('%Y-%m-%dT%H:%M:%SZ', 'now') END,
			price_cents = ? WHERE id = ?`, checked, checked, price, itemID)
	} else {
		res, err = s.db.W.ExecContext(ctx, `UPDATE shopping_items SET checked = ?, checked_at = CASE WHEN ? THEN strftime('%Y-%m-%dT%H:%M:%SZ', 'now') END
			WHERE id = ?`, checked, checked, itemID)
	}
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return db.ErrNotFound
	}
	return nil
}

// Remove retire une ligne : un ajout manuel disparaît, une ligne calculée
// est écartée jusqu'au rangement des courses.
func (s *Service) Remove(ctx context.Context, itemID int64) error {
	_, err := s.db.W.ExecContext(ctx, `UPDATE shopping_items SET dismissed = 1, manual = 0, manual_qty = 0, checked = 0 WHERE id = ?`, itemID)
	return err
}

// PutAway est une ligne rangée en stock.
type PutAway struct {
	ItemID     int64
	Qty        int64
	LocationID int64
	BestBefore calendar.Date
	PriceCents int64 // -1 : inconnu
}

// Store range les courses en une étape : chaque ligne cochée entre en
// stock (origine courses, prix et magasin relevés), puis quitte la liste ;
// les lignes non cochées restent pour les courses suivantes. Renvoie le
// nombre de lignes rangées.
func (s *Service) Store(ctx context.Context, lines []PutAway, userID int64) (int, error) {
	l, err := s.Current(ctx)
	if err != nil {
		return 0, err
	}
	byID := map[int64]Item{}
	for _, it := range l.Items {
		byID[it.ID] = it
	}
	n := 0
	today := calendar.Today()
	err = s.db.Tx(ctx, func(tx *sql.Tx) error {
		batch, err := stock.NextBatch(ctx, tx)
		if err != nil {
			return err
		}
		for _, pa := range lines {
			it, ok := byID[pa.ItemID]
			if !ok || !it.Checked {
				continue
			}
			if pa.Qty > 0 {
				if _, err := s.stock.AddTx(ctx, tx, stock.AddInput{ProductID: it.ProductID, LocationID: pa.LocationID, Qty: pa.Qty,
					BestBefore: pa.BestBefore, PriceCents: pa.PriceCents, StoreID: l.StoreID, Origin: stock.FromShopping, Date: today,
					UserID: userID}, batch); err != nil {
					return fmt.Errorf("%s : %w", it.Name, err)
				}
			}
			if _, err := tx.ExecContext(ctx, `DELETE FROM shopping_items WHERE id = ?`, it.ID); err != nil {
				return err
			}
			n++
		}
		// Les lignes écartées repartent à zéro pour la liste suivante.
		_, err = tx.ExecContext(ctx, `DELETE FROM shopping_items WHERE list_id = ? AND dismissed`, l.ID)
		return err
	})
	return n, err
}

// Text écrit la liste pour un message (copier-coller), par rayon.
func Text(items []Item, until calendar.Date) string {
	var b strings.Builder
	b.WriteString("Liste de courses")
	if until != "" {
		b.WriteString(" (jusqu'au " + until.Format() + ")")
	}
	b.WriteString("\n")
	aisle := ""
	for _, it := range items {
		if it.Checked {
			continue
		}
		if it.Aisle != aisle {
			aisle = it.Aisle
			b.WriteString("\n" + aisle + "\n")
		}
		b.WriteString("- " + it.Name + " : " + it.BuyLabel() + "\n")
	}
	return b.String()
}

// BuyLabel écrit la quantité à acheter : « 2 × 250 g », « 1 kg ».
func (it Item) BuyLabel() string {
	if it.PackCount > 1 {
		return fmt.Sprintf("%d × %s", it.PackCount, qty.Format(it.PackSize, it.Unit))
	}
	return qty.Format(it.Buy, it.Unit)
}

// SortItems range les lignes dans l'ordre du magasin.
func SortItems(items []Item, order map[int64]int) {
	lines := make([]Line, len(items))
	idx := map[int64]Item{}
	for i, it := range items {
		lines[i] = Line{ProductID: it.ProductID, AisleID: it.AisleID, Name: it.Name}
		idx[it.ProductID] = it
	}
	Sort(lines, order)
	for i, l := range lines {
		items[i] = idx[l.ProductID]
	}
}
