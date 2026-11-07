package stock

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/codecolors/petitchef/internal/catalog"
	"github.com/codecolors/petitchef/internal/platform/calendar"
	"github.com/codecolors/petitchef/internal/platform/db"
	"github.com/codecolors/petitchef/internal/platform/qty"
	"github.com/codecolors/petitchef/internal/platform/validation"
)

// Kind est la nature d'un mouvement de stock.
type Kind string

const (
	Purchase    Kind = "achat"
	Added       Kind = "ajout"
	Recipe      Kind = "recette"
	Consumption Kind = "consommation"
	Waste       Kind = "perte"
	Correction  Kind = "correction"
	Transfer    Kind = "transfert"
	Opening     Kind = "ouverture"
)

// Label décrit la nature du mouvement.
func (k Kind) Label() string {
	switch k {
	case Purchase:
		return "Achat"
	case Added:
		return "Ajout"
	case Recipe:
		return "Cuisiné"
	case Consumption:
		return "Consommé"
	case Waste:
		return "Jeté"
	case Correction:
		return "Inventaire"
	case Transfer:
		return "Déplacé"
	}
	return "Ouvert"
}

// Origin est l'origine d'un lot.
type Origin string

const (
	FromShopping Origin = "courses"
	Manual       Origin = "manuel"
	Leftovers    Origin = "restes"
	HomeMade     Origin = "fait_maison"
)

// Service gère l'inventaire.
type Service struct {
	db      *db.DB
	catalog *catalog.Service
}

// NewService crée le service de l'inventaire.
func NewService(d *db.DB, c *catalog.Service) *Service { return &Service{db: d, catalog: c} }

// Item est un lot de stock affiché.
type Item struct {
	ID         int64
	ProductID  int64
	Product    string
	Unit       qty.Base
	Universe   catalog.Universe
	Aisle      string
	LocationID int64
	Location   string
	LocKind    string
	Qty        int64
	BestBefore calendar.Date
	DateKind   catalog.DateKind
	OpenedOn   calendar.Date
	AddedOn    calendar.Date
	Origin     Origin
	RecipeID   int64
	Step       int64 // quantité d'un geste −/+ : plus petit conditionnement ou une pièce
	Staple     bool
	ShelfOpen  int64
}

// Expiry décrit la date limite d'un lot par rapport à aujourd'hui.
type Expiry struct {
	Days  int    // jours restants (négatif : périmé)
	Label string // « périme demain », « périmé depuis 3 j », « DDM 12/12/2026 »
	Class string // expiry-past, expiry-soon ou vide
}

// ExpiryOn renvoie l'état de la date limite au jour today.
func (i Item) ExpiryOn(today calendar.Date) Expiry {
	if i.BestBefore == "" {
		return Expiry{Days: 1 << 20}
	}
	d := today.DaysUntil(i.BestBefore)
	e := Expiry{Days: d}
	switch {
	case d < 0 && i.DateKind == catalog.DDM:
		e.Label, e.Class = fmt.Sprintf("DDM dépassée depuis %d j", -d), "expiry-soon"
	case d < 0:
		e.Label, e.Class = fmt.Sprintf("périmé depuis %d j", -d), "expiry-past"
	case d == 0:
		e.Label, e.Class = "à consommer aujourd'hui", "expiry-past"
	case d == 1:
		e.Label, e.Class = "périme demain", "expiry-soon"
	case d <= 3:
		e.Label, e.Class = fmt.Sprintf("périme dans %d j", d), "expiry-soon"
	default:
		e.Label = fmt.Sprintf("%s %s", i.DateKind, i.BestBefore.Format())
	}
	return e
}

const itemColumns = `s.id, s.product_id, p.name, p.unit, p.universe, a.name, coalesce(s.location_id, 0), coalesce(l.name, ''), coalesce(l.kind, 'ambiant'),
	s.qty, coalesce(s.best_before, ''), s.date_kind, coalesce(s.opened_on, ''), s.added_on, s.origin, coalesce(s.recipe_id, 0),
	coalesce((SELECT min(qty) FROM product_packages pk WHERE pk.product_id = p.id), 0), p.staple, coalesce(p.shelf_opened, -1)`

const itemFrom = ` FROM stock_items s JOIN products p ON p.id = s.product_id JOIN aisles a ON a.id = p.aisle_id
	LEFT JOIN locations l ON l.id = s.location_id`

