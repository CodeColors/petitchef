// Package web expose l'interface HTTP : routage, middlewares, pages rendues
// côté serveur et fichiers statiques embarqués. Les handlers ne contiennent
// aucune logique métier : ils délèguent aux packages du domaine.
package web

import (
	"errors"
	"html/template"
	"log/slog"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/codecolors/petitchef/internal/alerts"
	"github.com/codecolors/petitchef/internal/auth"
	"github.com/codecolors/petitchef/internal/backup"
	"github.com/codecolors/petitchef/internal/config"
	"github.com/codecolors/petitchef/internal/platform/calendar"
	"github.com/codecolors/petitchef/internal/platform/db"
	"github.com/codecolors/petitchef/internal/platform/money"
	"github.com/codecolors/petitchef/internal/platform/validation"
)

const (
	sessionCookie = "petitchef_session"
	flashCookie   = "petitchef_flash"
)

// maxFormBytes borne la taille des formulaires (hors futurs envois de
// fichiers, qui auront leur propre limite).
const maxFormBytes = 64 << 10

// Services regroupe les services du domaine utilisés par l'interface.
type Services struct {
	Auth    *auth.Service
	Alerts  *alerts.Service
	Backups *backup.Service
	// Restart arrête proprement PetitChef, que la politique de redémarrage
	// du conteneur relance (restauration d'une sauvegarde).
	Restart func()
}

// Server regroupe les dépendances des handlers.
type Server struct {
	Services
	db             *db.DB
	maxUpload      int64
	pages          *renderer
	assets         assets
	secureCookies  bool
	trustedProxies []netip.Prefix
	baseURL        string
}

// New construit le gestionnaire HTTP complet de l'application.
func New(cfg config.Config, database *db.DB, svc Services) (http.Handler, error) {
	static, err := loadAssets()
	if err != nil {
		return nil, err
	}
	pages, err := newRenderer(templateFuncs(static))
	if err != nil {
		return nil, err
	}
	s := &Server{
		Services:       svc,
		db:             database,
		maxUpload:      cfg.MaxUploadBytes(),
		pages:          pages,
		assets:         static,
		secureCookies:  cfg.SecureCookies(),
		trustedProxies: cfg.TrustedProxies,
	}
	if cfg.BaseURL != nil {
		s.baseURL = cfg.BaseURL.String()
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.healthz)
	mux.Handle("GET /static/{file}", s.assets)
	mux.HandleFunc("GET /setup", s.setupPage)
	mux.HandleFunc("POST /setup", s.setup)
	mux.HandleFunc("GET /login", s.loginPage)
	mux.HandleFunc("POST /login", s.login)
	mux.HandleFunc("POST /logout", s.logout)
	s.publicRoutes(mux)
	for pattern, h := range s.routes() {
		var handler http.Handler = h
		if strings.HasPrefix(pattern, "POST ") {
			handler = s.afterChange(handler)
		}
		mux.Handle(pattern, s.requireUser(handler.ServeHTTP))
	}
	mux.HandleFunc("/", s.notFound)

	// Protection CSRF : la bibliothèque standard refuse toute requête non
	// sûre émise depuis une autre origine (en-têtes Sec-Fetch-Site / Origin).
	csrf := http.NewCrossOriginProtection()
	if cfg.BaseURL != nil {
		if err := csrf.AddTrustedOrigin(cfg.BaseURL.Scheme + "://" + cfg.BaseURL.Host); err != nil {
			return nil, err
		}
	}
	csrf.SetDenyHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.renderError(w, r, http.StatusForbidden, "Requête refusée : elle ne provient pas de PetitChef.")
	}))

	return securityHeaders(logRequests(csrf.Handler(mux)), s.secureCookies), nil
}

