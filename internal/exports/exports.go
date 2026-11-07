// Package exports est le point d'entrée unique des exports : menus (CSV,
// PDF), liste de courses (texte, PDF), recettes (JSON, schema.org, livre
// de recettes PDF), inventaire (CSV), historique des achats (CSV, au format
// d'import de BuddyGet) et flux iCal du menu. Tout est produit en flux, à
// partir des services des domaines.
package exports

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/codecolors/petitchef/internal/menus"
	"github.com/codecolors/petitchef/internal/platform/calendar"
	"github.com/codecolors/petitchef/internal/platform/db"
	"github.com/codecolors/petitchef/internal/platform/qty"
	"github.com/codecolors/petitchef/internal/recipes"
	"github.com/codecolors/petitchef/internal/shopping"
)

// Service produit les exports.
type Service struct {
	db       *db.DB
	menus    *menus.Service
	recipes  *recipes.Service
	shopping *shopping.Service
}

// NewService crée le service des exports.
func NewService(d *db.DB, m *menus.Service, r *recipes.Service, sh *shopping.Service) *Service {
	return &Service{db: d, menus: m, recipes: r, shopping: sh}
}

// newCSV renvoie un écrivain CSV séparé par des points-virgules (tableurs
// français), avec la marque d'ordre des octets pour qu'Excel lise l'UTF-8.
func newCSV(w io.Writer) *csv.Writer {
	_, _ = io.WriteString(w, "\xef\xbb\xbf")
	c := csv.NewWriter(w)
	c.Comma = ';'
	return c
}

// MenusCSV écrit les menus d'une période.
func (s *Service) MenusCSV(ctx context.Context, w io.Writer, from, to calendar.Date) error {
	meals, err := s.menus.Range(ctx, from, to)
	if err != nil {
		return err
	}
	c := newCSV(w)
	_ = c.Write([]string{"Date", "Repas", "Format", "Rôle", "Plat", "Portions", "Indice d'équilibre", "État"})
	for _, m := range meals {
		for _, e := range m.Entries {
			_ = c.Write([]string{m.Date.Format(), m.SlotName, m.Format.Label(), e.Role.Label(), e.Title, qty.FormatDecimal(e.Portions),
				e.Grade, statusLabel(e)})
		}
	}
	c.Flush()
	return c.Error()
}

func statusLabel(e menus.Entry) string {
	switch {
	case e.Status == menus.Cooked:
		return "cuisiné"
	case e.Status == menus.Postponed:
		return "reporté"
	case e.IsLeftover():
		return "restes"
	}
	return "prévu"
}

var weekdays = [...]string{"Lundi", "Mardi", "Mercredi", "Jeudi", "Vendredi", "Samedi", "Dimanche"}

func dayTitle(d calendar.Date) string {
	return weekdays[(int(d.Time().Weekday())+6)%7] + " " + d.Format()
}

// MenusPDF écrit le menu d'une période sur une page A4 paysage par
// semaine (à afficher sur le réfrigérateur).
func (s *Service) MenusPDF(ctx context.Context, w io.Writer, from, to calendar.Date, now time.Time) error {
	meals, err := s.menus.Range(ctx, from, to)
	if err != nil {
		return err
	}
	doc := newDocument("Menus du "+from.Format()+" au "+to.AddDays(-1).Format(), "", now, true)
	cols := []column{{"Jour", 45, "L"}, {"Repas", 30, "L"}, {"Entrée", 65, "L"}, {"Plat", 72, "L"}, {"Dessert", 55, "L"}}
	var rows [][]string
	for _, m := range meals {
		if m.Format == "aucun" {
			continue
		}
		row := []string{dayTitle(m.Date), m.SlotName, "", "", ""}
		for _, e := range m.Entries {
			switch e.Role {
			case "entree":
				row[2] = e.Title
			case "plat":
				row[3] = e.Title
			case "dessert":
				row[4] = e.Title
			}
		}
		rows = append(rows, row)
	}
	doc.table(cols, rows)
	return doc.write(w)
}

// ShoppingPDF écrit la liste de courses par rayon, avec des cases.
func (s *Service) ShoppingPDF(ctx context.Context, w io.Writer, now time.Time) error {
	l, err := s.shopping.Current(ctx)
	if err != nil {
		return err
	}
	order, err := s.shopping.Order(ctx, l.StoreID)
	if err != nil {
		return err
	}
	shopping.SortItems(l.Items, order)
	sub := ""
	if l.Until != "" {
		sub = "Jusqu'au " + l.Until.Format()
	}
	doc := newDocument("Liste de courses", sub, now, false)
	aisle := ""
	var items []string
	flush := func() {
		if len(items) > 0 {
			doc.heading(aisle)
			doc.bullets(items, true)
			items = nil
		}
	}
	for _, it := range l.Items {
		if it.Checked {
			continue
		}
		if it.Aisle != aisle {
			flush()
			aisle = it.Aisle
		}
		items = append(items, it.Name+" : "+it.BuyLabel())
	}
	flush()
	return doc.write(w)
}