func scanItem(r db.Scanner) (Item, error) {
	var i Item
	err := r.Scan(&i.ID, &i.ProductID, &i.Product, &i.Unit, &i.Universe, &i.Aisle, &i.LocationID, &i.Location, &i.LocKind,
		&i.Qty, &i.BestBefore, &i.DateKind, &i.OpenedOn, &i.AddedOn, &i.Origin, &i.RecipeID, &i.Step, &i.Staple, &i.ShelfOpen)
	if i.Unit == qty.Piece && (i.Step == 0 || i.Step > 1000) {
		i.Step = 1000
	}
	return i, err
}

// Filter sélectionne les lots affichés.
type Filter struct {
	LocationID int64
	Universe   catalog.Universe
	AisleID    int64
	ProductID  int64
	Query      string
	Priority   bool // tri par date limite (à consommer en priorité)
	Page       int
}

// PageSize est le nombre de lots par page.
const PageSize = 60

// Page est une page de lots.
type Page struct {
	Items []Item
	Total int
	Page  int
	More  bool
}

// List renvoie une page de lots non épuisés.
func (s *Service) List(ctx context.Context, f Filter) (Page, error) {
	where := []string{"s.qty > 0"}
	var args []any
	if f.LocationID > 0 {
		where, args = append(where, "s.location_id = ?"), append(args, f.LocationID)
	}
	if f.Universe != "" {
		where, args = append(where, "p.universe = ?"), append(args, f.Universe)
	}
	if f.AisleID > 0 {
		where, args = append(where, "p.aisle_id = ?"), append(args, f.AisleID)
	}
	if f.ProductID > 0 {
		where, args = append(where, "s.product_id = ?"), append(args, f.ProductID)
	}
	if q := catalog.Normalize(f.Query); q != "" {
		where, args = append(where, `(p.search LIKE ? || '%' OR p.search LIKE '% ' || ? || '%')`), append(args, q, q)
	}
	cond := " WHERE " + strings.Join(where, " AND ")
	order := " ORDER BY a.position, p.search, s.best_before IS NULL, s.best_before"
	if f.Priority {
		order = " ORDER BY s.best_before IS NULL, s.best_before, p.search"
	}
	pg := Page{Page: max(f.Page, 0)}
	if err := s.db.R.QueryRowContext(ctx, `SELECT count(*)`+itemFrom+cond, args...).Scan(&pg.Total); err != nil {
		return pg, err
	}
	var err error
	pg.Items, err = db.Collect(ctx, s.db.R, scanItem, `SELECT `+itemColumns+itemFrom+cond+order+
		fmt.Sprintf(" LIMIT %d OFFSET %d", PageSize, pg.Page*PageSize), args...)
	pg.More = (pg.Page+1)*PageSize < pg.Total
	return pg, err
}

