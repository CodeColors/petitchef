package stock

import (
	"context"
	"database/sql"
	"errors"

	"github.com/codecolors/petitchef/internal/catalog"
	"github.com/codecolors/petitchef/internal/platform/calendar"
	"github.com/codecolors/petitchef/internal/platform/db"
	"github.com/codecolors/petitchef/internal/platform/qty"
	"github.com/codecolors/petitchef/internal/platform/validation"
)

// Settings sont les réglages de stock d'un produit.
type Settings struct {
	ProductID int64
	Min       int64 // stock minimum (unité de base)
	Target    int64 // stock cible, au moins le minimum
	Track     bool  // estimation de la consommation activée
	DailyUse  int64 // consommation estimée, millièmes d'unité par jour
	Low       bool  // produit de base signalé « presque vide »
	LowSince  calendar.Date
}

// Settings renvoie les réglages de stock d'un produit (valeurs nulles s'il
// n'en a pas).
func (s *Service) Settings(ctx context.Context, productID int64) (Settings, error) {
	st := Settings{ProductID: productID}
	err := s.db.R.QueryRowContext(ctx, `SELECT min_qty, target_qty, track, daily_use, low, coalesce(low_since, '')
		FROM stock_settings WHERE product_id = ?`, productID).Scan(&st.Min, &st.Target, &st.Track, &st.DailyUse, &st.Low, &st.LowSince)
	if errors.Is(err, sql.ErrNoRows) {
		return st, nil
	}
	return st, err
}

// SaveSettings enregistre le minimum, la cible et le suivi d'un produit ;
// une cible inférieure au minimum prend la valeur du minimum.
func (s *Service) SaveSettings(ctx context.Context, st Settings) error {
	if st.Min < 0 || st.Target < 0 {
		return validation.Error("Les quantités doivent être positives.")
	}
	st.Target = max(st.Target, st.Min)
	_, err := s.db.W.ExecContext(ctx, `INSERT INTO stock_settings (product_id, min_qty, target_qty, track) VALUES (?, ?, ?, ?)
		ON CONFLICT (product_id) DO UPDATE SET min_qty = excluded.min_qty, target_qty = excluded.target_qty, track = excluded.track`,
		st.ProductID, st.Min, st.Target, st.Track)
	if db.IsForeignKeyViolation(err) {
		return db.ErrNotFound
	}
	return err
}

// SetLow signale un produit (de base) « presque vide », ou retire le
// signalement ; il entre alors dans la liste de courses.
func (s *Service) SetLow(ctx context.Context, productID int64, low bool, today calendar.Date) error {
	var since any
	if low {
		since = today
	}
	_, err := s.db.W.ExecContext(ctx, `INSERT INTO stock_settings (product_id, low, low_since) VALUES (?, ?, ?)
		ON CONFLICT (product_id) DO UPDATE SET low = excluded.low, low_since = excluded.low_since`, productID, low, since)
	if db.IsForeignKeyViolation(err) {
		return db.ErrNotFound
	}
	return err
}

// Total renvoie la quantité en stock d'un produit, lots périmés exclus au
// jour today.
func (s *Service) Total(ctx context.Context, productID int64, today calendar.Date) (int64, error) {
	var n int64
	err := s.db.R.QueryRowContext(ctx, `SELECT coalesce(sum(qty), 0) FROM stock_items WHERE product_id = ? AND qty > 0
		AND (best_before IS NULL OR best_before >= ?)`, productID, today).Scan(&n)
	return n, err
}

// Watched est un produit suivi (minimum, consommation ou « presque vide »)
// avec sa quantité en stock.
type Watched struct {
	Settings
	Name     string
	Unit     qty.Base
	Universe catalog.Universe
	Staple   bool
	Stock    int64 // non périmé au jour demandé
}

// Watched renvoie les produits suivis et leur stock au jour today.
func (s *Service) Watched(ctx context.Context, today calendar.Date) ([]Watched, error) {
	return db.Collect(ctx, s.db.R, func(r db.Scanner) (Watched, error) {
		var w Watched
		return w, r.Scan(&w.ProductID, &w.Min, &w.Target, &w.Track, &w.DailyUse, &w.Low, &w.LowSince, &w.Name, &w.Unit, &w.Universe, &w.Staple, &w.Stock)
	}, `SELECT st.product_id, st.min_qty, st.target_qty, st.track, st.daily_use, st.low, coalesce(st.low_since, ''),
			p.name, p.unit, p.universe, p.staple,
			coalesce((SELECT sum(qty) FROM stock_items i WHERE i.product_id = st.product_id AND i.qty > 0
				AND (i.best_before IS NULL OR i.best_before >= ?)), 0)
		FROM stock_settings st JOIN products p ON p.id = st.product_id
		WHERE (st.min_qty > 0 OR st.low OR st.track) AND NOT p.archived
		ORDER BY p.universe, p.search`, today)
}