// CookbookPDF écrit un livre de recettes (une recette par page).
func (s *Service) CookbookPDF(ctx context.Context, w io.Writer, ids []int64, now time.Time) error {
	if len(ids) == 0 {
		return errors.New("aucune recette choisie")
	}
	doc := newDocument("Livre de recettes", fmt.Sprintf("%d recette(s)", len(ids)), now, false)
	for i, id := range ids {
		r, err := s.recipes.Get(ctx, id)
		if err != nil {
			return err
		}
		if i > 0 {
			doc.newPage()
		}
		doc.heading(r.Title)
		doc.figures([][2]string{
			{"Types", r.Types.Labels()},
			{"Portions", strconv.Itoa(r.Servings)},
			{"Temps", fmt.Sprintf("%d min (préparation %d, cuisson %d, repos %d)", r.TotalMin(), r.PrepMin, r.CookMin, r.RestMin)},
			{"Indice d'équilibre", r.Grade()},
		})
		doc.heading("Ingrédients")
		var ings []string
		for _, in := range r.Ingredients {
			ings = append(ings, recipes.FormatLine(in))
		}
		doc.bullets(ings, false)
		doc.heading("Préparation")
		for k, st := range r.Steps {
			doc.paragraph(fmt.Sprintf("%d. %s", k+1, st.Text))
		}
		if r.Notes != "" {
			doc.note(r.Notes)
		}
	}
	return doc.write(w)
}

// InventoryCSV écrit l'inventaire (lots en stock).
func (s *Service) InventoryCSV(ctx context.Context, w io.Writer) error {
	type row struct {
		name, aisle, location, unit, best, kind, opened, added, origin string
		q                                                              int64
	}
	rows, err := db.Collect(ctx, s.db.R, func(r db.Scanner) (row, error) {
		var x row
		return x, r.Scan(&x.name, &x.aisle, &x.location, &x.q, &x.unit, &x.best, &x.kind, &x.opened, &x.added, &x.origin)
	}, `SELECT p.name, a.name, coalesce(l.name, ''), s.qty, p.unit, coalesce(s.best_before, ''), s.date_kind, coalesce(s.opened_on, ''),
			s.added_on, s.origin
		FROM stock_items s JOIN products p ON p.id = s.product_id JOIN aisles a ON a.id = p.aisle_id LEFT JOIN locations l ON l.id = s.location_id
		WHERE s.qty > 0 ORDER BY a.position, p.search, s.best_before`)
	if err != nil {
		return err
	}
	c := newCSV(w)
	_ = c.Write([]string{"Produit", "Rayon", "Emplacement", "Quantité", "Unité", "Date limite", "Type de date", "Ouvert le", "Ajouté le", "Origine"})
	for _, x := range rows {
		_ = c.Write([]string{x.name, x.aisle, x.location, qty.FormatInput(x.q, qty.Base(x.unit)), qty.Base(x.unit).Label(),
			calendar.Date(x.best).Format(), x.kind, calendar.Date(x.opened).Format(), calendar.Date(x.added).Format(), x.origin})
	}
	c.Flush()
	return c.Error()
}

// PurchasesCSV écrit l'historique des achats. Avec forBuddyGet, une ligne
// par jour et par magasin au format d'import générique de BuddyGet (Date,
// Libellé, Montant négatif) ; sinon, le détail par produit.
func (s *Service) PurchasesCSV(ctx context.Context, w io.Writer, from, to calendar.Date, forBuddyGet bool) error {
	if forBuddyGet {
		type row struct {
			date, store string
			cents       int64
		}
		rows, err := db.Collect(ctx, s.db.R, func(r db.Scanner) (row, error) {
			var x row
			return x, r.Scan(&x.date, &x.store, &x.cents)
		}, `SELECT p.date, coalesce(st.name, ''), sum(p.price_cents) FROM prices p LEFT JOIN stores st ON st.id = p.store_id
			WHERE p.date BETWEEN ? AND ? GROUP BY p.date, p.store_id ORDER BY p.date`, from, to)
		if err != nil {
			return err
		}
		c := csv.NewWriter(w)
		_ = c.Write([]string{"Date", "Libellé", "Montant"})
		for _, x := range rows {
			label := "Courses"
			if x.store != "" {
				label += " " + x.store
			}
			_ = c.Write([]string{x.date, label, fmt.Sprintf("-%d.%02d", x.cents/100, x.cents%100)})
		}
		c.Flush()
		return c.Error()
	}
	type row struct {
		date, name, store, unit string
		q, cents                int64
	}
	rows, err := db.Collect(ctx, s.db.R, func(r db.Scanner) (row, error) {
		var x row
		return x, r.Scan(&x.date, &x.name, &x.store, &x.q, &x.unit, &x.cents)
	}, `SELECT p.date, pr.name, coalesce(st.name, ''), p.qty, pr.unit, p.price_cents FROM prices p JOIN products pr ON pr.id = p.product_id
		LEFT JOIN stores st ON st.id = p.store_id WHERE p.date BETWEEN ? AND ? ORDER BY p.date, pr.search`, from, to)
	if err != nil {
		return err
	}
	c := newCSV(w)
	_ = c.Write([]string{"Date", "Produit", "Magasin", "Quantité", "Unité", "Prix (EUR)"})
	for _, x := range rows {
		_ = c.Write([]string{calendar.Date(x.date).Format(), x.name, x.store, qty.FormatInput(x.q, qty.Base(x.unit)), qty.Base(x.unit).Label(),
			strings.Replace(fmt.Sprintf("%d.%02d", x.cents/100, x.cents%100), ".", ",", 1)})
	}
	c.Flush()
	return c.Error()
}

