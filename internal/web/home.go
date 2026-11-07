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
	Empty    bool
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
	v.Empty = len(meals) == 0 && len(v.Expiring) == 0
	s.render(w, r, http.StatusOK, "home", "Accueil", v)
}

var _ = menus.Monday