// Get renvoie un lot.
func (s *Service) Get(ctx context.Context, id int64) (Item, error) {
	i, err := scanItem(s.db.R.QueryRowContext(ctx, `SELECT `+itemColumns+itemFrom+` WHERE s.id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return i, db.ErrNotFound
	}
	return i, err
}

// Expiring renvoie les lots dont la date limite tombe au plus tard le
// jour until (périmés compris), dans l'ordre FEFO.
func (s *Service) Expiring(ctx context.Context, until calendar.Date, limit int) ([]Item, error) {
	return db.Collect(ctx, s.db.R, scanItem, `SELECT `+itemColumns+itemFrom+`
		WHERE s.qty > 0 AND s.best_before IS NOT NULL AND s.best_before <= ? ORDER BY s.best_before, p.search LIMIT ?`, until, limit)
}

// SuggestBestBefore propose une date limite d'après la conservation
// indicative du produit et le type d'emplacement (congelé ou non) ; vide si
// elle est inconnue.
func SuggestBestBefore(p catalog.Product, locationKind string, today calendar.Date) calendar.Date {
	days := p.ShelfClosed
	if locationKind == "congele" && p.ShelfFrozen >= 0 {
		days = p.ShelfFrozen
	}
	if days < 0 {
		return ""
	}
	return today.AddDays(int(days))
}

// AddInput est l'entrée en stock d'un lot.
type AddInput struct {
	ProductID  int64
	LocationID int64
	Qty        int64
	BestBefore calendar.Date
	DateKind   catalog.DateKind // vide : celui du produit
	OpenedOn   calendar.Date
	PriceCents int64 // -1 : inconnu
	StoreID    int64
	Origin     Origin
	RecipeID   int64
	Date       calendar.Date
	UserID     int64
}

// Add entre un lot en stock (une action annulable) ; renvoie le lot et le
// lot d'annulation.
func (s *Service) Add(ctx context.Context, in AddInput) (id, batch int64, err error) {
	err = s.db.Tx(ctx, func(tx *sql.Tx) error {
		if batch, err = NextBatch(ctx, tx); err != nil {
			return err
		}
		id, err = s.AddTx(ctx, tx, in, batch)
		return err
	})
	return id, batch, err
}

// NextBatch réserve un numéro d'action pour regrouper des mouvements.
func NextBatch(ctx context.Context, tx *sql.Tx) (int64, error) {
	var b int64
	err := tx.QueryRowContext(ctx, `SELECT coalesce(max(batch), 0) + 1 FROM stock_movements`).Scan(&b)
	return b, err
}

// AddTx entre un lot en stock dans une transaction (rangement des
// courses : toutes les lignes en une étape).
func (s *Service) AddTx(ctx context.Context, tx *sql.Tx, in AddInput, batch int64) (int64, error) {
	if in.Qty <= 0 {
		return 0, validation.Error("La quantité doit être positive.")
	}
	if in.Date == "" {
		in.Date = calendar.Today()
	}
	if in.Origin == "" {
		in.Origin = Manual
	}
	var kind catalog.DateKind
	var defLoc int64
	if err := tx.QueryRowContext(ctx, `SELECT date_kind, coalesce(location_id, 0) FROM products WHERE id = ?`, in.ProductID).Scan(&kind, &defLoc); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, db.ErrNotFound
		}
		return 0, err
	}
	if in.DateKind != "" {
		kind = in.DateKind
	}
	if in.LocationID == 0 {
		in.LocationID = defLoc // emplacement par défaut du produit
	}
	var price, priceQty any
	if in.PriceCents >= 0 {
		price, priceQty = in.PriceCents, in.Qty
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO stock_items (product_id, location_id, qty, best_before, date_kind, opened_on, added_on,
		price_cents, price_qty, origin, recipe_id) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		in.ProductID, nullID(in.LocationID), in.Qty, nullDate(in.BestBefore), kind, nullDate(in.OpenedOn), in.Date,
		price, priceQty, in.Origin, nullID(in.RecipeID))
	if err != nil {
		if db.IsForeignKeyViolation(err) {
			return 0, db.ErrNotFound
		}
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	mk := Added
	if in.Origin == FromShopping {
		mk = Purchase
	}
	if err := move(ctx, tx, movement{batch: batch, date: in.Date, productID: in.ProductID, itemID: id, kind: mk, qty: in.Qty, value: price, userID: in.UserID}); err != nil {
		return 0, err
	}
	if in.PriceCents >= 0 {
		if _, err := tx.ExecContext(ctx, `INSERT INTO prices (product_id, store_id, date, price_cents, qty) VALUES (?, ?, ?, ?, ?)`,
			in.ProductID, nullID(in.StoreID), in.Date, in.PriceCents, in.Qty); err != nil {
			return 0, err
		}
	}
	// Un produit de base réapprovisionné n'est plus « presque vide ».
	_, err = tx.ExecContext(ctx, `UPDATE stock_settings SET low = 0, low_since = NULL WHERE product_id = ?`, in.ProductID)
	return id, err
}

func nullID(v int64) any {
	if v <= 0 {
		return nil
	}
	return v
}

func nullDate(d calendar.Date) any {
	if d == "" {
		return nil
	}
	return string(d)
}

// movement est une ligne du journal.
type movement struct {
	batch, productID, itemID, menuEntryID, userID int64
	date                                          calendar.Date
	kind                                          Kind
	qty                                           int64
	value                                         any
	prevBestBefore, prevOpenedOn                  any
}

func move(ctx context.Context, tx *sql.Tx, m movement) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO stock_movements (date, batch, product_id, stock_item_id, kind, qty, value_cents, menu_entry_id,
		user_id, prev_best_before, prev_opened_on) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		m.date, m.batch, m.productID, nullID(m.itemID), m.kind, m.qty, m.value, nullID(m.menuEntryID), nullID(m.userID),
		m.prevBestBefore, m.prevOpenedOn)
	return err
}

// lotValue estime la valeur (centimes) d'une quantité prise dans un lot :
// prix payé pour le lot, sinon dernier prix connu du produit ; nil si
// inconnue.
func lotValue(ctx context.Context, tx *sql.Tx, itemID, q int64) (any, error) {
	var price, priceQty sql.NullInt64
	var productID int64
	if err := tx.QueryRowContext(ctx, `SELECT price_cents, price_qty, product_id FROM stock_items WHERE id = ?`, itemID).Scan(&price, &priceQty, &productID); err != nil {
		return nil, err
	}
	if price.Valid && priceQty.Valid && priceQty.Int64 > 0 {
		return (price.Int64*q + priceQty.Int64/2) / priceQty.Int64, nil
	}
	err := tx.QueryRowContext(ctx, `SELECT price_cents, qty FROM prices WHERE product_id = ? ORDER BY date DESC, id DESC LIMIT 1`, productID).Scan(&price, &priceQty)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil || priceQty.Int64 <= 0 {
		return nil, err
	}
	return (price.Int64*q + priceQty.Int64/2) / priceQty.Int64, nil
}

// change modifie la quantité d'un lot et journalise le mouvement.
func change(ctx context.Context, tx *sql.Tx, itemID, delta int64, kind Kind, date calendar.Date, batch, menuEntryID, userID int64) error {
	var productID, current int64
	if err := tx.QueryRowContext(ctx, `SELECT product_id, qty FROM stock_items WHERE id = ?`, itemID).Scan(&productID, &current); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return db.ErrNotFound
		}
		return err
	}
	if current+delta < 0 {
		delta = -current
	}
	if delta == 0 && kind != Correction {
		return nil
	}
	var value any
	if kind == Waste || kind == Consumption || kind == Recipe {
		var err error
		if value, err = lotValue(ctx, tx, itemID, -delta); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE stock_items SET qty = qty + ?, emptied_on = CASE WHEN qty + ? = 0 THEN ? END WHERE id = ?`,
		delta, delta, date, itemID); err != nil {
		return err
	}
	return move(ctx, tx, movement{batch: batch, date: date, productID: productID, itemID: itemID, kind: kind, qty: delta,
		value: value, menuEntryID: menuEntryID, userID: userID})
}

// action exécute une modification annulable dans une transaction.
func (s *Service) action(ctx context.Context, fn func(tx *sql.Tx, batch int64) error) (int64, error) {
	var batch int64
	err := s.db.Tx(ctx, func(tx *sql.Tx) error {
		var err error
		if batch, err = NextBatch(ctx, tx); err != nil {
			return err
		}
		return fn(tx, batch)
	})
	return batch, err
}

// Adjust ajoute (delta > 0) ou retire une quantité d'un lot ; kind précise
// la nature d'un retrait (consommation ou perte).
func (s *Service) Adjust(ctx context.Context, itemID, delta int64, kind Kind, today calendar.Date, userID int64) (int64, error) {
	if delta > 0 {
		kind = Correction
	}
	return s.action(ctx, func(tx *sql.Tx, batch int64) error {
		return change(ctx, tx, itemID, delta, kind, today, batch, 0, userID)
	})
}

// Empty retire tout le lot : « fini » (consommation) ou « jeté » (perte).
func (s *Service) Empty(ctx context.Context, itemID int64, kind Kind, today calendar.Date, userID int64) (int64, error) {
	return s.action(ctx, func(tx *sql.Tx, batch int64) error {
		var current int64
		if err := tx.QueryRowContext(ctx, `SELECT qty FROM stock_items WHERE id = ?`, itemID).Scan(&current); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return db.ErrNotFound
			}
			return err
		}
		return change(ctx, tx, itemID, -current, kind, today, batch, 0, userID)
	})
}

