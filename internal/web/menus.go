package web

import (
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/codecolors/petitchef/internal/household"
	"github.com/codecolors/petitchef/internal/menus"
	"github.com/codecolors/petitchef/internal/planner"
	"github.com/codecolors/petitchef/internal/platform/calendar"
	"github.com/codecolors/petitchef/internal/platform/qty"
	"github.com/codecolors/petitchef/internal/platform/validation"
	"github.com/codecolors/petitchef/internal/recipes"
)

// dayView est une journée du menu.
type dayView struct {
	Date  calendar.Date
	Today bool
	Meals []menus.Meal
}

// weekView est la donnée de la vue de la semaine.
type weekView struct {
	Monday   calendar.Date
	Prev     calendar.Date
	Next     calendar.Date
	Days     []dayView
	Summary  planner.Summary
	HasMeals bool // au moins un plat
	HasAny   bool // au moins un repas
	Print    bool
	// Mode sélection : cases à cocher pour supprimer plusieurs repas.
	Select    bool
	SelectAll bool
	// Alignement sur grand écran : rang de chaque créneau dans la semaine.
	SlotRow   map[int64]int
	SlotCount int
}

func (v weekView) bodyClass() string {
	if v.Print {
		return "print-week"
	}
	return ""
}

func (s *Server) weekData(r *http.Request) (weekView, error) {
	today := calendar.Today()
	mon := menus.Monday(today)
	if d, err := calendar.Parse(r.URL.Query().Get("semaine")); err == nil {
		mon = menus.Monday(d)
	}
	sel := r.URL.Query().Get("selection")
	v := weekView{Monday: mon, Prev: mon.AddDays(-7), Next: mon.AddDays(7), Select: sel != "", SelectAll: sel == "tout"}
	meals, err := s.Menus.Range(r.Context(), mon, mon.AddDays(7))
	if err != nil {
		return v, err
	}
	v.HasAny = len(meals) > 0
	v.SlotRow = slotRows(meals)
	v.SlotCount = max(len(v.SlotRow), 1)
	for i := range 7 {
		d := mon.AddDays(i)
		day := dayView{Date: d, Today: d == today}
		for _, m := range meals {
			if m.Date == d {
				day.Meals = append(day.Meals, m)
				if len(m.Entries) > 0 {
					v.HasMeals = true
				}
			}
		}
		v.Days = append(v.Days, day)
	}
	if v.HasMeals {
		if v.Summary, err = s.Menus.WeekSummary(r.Context(), meals); err != nil {
			return v, err
		}
	}
	return v, nil
}

func (s *Server) menusPage(w http.ResponseWriter, r *http.Request) {
	v, err := s.weekData(r)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.render(w, r, http.StatusOK, "menus", "Menus", v)
}

// slotRows numérote les créneaux présents dans la semaine, dans l'ordre de
// la journée : sur grand écran, chaque créneau occupe la même rangée dans
// les sept colonnes.
func slotRows(meals []menus.Meal) map[int64]int {
	pos := map[int64]int{}
	for _, m := range meals {
		pos[m.SlotID] = m.SlotPos
	}
	ids := make([]int64, 0, len(pos))
	for id := range pos {
		ids = append(ids, id)
	}
	slices.SortFunc(ids, func(a, b int64) int {
		if pos[a] != pos[b] {
			return pos[a] - pos[b]
		}
		return int(a - b)
	})
	rows := make(map[int64]int, len(ids))
	for i, id := range ids {
		rows[id] = i
	}
	return rows
}

// deleteMeals supprime un ou plusieurs repas (champ « repas » répété).
func (s *Server) deleteMeals(w http.ResponseWriter, r *http.Request) {
	if parseForm(w, r) != nil {
		s.renderError(w, r, http.StatusBadRequest, "Formulaire illisible.")
		return
	}
	back := "/menus"
	if d, err := calendar.Parse(r.PostFormValue("semaine")); err == nil {
		back += "?semaine=" + string(menus.Monday(d))
	}
	ids := formIDs(r.PostForm["repas"])
	if len(ids) == 0 {
		s.redirect(w, r, back+"&selection=1", "Cochez au moins un repas.")
		return
	}
	removed, kept, err := s.Menus.DeleteMeals(r.Context(), ids)
	if err != nil {
		s.failed(w, r, err, back)
		return
	}
	msg := plural(removed, "repas supprimé", "repas supprimés") + "."
	if kept > 0 {
		msg = "Repas vidés, sauf " + plural(kept, "plat déjà cuisiné, conservé", "plats déjà cuisinés, conservés") + "."
	}
	s.redirect(w, r, back, msg)
}