// ICalToken renvoie le jeton du flux iCal (créé au besoin).
func (s *Service) ICalToken(ctx context.Context) (string, error) {
	var tok string
	err := s.db.R.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = 'ical_token'`).Scan(&tok)
	if errors.Is(err, sql.ErrNoRows) {
		return s.RevokeICal(ctx)
	}
	return tok, err
}

// RevokeICal remplace le jeton du flux iCal : l'ancienne adresse cesse de
// fonctionner.
func (s *Service) RevokeICal(ctx context.Context) (string, error) {
	tok := rand.Text()
	_, err := s.db.W.ExecContext(ctx, `INSERT INTO settings (key, value) VALUES ('ical_token', ?) ON CONFLICT (key) DO UPDATE SET value = excluded.value`, tok)
	return tok, err
}

// CheckICal indique si un jeton est celui du flux.
func (s *Service) CheckICal(ctx context.Context, tok string) (bool, error) {
	cur, err := s.ICalToken(ctx)
	if err != nil {
		return false, err
	}
	return tok != "" && len(tok) == len(cur) && subtleEq(tok, cur), nil
}

func subtleEq(a, b string) bool {
	var v byte
	for i := 0; i < len(a); i++ {
		v |= a[i] ^ b[i]
	}
	return v == 0
}

// slotTimes donne l'heure indicative d'un créneau (événements iCal).
var slotTimes = map[string][2]string{
	"petit-dejeuner": {"080000", "083000"}, "dejeuner": {"123000", "133000"}, "gouter": {"163000", "170000"}, "diner": {"193000", "203000"},
}

// ICal écrit le flux iCal (lecture seule) des menus des 7 jours passés et
// des 8 semaines à venir : un événement par repas.
func (s *Service) ICal(ctx context.Context, w io.Writer, today calendar.Date, now time.Time) error {
	meals, err := s.menus.Range(ctx, today.AddDays(-7), today.AddDays(56))
	if err != nil {
		return err
	}
	slugs := map[int64]string{}
	slots, err := s.menus.Slots(ctx, false)
	if err != nil {
		return err
	}
	for _, sl := range slots {
		slugs[sl.ID] = sl.Slug
	}
	var b strings.Builder
	line := func(s string) {
		// Repli des lignes à 75 octets (RFC 5545).
		for len(s) > 75 {
			cut := 75
			for cut > 0 && (s[cut]&0xC0) == 0x80 {
				cut--
			}
			b.WriteString(s[:cut] + "\r\n ")
			s = s[cut:]
		}
		b.WriteString(s + "\r\n")
	}
	line("BEGIN:VCALENDAR")
	line("VERSION:2.0")
	line("PRODID:-//PetitChef//Menus//FR")
	line("CALSCALE:GREGORIAN")
	line("X-WR-CALNAME:Menus PetitChef")
	line("X-WR-TIMEZONE:Europe/Paris")
	stamp := now.UTC().Format("20060102T150405Z")
	for _, m := range meals {
		if len(m.Entries) == 0 {
			continue
		}
		var titles, lines []string
		for _, e := range m.Entries {
			titles = append(titles, e.Title)
			lines = append(lines, e.Role.Label()+" : "+e.Title)
		}
		t := slotTimes[slugs[m.SlotID]]
		if t[0] == "" {
			t = [2]string{"120000", "130000"}
		}
		day := strings.ReplaceAll(string(m.Date), "-", "")
		line("BEGIN:VEVENT")
		line(fmt.Sprintf("UID:repas-%d@petitchef", m.ID))
		line("DTSTAMP:" + stamp)
		line("DTSTART;TZID=Europe/Paris:" + day + "T" + t[0])
		line("DTEND;TZID=Europe/Paris:" + day + "T" + t[1])
		line("SUMMARY:" + icalEscape(m.SlotName+" : "+strings.Join(titles, ", ")))
		line("DESCRIPTION:" + icalEscape(strings.Join(lines, "\n")))
		line("END:VEVENT")
	}
	line("END:VCALENDAR")
	_, err = io.WriteString(w, b.String())
	return err
}

var icalReplacer = strings.NewReplacer(`\`, `\\`, ";", `\;`, ",", `\,`, "\n", `\n`)

func icalEscape(s string) string { return icalReplacer.Replace(s) }