// Open marque un lot ouvert aujourd'hui : la date limite est raccourcie
// selon la conservation après ouverture du produit.
func (s *Service) Open(ctx context.Context, itemID int64, today calendar.Date, userID int64) (int64, error) {
	return s.action(ctx, func(tx *sql.Tx, batch int64) error {
		var productID, shelf int64
		var best, opened sql.NullString
		err := tx.QueryRowContext(ctx, `SELECT s.product_id, coalesce(p.shelf_opened, -1), s.best_before, s.opened_on
			FROM stock_items s JOIN products p ON p.id = s.product_id WHERE s.id = ?`, itemID).Scan(&productID, &shelf, &best, &opened)
		if errors.Is(err, sql.ErrNoRows) {
			return db.ErrNotFound
		}
		if err != nil {
			return err
		}
		next := calendar.Date(best.String)
		if shelf >= 0 {
			if limit := today.AddDays(int(shelf)); next == "" || limit < next {
				next = limit
			}
		}
		if _, err := tx.ExecContext(ctx, `UPDATE stock_items SET opened_on = ?, best_before = ?, date_kind = CASE WHEN ? THEN 'DLC' ELSE date_kind END WHERE id = ?`,
			today, nullDate(next), shelf >= 0, itemID); err != nil {
			return err
		}
		return move(ctx, tx, movement{batch: batch, date: today, productID: productID, itemID: itemID, kind: Opening,
			prevBestBefore: nullStr(best), prevOpenedOn: nullStr(opened), userID: userID})
	})
}