// templateFuncs renvoie les fonctions disponibles dans les gabarits.
func templateFuncs(static assets) template.FuncMap {
	return template.FuncMap{
		"asset":     static.url,
		"euros":     money.Format,
		"navActive": navActive,
		"plural":    plural,
		"date":      func(d calendar.Date) string { return d.Format() },
		"datetime":  func(t time.Time) string { return t.In(time.Local).Format("02/01/2006 à 15h04") },
		"fileSize":  fileSize,
		"add":       func(a, b int) int { return a + b },
		"seq":       func(n int) []int { out := make([]int, n); for i := range out { out[i] = i }; return out },
	}
}

// render affiche une page dans la mise en page commune, avec le message
// de confirmation éventuellement laissé par l'action précédente et le
// nombre d'alertes à lire (cloche de l'en-tête).
func (s *Server) render(w http.ResponseWriter, r *http.Request, status int, page, title string, data any) {
	v := view{Title: title, Path: r.URL.Path, Theme: auth.ThemeAuto, User: currentUser(r.Context()), Data: data}
	if bc, ok := data.(interface{ bodyClass() string }); ok {
		v.BodyClass = bc.bodyClass()
	}
	if v.User != nil {
		v.Fab = fabFor(r.URL.Path)
		v.Theme = v.User.Theme
		v.Flash = s.popFlash(w, r)
		var err error
		if v.Alerts, err = s.Alerts.OpenCount(r.Context()); err != nil {
			slog.WarnContext(r.Context(), "décompte des alertes", "err", err)
		}
	}
	s.execute(w, r, status, page, "layout", v)
}

// renderBlock n'affiche qu'un bloc de la page (réponse à une requête HTMX).
func (s *Server) renderBlock(w http.ResponseWriter, r *http.Request, page, block string, data any) {
	s.execute(w, r, http.StatusOK, page, block, view{Path: r.URL.Path, User: currentUser(r.Context()), Data: data})
}

func (s *Server) execute(w http.ResponseWriter, r *http.Request, status int, page, block string, v view) {
	if err := s.pages.render(w, status, page, block, v); err != nil {
		slog.ErrorContext(r.Context(), "rendu de page", "page", page, "err", err)
		http.Error(w, "Erreur interne", http.StatusInternalServerError)
	}
}

var errorTitles = map[int]string{
	http.StatusBadRequest:          "Requête invalide",
	http.StatusForbidden:           "Accès refusé",
	http.StatusNotFound:            "Page introuvable",
	http.StatusInternalServerError: "Erreur interne",
}

func (s *Server) renderError(w http.ResponseWriter, r *http.Request, status int, message string) {
	s.render(w, r, status, "error", errorTitles[status], message)
}

func (s *Server) serverError(w http.ResponseWriter, r *http.Request, err error) {
	slog.ErrorContext(r.Context(), "erreur interne", "chemin", r.URL.Path, "err", err)
	s.renderError(w, r, http.StatusInternalServerError, "Une erreur interne est survenue. Réessayez dans un instant.")
}

func (s *Server) notFound(w http.ResponseWriter, r *http.Request) {
	s.renderError(w, r, http.StatusNotFound, "Cette page n'existe pas.")
}

// inputError renvoie le message d'une erreur de saisie, à réafficher dans le
// formulaire. Toute autre erreur reçoit ici sa réponse (404 ou 500) et ok
// vaut false.
func (s *Server) inputError(w http.ResponseWriter, r *http.Request, err error) (msg string, ok bool) {
	if msg, ok := validation.Message(err); ok {
		return msg, true
	}
	if errors.Is(err, db.ErrNotFound) || errors.Is(err, backup.ErrNotFound) || errors.Is(err, auth.ErrNotFound) {
		s.notFound(w, r)
	} else {
		s.serverError(w, r, err)
	}
	return "", false
}

// failed traite une erreur sans formulaire à réafficher : une saisie
// refusée devient un message de confirmation sur la page de retour.
func (s *Server) failed(w http.ResponseWriter, r *http.Request, err error, back string) {
	if msg, ok := s.inputError(w, r, err); ok {
		s.redirect(w, r, back, msg)
	}
}

