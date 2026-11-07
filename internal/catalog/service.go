package catalog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/codecolors/petitchef/internal/platform/calendar"
	"github.com/codecolors/petitchef/internal/platform/db"
	"github.com/codecolors/petitchef/internal/platform/qty"
	"github.com/codecolors/petitchef/internal/platform/validation"
)

// Service gère le catalogue.
type Service struct {
	db *db.DB
}

// NewService crée le service du catalogue.
func NewService(d *db.DB) *Service { return &Service{db: d} }

const productColumns = `p.id, p.name, p.search, p.universe, p.aisle_id, a.name, coalesce(p.location_id, 0), coalesce(l.name, ''),
	p.unit, coalesce(p.piece_g, 0), coalesce(p.density, 0), coalesce(p.shelf_closed, -1), coalesce(p.shelf_opened, -1),
	coalesce(p.shelf_frozen, -1), p.date_kind, p.food_group, p.season, p.allergens, p.diet, p.staple, coalesce(p.store_id, 0),
	p.brand, coalesce(p.kcal, -1), coalesce(p.proteins, -1), coalesce(p.carbs, -1), coalesce(p.fat, -1), coalesce(p.fibres, -1),
	p.photo, p.incomplete, p.origin, p.modified, p.archived`

const productFrom = ` FROM products p JOIN aisles a ON a.id = p.aisle_id LEFT JOIN locations l ON l.id = p.location_id`

func scanProduct(row db.Scanner) (Product, error) {
	var p Product
	n := &p.Nutrition
	err := row.Scan(&p.ID, &p.Name, &p.Search, &p.Universe, &p.AisleID, &p.AisleName, &p.LocationID, &p.Location,
		&p.Unit, &p.PieceG, &p.Density, &p.ShelfClosed, &p.ShelfOpened, &p.ShelfFrozen, &p.DateKind, &p.Group, &p.Season,
		&p.Allergens, &p.Traits, &p.Staple, &p.StoreID, &p.Brand, &n.Kcal, &n.Proteins, &n.Carbs, &n.Fat, &n.Fibres,
		&p.Photo, &p.Incomplete, &p.Origin, &p.Modified, &p.Archived)
	return p, err
}