func nullStr(s sql.NullString) any {
	if !s.Valid {
		return nil
	}
	return s.String
}

// Move déplace une quantité d'un lot vers un autre emplacement (tout le
// lot si q est nul), avec une nouvelle date limite (vide : celle du lot,
// ou la conservation au congélateur pour un emplacement congelé).
func (s *Service) Move(ctx context.Context, itemID, toLocation, q int64, bestBefore calendar.Date, today calendar.Date, userID int64) (int64, error) {
	return s.action(ctx, func(tx *sql.Tx, batch int64) error {
		var it struct {
			product, qty int64
			best, opened sql.NullString
			kind, origin string
			recipe       sql.NullInt64
			frozen       int64
			price, pq    sql.NullInt64
		}
		err := tx.QueryRowContext(ctx, `SELECT s.product_id, s.qty, s.best_before, s.opened_on, s.date_kind, s.origin, s.recipe_id,
			coalesce(p.shelf_frozen, -1), s.price_cents, s.price_qty
			FROM stock_items s JOIN products p ON p.id = s.product_id WHERE s.id = ?`, itemID).Scan(&it.product, &it.qty, &it.best, &it.opened,
			&it.kind, &it.origin, &it.recipe, &it.frozen, &it.price, &it.pq)
		if errors.Is(err, sql.ErrNoRows) {
			return db.ErrNotFound
		}
		if err != nil {
			return err
		}
		if q <= 0 || q > it.qty {
			q = it.qty
		}
		var locKind string
		if err := tx.QueryRowContext(ctx, `SELECT kind FROM locations WHERE id = ?`, toLocation).Scan(&locKind); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return validation.Error("Emplacement inconnu.")
			}
			return err
		}
		if bestBefore == "" {
			bestBefore = calendar.Date(it.best.String)
			if locKind == "congele" && it.frozen >= 0 {
				bestBefore = today.AddDays(int(it.frozen))
			}
		}
		var price, pq any
		if it.price.Valid && it.pq.Valid {
			price, pq = it.price.Int64*q/it.pq.Int64, q
		}
		res, err := tx.ExecContext(ctx, `INSERT INTO stock_items (product_id, location_id, qty, best_before, date_kind, opened_on, added_on,
			price_cents, price_qty, origin, recipe_id) SELECT product_id, ?, ?, ?, date_kind, opened_on, added_on, ?, ?, origin, recipe_id
			FROM stock_items WHERE id = ?`, toLocation, q, nullDate(bestBefore), price, pq, itemID)
		if err != nil {
			return err
		}
		newID, err := res.LastInsertId()
		if err != nil {
			return err
		}
		if err := change(ctx, tx, itemID, -q, Transfer, today, batch, 0, userID); err != nil {
			return err
		}
		return move(ctx, tx, movement{batch: batch, date: today, productID: it.product, itemID: newID, kind: Transfer, qty: q, userID: userID})
	})
}

