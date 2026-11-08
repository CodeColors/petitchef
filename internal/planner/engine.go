package planner

import (
	"cmp"
	"fmt"
	"math/rand/v2"
	"slices"
	"strconv"
	"unicode"

	"github.com/codecolors/petitchef/internal/catalog"
	"github.com/codecolors/petitchef/internal/platform/calendar"
	"github.com/codecolors/petitchef/internal/stock"
)

// cell désigne un plat à remplir : un créneau et un rôle.
type cell struct {
	slot int
	role Role
}

type engine struct {
	in        Input
	cands     []Candidate
	byID      map[int64]int
	virtual   map[int64][]stock.Lot // stock restant au fil du menu
	plan      []Placement
	at        map[cell]int // cellule → index dans plan
	static    map[[2]int]staticScore
	usedReady map[int64]bool
	median    int64
	// Dates converties une fois pour toutes en numéros de jour (les
	// calculs de dates sur chaînes dominaient le temps de génération).
	slotDay    []int
	slotMonth  []int
	slotWeek   []int
	slotSoon   []calendar.Date // date du repas + 3 jours
	lastCooked []int           // par candidat, -1 : jamais
	history    []int           // jour de chaque plat de l'historique
	reasons    bool            // construire les raisons (affichage)
}

// mondayRef est un lundi (3 janvier 2000), origine des numéros de semaine.
var mondayRef = dayNum("2000-01-03")

// dayNum convertit une date AAAA-MM-JJ en numéro de jour (algorithme des
// jours civils), sans passer par time.Parse.
func dayNum(d calendar.Date) int {
	if len(d) != 10 {
		return 0
	}
	n := func(s string) int {
		v := 0
		for i := 0; i < len(s); i++ {
			v = v*10 + int(s[i]-'0')
		}
		return v
	}
	y, m, dd := n(string(d[0:4])), n(string(d[5:7])), n(string(d[8:10]))
	if m <= 2 {
		y--
	}
	era := y / 400
	yoe := y - era*400
	mp := (m + 9) % 12
	doy := (153*mp+2)/5 + dd - 1
	doe := yoe*365 + yoe/4 - yoe/100 + doy
	return era*146097 + doe
}

type staticScore struct {
	value   int
	reasons []string
}

// gradeValue convertit une lettre en points (A = 100 … E = 0).
func gradeValue(g string) int {
	if len(g) != 1 || g[0] < 'A' || g[0] > 'E' {
		return 50
	}
	return 100 - int(g[0]-'A')*25
}

func newEngine(in Input) *engine {
	// Menus sans poisson : contrainte stricte ajoutée à chaque créneau.
	if in.Rules.NoFish {
		in.Rules.FishMin = 0
		slots := slices.Clone(in.Slots)
		for i := range slots {
			slots[i].Diets |= catalog.FishFree
		}
		in.Slots = slots
	}
	e := &engine{in: in, byID: map[int64]int{}, at: map[cell]int{}, static: map[[2]int]staticScore{}, usedReady: map[int64]bool{}}
	e.cands = slices.Clone(in.Candidates)
	slices.SortFunc(e.cands, func(a, b Candidate) int { return cmp.Compare(a.ID, b.ID) })
	for i, c := range e.cands {
		e.byID[c.ID] = i
	}
	e.virtual = make(map[int64][]stock.Lot, len(in.Stock))
	for id, lots := range in.Stock {
		e.virtual[id] = slices.Clone(lots)
	}
	for _, sl := range in.Slots {
		day := dayNum(sl.Date)
		e.slotDay = append(e.slotDay, day)
		e.slotMonth = append(e.slotMonth, int(sl.Date[5]-'0')*10+int(sl.Date[6]-'0'))
		e.slotWeek = append(e.slotWeek, (day-mondayRef+7*1000)/7) // numéro de semaine (lundi à dimanche)
		e.slotSoon = append(e.slotSoon, sl.Date.AddDays(3))
	}
	e.lastCooked = make([]int, len(e.cands))
	for i, c := range e.cands {
		e.lastCooked[i] = -1
		if c.LastCooked != "" {
			e.lastCooked[i] = dayNum(c.LastCooked)
		}
	}
	for _, h := range in.History {
		e.history = append(e.history, dayNum(h.Date))
	}
	var costs []int64
	for _, c := range e.cands {
		if c.CostPerPortion > 0 {
			costs = append(costs, c.CostPerPortion)
		}
	}
	if len(costs) > 0 {
		slices.Sort(costs)
		e.median = costs[len(costs)/2]
	}
	return e
}