func (s *Server) printMenu(w http.ResponseWriter, r *http.Request) {
	v, err := s.weekData(r)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	v.Print = true
	s.render(w, r, http.StatusOK, "menu_print", "Menu de la semaine du "+v.Monday.Format(), v)
}

// generateView est la donnée du formulaire de génération.
type generateView struct {
	Pref      menus.Preferences
	From      calendar.Date
	Templates []menus.Template
	Presets   []planner.Preset
	Error     string
}

func (s *Server) generatePage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	v := generateView{Presets: planner.Presets}
	var err error
	if v.Pref, err = s.Menus.Preferences(ctx); err == nil {
		v.Templates, err = s.Menus.Templates(ctx)
	}
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	v.From = calendar.Today()
	if d, err := calendar.Parse(r.URL.Query().Get("du")); err == nil {
		v.From = d
	} else if wd := (int(time.Now().Weekday()) + 6) % 7; wd >= 4 {
		v.From = menus.Monday(calendar.Today()).AddDays(7) // à partir de vendredi : la semaine suivante
	}
	s.render(w, r, http.StatusOK, "menu_generate", "Générer les menus", v)
}

func (s *Server) generateMenus(w http.ResponseWriter, r *http.Request) {
	if parseForm(w, r) != nil {
		s.renderError(w, r, http.StatusBadRequest, "Formulaire illisible.")
		return
	}
	ctx := r.Context()
	if page, err := s.Recipes.Search(ctx, recipes.Filter{PageSize: 1}); err != nil {
		s.serverError(w, r, err)
		return
	} else if page.Total == 0 {
		s.redirect(w, r, "/recettes", "Pas encore de recette : ajoutez ou importez vos recettes, puis générez le menu.")
		return
	}
	pref, err := s.Menus.Preferences(ctx)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	from, err := calendar.Parse(r.PostFormValue("from"))
	if err != nil {
		s.failed(w, r, err, "/menus/generer")
		return
	}
	pref.Weeks = int(formInt(r.PostFormValue("weeks"), 1))
	pref.TemplateID = formInt(r.PostFormValue("template"), 0)
	pref.Leftovers = r.PostFormValue("leftovers") != ""
	pref.Preset = r.PostFormValue("preset")
	for _, p := range planner.Presets {
		if p.Code == pref.Preset {
			pref.Weights = p.Weights
		}
	}
	if pref.Preset == "perso" || r.PostFormValue("custom") != "" {
		pref.Preset = "perso"
		for field, dst := range map[string]*int{"w_stock": &pref.Weights.Stock, "w_balance": &pref.Weights.Balance, "w_variety": &pref.Weights.Variety,
			"w_season": &pref.Weights.Season, "w_rating": &pref.Weights.Rating, "w_cost": &pref.Weights.Cost, "w_quick": &pref.Weights.Quick} {
			*dst = int(min(max(formInt(r.PostFormValue(field), int64(*dst)), 0), 10))
		}
	}
	if r.PostFormValue("rules") != "" {
		pref.Rules = planner.Rules{
			FishMin: int(formInt(r.PostFormValue("fish_min"), 0)), RedMeatMax: int(formInt(r.PostFormValue("red_max"), 7)),
			VegetarianMin: int(formInt(r.PostFormValue("veg_min"), 0)), VegEveryMeal: r.PostFormValue("veg_every") != "",
			NoRepeatDays: int(formInt(r.PostFormValue("no_repeat"), 10)),
			NoFish:       r.PostFormValue("no_fish") != "",
		}
	}
	pref.Seed = uint64(max(formInt(r.PostFormValue("seed"), 0), 0)) //nolint:gosec // valeur positive
	if r.PostFormValue("same_seed") == "" {
		pref.Seed = 0
	}
	res, err := s.Menus.Generate(ctx, menus.Request{From: from, Weeks: pref.Weeks, Preferences: pref})
	// Opération ponctuelle et lourde : sa mémoire de travail est rendue au
	// système, rien ne la reprendrait au repos.
	defer s.db.ReleaseMemory(ctx)
	if err != nil {
		s.failed(w, r, err, "/menus/generer")
		return
	}
	msg := fmt.Sprintf("Menu généré en %d ms : %s.", res.Duration.Milliseconds(), plural(res.Dishes, "plat", "plats"))
	if res.Empty > 0 {
		msg += fmt.Sprintf(" %s faute de recette compatible.", plural(res.Empty, "case reste vide", "cases restent vides"))
	}
	s.redirect(w, r, "/menus?semaine="+string(from), msg)
}

