package catalog

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"slices"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/codecolors/petitchef/internal/platform/db"
	"github.com/codecolors/petitchef/internal/platform/qty"
)

// SeedVersion est la version de la base de départ embarquée : à augmenter
// à chaque modification des fichiers de seed/ pour que les installations
// existantes reçoivent les nouveautés (sans toucher aux produits modifiés).
const SeedVersion = 1

//go:embed seed/*.yaml
var seedFS embed.FS

// seedFile est un fichier de la base de départ : un rayon, les valeurs par
// défaut de ses produits et une ligne par produit (voir seed/LISEZMOI.md).
type seedFile struct {
	Aisle    string            `yaml:"rayon"`
	Defaults map[string]string `yaml:"defauts"`
	Products []string          `yaml:"produits"`
}

// SeedProduct est un produit de la base de départ, prêt à insérer.
type SeedProduct struct {
	Key      string
	Aisle    string // slug du rayon
	Location string // slug de l'emplacement
	Product         // champs du produit (sans identifiants)
}

// LoadSeed lit et valide la base de départ embarquée.
func LoadSeed() ([]SeedProduct, error) { return loadSeed(seedFS) }

func loadSeed(fsys fs.FS) ([]SeedProduct, error) {
	files, err := fs.Glob(fsys, "seed/*.yaml")
	if err != nil {
		return nil, err
	}
	var out []SeedProduct
	var errs []error
	for _, f := range files {
		raw, err := fs.ReadFile(fsys, f)
		if err != nil {
			return nil, err
		}
		var sf seedFile
		if err := yaml.Unmarshal(raw, &sf); err != nil {
			return nil, fmt.Errorf("%s : %w", f, err)
		}
		for i, line := range sf.Products {
			p, err := parseSeedLine(sf, line)
			if err != nil {
				errs = append(errs, fmt.Errorf("%s, produit %d (%q) : %w", f, i+1, line, err))
				continue
			}
			out = append(out, p)
		}
	}
	return out, errors.Join(errs...)
}