// Correct enregistre les quantités relevées lors d'un inventaire (lot →
// quantité constatée) ; seuls les écarts sont journalisés, comme
// corrections, en une seule action.
func (s *Service) Correct(ctx context.Context, counted map[int64]int64, today calendar.Date, userID int64) (int64, int, error) {
	changed := 0
	batch, err := s.action(ctx, func(tx *sql.Tx, batch int64) error {
		for id, q := range counted {
			if q < 0 {
				return validation.Error("Quantité négative.")
			}
			var current int64
			if err := tx.QueryRowContext(ctx, `SELECT qty FROM stock_items WHERE id = ?`, id).Scan(&current); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					continue
				}
				return err
			}
			if q == current {
				continue
			}
			changed++
			if err := change(ctx, tx, id, q-current, Correction, today, batch, 0, userID); err != nil {
				return err
			}
		}
		return nil
	})
	return batch, changed, err
}

// ConsumeFEFO retire une quantité d'un produit dans l'ordre FEFO (lots non
// périmés à la date d'abord, puis périmés s'il le faut : on a cuisiné ce
// qu'on avait) ; renvoie la quantité qui manquait.
func ConsumeFEFO(ctx context.Context, tx *sql.Tx, productID, need int64, kind Kind, date calendar.Date, batch, menuEntryID, userID int64) (int64, error) {
	lots, err := lotsTx(ctx, tx, productID)
	if err != nil {
		return 0, err
	}
	takes, short := PlanFEFO(lots, need, date)
	if short > 0 {
		// Les lots périmés restants servent en dernier recours.
		Consume(lots, takes)
		var expired []Lot
		for _, l := range lots {
			if !l.Usable(date) && l.Qty > 0 {
				l.BestBefore = ""
				expired = append(expired, l)
			}
		}
		more, rest := PlanFEFO(expired, short, date)
		takes, short = append(takes, more...), rest
	}
	for _, t := range takes {
		if err := change(ctx, tx, t.LotID, -t.Qty, kind, date, batch, menuEntryID, userID); err != nil {
			return 0, err
		}
	}
	return short, nil
}

func lotsTx(ctx context.Context, tx *sql.Tx, productID int64) ([]Lot, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id, product_id, qty, coalesce(best_before, ''), added_on FROM stock_items WHERE product_id = ? AND qty > 0`, productID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Lot
	for rows.Next() {
		var l Lot
		if err := rows.Scan(&l.ID, &l.ProductID, &l.Qty, &l.BestBefore, &l.AddedOn); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// Lots renvoie les lots non épuisés des produits demandés (tous si ids est
// vide), par produit : données des moteurs de menus et de courses.
func (s *Service) Lots(ctx context.Context, ids []int64) (map[int64][]Lot, error) {
	q := `SELECT id, product_id, qty, coalesce(best_before, ''), added_on FROM stock_items WHERE qty > 0`
	var args []any
	if len(ids) > 0 {
		q += ` AND product_id IN (SELECT value FROM json_each(?))`
		args = append(args, idList(ids))
	}
	lots, err := db.Collect(ctx, s.db.R, func(r db.Scanner) (Lot, error) {
		var l Lot
		return l, r.Scan(&l.ID, &l.ProductID, &l.Qty, &l.BestBefore, &l.AddedOn)
	}, q, args...)
	if err != nil {
		return nil, err
	}
	out := make(map[int64][]Lot)
	for _, l := range lots {
		out[l.ProductID] = append(out[l.ProductID], l)
	}
	return out, nil
}

func idList(ids []int64) string {
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = fmt.Sprint(id)
	}
	return "[" + strings.Join(parts, ",") + "]"
}

// Undo annule une action : les quantités sont rétablies, les dates d'un
// lot ouvert restaurées, et les mouvements marqués annulés.
func (s *Service) Undo(ctx context.Context, batch int64) error {
	return s.db.Tx(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT id, coalesce(stock_item_id, 0), kind, qty, prev_best_before, prev_opened_on
			FROM stock_movements WHERE batch = ? AND NOT undone ORDER BY id DESC`, batch)
		if err != nil {
			return err
		}
		type mv struct {
			id, item, qty int64
			kind          Kind
			best, opened  sql.NullString
		}
		var list []mv
		for rows.Next() {
			var m mv
			if err := rows.Scan(&m.id, &m.item, &m.kind, &m.qty, &m.best, &m.opened); err != nil {
				_ = rows.Close()
				return err
			}
			list = append(list, m)
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			return err
		}
		if len(list) == 0 {
			return validation.Error("Rien à annuler.")
		}
		for _, m := range list {
			if m.item == 0 {
				return validation.Error("Cette action ne peut plus être annulée.")
			}
			if m.kind == Opening {
				if _, err := tx.ExecContext(ctx, `UPDATE stock_items SET best_before = ?, opened_on = ? WHERE id = ?`,
					nullStr(m.best), nullStr(m.opened), m.item); err != nil {
					return err
				}
			} else {
				res, err := tx.ExecContext(ctx, `UPDATE stock_items SET qty = qty - ?, emptied_on = NULL WHERE id = ? AND qty - ? >= 0`, m.qty, m.item, m.qty)
				if err != nil {
					return err
				}
				if n, _ := res.RowsAffected(); n == 0 {
					return validation.Error("Cette action ne peut plus être annulée : le stock a changé depuis.")
				}
			}
			if _, err := tx.ExecContext(ctx, `UPDATE stock_movements SET undone = 1 WHERE id = ?`, m.id); err != nil {
				return err
			}
		}
		// Un lot créé par l'action annulée (ajout, déplacement) et vide
		// disparaît.
		_, err = tx.ExecContext(ctx, `DELETE FROM stock_items WHERE qty = 0 AND id IN (SELECT stock_item_id FROM stock_movements
			WHERE batch = ? AND kind IN ('achat', 'ajout', 'transfert') AND qty > 0)
			AND NOT EXISTS (SELECT 1 FROM stock_movements m WHERE m.stock_item_id = stock_items.id AND NOT m.undone)`, batch)
		return err
	})
}