// Get renvoie un produit complet (synonymes, codes-barres, conversions,
// conditionnements).
func (s *Service) Get(ctx context.Context, id int64) (Product, error) {
	p, err := scanProduct(s.db.R.QueryRowContext(ctx, `SELECT `+productColumns+productFrom+` WHERE p.id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return p, db.ErrNotFound
	}
	if err != nil {
		return p, err
	}
	return p, s.details(ctx, &p)
}

func (s *Service) details(ctx context.Context, p *Product) error {
	var err error
	if p.Synonyms, err = db.Collect(ctx, s.db.R, scanString, `SELECT name FROM product_synonyms WHERE product_id = ? ORDER BY name`, p.ID); err != nil {
		return err
	}
	if p.Barcodes, err = db.Collect(ctx, s.db.R, scanString, `SELECT code FROM product_barcodes WHERE product_id = ? ORDER BY code`, p.ID); err != nil {
		return err
	}
	if p.Units, err = db.Collect(ctx, s.db.R, func(r db.Scanner) (UnitConv, error) {
		var u UnitConv
		return u, r.Scan(&u.Unit, &u.Qty)
	}, `SELECT unit, qty FROM product_units WHERE product_id = ? ORDER BY unit`, p.ID); err != nil {
		return err
	}
	p.Packages, err = db.Collect(ctx, s.db.R, func(r db.Scanner) (Package, error) {
		var pk Package
		return pk, r.Scan(&pk.ID, &pk.Qty, &pk.Label)
	}, `SELECT id, qty, label FROM product_packages WHERE product_id = ? ORDER BY qty`, p.ID)
	return err
}

func scanString(r db.Scanner) (string, error) {
	var s string
	return s, r.Scan(&s)
}

// Many renvoie les produits demandés, avec conversions et conditionnements
// (moteurs de calcul), indexés par identifiant.
func (s *Service) Many(ctx context.Context, ids []int64) (map[int64]Product, error) {
	out := make(map[int64]Product, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	list := idList(ids)
	rows, err := db.Collect(ctx, s.db.R, scanProduct, `SELECT `+productColumns+productFrom+` WHERE p.id IN (SELECT value FROM json_each(?))`, list)
	if err != nil {
		return nil, err
	}
	for _, p := range rows {
		out[p.ID] = p
	}
	type conv struct {
		id   int64
		unit string
		qty  int64
	}
	units, err := db.Collect(ctx, s.db.R, func(r db.Scanner) (conv, error) {
		var c conv
		return c, r.Scan(&c.id, &c.unit, &c.qty)
	}, `SELECT product_id, unit, qty FROM product_units WHERE product_id IN (SELECT value FROM json_each(?))`, list)
	if err != nil {
		return nil, err
	}
	for _, c := range units {
		p := out[c.id]
		p.Units = append(p.Units, UnitConv{Unit: c.unit, Qty: c.qty})
		out[c.id] = p
	}
	packs, err := db.Collect(ctx, s.db.R, func(r db.Scanner) (conv, error) {
		var c conv
		return c, r.Scan(&c.id, &c.qty, &c.unit)
	}, `SELECT product_id, qty, label FROM product_packages WHERE product_id IN (SELECT value FROM json_each(?)) ORDER BY qty`, list)
	if err != nil {
		return nil, err
	}
	for _, c := range packs {
		p := out[c.id]
		p.Packages = append(p.Packages, Package{Qty: c.qty, Label: c.unit})
		out[c.id] = p
	}
	return out, nil
}

// idList encode une liste d'identifiants pour json_each.
func idList(ids []int64) string {
	var b strings.Builder
	b.WriteByte('[')
	for i, id := range ids {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, "%d", id)
	}
	b.WriteByte(']')
	return b.String()
}

// Hit est un produit trouvé par la recherche instantanée.
type Hit struct {
	ID      int64
	Name    string
	Matched string // synonyme reconnu, s'il diffère du nom
	Aisle   string
	Unit    qty.Base
}

// Search renvoie au plus limit produits dont le nom ou un synonyme
// commence par la saisie (ou en contient un mot), les correspondances
// exactes et au début d'abord.
func (s *Service) Search(ctx context.Context, query string, limit int) ([]Hit, error) {
	q := Normalize(query)
	if q == "" {
		return nil, nil
	}
	like := escapeLike(q)
	return db.Collect(ctx, s.db.R, func(r db.Scanner) (Hit, error) {
		var h Hit
		return h, r.Scan(&h.ID, &h.Name, &h.Matched, &h.Aisle, &h.Unit)
	}, `
		WITH terms (product_id, term, name, rank) AS (
			SELECT id, search, '', 0 FROM products WHERE NOT archived
			UNION ALL
			SELECT product_id, search, name, 1 FROM product_synonyms
		), hits AS (
			SELECT product_id, min(CASE WHEN term = ?1 THEN 0 WHEN term LIKE ?2 || '%' ESCAPE '\' THEN 1 ELSE 2 END * 2 + rank) AS score,
			       min(CASE WHEN rank = 1 THEN name END) AS matched
			FROM terms
			WHERE term LIKE ?2 || '%' ESCAPE '\' OR term LIKE '% ' || ?2 || '%' ESCAPE '\'
			GROUP BY product_id
		)
		SELECT p.id, p.name, CASE WHEN h.score % 2 = 1 THEN coalesce(h.matched, '') ELSE '' END, a.name, p.unit
		FROM hits h JOIN products p ON p.id = h.product_id JOIN aisles a ON a.id = p.aisle_id
		WHERE NOT p.archived
		ORDER BY h.score, length(p.name), p.search
		LIMIT ?3`, q, like, limit)
}

func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

// Resolve renvoie le produit dont le nom ou un synonyme correspond
// exactement à la saisie (forme normalisée), ou db.ErrNotFound.
func (s *Service) Resolve(ctx context.Context, name string) (int64, error) {
	q := Normalize(name)
	var id int64
	err := s.db.R.QueryRowContext(ctx, `SELECT id FROM products WHERE search = ?1 AND NOT archived
		UNION ALL SELECT product_id FROM product_synonyms WHERE search = ?1 LIMIT 1`, q).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, db.ErrNotFound
	}
	return id, err
}

// ByBarcode renvoie le produit associé à un code-barres, ou db.ErrNotFound.
func (s *Service) ByBarcode(ctx context.Context, code string) (int64, error) {
	var id int64
	err := s.db.R.QueryRowContext(ctx, `SELECT product_id FROM product_barcodes WHERE code = ?`, cleanBarcode(code)).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, db.ErrNotFound
	}
	return id, err
}

func cleanBarcode(code string) string {
	return strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, code)
}

// AttachBarcode associe un code-barres (EAN-8 à EAN-14) à un produit ; il
// sera reconnu au prochain scan.
func (s *Service) AttachBarcode(ctx context.Context, productID int64, code string) error {
	code = cleanBarcode(code)
	if len(code) < 8 || len(code) > 14 {
		return validation.Error("Code-barres invalide : 8 à 14 chiffres attendus.")
	}
	_, err := s.db.W.ExecContext(ctx, `INSERT INTO product_barcodes (code, product_id) VALUES (?, ?)
		ON CONFLICT (code) DO UPDATE SET product_id = excluded.product_id`, code, productID)
	if db.IsForeignKeyViolation(err) {
		return db.ErrNotFound
	}
	return err
}

// Filter sélectionne les produits de la liste du catalogue.
type Filter struct {
	Query      string
	Universe   Universe
	AisleID    int64
	Incomplete bool
	Page       int // à partir de 0
}

// PageSize est le nombre de produits par page.
const PageSize = 50

// ListPage est une page de produits.
type ListPage struct {
	Items []Product
	Total int
	Page  int
	More  bool
}

// List renvoie une page de produits filtrés, triés par rayon puis par nom.
func (s *Service) List(ctx context.Context, f Filter) (ListPage, error) {
	where := []string{"NOT p.archived"}
	var args []any
	if q := Normalize(f.Query); q != "" {
		where = append(where, `(p.search LIKE ?1 || '%' ESCAPE '\' OR p.search LIKE '% ' || ?1 || '%' ESCAPE '\'
			OR p.id IN (SELECT product_id FROM product_synonyms WHERE search LIKE ?1 || '%' ESCAPE '\'))`)
		args = append(args, escapeLike(q))
	} else {
		args = append(args, "")
	}
	if f.Universe != "" {
		where = append(where, "p.universe = ?"+fmt.Sprint(len(args)+1))
		args = append(args, f.Universe)
	}
	if f.AisleID > 0 {
		where = append(where, "p.aisle_id = ?"+fmt.Sprint(len(args)+1))
		args = append(args, f.AisleID)
	}
	if f.Incomplete {
		where = append(where, "p.incomplete = 1")
	}
	cond := " WHERE " + strings.Join(where, " AND ")
	page := ListPage{Page: max(f.Page, 0)}
	if err := s.db.R.QueryRowContext(ctx, `SELECT count(*)`+productFrom+cond, args...).Scan(&page.Total); err != nil {
		return page, err
	}
	items, err := db.Collect(ctx, s.db.R, scanProduct, `SELECT `+productColumns+productFrom+cond+
		fmt.Sprintf(` ORDER BY a.position, p.search LIMIT %d OFFSET %d`, PageSize, page.Page*PageSize), args...)
	page.Items = items
	page.More = (page.Page+1)*PageSize < page.Total
	return page, err
}

// Input est la saisie d'un produit (formulaire complet).
type Input struct {
	Product
}

// validate contrôle la saisie et calcule la forme de recherche.
func validate(p *Product) error {
	p.Name = strings.TrimSpace(p.Name)
	if p.Name == "" {
		return validation.Error("Le nom du produit est obligatoire.")
	}
	if utf8.RuneCountInString(p.Name) > 120 {
		return validation.Error("Nom trop long : 120 caractères au plus.")
	}
	p.Search = Normalize(p.Name)
	if !p.Universe.Valid() {
		return validation.Error("Univers inconnu.")
	}
	if !p.Unit.Valid() {
		return validation.Error("Unité de base inconnue.")
	}
	if !p.Group.Valid() {
		return validation.Error("Groupe alimentaire inconnu.")
	}
	if p.AisleID <= 0 {
		return validation.Error("Choisissez un rayon.")
	}
	if p.DateKind != DLC && p.DateKind != DDM {
		p.DateKind = DDM
	}
	return nil
}

// Create ajoute un produit ; renvoie son identifiant.
func (s *Service) Create(ctx context.Context, p Product) (int64, error) {
	if err := validate(&p); err != nil {
		return 0, err
	}
	err := s.db.Tx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `INSERT INTO products (name, search, universe, aisle_id, unit) VALUES (?, ?, ?, ?, ?)`,
			p.Name, p.Search, p.Universe, p.AisleID, p.Unit)
		if err != nil {
			return err
		}
		if p.ID, err = res.LastInsertId(); err != nil {
			return err
		}
		return writeProduct(ctx, tx, p, true)
	})
	if db.IsUniqueViolation(err) {
		return 0, validation.Errorf("Un produit nommé « %s » (ou un synonyme) existe déjà.", p.Name)
	}
	return p.ID, err
}

// Update enregistre la fiche d'un produit. Un produit de la base de départ
// devient « modifié » : les mises à jour de la base ne le toucheront plus.
func (s *Service) Update(ctx context.Context, p Product) error {
	if err := validate(&p); err != nil {
		return err
	}
	err := s.db.Tx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE products SET modified = 1 WHERE id = ?`, p.ID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return db.ErrNotFound
		}
		return writeProduct(ctx, tx, p, true)
	})
	if db.IsUniqueViolation(err) {
		return validation.Errorf("Un autre produit porte déjà le nom « %s » ou l'un de ces synonymes.", p.Name)
	}
	return err
}

func nullable(v int64) any {
	if v <= 0 {
		return nil
	}
	return v
}

func nullableDays(v int64) any {
	if v < 0 {
		return nil
	}
	return v
}

// writeProduct écrit tous les champs d'un produit existant et remplace ses
// synonymes, conversions et conditionnements. user indique une saisie de
// l'utilisateur, qui recalcule l'indicateur « à compléter ».
func writeProduct(ctx context.Context, tx *sql.Tx, p Product, user bool) error {
	n := p.Nutrition
	nv := func(v int64) any {
		if v < 0 {
			return nil
		}
		return v
	}
	incomplete := 0
	if user && p.Incomplete {
		incomplete = 1
	}
	_, err := tx.ExecContext(ctx, `UPDATE products SET name = ?, search = ?, universe = ?, aisle_id = ?, location_id = ?, unit = ?,
		piece_g = ?, density = ?, shelf_closed = ?, shelf_opened = ?, shelf_frozen = ?, date_kind = ?, food_group = ?, season = ?,
		allergens = ?, diet = ?, staple = ?, store_id = ?, brand = ?, kcal = ?, proteins = ?, carbs = ?, fat = ?, fibres = ?, incomplete = ?
		WHERE id = ?`,
		p.Name, p.Search, p.Universe, p.AisleID, nullable(p.LocationID), p.Unit, nullable(p.PieceG), nullable(p.Density),
		nullableDays(p.ShelfClosed), nullableDays(p.ShelfOpened), nullableDays(p.ShelfFrozen), p.DateKind, p.Group, p.Season,
		p.Allergens, p.Traits, p.Staple, nullable(p.StoreID), p.Brand, nv(n.Kcal), nv(n.Proteins), nv(n.Carbs), nv(n.Fat), nv(n.Fibres),
		incomplete, p.ID)
	if err != nil {
		return err
	}
	for _, q := range []string{`DELETE FROM product_synonyms WHERE product_id = ?`, `DELETE FROM product_units WHERE product_id = ?`,
		`DELETE FROM product_packages WHERE product_id = ?`} {
		if _, err := tx.ExecContext(ctx, q, p.ID); err != nil {
			return err
		}
	}
	for _, syn := range p.Synonyms {
		search := Normalize(syn)
		if search == "" || search == p.Search {
			continue
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO product_synonyms (product_id, name, search) VALUES (?, ?, ?)
			ON CONFLICT (search) DO NOTHING`, p.ID, strings.TrimSpace(syn), search); err != nil {
			return err
		}
	}
	for _, u := range p.Units {
		if _, err := tx.ExecContext(ctx, `INSERT INTO product_units (product_id, unit, qty) VALUES (?, ?, ?)
			ON CONFLICT DO UPDATE SET qty = excluded.qty`, p.ID, u.Unit, u.Qty); err != nil {
			return err
		}
	}
	for _, pk := range p.Packages {
		if _, err := tx.ExecContext(ctx, `INSERT INTO product_packages (product_id, qty, label) VALUES (?, ?, ?)
			ON CONFLICT DO NOTHING`, p.ID, pk.Qty, pk.Label); err != nil {
			return err
		}
	}
	return nil
}