// parseSeedLine lit « Nom | clé valeur | clé valeur… », les clés absentes
// prenant la valeur par défaut du fichier.
func parseSeedLine(sf seedFile, line string) (SeedProduct, error) {
	parts := strings.Split(line, "|")
	name := strings.TrimSpace(parts[0])
	if name == "" {
		return SeedProduct{}, errors.New("nom manquant")
	}
	fields := make(map[string]string, len(sf.Defaults)+len(parts))
	for k, v := range sf.Defaults {
		fields[k] = v
	}
	flags := map[string]bool{}
	for _, part := range parts[1:] {
		part = strings.TrimSpace(part)
		k, v, _ := strings.Cut(part, " ")
		if v == "" && (k == "base" || k == "ddm" || k == "dlc") {
			flags[k] = true
			continue
		}
		fields[k] = strings.TrimSpace(v)
	}
	p := SeedProduct{Aisle: sf.Aisle, Location: fields["emp"]}
	p.Name = name
	p.Search = Normalize(name)
	p.Key = fields["cle"]
	if p.Key == "" {
		p.Key = strings.ReplaceAll(p.Search, " ", "-")
	}
	p.Universe = Universe(fields["univers"])
	if !p.Universe.Valid() {
		return p, fmt.Errorf("univers %q inconnu", fields["univers"])
	}
	p.Unit = qty.Base(fields["unite"])
	if !p.Unit.Valid() {
		return p, fmt.Errorf("unité %q inconnue", fields["unite"])
	}
	var err error
	if p.PieceG, err = optInt(fields["piece"]); err != nil {
		return p, fmt.Errorf("piece : %w", err)
	}
	if p.Density, err = optInt(fields["densite"]); err != nil {
		return p, fmt.Errorf("densite : %w", err)
	}
	if p.Units, err = parseUnits(fields["u"]); err != nil {
		return p, err
	}
	if p.Packages, err = parsePackages(fields["cond"]); err != nil {
		return p, err
	}
	if p.ShelfClosed, p.ShelfOpened, p.ShelfFrozen, err = parseShelf(fields["cons"]); err != nil {
		return p, err
	}
	p.DateKind = DateKind(strings.ToUpper(fields["date"]))
	if flags["ddm"] {
		p.DateKind = DDM
	}
	if flags["dlc"] {
		p.DateKind = DLC
	}
	if p.DateKind == "" {
		p.DateKind = DDM
	}
	if p.DateKind != DLC && p.DateKind != DDM {
		return p, fmt.Errorf("date %q : DLC ou DDM attendu", fields["date"])
	}
	p.Group = Group(fields["groupe"])
	if !p.Group.Valid() {
		return p, fmt.Errorf("groupe %q inconnu", p.Group)
	}
	if p.Season, err = parseSeason(fields["saison"]); err != nil {
		return p, err
	}
	for code := range strings.SplitSeq(fields["all"], ",") {
		code = strings.TrimSpace(code)
		if code == "" {
			continue
		}
		bit, ok := AllergenByCode(code)
		if !ok {
			return p, fmt.Errorf("allergène %q inconnu", code)
		}
		p.Allergens |= bit
	}
	if p.Universe == Food || p.Universe == Baby {
		p.Traits = DefaultTraits(p.Group, p.Allergens)
	}
	for t := range strings.FieldsSeq(fields["traits"]) {
		bit, ok := map[string]Traits{"viande": Meat | Animal, "animal": Animal, "porc": Pork | Meat | Animal, "alcool": Alcohol}[strings.TrimLeft(t, "+-")]
		if !ok {
			return p, fmt.Errorf("trait %q inconnu", t)
		}
		if strings.HasPrefix(t, "-") {
			p.Traits &^= bit
		} else {
			p.Traits |= bit
		}
	}
	p.Staple = flags["base"]
	for s := range strings.SplitSeq(fields["syn"], ",") {
		if s = strings.TrimSpace(s); s != "" {
			p.Synonyms = append(p.Synonyms, s)
		}
	}
	return p, nil
}

func optInt(s string) (int64, error) {
	if s == "" {
		return 0, nil
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("entier positif attendu, %q reçu", s)
	}
	return n, nil
}

// parseUnits lit « cs=15, gousse=5 ».
func parseUnits(s string) ([]UnitConv, error) {
	var out []UnitConv
	for item := range strings.SplitSeq(s, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		u, v, ok := strings.Cut(item, "=")
		code := qty.Canonical(u)
		n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
		if !ok || code == "" || err != nil || n <= 0 {
			return nil, fmt.Errorf("conversion %q illisible (unité=quantité)", item)
		}
		out = append(out, UnitConv{Unit: code, Qty: n})
	}
	return out, nil
}

// parsePackages lit « 250, 500 » ou « 6000:6 × 1 L » ; une virgule
// décimale dans un libellé (« 1500:1,5 L ») est conservée.
func parsePackages(s string) ([]Package, error) {
	var items []string
	for item := range strings.SplitSeq(s, ",") {
		if v, _, _ := strings.Cut(strings.TrimSpace(item), ":"); len(items) > 0 && !isDigits(v) {
			items[len(items)-1] += "," + item
			continue
		}
		items = append(items, item)
	}
	var out []Package
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		v, label, _ := strings.Cut(item, ":")
		n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
		if err != nil || n <= 0 {
			return nil, fmt.Errorf("conditionnement %q illisible", item)
		}
		out = append(out, Package{Qty: n, Label: strings.TrimSpace(label)})
	}
	return out, nil
}