// mealView est la donnée de l'édition d'un repas.
type mealView struct {
	Meal         menus.Meal
	Members      []household.Member
	Formats      []planner.Format
	Roles        []planner.Role
	Titles       map[planner.Role][]recipes.Summary
	Alternatives map[planner.Role][]menus.Alternative
	Dishes       map[planner.Role]menus.Entry
	Week         []swapTarget
	Back         string
}

// swapTarget est un plat de la semaine avec lequel échanger.
type swapTarget struct {
	ID    int64
	Label string
}

func (s *Server) mealPage(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		s.notFound(w, r)
		return
	}
	ctx := r.Context()
	v := mealView{Formats: planner.Formats, Titles: map[planner.Role][]recipes.Summary{}, Alternatives: map[planner.Role][]menus.Alternative{},
		Dishes: map[planner.Role]menus.Entry{}}
	var err error
	if v.Meal, err = s.Menus.Meal(ctx, id); err != nil {
		s.failed(w, r, err, "/menus")
		return
	}
	v.Back = "/menus?semaine=" + string(menus.Monday(v.Meal.Date))
	if v.Members, err = s.Household.Members(ctx, false); err != nil {
		s.serverError(w, r, err)
		return
	}
	v.Roles = v.Meal.Format.Roles()
	slices.SortFunc(v.Roles, func(a, b planner.Role) int { return slices.Index(planner.Roles, a) - slices.Index(planner.Roles, b) })
	for _, e := range v.Meal.Entries {
		v.Dishes[e.Role] = e
	}
	show := r.URL.Query().Get("alternatives")
	for _, role := range v.Roles {
		if v.Titles[role], err = s.Recipes.Titles(ctx, role.Type()); err != nil {
			s.serverError(w, r, err)
			return
		}
		if show == string(role) || show == "tout" {
			if v.Alternatives[role], err = s.Menus.AlternativesForMeal(ctx, id, role, 5); err != nil {
				s.failed(w, r, err, v.Back)
				return
			}
		}
	}
	week, err := s.Menus.Range(ctx, menus.Monday(v.Meal.Date), menus.Monday(v.Meal.Date).AddDays(7))
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	for _, m := range week {
		for _, e := range m.Entries {
			if m.ID != id && e.Status != menus.Cooked && !e.IsLeftover() {
				v.Week = append(v.Week, swapTarget{e.ID, fmt.Sprintf("%s %s · %s : %s", dayLabel(m.Date), strings.ToLower(m.SlotName), e.Role.Label(), e.Title)})
			}
		}
	}
	s.render(w, r, http.StatusOK, "meal", fmt.Sprintf("%s du %s", v.Meal.SlotName, dayLabel(v.Meal.Date)), v)
}

func (s *Server) updateMeal(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok || parseForm(w, r) != nil {
		s.notFound(w, r)
		return
	}
	in := menus.MealInput{Format: planner.Format(r.PostFormValue("format")), AllMembers: r.PostFormValue("all") != "",
		Guests: int(formInt(r.PostFormValue("guests"), 0)), MaxMin: int(formInt(r.PostFormValue("max_min"), -1))}
	for _, v := range r.PostForm["members"] {
		in.Members = append(in.Members, formInt(v, 0))
	}
	back := "/menus/repas/" + strconv.FormatInt(id, 10)
	if err := s.Menus.UpdateMeal(r.Context(), id, in); err != nil {
		s.failed(w, r, err, back)
		return
	}
	s.redirect(w, r, back, "Repas mis à jour.")
}