// Suggestion est le rayon et l'unité proposés pour un produit nouveau.
type Suggestion struct {
	AisleID  int64
	Unit     qty.Base
	Universe Universe
	Group    Group
}

// Suggest propose rayon, unité, univers et groupe d'après les produits
// existants qui partagent un mot avec le nom (« yaourt à la vanille »
// ressemble à « yaourt nature »). À défaut : épicerie salée, grammes.
func (s *Service) Suggest(ctx context.Context, name string) (Suggestion, error) {
	sg := Suggestion{Unit: qty.Gram, Universe: Food}
	if err := s.db.R.QueryRowContext(ctx, `SELECT id FROM aisles WHERE slug = 'epicerie-salee'`).Scan(&sg.AisleID); err != nil {
		return sg, err
	}
	words := strings.Fields(Normalize(name))
	for _, w := range words {
		if len(w) < 3 || stopWords[w] {
			continue
		}
		like := escapeLike(w)
		err := s.db.R.QueryRowContext(ctx, `SELECT aisle_id, unit, universe, food_group FROM products
			WHERE (search LIKE ?1 || '%' ESCAPE '\' OR search LIKE '% ' || ?1 || '%' ESCAPE '\') AND NOT archived AND NOT incomplete
			GROUP BY aisle_id, unit, universe, food_group ORDER BY count(*) DESC LIMIT 1`, like).Scan(&sg.AisleID, &sg.Unit, &sg.Universe, &sg.Group)
		if err == nil {
			return sg, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return sg, err
		}
	}
	return sg, nil
}

