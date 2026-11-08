package exports

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/codecolors/petitchef/internal/catalog"
	"github.com/codecolors/petitchef/internal/household"
	"github.com/codecolors/petitchef/internal/menus"
	"github.com/codecolors/petitchef/internal/platform/calendar"
	"github.com/codecolors/petitchef/internal/platform/db"
	"github.com/codecolors/petitchef/internal/recipes"
	"github.com/codecolors/petitchef/internal/recipes/recipetest"
	"github.com/codecolors/petitchef/internal/shopping"
	"github.com/codecolors/petitchef/internal/stock"
)

func TestExports(t *testing.T) {
	ctx := t.Context()
	d, err := db.Open(ctx, filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = d.Close() }()
	if err := catalog.SyncSeed(ctx, d); err != nil {
		t.Fatal(err)
	}
	cat := catalog.NewService(d)
	rec := recipes.NewService(d, cat)
	if err := recipetest.Load(ctx, rec); err != nil {
		t.Fatal(err)
	}
	st := stock.NewService(d, cat)
	mn := menus.NewService(d, cat, rec, household.NewService(d), st)
	sh := shopping.NewService(d, cat, rec, mn, st)
	s := NewService(d, mn, rec, sh)
	today := calendar.Today()
	pref := menus.DefaultPreferences()
	pref.Seed = 5
	if _, err := mn.Generate(ctx, menus.Request{From: today, Weeks: 1, Preferences: pref}); err != nil {
		t.Fatal(err)
	}
	milk, _ := cat.Resolve(ctx, "lait")
	if _, _, err := st.Add(ctx, stock.AddInput{ProductID: milk, Qty: 6000, PriceCents: 594, Origin: stock.FromShopping, Date: today}); err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	if err := s.MenusCSV(ctx, &b, today, today.AddDays(7)); err != nil || !strings.HasPrefix(b.String(), "\xef\xbb\xbfDate;Repas;Format") || strings.Count(b.String(), "\n") < 8 {
		t.Errorf("menus CSV : %v\n%s", err, b.String())
	}
	b.Reset()
	if err := s.InventoryCSV(ctx, &b); err != nil || !strings.Contains(b.String(), "Lait demi-écrémé;Crèmerie;Placard;6000;millilitres") {
		t.Errorf("inventaire CSV : %v\n%s", err, b.String())
	}
	b.Reset()
	if err := s.PurchasesCSV(ctx, &b, today, today, true); err != nil || !strings.Contains(b.String(), string(today)+",Courses,-5.94") {
		t.Errorf("achats BuddyGet : %v\n%s", err, b.String())
	}
	for name, fn := range map[string]func() error{
		"menus":   func() error { return s.MenusPDF(ctx, &b, today, today.AddDays(7), time.Now()) },
		"courses": func() error { return s.ShoppingPDF(ctx, &b, time.Now()) },
		"livre":   func() error { return s.CookbookPDF(ctx, &b, []int64{1, 2, 3}, time.Now()) },
	} {
		b.Reset()
		if err := fn(); err != nil || !bytes.HasPrefix(b.Bytes(), []byte("%PDF")) {
			t.Errorf("PDF %s : %v", name, err)
		}
	}
	tok, _ := s.ICalToken(ctx)
	if ok, _ := s.CheckICal(ctx, tok); !ok {
		t.Error("jeton iCal refusé")
	}
	if _, err := s.RevokeICal(ctx); err != nil {
		t.Fatal(err)
	}
	if ok, _ := s.CheckICal(ctx, tok); ok {
		t.Error("ancien jeton iCal encore accepté")
	}
	b.Reset()
	if err := s.ICal(ctx, &b, today, time.Now()); err != nil {
		t.Fatal(err)
	}
	ics := b.String()
	if !strings.HasPrefix(ics, "BEGIN:VCALENDAR\r\n") || !strings.Contains(ics, "BEGIN:VEVENT") || !strings.HasSuffix(ics, "END:VCALENDAR\r\n") {
		t.Errorf("iCal :\n%s", ics)
	}
	for line := range strings.SplitSeq(ics, "\r\n") {
		if len(line) > 75 {
			t.Errorf("ligne iCal de %d octets", len(line))
		}
	}
}
