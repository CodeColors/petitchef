package web

import (
	"net/http"

	"github.com/codecolors/petitchef/internal/alerts"
	"github.com/codecolors/petitchef/internal/menus"
	"github.com/codecolors/petitchef/internal/platform/calendar"
	"github.com/codecolors/petitchef/internal/stock"
)

// homeView est la donnée de l'accueil.
type homeView struct {
	Today    calendar.Date
	Days     []dayView
	Expiring []stock.Item
	Alerts   []alerts.Alert
	ToBuy    int
	Back     string
	Steps    []onboardingStep
	Done     int
}

// onboardingStep est une étape des premiers pas.
type onboardingStep struct {
	Label, Hint, Href string
	Done              bool
}

func (s *Server) home(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	v := homeView{Today: calendar.Today(), Back: "/"}
	meals, err := s.Menus.Range(ctx, v.Today, v.Today.AddDays(2))
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	for i := range 2 {
		d := v.Today.AddDays(i)
		day := dayView{Date: d, Today: i == 0}
		for _, m := range meals {
			if m.Date == d && m.Format != "aucun" {
				day.Meals = append(day.Meals, m)
			}
		}
		v.Days = append(v.Days, day)
	}
	if v.Expiring, err = s.Stock.Expiring(ctx, v.Today.AddDays(3), 6); err != nil {
		s.serverError(w, r, err)
		return
	}
	if v.Alerts, err = s.Alerts.Open(ctx); err != nil {
		s.serverError(w, r, err)
		return
	}
	if len(v.Alerts) > 3 {
		v.Alerts = v.Alerts[:3]
	}
	l, err := s.Shopping.Current(ctx)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	for _, it := range l.Items {
		if !it.Checked {
			v.ToBuy++
		}
	}
	if v.Steps, err = s.onboarding(r); err != nil {
		s.serverError(w, r, err)
		return
	}
	for _, st := range v.Steps {
		if st.Done {
			v.Done++
		}
	}
	s.render(w, r, http.StatusOK, "home", "Accueil", v)
}

// onboarding renvoie les premiers pas et ce qui est déjà fait.
func (s *Server) onboarding(r *http.Request) ([]onboardingStep, error) {
	ctx := r.Context()
	count := func(q string) (bool, error) {
		var n int
		err := s.db.R.QueryRowContext(ctx, q).Scan(&n)
		return n > 0, err
	}
	steps := []struct {
		label, hint, href, query string
	}{
		{"Présenter le foyer", "Qui mange à la maison, allergies et régimes", "/foyer", `SELECT count(*) FROM household_members WHERE NOT archived`},
		{"Ajouter vos recettes", "Saisissez-les, collez une liste d'ingrédients ou importez-les", "/recettes/nouvelle", `SELECT count(*) FROM recipes`},
		{"Faire le tour des placards", "Ce que vous avez déjà, avec les dates limites", "/stock/ajouter", `SELECT count(*) FROM stock_items`},
		{"Générer le menu de la semaine", "En trois clics, selon vos envies", "/menus/generer", `SELECT count(*) FROM menu_entries`},
		{"Préparer la liste de courses", "Triée dans l'ordre de votre magasin", "/courses", `SELECT count(*) FROM shopping_items`},
	}
	out := make([]onboardingStep, len(steps))
	for i, st := range steps {
		done, err := count(st.query)
		if err != nil {
			return nil, err
		}
		out[i] = onboardingStep{Label: st.label, Hint: st.hint, Href: st.href, Done: done}
	}
	return out, nil
}

var _ = menus.Monday