var stopWords = map[string]bool{"de": true, "du": true, "des": true, "la": true, "le": true, "les": true, "au": true, "aux": true,
	"et": true, "en": true, "pour": true, "avec": true, "sans": true, "frai": true, "bio": true}

// QuickCreate crée un produit en une étape d'après son nom, avec le rayon
// et l'unité suggérés ; il est marqué « à compléter ». Si le nom existe
// déjà, le produit existant est renvoyé.
func (s *Service) QuickCreate(ctx context.Context, name string) (int64, error) {
	if id, err := s.Resolve(ctx, name); err == nil {
		return id, nil
	} else if !errors.Is(err, db.ErrNotFound) {
		return 0, err
	}
	sg, err := s.Suggest(ctx, name)
	if err != nil {
		return 0, err
	}
	name = strings.TrimSpace(name)
	if r, size := utf8.DecodeRuneInString(name); size > 0 {
		name = strings.ToUpper(string(r)) + name[size:]
	}
	p := Product{Name: name, Universe: sg.Universe, AisleID: sg.AisleID, Unit: sg.Unit, Group: sg.Group, DateKind: DDM,
		ShelfClosed: -1, ShelfOpened: -1, ShelfFrozen: -1, Incomplete: true, Nutrition: Nutrition{-1, -1, -1, -1, -1}}
	if p.Universe == Food || p.Universe == Baby {
		p.Traits = DefaultTraits(p.Group, 0)
	}
	return s.Create(ctx, p)
}

