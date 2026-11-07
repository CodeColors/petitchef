package web

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/codecolors/petitchef/internal/menus"
	"github.com/codecolors/petitchef/internal/platform/qty"
	"github.com/codecolors/petitchef/internal/recipes"
)

// cookView est la donnée de l'écran de confirmation « cuisiné ».
type cookView struct {
	Entry menus.Entry
	Meal  menus.Meal
	Uses  []menus.Use
	Back  string
}

func (s *Server) cookPage(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		s.notFound(w, r)
		return
	}
	ctx := r.Context()
	v := cookView{Back: r.URL.Query().Get("retour")}
	var err error
	if v.Entry, v.Uses, err = s.Menus.PlannedUses(ctx, id); err == nil {
		_, v.Meal, err = s.Menus.Entry(ctx, id)
	}
	if err != nil {
		s.failed(w, r, err, "/menus")
		return
	}
	if v.Back == "" {
		v.Back = "/menus?semaine=" + string(menus.Monday(v.Meal.Date))
	}
	s.render(w, r, http.StatusOK, "cook", "Cuisiné : "+v.Entry.Title, v)
}

// cook enregistre le plat cuisiné avec les quantités confirmées (lignes
// modifiables, décochables, produits remplaçables, lignes ajoutées).
func (s *Server) cook(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok || parseForm(w, r) != nil {
		s.notFound(w, r)
		return
	}
	ctx := r.Context()
	page := "/menus/plats/" + strconv.FormatInt(id, 10) + "/cuisine"
	var uses []menus.Use
	for i := range int(formInt(r.PostFormValue("lines"), 0)) + 1 {
		key := strconv.Itoa(i)
		if r.PostFormValue("use_"+key) == "" && i < int(formInt(r.PostFormValue("lines"), 0)) {
			continue // décoché : pas utilisé
		}
		if strings.TrimSpace(r.PostFormValue("produit_"+key)) == "" || strings.TrimSpace(r.PostFormValue("qty_"+key)) == "" {
			continue
		}
		pid, _, err := s.resolveProduct(r, "produit_"+key)
		if err != nil {
			s.failed(w, r, err, page)
			return
		}
		p, err := s.Catalog.Get(ctx, pid)
		if err != nil {
			s.failed(w, r, err, page)
			return
		}
		q, err := baseQty(r.PostFormValue("qty_"+key), p.Unit)
		if err != nil {
			s.failed(w, r, err, page)
			return
		}
		uses = append(uses, menus.Use{ProductID: pid, Name: p.Name, Unit: p.Unit, Qty: q})
	}
	var leftovers int64
	if v := strings.TrimSpace(r.PostFormValue("leftovers")); v != "" && v != "0" {
		var err error
		if leftovers, err = qty.ParseDecimal(v); err != nil {
			s.failed(w, r, err, page)
			return
		}
	}
	missing, err := s.Menus.Cook(ctx, id, uses, leftovers, currentUser(ctx).ID)
	if err != nil {
		s.failed(w, r, err, page)
		return
	}
	back := localPath(r.PostFormValue("back"))
	if r.PostFormValue("back") == "" {
		back = "/menus"
	}
	msg := "Bon appétit ! Stock mis à jour (annulable depuis le plat)." + menus.Describe(missing)
	if leftovers > 0 {
		msg += " Restes rangés au réfrigérateur."
	}
	s.redirect(w, r, back, msg)
}

func (s *Server) uncook(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok || parseForm(w, r) != nil {
		s.notFound(w, r)
		return
	}
	back := localPath(r.PostFormValue("back"))
	if r.PostFormValue("back") == "" {
		back = "/menus"
	}
	if err := s.Menus.Uncook(r.Context(), id); err != nil {
		s.failed(w, r, err, back)
		return
	}
	s.redirect(w, r, back, "« Cuisiné » annulé : le stock est restauré.")
}

// cookingView est la donnée du mode cuisine.
type cookingView struct {
	recipeView
	EntryID int64
}

func (v cookingView) bodyClass() string { return "cooking-mode" }

// cookingMode affiche une recette en plein écran, étape par étape, avec
// les ingrédients cochables, les minuteries et l'écran maintenu allumé.
func (s *Server) cookingMode(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		s.notFound(w, r)
		return
	}
	rv, err := s.recipeData(r, id)
	if err != nil {
		s.failed(w, r, err, "/recettes")
		return
	}
	v := cookingView{recipeView: rv}
	if v.EntryID, err = s.Menus.TodayEntry(r.Context(), id); err != nil {
		s.serverError(w, r, err)
		return
	}
	s.render(w, r, http.StatusOK, "cooking", rv.Recipe.Title, v)
}

var _ = recipes.Main