// redirect renvoie vers une page de l'application, avec un message
// facultatif affiché à l'arrivée.
func (s *Server) redirect(w http.ResponseWriter, r *http.Request, to, flash string) {
	if flash != "" {
		s.writeCookie(w, flashCookie, url.QueryEscape(flash), time.Now().Add(time.Minute))
	}
	http.Redirect(w, r, localPath(to), http.StatusSeeOther)
}

func (s *Server) popFlash(w http.ResponseWriter, r *http.Request) string {
	c, err := r.Cookie(flashCookie)
	if err != nil {
		return ""
	}
	s.writeCookie(w, flashCookie, "", time.Time{})
	msg, _ := url.QueryUnescape(c.Value)
	return msg
}

func (s *Server) session(r *http.Request) (auth.Session, error) {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return auth.Session{}, auth.ErrNoSession
	}
	return s.Auth.Session(r.Context(), c.Value)
}

func (s *Server) setSessionCookie(w http.ResponseWriter, sess auth.Session) {
	s.writeCookie(w, sessionCookie, sess.Token, sess.ExpiresAt)
}

func (s *Server) clearSessionCookie(w http.ResponseWriter) {
	s.writeCookie(w, sessionCookie, "", time.Time{})
}

// writeCookie est l'unique point d'écriture des cookies ; une valeur vide
// efface le cookie. Secure suit BASE_URL : l'accès en HTTP simple sur le
// réseau local doit rester possible.
func (s *Server) writeCookie(w http.ResponseWriter, name, value string, expires time.Time) {
	c := &http.Cookie{ //nolint:gosec // G124 : Secure configurable, voir ci-dessus.
		Name:     name,
		Value:    value,
		Path:     "/",
		Expires:  expires,
		HttpOnly: true,
		Secure:   s.secureCookies,
		SameSite: http.SameSiteLaxMode,
	}
	if value == "" {
		c.MaxAge = -1
	}
	http.SetCookie(w, c)
}

// parseForm lit un formulaire en bornant sa taille.
func parseForm(w http.ResponseWriter, r *http.Request) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBytes)
	return r.ParseForm()
}

// longTransfer est le délai accordé à un transfert volumineux : fichier
// déposé (relevé, pièce jointe, sauvegarde) ou produit (export, sauvegarde).
// Les délais ordinaires du serveur (lecture 30 s, écriture 60 s) protègent
// des clients lents mais couperaient ces requêtes sur une connexion modeste.
const longTransfer = 30 * time.Minute

// allowLongTransfer prolonge les délais de lecture et d'écriture de la
// requête en cours.
func allowLongTransfer(w http.ResponseWriter) {
	rc := http.NewResponseController(w)
	deadline := time.Now().Add(longTransfer)
	// Sans prise en charge (serveur de test), les délais restent ceux du
	// serveur : rien à faire.
	_ = rc.SetReadDeadline(deadline)
	_ = rc.SetWriteDeadline(deadline)
}

// pathID lit l'identifiant numérique {id} du chemin.
func pathID(r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	return id, err == nil && id > 0
}

// formID lit un identifiant facultatif d'un champ de formulaire (0 si vide).
func formID(r *http.Request, field string) int64 {
	id, _ := strconv.ParseInt(r.FormValue(field), 10, 64)
	return max(id, 0)
}

// navActive indique si le chemin courant relève d'une rubrique.
func navActive(path, section string) bool {
	if section == "/" {
		return path == "/"
	}
	return path == section || strings.HasPrefix(path, section+"/")
}

// plural accorde un nom à la française : « 0 opération », « 3 opérations ».
func plural(n int, one, many string) string {
	if n <= 1 {
		return strconv.Itoa(n) + " " + one
	}
	return strconv.Itoa(n) + " " + many
}
