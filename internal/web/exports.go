package web

import (
	"net/http"
	"strings"
	"time"

	"github.com/codecolors/petitchef/internal/menus"
	"github.com/codecolors/petitchef/internal/platform/calendar"
	"github.com/codecolors/petitchef/internal/recipes"
)

// exportsView est la donnée du point d'entrée des exports.
type exportsView struct {
	Monday  calendar.Date
	From    calendar.Date
	Today   calendar.Date
	ICalURL string
	Recipes []recipes.Summary
}

func (s *Server) exportsPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	today := calendar.Today()
	v := exportsView{Monday: menus.Monday(today), Today: today, From: calendar.FromTime(today.Time().AddDate(-1, 0, 0))}
	tok, err := s.Exports.ICalToken(ctx)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	base := s.baseURL
	if base == "" {
		scheme := "http"
		if r.TLS != nil {
			scheme = "https"
		}
		base = scheme + "://" + r.Host
	}
	v.ICalURL = base + "/ical/" + tok + ".ics"
	if v.Recipes, err = s.Recipes.Titles(ctx, 0); err != nil {
		s.serverError(w, r, err)
		return
	}
	s.render(w, r, http.StatusOK, "exports", "Exports", v)
}

// period lit une période (du, au) ; par défaut, la semaine en cours.
func period(r *http.Request) (calendar.Date, calendar.Date) {
	from := menus.Monday(calendar.Today())
	if d, err := calendar.Parse(r.URL.Query().Get("du")); err == nil {
		from = d
	}
	to := from.AddDays(7)
	if d, err := calendar.Parse(r.URL.Query().Get("au")); err == nil && d >= from {
		to = d.AddDays(1)
	}
	if from.DaysUntil(to) > 400 {
		to = from.AddDays(400)
	}
	return from, to
}

func attachment(w http.ResponseWriter, contentType, name string) {
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
}

// export produit un export ; le nom de route indique le contenu.
func (s *Server) export(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	allowLongTransfer(w)
	now := time.Now()
	today := string(calendar.Today())
	from, to := period(r)
	var err error
	switch r.PathValue("name") {
	case "menus.csv":
		attachment(w, "text/csv; charset=utf-8", "menus-"+string(from)+".csv")
		err = s.Exports.MenusCSV(ctx, w, from, to)
	case "menus.pdf":
		attachment(w, "application/pdf", "menus-"+string(from)+".pdf")
		err = s.Exports.MenusPDF(ctx, w, from, to, now)
	case "courses.pdf":
		attachment(w, "application/pdf", "liste-de-courses-"+today+".pdf")
		err = s.Exports.ShoppingPDF(ctx, w, now)
	case "recettes.pdf":
		var ids []int64
		for _, v := range r.URL.Query()["id"] {
			if id := formInt(v, 0); id > 0 {
				ids = append(ids, id)
			}
		}
		if len(ids) == 0 {
			s.redirect(w, r, "/exports#recettes", "Choisissez au moins une recette pour le livre de recettes.")
			return
		}
		attachment(w, "application/pdf", "livre-de-recettes.pdf")
		err = s.Exports.CookbookPDF(ctx, w, ids, now)
	case "inventaire.csv":
		attachment(w, "text/csv; charset=utf-8", "inventaire-"+today+".csv")
		err = s.Exports.InventoryCSV(ctx, w)
	case "achats.csv", "achats-buddyget.csv":
		if r.URL.Query().Get("du") == "" {
			from = calendar.FromTime(calendar.Today().Time().AddDate(-1, 0, 0))
			to = calendar.Today().AddDays(1)
		}
		attachment(w, "text/csv; charset=utf-8", strings.TrimSuffix(r.PathValue("name"), ".csv")+"-"+today+".csv")
		err = s.Exports.PurchasesCSV(ctx, w, from, to.AddDays(-1), r.PathValue("name") == "achats-buddyget.csv")
	default:
		s.notFound(w, r)
		return
	}
	if err != nil {
		s.serverError(w, r, err)
	}
}

func (s *Server) revokeICal(w http.ResponseWriter, r *http.Request) {
	if _, err := s.Exports.RevokeICal(r.Context()); err != nil {
		s.serverError(w, r, err)
		return
	}
	s.redirect(w, r, "/exports#ical", "Nouvelle adresse iCal créée : l'ancienne ne fonctionne plus.")
}

// ical sert le flux iCal du menu, sans session : le jeton secret de
// l'adresse fait office d'autorisation (révocable).
func (s *Server) ical(w http.ResponseWriter, r *http.Request) {
	tok := strings.TrimSuffix(r.PathValue("token"), ".ics")
	ok, err := s.Exports.CheckICal(r.Context(), tok)
	if err != nil {
		http.Error(w, "erreur interne", http.StatusInternalServerError)
		return
	}
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/calendar; charset=utf-8")
	w.Header().Set("Cache-Control", "private, max-age=900")
	if err := s.Exports.ICal(r.Context(), w, calendar.Today(), time.Now()); err != nil {
		http.Error(w, "erreur interne", http.StatusInternalServerError)
	}
}
