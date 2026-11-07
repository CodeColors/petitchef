package stock

import (
	"testing"

	"github.com/codecolors/petitchef/internal/platform/calendar"
)

func TestPlanFEFO(t *testing.T) {
	lots := []Lot{
		{ID: 1, Qty: 200, BestBefore: "2026-11-20", AddedOn: "2026-11-01"},
		{ID: 2, Qty: 100, BestBefore: "2026-11-10", AddedOn: "2026-11-02"},
		{ID: 3, Qty: 500, AddedOn: "2026-10-01"},                           // sans date : en dernier
		{ID: 4, Qty: 300, BestBefore: "2026-11-05", AddedOn: "2026-10-20"}, // périmé le 8
	}
	takes, short := PlanFEFO(lots, 350, "2026-11-08")
	want := []Take{{2, 100}, {1, 200}, {3, 50}}
	if short != 0 || len(takes) != len(want) {
		t.Fatalf("prélèvements : %v, manque %d", takes, short)
	}
	for i := range want {
		if takes[i] != want[i] {
			t.Errorf("prélèvement %d : %v, attendu %v", i, takes[i], want[i])
		}
	}
	if _, short := PlanFEFO(lots, 1000, "2026-11-08"); short != 200 {
		t.Errorf("manque : %d, attendu 200 (le lot périmé ne compte pas)", short)
	}
	// Le 4 novembre, le lot 4 est encore bon et passe en premier.
	if takes, _ := PlanFEFO(lots, 10, "2026-11-04"); takes[0].LotID != 4 {
		t.Errorf("FEFO : %v", takes)
	}
	Consume(lots, []Take{{2, 100}})
	if lots[1].Qty != 0 {
		t.Error("Consume")
	}
}

func TestEstimateDailyUse(t *testing.T) {
	today := calendar.Date("2026-11-07")
	// Une bouteille de 500 ml de liquide vaisselle toutes les 5 semaines.
	events := []Event{{"2026-07-25", 500}, {"2026-08-29", 500}, {"2026-10-03", 500}, {"2026-11-07", 500}}
	rate, ok := EstimateDailyUse(events, today)
	if !ok || rate != 2000*1000/105 {
		t.Fatalf("consommation : %d, %v", rate, ok)
	}
	if d, ok := RunOutDate(500, 0, rate, today); !ok || d != "2026-12-03" {
		t.Errorf("rupture prévue : %s", d)
	}
	if _, ok := EstimateDailyUse(events[3:], today); ok {
		t.Error("une seule consommation ne suffit pas")
	}
	if _, ok := EstimateDailyUse([]Event{{"2026-11-01", 5}, {"2026-11-06", 5}}, today); ok {
		t.Error("moins de 14 jours d'observation ne suffisent pas")
	}
	if got := Projected(1000, rate, 14); got != 1000-rate*14/1000 {
		t.Errorf("projection : %d", got)
	}
}