// LastAction décrit la dernière action annulable d'un utilisateur, faite
// depuis moins de 15 minutes.
type LastAction struct {
	Batch   int64
	Label   string
	Product string
}

// Last renvoie la dernière action annulable de l'utilisateur.
func (s *Service) Last(ctx context.Context, userID int64) (LastAction, bool, error) {
	var a LastAction
	var kind Kind
	var n int
	err := s.db.R.QueryRowContext(ctx, `SELECT m.batch, m.kind, p.name, (SELECT count(DISTINCT product_id) FROM stock_movements WHERE batch = m.batch)
		FROM stock_movements m JOIN products p ON p.id = m.product_id
		WHERE m.user_id = ? AND NOT m.undone AND m.at >= strftime('%Y-%m-%dT%H:%M:%SZ', 'now', '-15 minutes')
		ORDER BY m.batch DESC, m.id LIMIT 1`, userID).Scan(&a.Batch, &kind, &a.Product, &n)
	if errors.Is(err, sql.ErrNoRows) {
		return a, false, nil
	}
	if err != nil {
		return a, false, err
	}
	a.Label = kind.Label()
	if n > 1 {
		a.Product = fmt.Sprintf("%s et %d autres produits", a.Product, n-1)
	}
	return a, true, nil
}

// Purge supprime les lots épuisés depuis plus de 30 jours (tâche
// quotidienne) ; leurs mouvements restent au journal.
func (s *Service) Purge(ctx context.Context, today calendar.Date) (int64, error) {
	res, err := s.db.W.ExecContext(ctx, `DELETE FROM stock_items WHERE qty = 0 AND emptied_on IS NOT NULL AND emptied_on < ?`, today.AddDays(-30))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// Movement est une ligne du journal affichée.
type Movement struct {
	Date    calendar.Date
	Kind    Kind
	Qty     int64
	Value   int64 // centimes, -1 : inconnue
	Undone  bool
	Product string
	Unit    qty.Base
}

// History renvoie les derniers mouvements d'un produit (tous si nul).
func (s *Service) History(ctx context.Context, productID int64, limit int) ([]Movement, error) {
	return db.Collect(ctx, s.db.R, func(r db.Scanner) (Movement, error) {
		var m Movement
		return m, r.Scan(&m.Date, &m.Kind, &m.Qty, &m.Value, &m.Undone, &m.Product, &m.Unit)
	}, `SELECT m.date, m.kind, m.qty, coalesce(m.value_cents, -1), m.undone, p.name, p.unit
		FROM stock_movements m JOIN products p ON p.id = m.product_id
		WHERE (? = 0 OR m.product_id = ?) AND NOT (m.kind = 'transfert' AND m.qty > 0)
		ORDER BY m.id DESC LIMIT ?`, productID, productID, limit)
}