// Archive retire un produit des recherches ; il reste lié à l'historique.
// Un produit jamais utilisé est supprimé.
func (s *Service) Archive(ctx context.Context, id int64) error {
	_, err := s.db.W.ExecContext(ctx, `DELETE FROM products WHERE id = ?`, id)
	if db.IsForeignKeyViolation(err) {
		_, err = s.db.W.ExecContext(ctx, `UPDATE products SET archived = 1, modified = 1 WHERE id = ?`, id)
	}
	return err
}

// Aisles renvoie les rayons dans l'ordre par défaut, avec leur nombre de
// produits.
func (s *Service) Aisles(ctx context.Context) ([]Aisle, error) {
	return db.Collect(ctx, s.db.R, func(r db.Scanner) (Aisle, error) {
		var a Aisle
		return a, r.Scan(&a.ID, &a.Slug, &a.Name, &a.Position, &a.Products)
	}, `SELECT a.id, a.slug, a.name, a.position, (SELECT count(*) FROM products p WHERE p.aisle_id = a.id AND NOT p.archived)
		FROM aisles a ORDER BY a.position`)
}

// Locations renvoie les emplacements dans l'ordre.
func (s *Service) Locations(ctx context.Context) ([]Location, error) {
	return db.Collect(ctx, s.db.R, func(r db.Scanner) (Location, error) {
		var l Location
		return l, r.Scan(&l.ID, &l.Slug, &l.Name, &l.Kind, &l.Position)
	}, `SELECT id, coalesce(slug, ''), name, kind, position FROM locations ORDER BY position`)
}

