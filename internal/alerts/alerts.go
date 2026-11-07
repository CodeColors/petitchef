// Package alerts est le système d'alertes unique de PetitChef : il examine
// la situation (dates limites, stock minimum, ruptures prévues, menus,
// liste de courses) au moyen des examens fournis par les domaines, crée une
// alerte par situation nouvelle, les présente jusqu'à leur acquittement et
// les relaie, si l'utilisateur l'a activé, par e-mail ou webhook (ntfy).
// L'examen a lieu après une action et une fois par jour, jamais par
// interrogation périodique.
package alerts

import (
	"context"
	"database/sql"
	"slices"
	"sync"

	"github.com/codecolors/petitchef/internal/platform/calendar"
	"github.com/codecolors/petitchef/internal/platform/db"
	"github.com/codecolors/petitchef/internal/platform/validation"
)

// Kind est un type d'alerte.
type Kind string

const (
	ExpiringSoon Kind = "peremption"    // produits qui périment bientôt (résumé quotidien)
	Expired      Kind = "perime"        // produits périmés encore en stock
	BelowMinimum Kind = "stock_minimum" // stock sous le minimum
	Shortage     Kind = "rupture"       // rupture prévue avant les prochaines courses
	NoMenu       Kind = "menu_absent"   // menu non planifié pour la semaine suivante
	ListReady    Kind = "liste_prete"   // liste de courses prête
)

// Kinds liste les types dans l'ordre d'affichage.
var Kinds = []Kind{ExpiringSoon, Expired, BelowMinimum, Shortage, NoMenu, ListReady}

// Label décrit le type d'alerte.
func (k Kind) Label() string {
	switch k {
	case ExpiringSoon:
		return "Produits qui périment bientôt"
	case Expired:
		return "Produits périmés"
	case BelowMinimum:
		return "Stock sous le minimum"
	case Shortage:
		return "Rupture prévue avant les prochaines courses"
	case NoMenu:
		return "Menu de la semaine suivante non planifié"
	}
	return "Liste de courses prête"
}

// ParamLabel décrit le paramètre du type.
func (k Kind) ParamLabel() string {
	switch k {
	case ExpiringSoon:
		return "Prévenir (jours avant la date limite)"
	case Expired:
		return "Rappeler après (jours)"
	case BelowMinimum:
		return "Délai avant alerte (jours)"
	case Shortage:
		return "Horizon examiné (jours)"
	case NoMenu:
		return "Prévenir (jours avant le lundi)"
	}
	return "Lignes minimum"
}

// paramMax renvoie la valeur maximale du paramètre (le minimum est 1).
func (k Kind) paramMax() int {
	switch k {
	case ExpiringSoon, NoMenu:
		return 14
	case Shortage:
		return 60
	}
	return 100
}

// Check examine une partie de la situation pour les types activés ; il est
// fourni par le domaine concerné (stock, menus, courses).
type Check func(ctx context.Context, today calendar.Date, settings map[Kind]Setting) ([]Alert, error)

// Setting est le réglage d'un type d'alerte.
type Setting struct {
	Kind    Kind
	Enabled bool
	Param   int
}

// Alert est une alerte présentée dans l'application.
type Alert struct {
	ID           int64
	Kind         Kind
	Key          string // situation signalée : une seule alerte par clé
	Title        string
	Detail       string
	Link         string
	CreatedOn    calendar.Date
	Acknowledged bool
}

// Service examine la situation et gère les alertes.
type Service struct {
	db        *db.DB
	checks    []Check
	baseURL   string // adresse publique, pour les liens des notifications
	transport transport
	trigger   chan struct{}
	mu        sync.Mutex // un examen à la fois
}

// NewService crée le service des alertes ; baseURL (facultative) sert aux
// liens des notifications.
func NewService(d *db.DB, baseURL string) *Service {
	return &Service{
		db: d, baseURL: baseURL,
		transport: netTransport{}, trigger: make(chan struct{}, 1),
	}
}

