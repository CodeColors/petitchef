package web

import (
	"github.com/codecolors/petitchef/internal/planner"
	"github.com/codecolors/petitchef/internal/platform/money"
	"github.com/codecolors/petitchef/internal/stats"
)

// Les graphiques sont des SVG rendus côté serveur, sans script ni style en
// ligne (CSP) : barres dans un repère de 1000 × 1000 étiré au cadre,
// textes placés en pourcentages ; chaque graphique a son tableau de
// valeurs (alternative accessible).

// barChart est un diagramme en barres.
type barChart struct {
	ID    string
	Title string
	Desc  string
	Bars  []chartBar
	Max   string // valeur maximale affichée
	Rows  []chartRow
	Empty bool
}

type chartBar struct {
	X, Y, W, H float64
	LabelX     float64 // position du libellé, en %
	Label      string
}

type chartRow struct{ Label, Value string }

// newBarChart construit un diagramme ; format écrit une valeur.
func newBarChart(id, title, desc string, points []stats.Point, format func(int64) string) barChart {
	c := barChart{ID: id, Title: title, Desc: desc}
	var top int64
	for _, p := range points {
		top = max(top, p.Value)
	}
	c.Empty = top == 0
	if top == 0 {
		top = 1
	}
	c.Max = format(top)
	n := float64(max(len(points), 1))
	slot := 1000 / n
	for i, p := range points {
		h := float64(p.Value) * 900 / float64(top)
		c.Bars = append(c.Bars, chartBar{X: float64(i)*slot + slot*0.15, W: slot * 0.7, Y: 1000 - h, H: h,
			LabelX: (float64(i) + 0.5) * 100 / n, Label: p.Label})
		c.Rows = append(c.Rows, chartRow{Label: p.Label, Value: format(p.Value)})
	}
	return c
}

func euroValue(v int64) string { return money.Format(v) }

func gradeValue(v int64) string {
	letter := planner.Letter(int(v))
	return letter + " (" + itoa(v) + "/100)"
}

func itoa(v int64) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var b []byte
	for v > 0 {
		b = append([]byte{byte('0' + v%10)}, b...)
		v /= 10
	}
	if neg {
		b = append([]byte{'-'}, b...)
	}
	return string(b)
}