// parseShelf lit « fermé/ouvert/congelé » en jours, « - » pour inconnu.
func parseShelf(s string) (closed, opened, frozen int64, err error) {
	vals := [3]int64{-1, -1, -1}
	if s != "" {
		parts := strings.Split(s, "/")
		if len(parts) != 3 {
			return 0, 0, 0, fmt.Errorf("conservation %q : fermé/ouvert/congelé attendu", s)
		}
		for i, p := range parts {
			p = strings.TrimSpace(p)
			if p == "-" || p == "" {
				continue
			}
			n, err := strconv.ParseInt(p, 10, 64)
			if err != nil || n < 0 {
				return 0, 0, 0, fmt.Errorf("conservation %q illisible", s)
			}
			vals[i] = n
		}
	}
	return vals[0], vals[1], vals[2], nil
}

// parseSeason lit « 6-9 », « 11-2 », « 3,4,9 » ou « toute ».
func parseSeason(s string) (Months, error) {
	if s == "" {
		return 0, nil
	}
	if s == "toute" {
		return AllYear, nil
	}
	var m Months
	for item := range strings.SplitSeq(s, ",") {
		a, b, isRange := strings.Cut(strings.TrimSpace(item), "-")
		from, err1 := strconv.Atoi(a)
		to := from
		var err2 error
		if isRange {
			to, err2 = strconv.Atoi(b)
		}
		if err1 != nil || err2 != nil || from < 1 || from > 12 || to < 1 || to > 12 {
			return 0, fmt.Errorf("saison %q illisible", s)
		}
		m |= MonthRange(from, to)
	}
	return m, nil
}

// SyncSeed charge la base de départ dans le catalogue : produits nouveaux
// insérés, produits de la base non modifiés par l'utilisateur mis à jour,
// produits modifiés laissés tels quels. Ne fait rien si la base est déjà à
// jour. Appelée au démarrage, après les migrations.
func SyncSeed(ctx context.Context, d *db.DB) error {
	var current int
	err := d.W.QueryRowContext(ctx, `SELECT CAST(value AS INTEGER) FROM settings WHERE key = 'catalog_seed_version'`).Scan(&current)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if current >= SeedVersion {
		return nil
	}
	seed, err := LoadSeed()
	if err != nil {
		return fmt.Errorf("base de départ du catalogue : %w", err)
	}
	inserted, updated := 0, 0
	err = d.Tx(ctx, func(tx *sql.Tx) error {
		aisles, err := slugIDs(ctx, tx, "aisles")
		if err != nil {
			return err
		}
		locations, err := slugIDs(ctx, tx, "locations")
		if err != nil {
			return err
		}
		for _, sp := range seed {
			aisle, ok := aisles[sp.Aisle]
			if !ok {
				return fmt.Errorf("rayon %q inconnu", sp.Aisle)
			}
			var loc any
			if sp.Location != "" {
				id, ok := locations[sp.Location]
				if !ok {
					return fmt.Errorf("emplacement %q inconnu", sp.Location)
				}
				loc = id
			}
			var id int64
			var modified bool
			err := tx.QueryRowContext(ctx, `SELECT id, modified FROM products WHERE seed_key = ?`, sp.Key).Scan(&id, &modified)
			switch {
			case errors.Is(err, sql.ErrNoRows):
				// Un produit créé par l'utilisateur sous le même nom l'emporte.
				var exists bool
				if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM products WHERE search = ?)
					OR EXISTS (SELECT 1 FROM product_synonyms WHERE search = ?)`, sp.Search, sp.Search).Scan(&exists); err != nil {
					return err
				}
				if exists {
					continue
				}
				res, err := tx.ExecContext(ctx, `INSERT INTO products (name, search, universe, aisle_id, location_id, unit, origin, seed_key)
					VALUES (?, ?, ?, ?, ?, ?, 'seed', ?)`, sp.Name, sp.Search, sp.Universe, aisle, loc, sp.Unit, sp.Key)
				if err != nil {
					return fmt.Errorf("%s : %w", sp.Name, err)
				}
				if id, err = res.LastInsertId(); err != nil {
					return err
				}
				inserted++
			case err != nil:
				return err
			case modified:
				continue
			default:
				updated++
			}
			p := sp.Product
			p.ID, p.AisleID = id, aisle
			if loc != nil {
				p.LocationID = loc.(int64)
			}
			if err := writeProduct(ctx, tx, p, false); err != nil {
				return fmt.Errorf("%s : %w", sp.Name, err)
			}
			if _, err := tx.ExecContext(ctx, `UPDATE products SET origin = 'seed', seed_version = ?, modified = 0 WHERE id = ?`, SeedVersion, id); err != nil {
				return err
			}
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO settings (key, value) VALUES ('catalog_seed_version', ?)
			ON CONFLICT (key) DO UPDATE SET value = excluded.value`, strconv.Itoa(SeedVersion))
		return err
	})
	if err != nil {
		return err
	}
	slog.InfoContext(ctx, "base de départ du catalogue chargée", "version", SeedVersion, "ajoutés", inserted, "mis à jour", updated)
	return nil
}