// SaveLocation crée (id nul) ou renomme un emplacement.
func (s *Service) SaveLocation(ctx context.Context, id int64, name, kind string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return validation.Error("Nom de l'emplacement obligatoire.")
	}
	if kind != "ambiant" && kind != "frais" && kind != "congele" {
		return validation.Error("Type d'emplacement inconnu.")
	}
	var err error
	if id == 0 {
		_, err = s.db.W.ExecContext(ctx, `INSERT INTO locations (name, kind, position) VALUES (?, ?, (SELECT coalesce(max(position), 0) + 1 FROM locations))`, name, kind)
	} else {
		_, err = s.db.W.ExecContext(ctx, `UPDATE locations SET name = ?, kind = ? WHERE id = ?`, name, kind, id)
	}
	if db.IsUniqueViolation(err) {
		return validation.Errorf("L'emplacement « %s » existe déjà.", name)
	}
	return err
}

// DeleteLocation supprime un emplacement vide.
func (s *Service) DeleteLocation(ctx context.Context, id int64) error {
	_, err := s.db.W.ExecContext(ctx, `DELETE FROM locations WHERE id = ?`, id)
	if db.IsForeignKeyViolation(err) {
		return validation.Error("Cet emplacement contient encore du stock : déplacez-le d'abord.")
	}
	return err
}

// Stores renvoie les magasins.
func (s *Service) Stores(ctx context.Context) ([]Store, error) {
	return db.Collect(ctx, s.db.R, func(r db.Scanner) (Store, error) {
		var st Store
		return st, r.Scan(&st.ID, &st.Name, &st.IsDefault)
	}, `SELECT id, name, is_default FROM stores ORDER BY is_default DESC, name`)
}

// SaveStore crée (id nul) ou renomme un magasin ; isDefault en fait le
// magasin proposé par défaut.
func (s *Service) SaveStore(ctx context.Context, id int64, name string, isDefault bool) (int64, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return 0, validation.Error("Nom du magasin obligatoire.")
	}
	err := s.db.Tx(ctx, func(tx *sql.Tx) error {
		if isDefault {
			if _, err := tx.ExecContext(ctx, `UPDATE stores SET is_default = 0`); err != nil {
				return err
			}
		}
		if id == 0 {
			res, err := tx.ExecContext(ctx, `INSERT INTO stores (name, is_default) VALUES (?, ?)`, name, isDefault)
			if err != nil {
				return err
			}
			id, err = res.LastInsertId()
			return err
		}
		_, err := tx.ExecContext(ctx, `UPDATE stores SET name = ?, is_default = ? WHERE id = ?`, name, isDefault, id)
		return err
	})
	if db.IsUniqueViolation(err) {
		return 0, validation.Errorf("Le magasin « %s » existe déjà.", name)
	}
	return id, err
}

// DeleteStore supprime un magasin.
func (s *Service) DeleteStore(ctx context.Context, id int64) error {
	_, err := s.db.W.ExecContext(ctx, `DELETE FROM stores WHERE id = ?`, id)
	return err
}

// StoreAisles renvoie les rayons dans l'ordre du parcours du magasin
// (storeID nul : ordre par défaut).
func (s *Service) StoreAisles(ctx context.Context, storeID int64) ([]Aisle, error) {
	return db.Collect(ctx, s.db.R, func(r db.Scanner) (Aisle, error) {
		var a Aisle
		return a, r.Scan(&a.ID, &a.Slug, &a.Name, &a.Position)
	}, `SELECT a.id, a.slug, a.name, row_number() OVER (ORDER BY o.position IS NULL, o.position, a.position)
		FROM aisles a LEFT JOIN store_aisle_order o ON o.aisle_id = a.id AND o.store_id = ?
		ORDER BY o.position IS NULL, o.position, a.position`, storeID)
}