// setDish place une recette (champ recipe) ou un produit simple (champ
// produit, créé à la volée s'il est inconnu) dans un repas.
func (s *Server) setDish(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok || parseForm(w, r) != nil {
		s.notFound(w, r)
		return
	}
	ctx := r.Context()
	back := "/menus/repas/" + strconv.FormatInt(id, 10)
	role := planner.Role(r.PostFormValue("role"))
	recipe := formInt(r.PostFormValue("recipe"), 0)
	var product int64
	if recipe == 0 && strings.TrimSpace(r.PostFormValue("produit")) != "" {
		var err error
		if product, _, err = s.resolveProduct(r, "produit"); err != nil {
			s.failed(w, r, err, back)
			return
		}
	}
	reasons := []string{"choisi à la main"}
	if why := r.PostFormValue("reasons"); why != "" {
		reasons = strings.Split(why, "|")
	}
	if _, err := s.Menus.SetDish(ctx, id, role, recipe, product, 0, reasons); err != nil {
		s.failed(w, r, err, back)
		return
	}
	if b := r.PostFormValue("back"); b != "" {
		back = localPath(b)
	}
	s.redirect(w, r, back, role.Label()+" remplacé.")
}

// dishAction applique une action à un plat du menu.
func (s *Server) dishAction(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok || parseForm(w, r) != nil {
		s.notFound(w, r)
		return
	}
	ctx := r.Context()
	e, m, err := s.Menus.Entry(ctx, id)
	if err != nil {
		s.failed(w, r, err, "/menus")
		return
	}
	back := localPath(r.PostFormValue("back"))
	if r.PostFormValue("back") == "" {
		back = "/menus?semaine=" + string(menus.Monday(m.Date))
	}
	var msg string
	switch r.PathValue("action") {
	case "verrou":
		err = s.Menus.ToggleLock(ctx, id)
		msg = "Plat déverrouillé."
		if !e.Locked {
			msg = "Plat verrouillé : il sera conservé à la prochaine génération."
		}
	case "retirer":
		err = s.Menus.RemoveDish(ctx, id)
		msg = e.Title + " retiré du menu."
	case "reporter":
		err = s.Menus.Postpone(ctx, id, e.Status != menus.Postponed)
		msg = e.Title + " reporté : replacez-le quand vous voulez, ses ingrédients restent prévus."
		if e.Status == menus.Postponed {
			msg = e.Title + " de nouveau prévu."
		}
	case "restes":
		err = s.Menus.CookDouble(ctx, id, formInt(r.PostFormValue("meal"), 0))
		msg = e.Title + " : à cuisiner pour deux repas, restes ajoutés au menu."
	case "echanger":
		err = s.Menus.Swap(ctx, id, formInt(r.PostFormValue("with"), 0))
		msg = "Plats échangés."
	case "deplacer":
		err = s.Menus.Move(ctx, id, formInt(r.PostFormValue("meal"), 0))
		msg = e.Title + " déplacé."
	default:
		s.notFound(w, r)
		return
	}
	if err != nil {
		s.failed(w, r, err, back)
		return
	}
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Refresh", "true")
		w.WriteHeader(http.StatusNoContent)
		return
	}
	s.redirect(w, r, back, msg)
}

// addToMenuView est la donnée de l'ajout d'une recette au menu.
type addToMenuView struct {
	Recipe recipes.Recipe
	Date   calendar.Date
	Slots  []menus.MealSlot
	Roles  []planner.Role
}

func (s *Server) addToMenuPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	v := addToMenuView{Date: calendar.Today()}
	var err error
	if v.Recipe, err = s.Recipes.Get(ctx, formInt(r.URL.Query().Get("recette"), 0)); err != nil {
		s.failed(w, r, err, "/recettes")
		return
	}
	if v.Slots, err = s.Menus.Slots(ctx, true); err != nil {
		s.serverError(w, r, err)
		return
	}
	for _, role := range planner.Roles {
		if v.Recipe.Types.Has(role.Type()) {
			v.Roles = append(v.Roles, role)
		}
	}
	if len(v.Roles) == 0 {
		v.Roles = []planner.Role{planner.Main}
	}
	s.render(w, r, http.StatusOK, "menu_add", "Ajouter au menu", v)
}

