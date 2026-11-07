// Package watch fournit au système d'alertes unique (internal/alerts) les
// examens des domaines : dates limites, produits périmés, stock minimum,
// ruptures prévues, menu non planifié, liste de courses prête. Il ne crée
// aucune alerte lui-même : il décrit les situations, le système d'alertes
// les enregistre, les présente et les relaie.
package watch

import (
	"context"
	"fmt"
	"strings"

	"github.com/codecolors/petitchef/internal/alerts"
	"github.com/codecolors/petitchef/internal/menus"
	"github.com/codecolors/petitchef/internal/platform/calendar"
	"github.com/codecolors/petitchef/internal/platform/qty"
	"github.com/codecolors/petitchef/internal/shopping"
	"github.com/codecolors/petitchef/internal/stock"
)

// Register inscrit les examens auprès du système d'alertes.
func Register(a *alerts.Service, st *stock.Service, mn *menus.Service, sh *shopping.Service) {
	a.AddCheck(expiry(st))
	a.AddCheck(minimum(st, sh))
	a.AddCheck(menu(mn))
	a.AddCheck(list(sh))
}

func names(items []stock.Item, n int) string {
	var out []string
	seen := map[string]bool{}
	for _, it := range items {
		if !seen[it.Product] {
			seen[it.Product] = true
			out = append(out, it.Product)
		}
	}
	if len(out) > n {
		return strings.Join(out[:n], ", ") + fmt.Sprintf(" et %d autres", len(out)-n)
	}
	return strings.Join(out, ", ")
}

// expiry signale, en un seul résumé quotidien, les produits qui périment
// dans N jours, et ceux déjà périmés (DLC dépassée).
func expiry(st *stock.Service) alerts.Check {
	return func(ctx context.Context, today calendar.Date, settings map[alerts.Kind]alerts.Setting) ([]alerts.Alert, error) {
		horizon := max(settings[alerts.ExpiringSoon].Param, 1)
		items, err := st.Expiring(ctx, today.AddDays(horizon), 200)
		if err != nil {
			return nil, err
		}
		var soon, past []stock.Item
		for _, it := range items {
			switch {
			case it.BestBefore < today && it.DateKind == "DLC":
				past = append(past, it)
			case it.BestBefore >= today:
				soon = append(soon, it)
			}
		}
		var out []alerts.Alert
		if len(soon) > 0 {
			out = append(out, alerts.Alert{Kind: alerts.ExpiringSoon, Key: "peremption:" + string(today),
				Title:  fmt.Sprintf("%d produit(s) à consommer d'ici %d jour(s)", len(soon), horizon),
				Detail: names(soon, 6) + ".", Link: "/stock/priorite"})
		}
		if len(past) > 0 {
			out = append(out, alerts.Alert{Kind: alerts.Expired, Key: "perime:" + string(today),
				Title:  fmt.Sprintf("%d produit(s) périmé(s) encore en stock", len(past)),
				Detail: names(past, 6) + " : à vérifier, jeter ou consommer.", Link: "/stock/priorite"})
		}
		return out, nil
	}
}

// minimum signale les produits sous leur minimum et ceux dont la rupture
// est prévue avant les courses suivantes.
func minimum(st *stock.Service, sh *shopping.Service) alerts.Check {
	return func(ctx context.Context, today calendar.Date, settings map[alerts.Kind]alerts.Setting) ([]alerts.Alert, error) {
		watched, err := st.Watched(ctx, today)
		if err != nil {
			return nil, err
		}
		sc, err := sh.Schedule(ctx)
		if err != nil {
			return nil, err
		}
		next, _ := sc.Dates(today)
		horizon := today.AddDays(max(settings[alerts.Shortage].Param, 1))
		if next > horizon {
			horizon = next
		}
		week := weekKey(today)
		var out []alerts.Alert
		for _, w := range watched {
			switch {
			case w.Min > 0 && w.Stock < w.Min:
				out = append(out, alerts.Alert{Kind: alerts.BelowMinimum, Key: fmt.Sprintf("minimum:%d:%s", w.ProductID, week),
					Title:  w.Name + " sous le stock minimum",
					Detail: fmt.Sprintf("Reste %s pour un minimum de %s.", qty.Format(w.Stock, w.Unit), qty.Format(w.Min, w.Unit)),
					Link:   fmt.Sprintf("/stock/produit/%d", w.ProductID)})
			case w.DailyUse > 0:
				if d, ok := stock.RunOutDate(w.Stock, 0, w.DailyUse, today); ok && d <= horizon {
					out = append(out, alerts.Alert{Kind: alerts.Shortage, Key: fmt.Sprintf("rupture:%d:%s", w.ProductID, next),
						Title:  w.Name + " : rupture prévue avant les prochaines courses",
						Detail: fmt.Sprintf("Au rythme habituel, il n'y en aura plus vers le %s (courses prévues le %s).", d.Format(), next.Format()),
						Link:   "/courses"})
				}
			}
		}
		return out, nil
	}
}

func weekKey(d calendar.Date) string { return string(menus.Monday(d)) }

// menu signale une semaine suivante sans menu, N jours avant son lundi.
func menu(mn *menus.Service) alerts.Check {
	return func(ctx context.Context, today calendar.Date, settings map[alerts.Kind]alerts.Setting) ([]alerts.Alert, error) {
		nextMonday := menus.Monday(today).AddDays(7)
		if today.DaysUntil(nextMonday) > max(settings[alerts.NoMenu].Param, 1) {
			return nil, nil
		}
		meals, err := mn.Range(ctx, nextMonday, nextMonday.AddDays(7))
		if err != nil {
			return nil, err
		}
		for _, m := range meals {
			if len(m.Entries) > 0 {
				return nil, nil
			}
		}
		return []alerts.Alert{{Kind: alerts.NoMenu, Key: "menu:" + string(nextMonday),
			Title:  "Le menu de la semaine prochaine n'est pas planifié",
			Detail: "Semaine du " + nextMonday.Format() + " : générez-le en trois clics.", Link: "/menus/generer?du=" + string(nextMonday)}}, nil
	}
}

// list signale la liste de courses prête la veille ou le jour des courses.
func list(sh *shopping.Service) alerts.Check {
	return func(ctx context.Context, today calendar.Date, settings map[alerts.Kind]alerts.Setting) ([]alerts.Alert, error) {
		sc, err := sh.Schedule(ctx)
		if err != nil {
			return nil, err
		}
		next, _ := sc.Dates(today)
		if today.DaysUntil(next) > 1 {
			return nil, nil
		}
		l, err := sh.Current(ctx)
		if err != nil {
			return nil, err
		}
		left := 0
		for _, it := range l.Items {
			if !it.Checked {
				left++
			}
		}
		if left < max(settings[alerts.ListReady].Param, 1) {
			return nil, nil
		}
		return []alerts.Alert{{Kind: alerts.ListReady, Key: "liste:" + string(next),
			Title:  fmt.Sprintf("Liste de courses prête : %d article(s)", left),
			Detail: "Courses prévues le " + next.Format() + ".", Link: "/courses/magasin"}}, nil
	}
}