// SetStoreOrder enregistre l'ordre des rayons d'un magasin (identifiants
// de rayon dans l'ordre du parcours).
func (s *Service) SetStoreOrder(ctx context.Context, storeID int64, aisles []int64) error {
	return s.db.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM store_aisle_order WHERE store_id = ?`, storeID); err != nil {
			return err
		}
		for i, a := range aisles {
			if _, err := tx.ExecContext(ctx, `INSERT INTO store_aisle_order (store_id, aisle_id, position) VALUES (?, ?, ?)
				ON CONFLICT DO NOTHING`, storeID, a, i+1); err != nil {
				if db.IsForeignKeyViolation(err) {
					return db.ErrNotFound
				}
				return err
			}
		}
		return nil
	})
}

// MoveAisle monte (delta -1) ou descend (delta +1) un rayon dans le
// parcours d'un magasin : alternative au glisser-déposer, accessible au
// clavier.
func (s *Service) MoveAisle(ctx context.Context, storeID, aisleID int64, delta int) error {
	order, err := s.StoreAisles(ctx, storeID)
	if err != nil {
		return err
	}
	ids := make([]int64, len(order))
	at := -1
	for i, a := range order {
		ids[i] = a.ID
		if a.ID == aisleID {
			at = i
		}
	}
	to := at + delta
	if at < 0 || to < 0 || to >= len(ids) {
		return nil
	}
	ids[at], ids[to] = ids[to], ids[at]
	return s.SetStoreOrder(ctx, storeID, ids)
}

// DefaultStore renvoie le magasin par défaut (0 s'il n'y en a pas).
func (s *Service) DefaultStore(ctx context.Context) (int64, error) {
	var id int64
	err := s.db.R.QueryRowContext(ctx, `SELECT id FROM stores ORDER BY is_default DESC, id LIMIT 1`).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return id, err
}

// RecordPrice ajoute un prix relevé (qty unités de base pour priceCents).
func (s *Service) RecordPrice(ctx context.Context, exec db.Execer, productID, storeID int64, date calendar.Date, priceCents, quantity int64) error {
	if priceCents < 0 || quantity <= 0 {
		return validation.Error("Prix ou quantité invalide.")
	}
	_, err := exec.ExecContext(ctx, `INSERT INTO prices (product_id, store_id, date, price_cents, qty) VALUES (?, ?, ?, ?, ?)`,
		productID, nullable(storeID), date, priceCents, quantity)
	return err
}

// Price est un prix relevé.
type Price struct {
	Date       calendar.Date
	Store      string
	PriceCents int64
	Qty        int64
}

// Prices renvoie l'historique des prix d'un produit, les plus récents
// d'abord.
func (s *Service) Prices(ctx context.Context, productID int64, limit int) ([]Price, error) {
	return db.Collect(ctx, s.db.R, func(r db.Scanner) (Price, error) {
		var p Price
		return p, r.Scan(&p.Date, &p.Store, &p.PriceCents, &p.Qty)
	}, `SELECT p.date, coalesce(s.name, ''), p.price_cents, p.qty FROM prices p LEFT JOIN stores s ON s.id = p.store_id
		WHERE p.product_id = ? ORDER BY p.date DESC, p.id DESC LIMIT ?`, productID, limit)
}

// UnitPrices renvoie le dernier prix connu de chaque produit demandé, en
// millièmes de centime par unité de base (précision suffisante pour le sel
// au gramme) ; les produits sans prix sont absents.
func (s *Service) UnitPrices(ctx context.Context, ids []int64) (map[int64]int64, error) {
	out := map[int64]int64{}
	if len(ids) == 0 {
		return out, nil
	}
	type row struct{ id, price, qty int64 }
	rows, err := db.Collect(ctx, s.db.R, func(r db.Scanner) (row, error) {
		var x row
		return x, r.Scan(&x.id, &x.price, &x.qty)
	}, `SELECT product_id, price_cents, qty FROM (
			SELECT product_id, price_cents, qty, row_number() OVER (PARTITION BY product_id ORDER BY date DESC, id DESC) AS n
			FROM prices WHERE product_id IN (SELECT value FROM json_each(?)))
		WHERE n = 1`, idList(ids))
	if err != nil {
		return nil, err
	}
	for _, x := range rows {
		out[x.id] = x.price * 1000 / x.qty
	}
	return out, nil
}

// Cost renvoie le coût estimé (centimes) d'une quantité de base, d'après
// un prix unitaire en millièmes de centime.
func Cost(unitPrice, quantity int64) int64 { return (unitPrice*quantity + 500) / 1000 }