// ConsumptionEvents renvoie les consommations d'un produit (cuisine,
// consommation manuelle ; ni pertes ni corrections) des 180 derniers jours.
func (s *Service) ConsumptionEvents(ctx context.Context, productID int64, today calendar.Date) ([]Event, error) {
	return db.Collect(ctx, s.db.R, func(r db.Scanner) (Event, error) {
		var e Event
		return e, r.Scan(&e.Date, &e.Qty)
	}, `SELECT date, -sum(qty) FROM stock_movements WHERE product_id = ? AND kind IN ('consommation', 'recette') AND NOT undone
		AND date >= ? GROUP BY date ORDER BY date`, productID, today.AddDays(-estimateWindow))
}

// RefreshEstimates recalcule la consommation estimée des produits suivis
// (tâche quotidienne) ; renvoie le nombre d'estimations disponibles.
func (s *Service) RefreshEstimates(ctx context.Context, today calendar.Date) (int, error) {
	ids, err := db.Collect(ctx, s.db.R, func(r db.Scanner) (int64, error) {
		var id int64
		return id, r.Scan(&id)
	}, `SELECT product_id FROM stock_settings WHERE track`)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, id := range ids {
		events, err := s.ConsumptionEvents(ctx, id, today)
		if err != nil {
			return n, err
		}
		rate, ok := EstimateDailyUse(events, today)
		if ok {
			n++
		}
		if _, err := s.db.W.ExecContext(ctx, `UPDATE stock_settings SET daily_use = ? WHERE product_id = ?`, rate, id); err != nil {
			return n, err
		}
	}
	return n, nil
}

// Template est un modèle de stock proposé à la création du foyer : des
// produits et leur minimum et cible (en conditionnements ou unités de
// base), entièrement modifiables ensuite.
type Template struct {
	Slug  string
	Name  string
	Desc  string
	Items []TemplateItem
}

// TemplateItem est une ligne de modèle : le produit est désigné par son
// nom (ou un synonyme) du catalogue ; Min et Target sont en unités de base.
type TemplateItem struct {
	Product     string
	Min, Target int64
	Track       bool
}

// Templates sont les modèles de stock proposés.
var Templates = []Template{
	{Slug: "salle-de-bain", Name: "Kit salle de bain", Desc: "Hygiène courante du foyer", Items: []TemplateItem{
		{"Dentifrice", 75, 225, true}, {"Brosse à dents", 1000, 2000, false}, {"Gel douche", 250, 750, true},
		{"Shampooing", 250, 500, true}, {"Savon liquide mains", 300, 600, true}, {"Déodorant", 1000, 2000, true},
		{"Papier toilette", 6000, 12000, true}, {"Mouchoirs en papier", 10000, 20000, true}, {"Coton-tiges", 50000, 200000, false},
	}},
	{Slug: "menage", Name: "Kit ménage", Desc: "Entretien de la maison et du linge", Items: []TemplateItem{
		{"Liquide vaisselle", 250, 750, true}, {"Tablettes lave-vaisselle", 10000, 60000, true}, {"Lessive liquide", 750, 3000, true},
		{"Nettoyant multi-usages", 375, 750, true}, {"Éponges", 1000, 3000, false}, {"Sacs poubelle 30 L", 10000, 50000, true},
		{"Essuie-tout", 1000, 4000, true}, {"Javel", 500, 1000, false},
	}},
	{Slug: "placard", Name: "Placard de base", Desc: "Produits du quotidien et petit-déjeuner", Items: []TemplateItem{
		{"Café moulu", 250, 500, true}, {"Lait demi-écrémé", 2000, 6000, true}, {"Riz basmati", 500, 1000, false},
		{"Spaghetti", 500, 1000, false}, {"Farine de blé", 500, 1000, false}, {"Sucre en poudre", 500, 1000, false},
		{"Huile d'olive", 250, 750, false}, {"Œufs", 6000, 12000, true}, {"Beurre doux", 125, 250, true},
		{"Tomates pelées en conserve", 400, 1200, false},
	}},
}

// ApplyTemplate applique un modèle : chaque produit trouvé reçoit le
// minimum et la cible du modèle (ses réglages existants sont remplacés).
// Renvoie le nombre de produits réglés.
func (s *Service) ApplyTemplate(ctx context.Context, slug string) (int, error) {
	var tpl *Template
	for i := range Templates {
		if Templates[i].Slug == slug {
			tpl = &Templates[i]
		}
	}
	if tpl == nil {
		return 0, db.ErrNotFound
	}
	n := 0
	for _, it := range tpl.Items {
		id, err := s.catalog.Resolve(ctx, it.Product)
		if errors.Is(err, db.ErrNotFound) {
			continue
		}
		if err != nil {
			return n, err
		}
		if err := s.SaveSettings(ctx, Settings{ProductID: id, Min: it.Min, Target: it.Target, Track: it.Track}); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}