func slugIDs(ctx context.Context, tx *sql.Tx, table string) (map[string]int64, error) {
	rows, err := tx.QueryContext(ctx, `SELECT slug, id FROM `+table+` WHERE slug IS NOT NULL`) //nolint:gosec // nom de table constant
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := map[string]int64{}
	for rows.Next() {
		var slug string
		var id int64
		if err := rows.Scan(&slug, &id); err != nil {
			return nil, err
		}
		out[slug] = id
	}
	return out, rows.Err()
}

// SeedProblems contrôle la cohérence de la base de départ : doublons de
// nom ou de synonyme, conversions et conditionnements valides, rayon et
// unité renseignés. Utilisée par les tests.
func SeedProblems(seed []SeedProduct, aisles, locations []string) []string {
	var problems []string
	seen := map[string]string{}
	keys := map[string]bool{}
	claim := func(search, owner string) {
		if prev, ok := seen[search]; ok && prev != owner {
			problems = append(problems, fmt.Sprintf("« %s » : nom ou synonyme déjà pris par « %s »", owner, prev))
			return
		}
		seen[search] = owner
	}
	for _, p := range seed {
		if keys[p.Key] {
			problems = append(problems, fmt.Sprintf("« %s » : clé %q en double", p.Name, p.Key))
		}
		keys[p.Key] = true
		claim(p.Search, p.Name)
		for _, s := range p.Synonyms {
			claim(Normalize(s), p.Name)
		}
		if !slices.Contains(aisles, p.Aisle) {
			problems = append(problems, fmt.Sprintf("« %s » : rayon %q inconnu", p.Name, p.Aisle))
		}
		if p.Location != "" && !slices.Contains(locations, p.Location) {
			problems = append(problems, fmt.Sprintf("« %s » : emplacement %q inconnu", p.Name, p.Location))
		}
		c := p.Conv()
		for _, u := range p.Units {
			if _, ok := c.ToBase(1000, u.Unit); !ok {
				problems = append(problems, fmt.Sprintf("« %s » : conversion %s invalide", p.Name, u.Unit))
			}
		}
		if p.Unit == qty.Piece && p.Universe == Food && p.PieceG == 0 {
			problems = append(problems, fmt.Sprintf("« %s » : unité pièce sans poids d'une pièce", p.Name))
		}
		if p.Universe == Food && p.Group == NoGroup {
			problems = append(problems, fmt.Sprintf("« %s » : groupe alimentaire manquant", p.Name))
		}
		if p.Universe != Food && p.Universe != Baby && p.Universe != Pets && p.Group != NoGroup {
			problems = append(problems, fmt.Sprintf("« %s » : groupe alimentaire pour un produit non alimentaire", p.Name))
		}
		for _, pk := range p.Packages {
			if pk.Qty <= 0 {
				problems = append(problems, fmt.Sprintf("« %s » : conditionnement nul", p.Name))
			}
		}
	}
	return problems
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
