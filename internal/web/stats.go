package web

import (
	"net/http"

	"github.com/codecolors/petitchef/internal/platform/calendar"
	"github.com/codecolors/petitchef/internal/stats"
)

// statsView est la donnée des statistiques.
type statsView struct {
	Report stats.Report
	Months int
	Charts []barChart
}

func (s *Server) statsPage(w http.ResponseWriter, r *http.Request) {
	months := int(formInt(r.URL.Query().Get("mois"), 6))
	if months < 1 || months > 24 {
		months = 6
	}
	today := calendar.Today()
	from := calendar.FromTime(today.Time().AddDate(0, -months+1, 0))
	from = calendar.Date(string(from)[:8] + "01")
	rep, err := s.Stats.Compute(r.Context(), from, today)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	v := statsView{Report: rep, Months: months, Charts: []barChart{
		newBarChart("depenses", "Dépenses de courses par mois", "Montant des achats rangés en stock, par mois.", rep.SpendByMonth, euroValue),
		newBarChart("gaspillage", "Gaspillage par mois", "Valeur des produits jetés, par mois.", rep.WasteByMonth, euroValue),
		newBarChart("equilibre", "Équilibre moyen des semaines", "Indice moyen des plats de chaque semaine (100 = A, 0 = E).", rep.Balance, gradeValue),
	}}
	s.render(w, r, http.StatusOK, "stats", "Statistiques", v)
}