// eligible applique les contraintes strictes (jamais violées) : type,
// temps maximum, allergies, régimes, aliments refusés, recettes « à ne
// plus proposer ».
func eligible(c *Candidate, s *Slot, role Role) bool {
	if !c.Types.Has(role.Type()) {
		return false
	}
	if s.MaxMin > 0 && c.ActiveMin > s.MaxMin {
		return false
	}
	if c.Allergens&s.Allergens != 0 || !catalog.Compatible(s.Diets, c.Traits, c.Allergens) {
		return false
	}
	for _, u := range c.Uses {
		if s.Refused[u.ProductID] {
			return false
		}
	}
	for _, m := range s.Members {
		if c.BannedBy[m] {
			return false
		}
	}
	return true
}

// repeated indique que la recette a été servie (historique ou menu) moins
// de NoRepeatDays jours avant ou après la date.
func (e *engine) repeated(id int64, slotIdx int, except int) bool {
	n := e.in.Rules.NoRepeatDays
	if n <= 0 {
		return false
	}
	d := e.slotDay[slotIdx]
	for i, p := range e.in.History {
		if p.RecipeID == id && abs(e.history[i]-d) < n {
			return true
		}
	}
	for i, p := range e.plan {
		if i == except || p.RecipeID != id || p.LeftoverOf > 0 {
			continue
		}
		if abs(e.slotDay[p.Slot]-d) < n {
			return true
		}
	}
	return false
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// coverage évalue la part (en poids) des ingrédients disponibles dans un
// stock à la date d, et les produits utilisés qui périment bientôt.
func coverage(c *Candidate, portions int64, d, soon calendar.Date, lots map[int64][]stock.Lot, names ProductNames, withReasons bool) (pct int, urgent []string, missing int) {
	var total, covered int64
	for _, u := range c.Uses {
		if u.Staple {
			continue
		}
		need := u.PerPortion * portions / 1_000_000
		w := max(u.Grams*portions/1000, 1)
		total += w
		var avail int64
		soonest := calendar.Date("")
		for _, l := range lots[u.ProductID] {
			if l.Qty > 0 && l.Usable(d) {
				avail += l.Qty
				if l.BestBefore != "" && (soonest == "" || l.BestBefore < soonest) {
					soonest = l.BestBefore
				}
			}
		}
		if avail >= need || need == 0 {
			covered += w
		} else {
			covered += w * avail / need
			missing++
		}
		if avail > 0 && soonest != "" && soonest <= soon {
			if name := names[u.ProductID]; name != "" && withReasons {
				urgent = append(urgent, fmt.Sprintf("utilise %s qui périme %s", lowerFirst(name), dayWord(d, soonest)))
			} else {
				urgent = append(urgent, "")
			}
		}
	}
	if total == 0 {
		return 0, nil, 0
	}
	return int(covered * 100 / total), urgent, missing
}

func lowerFirst(s string) string {
	r := []rune(s)
	// Un sigle (« WC ») garde sa majuscule.
	if len(r) > 1 && unicode.IsUpper(r[0]) && !unicode.IsUpper(r[1]) {
		r[0] = unicode.ToLower(r[0])
	}
	return string(r)
}

var dayNames = [...]string{"lundi", "mardi", "mercredi", "jeudi", "vendredi", "samedi", "dimanche"}

// dayWord situe une date limite par rapport au repas : « aujourd'hui »,
// « demain », « jeudi ».
func dayWord(meal, d calendar.Date) string {
	switch meal.DaysUntil(d) {
	case 0:
		return "le jour même"
	case 1:
		return "le lendemain"
	}
	wd := (int(d.Time().Weekday()) + 6) % 7
	return dayNames[wd]
}

// staticScore note une recette pour une cellule, hors contexte du menu
// (stock initial, équilibre, saison, avis, coût, rapidité), avec ses
// raisons ; mis en cache.
func (e *engine) staticScore(ci int, s *Slot, slotIdx int, lots map[int64][]stock.Lot, cache bool) staticScore {
	key := [2]int{ci, slotIdx}
	if cache {
		if v, ok := e.static[key]; ok {
			return v
		}
	}
	c := &e.cands[ci]
	w := e.in.Weights
	var reasons []string
	cov, urgent, _ := coverage(c, s.Portions, s.Date, e.slotSoon[slotIdx], lots, e.in.Names, e.reasons)
	stockComp := cov * 7 / 10
	if len(urgent) > 0 {
		stockComp += 30
		if urgent[0] != "" {
			reasons = append(reasons, urgent[0])
		}
	} else if cov >= 60 && e.reasons {
		reasons = append(reasons, fmt.Sprintf("%d %% déjà en stock", cov))
	}
	balance := gradeValue(c.Grade)
	if c.Grade == "A" {
		reasons = append(reasons, "équilibré (A)")
	}
	season := 50
	month := e.slotMonth[slotIdx]
	if c.Season != catalog.AllYear && c.Season != 0 {
		if c.Season.Has(month) {
			season = 100
			reasons = append(reasons, "de saison")
		} else {
			season = 0
		}
	}
	rating := 50
	if c.Stars > 0 {
		rating = c.Stars * 2
		if c.Stars >= 40 {
			if e.reasons {
				reasons = append(reasons, "noté "+strconv.FormatFloat(float64(c.Stars)/10, 'f', -1, 64)+"/5")
			}
		}
	}
	if c.Favorite {
		rating = min(rating+30, 100)
		reasons = append(reasons, "favori")
	}
	cost := 50
	if c.CostPerPortion > 0 && e.median > 0 {
		cost = int(100 * e.median / (c.CostPerPortion + e.median))
		if c.CostPerPortion*10 <= e.median*6 {
			reasons = append(reasons, "économique")
		}
	}
	quick := max(0, 100-c.ActiveMin*100/90)
	if c.ActiveMin <= 30 && e.reasons {
		reasons = append(reasons, strconv.Itoa(c.ActiveMin)+" min")
	}
	sum := w.Stock + w.Balance + w.Season + w.Rating + w.Cost + w.Quick + w.Variety
	if sum == 0 {
		sum = 1
	}
	v := (w.Stock*stockComp + w.Balance*balance + w.Season*season + w.Rating*rating + w.Cost*cost + w.Quick*quick) * 100 / (sum * 100)
	out := staticScore{value: v, reasons: reasons}
	if cache {
		e.static[key] = out
	}
	return out
}

// noise départage les égalités de façon déterministe (graine).
func (e *engine) noise(ci, slot int, role Role) int {
	x := e.in.Seed ^ uint64(e.cands[ci].ID)*0x9E3779B97F4A7C15 ^ uint64(slot)*0xBF58476D1CE4E5B9 ^ uint64(len(role))*0x94D049BB133111EB //nolint:gosec // hachage : le débordement est voulu
	x ^= x >> 31
	x *= 0xD6E8FEB86659FD93
	x ^= x >> 32
	return int(x % 4)
}

// mainAt renvoie le plat principal placé dans un créneau (-1 : aucun).
func (e *engine) mainAt(slot int) int {
	if i, ok := e.at[cell{slot, Main}]; ok && e.plan[i].RecipeID > 0 {
		return e.byID[e.plan[i].RecipeID]
	}
	return -1
}

// context note la variété, la cohérence du repas et les règles de la
// semaine d'une recette placée dans le menu courant (variety : 0 à 100 ;
// adjust : bonus ou malus en points), avec ses raisons.
func (e *engine) context(ci int, slotIdx int, role Role, self int) (variety, adjust int, reasons []string) {
	c := &e.cands[ci]
	variety = 100
	if role == Main {
		// Plats principaux voisins (créneau précédent et suivant, même jour).
		for _, other := range []int{slotIdx - 1, slotIdx + 1} {
			if other < 0 || other >= len(e.in.Slots) {
				continue
			}
			oi := e.mainAt(other)
			if oi < 0 || (self >= 0 && e.at[cell{other, Main}] == self) {
				continue
			}
			o := &e.cands[oi]
			if c.Protein != "" && o.Protein == c.Protein {
				variety -= 35
			}
			if c.StarchID != 0 && o.StarchID == c.StarchID {
				variety -= 25
			}
		}
		for hi, p := range e.in.History {
			if delta := e.history[hi] - e.slotDay[slotIdx]; delta >= -1 && delta < 0 {
				if c.Protein != "" && p.Protein == c.Protein {
					variety -= 20
				}
				if c.StarchID != 0 && p.StarchID == c.StarchID {
					variety -= 15
				}
			}
		}
	}
	if lc := e.lastCooked[ci]; lc >= 0 {
		since := e.slotDay[slotIdx] - lc
		switch {
		case since < 14:
			variety -= 30
		case since >= 42 && e.reasons:
			reasons = append(reasons, fmt.Sprintf("pas cuisiné depuis %d semaines", since/7))
		}
	}
	variety = max(variety, 0)
	// Cohérence du repas : entrée et dessert plus légers si le plat est copieux.
	if role != Main {
		if mi := e.mainAt(slotIdx); mi >= 0 && e.cands[mi].Heavy {
			if c.Heavy {
				adjust -= 25
			} else {
				adjust += 8
				reasons = append(reasons, "léger, le plat est copieux")
			}
		}
		if role == Starter && c.Protein != "" {
			if mi := e.mainAt(slotIdx); mi >= 0 && e.cands[mi].Protein == c.Protein {
				adjust -= 15
			}
		}
	}
	// Légumes à chaque repas.
	if e.in.Rules.VegEveryMeal && c.VegShare < 15 {
		hasVeg := false
		for _, r := range Roles {
			if i, ok := e.at[cell{slotIdx, r}]; ok && i != self && e.plan[i].RecipeID > 0 && e.cands[e.byID[e.plan[i].RecipeID]].VegShare >= 15 {
				hasVeg = true
			}
		}
		if !hasVeg {
			adjust -= 12
		}
	}
	return variety, adjust, reasons
}

// weekKey regroupe les dates par semaine (lundi).
func weekKey(d calendar.Date) calendar.Date {
	wd := (int(d.Time().Weekday()) + 6) % 7
	return d.AddDays(-wd)
}

// weekCounts compte, pour la semaine d'un créneau, les plats principaux
// de poisson, de viande rouge et végétariens du menu (hors self).
func (e *engine) weekCounts(slotIdx, self int) (fish, red, veg int) {
	wk := e.slotWeek[slotIdx]
	for i, p := range e.plan {
		if i == self || p.Role != Main || p.RecipeID == 0 || e.slotWeek[p.Slot] != wk {
			continue
		}
		c := &e.cands[e.byID[p.RecipeID]]
		if c.Fish {
			fish++
		}
		if c.RedMeat {
			red++
		}
		if c.Vegetarian {
			veg++
		}
	}
	return
}

// rules ajuste le score d'un plat principal selon les règles de la
// semaine (poisson au moins N fois, viande rouge au plus N fois, au moins
// un repas végétarien).
func (e *engine) rules(ci, slotIdx int, self int) (int, []string) {
	c := &e.cands[ci]
	r := e.in.Rules
	fish, red, veg := e.weekCounts(slotIdx, self)
	adj := 0
	var reasons []string
	if c.Fish && fish < r.FishMin {
		adj += 18
		reasons = append(reasons, "poisson de la semaine")
	}
	if c.RedMeat && r.RedMeatMax >= 0 && red >= r.RedMeatMax {
		adj -= 45
	}
	if c.Vegetarian && veg < r.VegetarianMin {
		adj += 12
		reasons = append(reasons, "repas végétarien de la semaine")
	}
	return adj * max(e.in.Weights.Balance, 3) / 7, reasons
}

// total note une recette dans une cellule, menu courant compris.
func (e *engine) total(ci, slotIdx int, role Role, lots map[int64][]stock.Lot, cache bool, self int) (int, []string) {
	return e.score(ci, slotIdx, role, lots, cache, self, true)
}

// score note une recette ; withRules ajoute les règles de la semaine
// (l'objectif global les compte à part).
func (e *engine) score(ci, slotIdx int, role Role, lots map[int64][]stock.Lot, cache bool, self int, withRules bool) (int, []string) {
	s := &e.in.Slots[slotIdx]
	st := e.staticScore(ci, s, slotIdx, lots, cache)
	variety, adjust, ctxReasons := e.context(ci, slotIdx, role, self)
	w := e.in.Weights
	sum := w.Stock + w.Balance + w.Season + w.Rating + w.Cost + w.Quick + w.Variety
	if sum == 0 {
		sum = 1
	}
	v := st.value + w.Variety*variety/sum + adjust
	reasons := append(slices.Clone(st.reasons), ctxReasons...)
	if role == Main && withRules {
		adj, rr := e.rules(ci, slotIdx, self)
		v += adj
		reasons = append(reasons, rr...)
	}
	return v, reasons
}

// consume retire du stock virtuel les ingrédients d'une recette servie.
func (e *engine) consume(ci int, s *Slot) {
	for _, u := range e.cands[ci].Uses {
		need := u.PerPortion * s.Portions / 1_000_000
		takes, _ := stock.PlanFEFO(e.virtual[u.ProductID], need, s.Date)
		stock.Consume(e.virtual[u.ProductID], takes)
	}
}

func (e *engine) put(p Placement) int {
	e.plan = append(e.plan, p)
	i := len(e.plan) - 1
	e.at[cell{p.Slot, p.Role}] = i
	return i
}

// candidatesFor renvoie les recettes admissibles d'une cellule ; si la
// règle de non-répétition les exclut toutes, elle est levée.
func (e *engine) candidatesFor(slotIdx int, role Role, self int) []int {
	s := &e.in.Slots[slotIdx]
	var out, relaxed []int
	for i := range e.cands {
		c := &e.cands[i]
		if !eligible(c, s, role) {
			continue
		}
		relaxed = append(relaxed, i)
		if !e.repeated(c.ID, slotIdx, self) {
			out = append(out, i)
		}
	}
	if len(out) == 0 {
		return relaxed
	}
	return out
}

// Generate construit un menu : affectation gloutonne des cellules dans
// l'ordre chronologique (plat d'abord), puis amélioration locale bornée.
func Generate(in Input) ([]Placement, Summary) {
	e := newEngine(in)
	e.greedy()
	e.improve()
	e.explain()
	return e.plan, e.summary()
}

func (e *engine) greedy() {
	for si := range e.in.Slots {
		s := &e.in.Slots[si]
		for _, role := range s.Format.Roles() {
			if _, done := e.at[cell{si, role}]; done {
				continue
			}
			if f, ok := s.Fixed[role]; ok {
				p := Placement{Slot: si, Role: role, RecipeID: f.RecipeID, Fixed: true}
				if f.RecipeID > 0 {
					if ci, ok := e.byID[f.RecipeID]; ok && !f.Leftover {
						e.consume(ci, s)
					}
				}
				e.put(p)
				continue
			}
			best, bestScore := -1, 0
			var bestReasons []string
			for _, ci := range e.candidatesFor(si, role, -1) {
				v, reasons := e.total(ci, si, role, e.virtual, false, -1)
				v += e.noise(ci, si, role)
				if best < 0 || v > bestScore {
					best, bestScore, bestReasons = ci, v, reasons
				}
			}
			if role == Main {
				if r, v := e.bestReady(si, bestScore); r >= 0 {
					rd := e.in.Ready[r]
					e.usedReady[rd.StockItemID] = true
					e.put(Placement{Slot: si, Role: role, RecipeID: rd.RecipeID, ReadyItem: rd.StockItemID, Score: v,
						Reasons: []string{"plat prêt en stock"}})
					continue
				}
			}
			if best < 0 {
				continue // aucune recette possible : la case reste vide
			}
			e.consume(best, s)
			idx := e.put(Placement{Slot: si, Role: role, RecipeID: e.cands[best].ID, Score: bestScore, Reasons: bestReasons})
			if role == Main && e.in.Leftovers && s.Dinner && e.cands[best].BatchFriendly {
				e.placeLeftover(si, idx, best)
			}
		}
	}
}

// bestReady choisit un plat prêt du stock pour un plat principal s'il
// convient aux convives et bat la meilleure recette ; -1 sinon.
func (e *engine) bestReady(si, bestScore int) (int, int) {
	s := &e.in.Slots[si]
	found, foundScore := -1, bestScore
	for i, r := range e.in.Ready {
		if e.usedReady[r.StockItemID] || r.Portions*4 < s.Portions*3 || (r.BestBefore != "" && r.BestBefore < s.Date) {
			continue
		}
		ci, ok := e.byID[r.RecipeID]
		if !ok || !eligible(&e.cands[ci], s, Main) {
			continue
		}
		v, _ := e.total(ci, si, Main, e.virtual, false, -1)
		v += 25 // déjà prêt : anti-gaspillage et temps gagné
		if v > foundScore {
			found, foundScore = i, v
		}
	}
	return found, foundScore
}

// placeLeftover sert les restes d'un dîner le lendemain midi.
func (e *engine) placeLeftover(si, idx, ci int) {
	s := &e.in.Slots[si]
	for ti := si + 1; ti < len(e.in.Slots); ti++ {
		t := &e.in.Slots[ti]
		if t.Date != s.Date.AddDays(1) || !t.Lunch {
			continue
		}
		if _, done := e.at[cell{ti, Main}]; done {
			return
		}
		if _, fixed := t.Fixed[Main]; fixed || !slices.Contains(t.Format.Roles(), Main) || !eligible(&e.cands[ci], t, Main) {
			return
		}
		e.plan[idx].CookDouble = true
		e.put(Placement{Slot: ti, Role: Main, RecipeID: e.cands[ci].ID, LeftoverOf: idx + 1,
			Reasons: []string{"restes du dîner de " + dayNames[(int(s.Date.Time().Weekday())+6)%7]}})
		return
	}
}

// objective est la note globale du menu (stock initial, variété, règles
// de la semaine), qui guide l'amélioration locale.
func (e *engine) objective() int {
	sum := 0
	for i, p := range e.plan {
		if p.RecipeID == 0 || p.Fixed || p.LeftoverOf > 0 || p.ReadyItem > 0 {
			continue
		}
		v, _ := e.score(e.byID[p.RecipeID], p.Slot, p.Role, e.in.Stock, true, i, false)
		sum += v
	}
	// Pénalités des règles non tenues, semaine par semaine.
	weeks := map[int][3]int{}
	for _, p := range e.plan {
		if p.Role != Main || p.RecipeID == 0 {
			continue
		}
		wk := e.slotWeek[p.Slot]
		c := &e.cands[e.byID[p.RecipeID]]
		x := weeks[wk]
		if c.Fish {
			x[0]++
		}
		if c.RedMeat {
			x[1]++
		}
		if c.Vegetarian {
			x[2]++
		}
		weeks[wk] = x
	}
	r := e.in.Rules
	for _, x := range weeks {
		sum -= max(0, r.FishMin-x[0])*30 + max(0, x[1]-r.RedMeatMax)*45 + max(0, r.VegetarianMin-x[2])*20
	}
	return sum
}

// improve essaie, un nombre borné de fois, de remplacer un plat par une
// autre recette admissible quand la note globale y gagne.
func (e *engine) improve() {
	var movable []int
	for i, p := range e.plan {
		if !p.Fixed && p.LeftoverOf == 0 && !p.CookDouble && p.ReadyItem == 0 && p.RecipeID > 0 {
			movable = append(movable, i)
		}
	}
	if len(movable) == 0 {
		return
	}
	rng := rand.New(rand.NewPCG(e.in.Seed, e.in.Seed^0x5DEECE66D)) //nolint:gosec // déterminisme voulu (même graine, même menu), pas de sécurité en jeu
	iterations := min(3*len(movable), 240)
	current := e.objective()
	for range iterations {
		i := movable[rng.IntN(len(movable))]
		p := e.plan[i]
		cands := e.candidatesFor(p.Slot, p.Role, i)
		// Les meilleures alternatives selon la note statique, pour borner l'effort.
		slices.SortFunc(cands, func(a, b int) int {
			sa := e.staticScore(a, &e.in.Slots[p.Slot], p.Slot, e.in.Stock, true).value
			sb := e.staticScore(b, &e.in.Slots[p.Slot], p.Slot, e.in.Stock, true).value
			if sa != sb {
				return sb - sa
			}
			return cmp.Compare(e.cands[a].ID, e.cands[b].ID)
		})
		orig := p.RecipeID
		best, bestObj := orig, current
		for _, ci := range cands[:min(len(cands), 8)] {
			id := e.cands[ci].ID
			if id == orig {
				continue
			}
			e.plan[i].RecipeID = id
			if v := e.objective(); v > bestObj {
				best, bestObj = id, v
			}
		}
		e.plan[i].RecipeID = best
		current = bestObj
	}
}

// explain recalcule les raisons de chaque plat dans le menu final.
func (e *engine) explain() {
	e.reasons = true
	e.virtual = make(map[int64][]stock.Lot, len(e.in.Stock))
	for id, lots := range e.in.Stock {
		e.virtual[id] = slices.Clone(lots)
	}
	order := make([]int, len(e.plan))
	for i := range order {
		order[i] = i
	}
	slices.SortStableFunc(order, func(a, b int) int { return cmp.Compare(e.plan[a].Slot, e.plan[b].Slot) })
	for _, i := range order {
		p := &e.plan[i]
		if p.RecipeID == 0 || p.Fixed {
			continue
		}
		ci, ok := e.byID[p.RecipeID]
		if !ok {
			continue
		}
		s := &e.in.Slots[p.Slot]
		if p.LeftoverOf > 0 || p.ReadyItem > 0 {
			continue
		}
		v, reasons := e.total(ci, p.Slot, p.Role, e.virtual, false, i)
		p.Score, p.Reasons = v, dedupe(reasons, 4)
		if p.CookDouble {
			p.Reasons = append(p.Reasons, "cuisiner pour deux repas")
		}
		e.consume(ci, s)
	}
}

func dedupe(list []string, n int) []string {
	var out []string
	for _, r := range list {
		if !slices.Contains(out, r) {
			out = append(out, r)
		}
		if len(out) == n {
			break
		}
	}
	return out
}

// summary dresse le bilan du menu.
func (e *engine) summary() Summary {
	sm := Summary{Proteins: map[string]int{}, CostKnown: true}
	lots := make(map[int64][]stock.Lot, len(e.in.Stock))
	for id, l := range e.in.Stock {
		lots[id] = slices.Clone(l)
	}
	var gradeSum, graded int
	var weight, covered int64
	for _, p := range e.plan {
		if p.RecipeID == 0 {
			continue
		}
		ci, ok := e.byID[p.RecipeID]
		if !ok {
			continue
		}
		c := &e.cands[ci]
		s := &e.in.Slots[p.Slot]
		sm.Dishes++
		gradeSum += gradeValue(c.Grade)
		graded++
		if p.Role == Main {
			switch {
			case c.Vegetarian:
				sm.Proteins["Végétarien"]++
				sm.Vegetarian++
			case c.Protein != "":
				sm.Proteins[c.Protein.Label()]++
			default:
				sm.Proteins["Autre"]++
			}
		}
		if p.LeftoverOf > 0 || p.ReadyItem > 0 {
			continue
		}
		if c.CostPerPortion >= 0 {
			sm.Cost += c.CostPerPortion * s.Portions / 1000
		} else {
			sm.CostKnown = false
		}
		for _, u := range c.Uses {
			if u.Staple {
				continue
			}
			need := u.PerPortion * s.Portions / 1_000_000
			w := u.Grams * s.Portions / 1000
			takes, short := stock.PlanFEFO(lots[u.ProductID], need, s.Date)
			stock.Consume(lots[u.ProductID], takes)
			weight += w
			if need > 0 {
				covered += w * (need - short) / need
			}
		}
	}
	if graded > 0 {
		avg := gradeSum / graded
		sm.Grade = letterOf(avg)
	}
	if weight > 0 {
		sm.StockShare = int(covered * 100 / weight)
	}
	return sm
}

// Alternative est une recette proposée pour remplacer un plat.
type Alternative struct {
	RecipeID int64
	Score    int
	Reasons  []string
}

// Alternatives propose les n meilleures recettes pour une cellule, le
// reste du menu étant fixé (plan : les plats actuels).
func Alternatives(in Input, plan []Placement, slotIdx int, role Role, n int) []Alternative {
	e := newEngine(in)
	e.reasons = true
	self := -1
	for _, p := range plan {
		i := e.put(p)
		if p.Slot == slotIdx && p.Role == role {
			self = i
		}
	}
	var out []Alternative
	for _, ci := range e.candidatesFor(slotIdx, role, self) {
		if self >= 0 && e.plan[self].RecipeID == e.cands[ci].ID {
			continue
		}
		v, reasons := e.total(ci, slotIdx, role, e.in.Stock, true, self)
		out = append(out, Alternative{RecipeID: e.cands[ci].ID, Score: v + e.noise(ci, slotIdx, role), Reasons: dedupe(reasons, 4)})
	}
	slices.SortFunc(out, func(a, b Alternative) int {
		if a.Score != b.Score {
			return b.Score - a.Score
		}
		return cmp.Compare(a.RecipeID, b.RecipeID)
	})
	return out[:min(n, len(out))]
}

// Feasible est une recette classée par part des ingrédients disponibles
// (« Que cuisiner ce soir ? »).
type Feasible struct {
	RecipeID int64
	Coverage int // % du poids en stock
	Missing  int // ingrédients manquants
	Score    int
	Reasons  []string
}

// RankTonight classe les recettes d'un rôle pour un repas avec le moteur
// de score du générateur, en favorisant ce qui est en stock et ce qui
// périme bientôt ; only, s'il n'est pas nul, ne garde que les recettes qui
// utilisent ce produit.
func RankTonight(in Input, s Slot, role Role, only int64, n int) []Feasible {
	in.Slots = []Slot{s}
	e := newEngine(in)
	e.reasons = true
	var out []Feasible
	for ci := range e.cands {
		c := &e.cands[ci]
		if !eligible(c, &s, role) {
			continue
		}
		if only > 0 && !slices.ContainsFunc(c.Uses, func(u Use) bool { return u.ProductID == only }) {
			continue
		}
		cov, _, missing := coverage(c, s.Portions, s.Date, e.slotSoon[0], in.Stock, in.Names, false)
		v, reasons := e.total(ci, 0, role, in.Stock, false, -1)
		out = append(out, Feasible{RecipeID: c.ID, Coverage: cov, Missing: missing, Score: v + cov/2, Reasons: dedupe(reasons, 4)})
	}
	slices.SortFunc(out, func(a, b Feasible) int {
		if a.Missing != b.Missing && (a.Missing == 0 || b.Missing == 0) {
			return a.Missing - b.Missing
		}
		if a.Score != b.Score {
			return b.Score - a.Score
		}
		return cmp.Compare(a.RecipeID, b.RecipeID)
	})
	return out[:min(n, len(out))]
}

// Summarize dresse le bilan d'un menu existant (vue de la semaine).
func Summarize(in Input, plan []Placement) Summary {
	e := newEngine(in)
	for _, p := range plan {
		e.put(p)
	}
	return e.summary()
}

// letterOf convertit une note moyenne (0 à 100) en lettre A à E.
func letterOf(v int) string {
	i := min(max((100-v+12)/25, 0), 4)
	return "ABCDE"[i : i+1]
}

// Letter convertit une note moyenne (0 à 100) en lettre A à E.
func Letter(v int) string { return letterOf(v) }