func (s *Server) addToMenu(w http.ResponseWriter, r *http.Request) {
	if parseForm(w, r) != nil {
		s.renderError(w, r, http.StatusBadRequest, "Formulaire illisible.")
		return
	}
	ctx := r.Context()
	recipe := formInt(r.PostFormValue("recipe"), 0)
	date, err := calendar.Parse(r.PostFormValue("date"))
	if err != nil {
		s.failed(w, r, err, "/menus/ajouter?recette="+strconv.FormatInt(recipe, 10))
		return
	}
	if _, err := s.Menus.AddToMeal(ctx, date, formInt(r.PostFormValue("slot"), 0), planner.Role(r.PostFormValue("role")), recipe); err != nil {
		s.failed(w, r, err, "/menus/ajouter?recette="+strconv.FormatInt(recipe, 10))
		return
	}
	s.redirect(w, r, "/menus?semaine="+string(date), "Recette ajoutée au menu du "+dayLabel(date)+".")
}

// slotsView est la donnée des créneaux et semaines types.
type slotsView struct {
	Slots     []menus.MealSlot
	Templates []menus.Template
	Template  menus.Template
	Cells     map[[2]int64]menus.Cell
	Members   []household.Member
	Formats   []planner.Format
	Days      []int
}

func (s *Server) slotsPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	v := slotsView{Formats: planner.Formats, Days: seq(7)}
	var err error
	if v.Slots, err = s.Menus.Slots(ctx, false); err == nil {
		if v.Templates, err = s.Menus.Templates(ctx); err == nil {
			v.Members, err = s.Household.Members(ctx, false)
		}
	}
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	sel := formInt(r.URL.Query().Get("semaine"), 0)
	for _, t := range v.Templates {
		if t.ID == sel || (sel == 0 && v.Template.ID == 0) {
			v.Template = t
		}
	}
	if v.Cells, err = s.Menus.Cells(ctx, v.Template.ID); err != nil {
		s.serverError(w, r, err)
		return
	}
	s.render(w, r, http.StatusOK, "slots", "Créneaux et semaines types", v)
}

func (s *Server) saveSlot(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok || parseForm(w, r) != nil {
		s.notFound(w, r)
		return
	}
	m := menus.MealSlot{ID: id, Name: r.PostFormValue("name"), Enabled: r.PostFormValue("enabled") != "",
		MaxMinWeekday: int(formInt(r.PostFormValue("max_weekday"), 0)), MaxMinWeekend: int(formInt(r.PostFormValue("max_weekend"), 0))}
	if err := s.Menus.SaveSlot(r.Context(), m); err != nil {
		s.failed(w, r, err, "/creneaux")
		return
	}
	s.redirect(w, r, "/creneaux", "Créneau « "+m.Name+" » enregistré.")
}

// saveTemplate enregistre toute la grille d'une semaine type (un champ
// par jour et créneau).
func (s *Server) saveTemplate(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok || parseForm(w, r) != nil {
		s.notFound(w, r)
		return
	}
	ctx := r.Context()
	back := "/creneaux?semaine=" + strconv.FormatInt(id, 10)
	slotList, err := s.Menus.Slots(ctx, false)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	for d := range 7 {
		for _, sl := range slotList {
			key := fmt.Sprintf("%d_%d", d, sl.ID)
			f := r.PostFormValue("format_" + key)
			if f == "" {
				continue
			}
			c := menus.Cell{Weekday: d, SlotID: sl.ID, Format: planner.Format(f), Guests: int(formInt(r.PostFormValue("guests_"+key), 0)), AllMembers: true}
			if vals := r.PostForm["members_"+key]; len(vals) > 0 && r.PostFormValue("present_"+key) == "some" {
				c.AllMembers = false
				for _, v := range vals {
					c.Members = append(c.Members, formInt(v, 0))
				}
			}
			if err := s.Menus.SaveCell(ctx, id, c); err != nil {
				s.failed(w, r, err, back)
				return
			}
		}
	}
	if r.PostFormValue("default") != "" {
		if err := s.Menus.SetDefaultTemplate(ctx, id); err != nil {
			s.serverError(w, r, err)
			return
		}
	}
	s.redirect(w, r, back, "Semaine type enregistrée. Elle s'applique aux repas qui ne sont pas encore planifiés.")
}

