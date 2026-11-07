package web

import (
	"html/template"
	"strconv"
	"time"

	"github.com/codecolors/petitchef/internal/catalog"
	"github.com/codecolors/petitchef/internal/platform/calendar"
	"github.com/codecolors/petitchef/internal/platform/money"
	"github.com/codecolors/petitchef/internal/platform/qty"
)

// templateFuncs renvoie les fonctions disponibles dans les gabarits.
func templateFuncs(static assets) template.FuncMap {
	return template.FuncMap{
		"asset":      static.url,
		"euros":      money.Format,
		"navActive":  navActive,
		"plural":     plural,
		"date":       func(d calendar.Date) string { return d.Format() },
		"datetime":   func(t time.Time) string { return t.In(time.Local).Format("02/01/2006 à 15h04") },
		"fileSize":   fileSize,
		"add":        func(a, b int) int { return a + b },
		"seq":        seq,
		"qty":        qty.Format,
		"qtyShort":   qty.FormatShort,
		"qtyInput":   qty.FormatInput,
		"decimal":    qty.FormatDecimal,
		"unitLabel":  qty.UnitLabel,
		"monthNames": func() []string { return catalog.MonthNames[:] },
		"allergens":  func() []catalog.AllergenInfo { return catalog.AllAllergens },
		"traits":     func() []catalog.TraitInfo { return catalog.AllTraits },
		"groups":     func() []catalog.Group { return catalog.Groups },
		"universes":  func() []catalog.Universe { return catalog.Universes },
		"bases":      func() []qty.Base { return qty.Bases },
		"diets":      func() []catalog.DietInfo { return catalog.AllDiets },
		"days":       days,
		"eq64":       func(a, b int64) bool { return a == b },
		"dict":       dict,
		"mul100":     func(v int64) int64 { return v * 100 },
		"div1000x30": func(v int64) int64 { return v * 30 / 1000 },
		"weekday":    weekdayName,
		"dayLabel":   dayLabel,
		"pct": func(a, b int64) int64 {
			if b == 0 {
				return 0
			}
			return a * 100 / b
		},
	}
}

func seq(n int) []int {
	out := make([]int, n)
	for i := range out {
		out[i] = i
	}
	return out
}

func days(n int64) string {
	if n < 0 {
		return ""
	}
	return strconv.FormatInt(n, 10)
}

// dict construit un paramètre de gabarit à partir de paires clé, valeur.
func dict(kv ...any) map[string]any {
	m := make(map[string]any, len(kv)/2)
	for i := 0; i+1 < len(kv); i += 2 {
		k, _ := kv[i].(string)
		m[k] = kv[i+1]
	}
	return m
}

var weekdays = [...]string{"lundi", "mardi", "mercredi", "jeudi", "vendredi", "samedi", "dimanche"}

// weekdayName nomme un jour de la semaine (0 = lundi).
func weekdayName(i int) string { return weekdays[((i%7)+7)%7] }

// dayLabel écrit une date : « lundi 9 novembre ».
func dayLabel(d calendar.Date) string {
	t := d.Time()
	wd := (int(t.Weekday()) + 6) % 7
	return weekdays[wd] + " " + strconv.Itoa(t.Day()) + " " + catalog.MonthNames[t.Month()-1]
}