// Settings renvoie le réglage de chaque type, dans l'ordre d'affichage.
func (s *Service) Settings(ctx context.Context) ([]Setting, error) {
	rows, err := s.db.R.QueryContext(ctx, `SELECT kind, enabled, param FROM alert_settings`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Setting
	for rows.Next() {
		var st Setting
		if err := rows.Scan(&st.Kind, &st.Enabled, &st.Param); err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	slices.SortFunc(out, func(a, b Setting) int { return slices.Index(Kinds, a.Kind) - slices.Index(Kinds, b.Kind) })
	return out, rows.Err()
}

// Setting renvoie le réglage d'un type.
func (s *Service) Setting(ctx context.Context, k Kind) (Setting, error) {
	st := Setting{Kind: k}
	err := s.db.R.QueryRowContext(ctx, `SELECT enabled, param FROM alert_settings WHERE kind = ?`, k).Scan(&st.Enabled, &st.Param)
	return st, err
}

// SaveSettings enregistre les réglages des types d'alerte.
func (s *Service) SaveSettings(ctx context.Context, settings []Setting) error {
	for _, st := range settings {
		if !slices.Contains(Kinds, st.Kind) {
			return validation.Error("Type d'alerte inconnu.")
		}
		if hi := st.Kind.paramMax(); st.Param < 1 || st.Param > hi {
			return validation.Errorf("%s : valeur entre 1 et %d.", st.Kind.Label(), hi)
		}
	}
	return s.db.Tx(ctx, func(tx *sql.Tx) error {
		for _, st := range settings {
			if _, err := tx.ExecContext(ctx, `UPDATE alert_settings SET enabled = ?, param = ? WHERE kind = ?`, st.Enabled, st.Param, st.Kind); err != nil {
				return err
			}
		}
		return nil
	})
}

const alertSelect = `SELECT id, kind, key, title, detail, link, created_on, acknowledged FROM alerts`

func (s *Service) list(ctx context.Context, query string, args ...any) ([]Alert, error) {
	rows, err := s.db.R.QueryContext(ctx, alertSelect+query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Alert
	for rows.Next() {
		var a Alert
		if err := rows.Scan(&a.ID, &a.Kind, &a.Key, &a.Title, &a.Detail, &a.Link, &a.CreatedOn, &a.Acknowledged); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// Open renvoie les alertes non acquittées, les plus récentes d'abord.
func (s *Service) Open(ctx context.Context) ([]Alert, error) {
	return s.list(ctx, ` WHERE NOT acknowledged ORDER BY id DESC`)
}

// Acknowledged renvoie les dernières alertes acquittées.
func (s *Service) Acknowledged(ctx context.Context, limit int) ([]Alert, error) {
	return s.list(ctx, ` WHERE acknowledged ORDER BY id DESC LIMIT ?`, limit)
}

// OpenCount compte les alertes non acquittées (cloche de l'en-tête).
func (s *Service) OpenCount(ctx context.Context) (int, error) {
	var n int
	err := s.db.R.QueryRowContext(ctx, `SELECT count(*) FROM alerts WHERE NOT acknowledged`).Scan(&n)
	return n, err
}

// Acknowledge acquitte une alerte ; id nul : toutes.
func (s *Service) Acknowledge(ctx context.Context, id int64) error {
	_, err := s.db.W.ExecContext(ctx, `UPDATE alerts SET acknowledged = 1 WHERE NOT acknowledged AND (? = 0 OR id = ?)`, id, id)
	return err
}

// keepDays est la durée de conservation des alertes acquittées.
const keepDays = 90

// record enregistre les alertes nouvelles (une situation déjà signalée,
// même acquittée, ne l'est pas de nouveau) et purge les alertes acquittées
// anciennes. Renvoie le nombre d'alertes créées.
func (s *Service) record(ctx context.Context, found []Alert, today calendar.Date) (int, error) {
	created := 0
	err := s.db.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM alerts WHERE acknowledged AND created_on < ?`, today.AddDays(-keepDays)); err != nil {
			return err
		}
		for _, a := range found {
			res, err := tx.ExecContext(ctx, `
				INSERT INTO alerts (kind, key, title, detail, link, created_on) VALUES (?, ?, ?, ?, ?, ?)
				ON CONFLICT (key) DO NOTHING`, a.Kind, a.Key, a.Title, a.Detail, a.Link, today)
			if err != nil {
				return err
			}
			n, err := res.RowsAffected()
			if err != nil {
				return err
			}
			created += int(n)
		}
		return nil
	})
	return created, err
}

// AddCheck inscrit un examen ; appelé au démarrage, avant Run.
func (s *Service) AddCheck(c Check) { s.checks = append(s.checks, c) }

// Evaluate exécute les examens et enregistre les alertes nouvelles ;
// renvoie le nombre d'alertes créées.
func (s *Service) Evaluate(ctx context.Context, today calendar.Date) (int, error) {
	list, err := s.Settings(ctx)
	if err != nil {
		return 0, err
	}
	settings := make(map[Kind]Setting, len(list))
	for _, st := range list {
		settings[st.Kind] = st
	}
	var found []Alert
	for _, c := range s.checks {
		a, err := c(ctx, today, settings)
		if err != nil {
			return 0, err
		}
		for _, x := range a {
			if settings[x.Kind].Enabled {
				found = append(found, x)
			}
		}
	}
	return s.record(ctx, found, today)
}