func (s *Server) createTemplate(w http.ResponseWriter, r *http.Request) {
	if parseForm(w, r) != nil {
		s.renderError(w, r, http.StatusBadRequest, "Formulaire illisible.")
		return
	}
	id, err := s.Menus.CreateTemplate(r.Context(), r.PostFormValue("name"), formInt(r.PostFormValue("from"), 0))
	if err != nil {
		s.failed(w, r, err, "/creneaux")
		return
	}
	s.redirect(w, r, "/creneaux?semaine="+strconv.FormatInt(id, 10), "Semaine type créée.")
}

func (s *Server) deleteTemplate(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		s.notFound(w, r)
		return
	}
	if err := s.Menus.DeleteTemplate(r.Context(), id); err != nil {
		s.failed(w, r, err, "/creneaux")
		return
	}
	s.redirect(w, r, "/creneaux", "Semaine type supprimée.")
}

// modelsView est la donnée des menus enregistrés et de l'historique.
type modelsView struct {
	Models []menus.Model
	Weeks  []calendar.Date
	Monday calendar.Date
}

func (s *Server) modelsPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	v := modelsView{Monday: menus.Monday(calendar.Today())}
	var err error
	if v.Models, err = s.Menus.Models(ctx); err == nil {
		v.Weeks, err = s.Menus.Weeks(ctx, 26)
	}
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.render(w, r, http.StatusOK, "menu_models", "Menus enregistrés et historique", v)
}

func (s *Server) saveModel(w http.ResponseWriter, r *http.Request) {
	if parseForm(w, r) != nil {
		s.renderError(w, r, http.StatusBadRequest, "Formulaire illisible.")
		return
	}
	from, err := calendar.Parse(r.PostFormValue("from"))
	if err == nil {
		_, err = s.Menus.SaveModel(r.Context(), r.PostFormValue("name"), from, int(formInt(r.PostFormValue("days"), 7)))
	}
	if err != nil {
		s.failed(w, r, err, "/menus/modeles")
		return
	}
	s.redirect(w, r, "/menus/modeles", "Menu enregistré comme modèle.")
}

func (s *Server) applyModel(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok || parseForm(w, r) != nil {
		s.notFound(w, r)
		return
	}
	from, err := calendar.Parse(r.PostFormValue("from"))
	if err != nil {
		s.failed(w, r, err, "/menus/modeles")
		return
	}
	n, err := s.Menus.ApplyModel(r.Context(), id, from)
	if err != nil {
		s.failed(w, r, err, "/menus/modeles")
		return
	}
	s.redirect(w, r, "/menus?semaine="+string(from), fmt.Sprintf("Menu appliqué : %s.", plural(n, "plat placé", "plats placés")))
}

func (s *Server) deleteModel(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		s.notFound(w, r)
		return
	}
	if err := s.Menus.DeleteModel(r.Context(), id); err != nil {
		s.failed(w, r, err, "/menus/modeles")
		return
	}
	s.redirect(w, r, "/menus/modeles", "Menu enregistré supprimé.")
}

// tonightView est la donnée de « Que cuisiner ce soir ? ».
type tonightView struct {
	Items     []menus.Tonight
	Role      planner.Role
	Product   string
	ProductID int64
	Portions  int64
	Slots     []menus.MealSlot
	Today     calendar.Date
}

func (s *Server) tonightPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	q := r.URL.Query()
	v := tonightView{Role: planner.Main, ProductID: formInt(q.Get("produit"), 0), Today: calendar.Today()}
	if role := planner.Role(q.Get("role")); role == planner.Starter || role == planner.Dessert {
		v.Role = role
	}
	if p, err := qty.ParseDecimal(q.Get("portions")); err == nil {
		v.Portions = p
	}
	if v.ProductID > 0 {
		p, err := s.Catalog.Get(ctx, v.ProductID)
		if err != nil {
			s.failed(w, r, err, "/ce-soir")
			return
		}
		v.Product = p.Name
	}
	var err error
	if v.Items, err = s.Menus.Tonight(ctx, v.Role, v.ProductID, v.Portions, 20); err == nil {
		v.Slots, err = s.Menus.Slots(ctx, true)
	}
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.render(w, r, http.StatusOK, "tonight", "Que cuisiner ce soir ?", v)
}

var _ = validation.Error("")
