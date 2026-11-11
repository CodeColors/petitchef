package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/codecolors/petitchef/internal/catalog"
	"github.com/codecolors/petitchef/internal/household"
	"github.com/codecolors/petitchef/internal/photos"
	"github.com/codecolors/petitchef/internal/platform/calendar"
	"github.com/codecolors/petitchef/internal/platform/qty"
	"github.com/codecolors/petitchef/internal/platform/validation"
	"github.com/codecolors/petitchef/internal/recipes"
)

// recipesView est la donnée de la liste des recettes.
type recipesView struct {
	Filter    recipes.Filter
	Page      recipes.Page
	Tags      []recipes.Tag
	Query     string
	Season    bool
	Select    bool   // mode sélection : cases à cocher pour supprimer
	SelectAll bool   // toutes les recettes de la page cochées
	AllQuery  string // filtres, toutes les cases cochées
}

func recipeFilter(r *http.Request) (recipes.Filter, bool) {
	q := r.URL.Query()
	f := recipes.Filter{Query: q.Get("q"), MaxMinutes: int(formInt(q.Get("temps"), 0)), Tag: q.Get("etiquette"),
		MaxGrade: q.Get("indice"), MinStars: int(formInt(q.Get("note"), 0)), Favorites: q.Get("favoris") != "",
		Page: int(formInt(q.Get("page"), 0))}
	if t, ok := recipes.TypeByCode(q.Get("type")); ok {
		f.Type = t
	}
	for _, d := range catalog.AllDiets {
		if q.Get("regime") == d.Code {
			f.Diet = d.Bit
		}
	}
	season := q.Get("saison") != ""
	if season {
		f.Month = int(calendar.Today().Time().Month())
	}
	if f.MaxGrade != "" && (len(f.MaxGrade) != 1 || f.MaxGrade[0] < 'A' || f.MaxGrade[0] > 'E') {
		f.MaxGrade = ""
	}
	return f, season
}

func (s *Server) recipesPage(w http.ResponseWriter, r *http.Request) {
	f, season := recipeFilter(r)
	sel := r.URL.Query().Get("selection")
	v := recipesView{Filter: f, Season: season, Select: sel != "", SelectAll: sel == "tout"}
	var err error
	if v.Page, err = s.Recipes.Search(r.Context(), f); err == nil {
		v.Tags, err = s.Recipes.Tags(r.Context())
	}
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	q := r.URL.Query()
	q.Del("page")
	if v.Select {
		q.Set("selection", "tout")
		v.AllQuery = q.Encode()
		q.Set("selection", "1")
	}
	v.Query = q.Encode()
	if r.Header.Get("HX-Request") == "true" && r.Header.Get("HX-Target") == "resultats" {
		s.renderBlock(w, r, "recipes", "results", v)
		return
	}
	s.render(w, r, http.StatusOK, "recipes", "Recettes", v)
}

// recipeView est la donnée de la fiche d'une recette.
type recipeView struct {
	Recipe    recipes.Recipe
	Analysis  recipes.Analysis
	Portions  int64 // millièmes
	Lines     []scaledLine
	Ratings   []recipes.Rating
	Cost      int64
	CostKnown int
	CostTotal int
	Diets     []string
	Allergens []string
	Grid      recipes.Grid
	Month     int
}

// scaledLine est un ingrédient mis à l'échelle pour l'affichage.
type scaledLine struct {
	recipes.Ingredient
	Scaled string
}

func (s *Server) recipePage(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		s.notFound(w, r)
		return
	}
	v, err := s.recipeData(r, id)
	if err != nil {
		s.failed(w, r, err, "/recettes")
		return
	}
	s.render(w, r, http.StatusOK, "recipe", v.Recipe.Title, v)
}

// recipeData prépare la fiche d'une recette, mise à l'échelle des portions
// demandées (?portions=, décimal accepté).
func (s *Server) recipeData(r *http.Request, id int64) (recipeView, error) {
	ctx := r.Context()
	var v recipeView
	var err error
	if v.Recipe, err = s.Recipes.Get(ctx, id); err != nil {
		return v, err
	}
	if v.Grid, err = s.Recipes.Grid(ctx); err != nil {
		return v, err
	}
	if v.Analysis, err = s.Recipes.Analyze(ctx, v.Recipe, v.Grid); err != nil {
		return v, err
	}
	v.Portions = int64(v.Recipe.Servings) * 1000
	if p, err := qty.ParseDecimal(r.URL.Query().Get("portions")); err == nil && p <= 100_000 {
		v.Portions = p
	}
	for _, in := range v.Recipe.Ingredients {
		l := scaledLine{Ingredient: in}
		scaled := recipes.ScaleQty(in, v.Recipe.Servings, v.Portions)
		switch {
		case in.SubRecipe > 0:
			l.Scaled = qty.FormatDecimal(scaled) + " portion(s)"
		case in.InputQty == 0:
			l.Scaled = ""
		case in.Qty > 0:
			l.Scaled = scaledInput(in, scaled)
		default:
			l.Scaled = qty.FormatDecimal(in.InputQty*v.Portions/(int64(v.Recipe.Servings)*1000)) + " " + qty.UnitLabel(in.InputUnit, in.InputQty)
		}
		v.Lines = append(v.Lines, l)
	}
	if v.Ratings, err = s.Recipes.Ratings(ctx, id); err != nil {
		return v, err
	}
	needs, err := s.Recipes.Needs(ctx, id, v.Portions)
	if err != nil {
		return v, err
	}
	ids := make([]int64, 0, len(needs))
	for _, n := range needs {
		if !n.ToTaste && !n.Optional {
			ids = append(ids, n.ProductID)
		}
	}
	prices, err := s.Catalog.UnitPrices(ctx, ids)
	if err != nil {
		return v, err
	}
	for _, n := range needs {
		if n.ToTaste || n.Optional {
			continue
		}
		v.CostTotal++
		if p, ok := prices[n.ProductID]; ok {
			v.Cost += catalog.Cost(p, n.Qty)
			v.CostKnown++
		}
	}
	v.Diets = v.Recipe.Diets().Labels()
	v.Allergens = v.Recipe.Allergens.Labels()
	v.Month = int(calendar.Today().Time().Month())
	return v, nil
}

// scaledInput écrit une quantité mise à l'échelle dans l'unité de saisie
// quand c'est possible (« 3 c. à soupe »), sinon dans l'unité de base.
func scaledInput(in recipes.Ingredient, scaled int64) string {
	if in.InputUnit == "" || in.InputUnit == "piece" || in.Qty == 0 {
		if in.Unit == qty.Piece {
			return qty.FormatPieces(scaled)
		}
		if in.InputUnit == "piece" {
			// Saisi en pièces pour un produit au poids : on garde les pièces.
			p := in.InputQty * scaled / in.Qty
			return qty.FormatPieces(catalog.ScalePieces(p, in.InputQty%1000 != 0))
		}
		return qty.FormatShort(scaled, in.Unit)
	}
	switch in.InputUnit {
	case "g", "kg", "ml", "cl", "dl", "l":
		return qty.FormatShort(scaled, in.Unit)
	}
	v := in.InputQty * scaled / in.Qty
	return qty.FormatDecimal(roundTo(v, 250)) + " " + qty.UnitLabel(in.InputUnit, v)
}

func roundTo(v, step int64) int64 {
	r := (v + step/2) / step * step
	if r == 0 {
		return step
	}
	return r
}

// recipeForm est la donnée du formulaire d'une recette.
type recipeForm struct {
	ID     int64
	Input  recipes.Input
	Rows   []ingRow // lignes d'ingrédients
	Paste  string   // liste collée, à convertir en lignes
	Steps  string
	Tags   string
	Error  string
	Focus  int // ligne à mettre en évidence (ajout) ; -1 : aucune
	Titles []recipes.Summary
	Units  []qty.Choice
}

func (s *Server) newRecipePage(w http.ResponseWriter, r *http.Request) {
	f := recipeForm{Input: recipes.Input{Servings: 4, Types: recipes.Main, Difficulty: 1}, Rows: []ingRow{newRow(0)}, Focus: -1}
	if t, ok := recipes.TypeByCode(r.URL.Query().Get("type")); ok {
		f.Input.Types = t
	}
	s.renderRecipeForm(w, r, http.StatusOK, f)
}

func (s *Server) editRecipePage(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		s.notFound(w, r)
		return
	}
	rec, err := s.Recipes.Get(r.Context(), id)
	if err != nil {
		s.failed(w, r, err, "/recettes")
		return
	}
	f := recipeForm{ID: id, Input: recipes.InputFrom(rec), Rows: rowsFrom(rec.Ingredients), Tags: strings.Join(rec.Tags, ", "), Focus: -1}
	if len(f.Rows) == 0 {
		f.Rows = []ingRow{newRow(0)}
	}
	var steps []string
	for _, st := range rec.Steps {
		steps = append(steps, st.Text)
	}
	f.Steps = strings.Join(steps, "\n")
	s.renderRecipeForm(w, r, http.StatusOK, f)
}

func (s *Server) renderRecipeForm(w http.ResponseWriter, r *http.Request, status int, f recipeForm) {
	title := "Nouvelle recette"
	if f.ID > 0 {
		title = "Modifier « " + f.Input.Title + " »"
	}
	all, err := s.Recipes.Titles(r.Context(), 0)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	for _, t := range all {
		if t.ID != f.ID {
			f.Titles = append(f.Titles, t)
		}
	}
	f.Units = qty.KitchenUnits
	s.render(w, r, status, "recipe_form", title, f)
}

// saveRecipe enregistre une recette. Les ingrédients arrivent en lignes
// structurées (quantité, unité, ingrédient) contrôlées une à une. Les
// boutons « + » (action=add), « Retirer » (remove=N) et « Convertir la
// liste » (action=paste) renvoient le formulaire complété sans enregistrer :
// l'écran fonctionne sans JavaScript.
func (s *Server) saveRecipe(w http.ResponseWriter, r *http.Request) {
	if parseForm(w, r) != nil {
		s.renderError(w, r, http.StatusBadRequest, "Formulaire illisible.")
		return
	}
	ctx := r.Context()
	id, _ := pathID(r)
	remove := -1
	if v := r.PostFormValue("remove"); v != "" {
		remove = int(formInt(v, -1))
	}
	f := recipeForm{ID: id, Rows: parseRows(r.PostForm, remove), Paste: r.PostFormValue("paste"), Steps: r.PostFormValue("steps"),
		Tags: r.PostFormValue("tags"), Focus: -1}
	in := recipes.Input{Title: r.PostFormValue("title"), Servings: int(formInt(r.PostFormValue("servings"), 0)),
		PrepMin: int(formInt(r.PostFormValue("prep"), 0)), CookMin: int(formInt(r.PostFormValue("cook"), 0)),
		RestMin: int(formInt(r.PostFormValue("rest"), 0)), Difficulty: int(formInt(r.PostFormValue("difficulty"), 1)),
		Utensils: strings.TrimSpace(r.PostFormValue("utensils")), Source: strings.TrimSpace(r.PostFormValue("source")),
		Notes: strings.TrimSpace(r.PostFormValue("notes")), Tags: splitList(f.Tags), BalanceManual: r.PostFormValue("grade")}
	for _, t := range recipes.AllTypes {
		if r.PostFormValue("type_"+t.Code) != "" {
			in.Types |= t.Bit
		}
	}
	for _, st := range strings.Split(f.Steps, "\n") {
		if st = strings.TrimSpace(st); st != "" {
			in.Steps = append(in.Steps, st)
		}
	}
	f.Input = in
	switch {
	case r.PostFormValue("action") == "add":
		f.Rows = renumber(append(f.Rows, newRow(len(f.Rows))))
		f.Focus = len(f.Rows) - 1
		s.renderRecipeForm(w, r, http.StatusOK, f)
		return
	case remove >= 0:
		if len(f.Rows) == 0 {
			f.Rows = []ingRow{newRow(0)}
		}
		s.renderRecipeForm(w, r, http.StatusOK, f)
		return
	case r.PostFormValue("action") == "paste":
		if err := s.pasteRows(ctx, &f); err != nil {
			s.serverError(w, r, err)
		} else {
			s.renderRecipeForm(w, r, http.StatusOK, f)
		}
		return
	}
	list, ok, err := checkRows(ctx, serverChecker{s}, f.Rows)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	if len(f.Rows) == 0 {
		f.Rows = []ingRow{newRow(0)}
	}
	switch {
	case !ok:
		f.Error = "Certains ingrédients sont à corriger : voyez les lignes signalées."
	case len(list) == 0:
		f.Rows[0].Error = "Ajoutez au moins un ingrédient."
		f.Error = "La recette n'a aucun ingrédient."
	}
	if f.Error != "" {
		s.renderRecipeForm(w, r, http.StatusUnprocessableEntity, f)
		return
	}
	in.Ingredients = list
	nid, err := s.Recipes.Save(ctx, id, in)
	if err != nil {
		if msg, ok := s.inputError(w, r, err); ok {
			f.Error = msg
			s.renderRecipeForm(w, r, http.StatusUnprocessableEntity, f)
		}
		return
	}
	s.redirect(w, r, "/recettes/"+strconv.FormatInt(nid, 10), "Recette enregistrée.")
}

// pasteRows convertit une liste collée en lignes, à la suite des lignes
// déjà remplies ; les noms non reconnus restent signalés, à choisir.
func (s *Server) pasteRows(ctx context.Context, f *recipeForm) error {
	resolved, err := s.Recipes.Resolve(ctx, recipes.ParseLines(f.Paste))
	if err != nil {
		return err
	}
	names := map[int64]string{}
	for _, x := range resolved {
		if x.ProductID > 0 && x.Confidence >= recipes.Partial {
			p, err := s.Catalog.Get(ctx, x.ProductID)
			if err != nil {
				return err
			}
			names[x.ProductID] = p.Name
		}
	}
	var kept []ingRow
	for _, row := range f.Rows {
		if !row.blank() {
			kept = append(kept, row)
		}
	}
	f.Rows = renumber(append(kept, rowsFromLines(resolved, names)...))
	if len(f.Rows) == 0 {
		f.Rows = []ingRow{newRow(0)}
	}
	f.Paste = ""
	return nil
}

func (s *Server) deleteRecipe(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		s.notFound(w, r)
		return
	}
	rec, err := s.Recipes.Get(r.Context(), id)
	if err != nil {
		s.failed(w, r, err, "/recettes")
		return
	}
	photos, err := s.Recipes.DeleteMany(r.Context(), []int64{id})
	if err != nil {
		s.failed(w, r, err, "/recettes/"+strconv.FormatInt(id, 10))
		return
	}
	s.deletePhotos(photos)
	s.redirect(w, r, "/recettes", "« "+rec.Title+" » supprimée et retirée des menus.")
}

// deleteRecipes supprime plusieurs recettes (champ « recette » répété),
// retirées aussi de tous les menus.
func (s *Server) deleteRecipes(w http.ResponseWriter, r *http.Request) {
	if parseForm(w, r) != nil {
		s.renderError(w, r, http.StatusBadRequest, "Formulaire illisible.")
		return
	}
	back := "/recettes?selection=1"
	if q := r.PostFormValue("retour"); strings.HasPrefix(q, "/recettes") {
		back = localPath(q)
	}
	ids := formIDs(r.PostForm["recette"])
	if len(ids) == 0 {
		s.redirect(w, r, back, "Cochez au moins une recette.")
		return
	}
	photos, err := s.Recipes.DeleteMany(r.Context(), ids)
	if err != nil {
		s.failed(w, r, err, back)
		return
	}
	s.deletePhotos(photos)
	s.redirect(w, r, "/recettes", plural(len(ids), "recette supprimée et retirée des menus.", "recettes supprimées et retirées des menus."))
}

func (s *Server) deletePhotos(names []string) {
	for _, p := range names {
		_ = s.Photos.Delete(p)
	}
}

func (s *Server) duplicateRecipe(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		s.notFound(w, r)
		return
	}
	nid, err := s.Recipes.Duplicate(r.Context(), id)
	if err != nil {
		s.failed(w, r, err, "/recettes/"+strconv.FormatInt(id, 10))
		return
	}
	s.redirect(w, r, "/recettes/"+strconv.FormatInt(nid, 10)+"/modifier", "Variante créée : adaptez-la.")
}

func (s *Server) rateRecipe(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok || parseForm(w, r) != nil {
		s.notFound(w, r)
		return
	}
	back := "/recettes/" + strconv.FormatInt(id, 10)
	if b := r.PostFormValue("back"); b != "" {
		back = localPath(b)
	}
	rt := recipes.Rating{MemberID: formInt(r.PostFormValue("member"), 0), Stars: int(formInt(r.PostFormValue("stars"), 0)),
		Favorite: r.PostFormValue("favorite") != "", Banned: r.PostFormValue("banned") != "", Comment: r.PostFormValue("comment")}
	if err := s.Recipes.Rate(r.Context(), id, rt); err != nil {
		s.failed(w, r, err, back)
		return
	}
	s.redirect(w, r, back+"#avis", "Avis enregistré.")
}

func (s *Server) uploadRecipePhoto(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		s.notFound(w, r)
		return
	}
	back := "/recettes/" + strconv.FormatInt(id, 10)
	allowLongTransfer(w)
	r.Body = http.MaxBytesReader(w, r.Body, s.maxUpload+1<<20)
	file, _, err := r.FormFile("photo")
	if err != nil {
		s.redirect(w, r, back, "Photo illisible ou trop lourde.")
		return
	}
	defer func() { _ = file.Close() }()
	name, err := s.Photos.Save(file)
	if err != nil {
		s.failed(w, r, err, back)
		return
	}
	old, err := s.Recipes.SetPhoto(r.Context(), id, name)
	if err != nil {
		_ = s.Photos.Delete(name)
		s.failed(w, r, err, back)
		return
	}
	if old != "" && old != name {
		// Une variante peut partager la photo : on ne la supprime que si
		// plus aucune recette ne l'utilise.
		if n, _ := s.Recipes.PhotoUses(r.Context(), old); n == 0 {
			_ = s.Photos.Delete(old)
		}
	}
	s.redirect(w, r, back, "Photo enregistrée.")
}

func (s *Server) servePhoto(w http.ResponseWriter, r *http.Request) {
	p, err := s.Photos.Path(r.PathValue("name"))
	if errors.Is(err, photos.ErrNotFound) {
		s.notFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
	http.ServeFile(w, r, p)
}

func (s *Server) exportRecipe(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		s.notFound(w, r)
		return
	}
	ctx := r.Context()
	if r.PathValue("format") == "schema.json" {
		rec, err := s.Recipes.Get(ctx, id)
		if err != nil {
			s.failed(w, r, err, "/recettes")
			return
		}
		w.Header().Set("Content-Type", "application/ld+json; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="`+url.PathEscape(slug(rec.Title))+`.jsonld"`)
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		_ = enc.Encode(recipes.SchemaOrg(rec))
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="recette-`+strconv.FormatInt(id, 10)+`.json"`)
	if err := s.Recipes.Export(ctx, w, []int64{id}); err != nil {
		s.serverError(w, r, err)
	}
}

func slug(s string) string {
	return strings.ReplaceAll(catalog.Normalize(s), " ", "-")
}

// importView est la donnée de l'import de recettes.
type importView struct {
	Error string
}

func (s *Server) importRecipesPage(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, http.StatusOK, "recipe_import", "Importer des recettes", importView{})
}

func (s *Server) importRecipes(w http.ResponseWriter, r *http.Request) {
	allowLongTransfer(w)
	r.Body = http.MaxBytesReader(w, r.Body, s.maxUpload+1<<20)
	file, _, err := r.FormFile("file")
	if err != nil {
		s.render(w, r, http.StatusBadRequest, "recipe_import", "Importer des recettes", importView{Error: "Fichier manquant ou trop lourd."})
		return
	}
	defer func() { _ = file.Close() }()
	res, err := s.Recipes.Import(r.Context(), file)
	defer s.db.ReleaseMemory(r.Context())
	if err != nil {
		if msg, ok := s.inputError(w, r, err); ok {
			s.render(w, r, http.StatusUnprocessableEntity, "recipe_import", "Importer des recettes", importView{Error: msg})
		}
		return
	}
	msg := fmt.Sprintf("Import terminé : %s.", plural(res.Created, "recette créée", "recettes créées"))
	if res.Products > 0 {
		msg += fmt.Sprintf(" %s à compléter dans le catalogue.", plural(res.Products, "produit créé", "produits créés"))
	}
	if len(res.Skipped) > 0 {
		msg += fmt.Sprintf(" %s (titre déjà présent).", plural(len(res.Skipped), "recette ignorée", "recettes ignorées"))
	}
	s.redirect(w, r, "/recettes", msg)
}

func (s *Server) exportAllRecipes(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="petitchef-recettes-`+string(calendar.Today())+`.json"`)
	var ids []int64
	for _, v := range r.URL.Query()["id"] {
		if id := formInt(v, 0); id > 0 {
			ids = append(ids, id)
		}
	}
	if err := s.Recipes.Export(r.Context(), w, ids); err != nil {
		s.serverError(w, r, err)
	}
}

// gridView est la donnée de la grille de l'indice d'équilibre.
type gridView struct {
	Grid   recipes.Grid
	Groups []catalog.Group
	Error  string
}

func (s *Server) gridPage(w http.ResponseWriter, r *http.Request) {
	g, err := s.Recipes.Grid(r.Context())
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.render(w, r, http.StatusOK, "grid", "Grille de l'indice d'équilibre", gridView{Grid: g, Groups: catalog.Groups})
}

func (s *Server) saveGrid(w http.ResponseWriter, r *http.Request) {
	if parseForm(w, r) != nil {
		s.renderError(w, r, http.StatusBadRequest, "Formulaire illisible.")
		return
	}
	g := recipes.DefaultGrid()
	if r.PostFormValue("reset") == "" {
		for _, grp := range catalog.Groups {
			if v := r.PostFormValue("coef_" + string(grp)); v != "" {
				milli, err := parseSigned(v)
				if err != nil {
					s.render(w, r, http.StatusUnprocessableEntity, "grid", "Grille de l'indice d'équilibre", gridView{Grid: g, Groups: catalog.Groups, Error: grp.Label() + " : coefficient illisible."})
					return
				}
				g.Coef[grp] = int(milli / 100)
			}
		}
		for i := range g.Thresholds {
			g.Thresholds[i] = int(formInt(r.PostFormValue("seuil_"+strconv.Itoa(i)), int64(g.Thresholds[i])))
		}
	}
	if err := s.Recipes.SaveGrid(r.Context(), g); err != nil {
		if msg, ok := s.inputError(w, r, err); ok {
			s.render(w, r, http.StatusUnprocessableEntity, "grid", "Grille de l'indice d'équilibre", gridView{Grid: g, Groups: catalog.Groups, Error: msg})
		}
		return
	}
	s.redirect(w, r, "/equilibre", "Grille enregistrée : toutes les recettes ont été recalculées.")
}

// parseSigned lit un décimal signé (« -1,5 ») en millièmes.
func parseSigned(v string) (int64, error) {
	v = strings.TrimSpace(strings.ReplaceAll(v, "−", "-"))
	neg := strings.HasPrefix(v, "-")
	v = strings.TrimPrefix(strings.TrimPrefix(v, "-"), "+")
	if v == "0" {
		return 0, nil
	}
	m, err := qty.ParseDecimal(v)
	if neg {
		m = -m
	}
	return m, err
}

// householdView est la donnée de la page du foyer.
type householdView struct {
	Members []household.Member
	Edit    household.Member
	Error   string
}

func (s *Server) householdPage(w http.ResponseWriter, r *http.Request) {
	v := householdView{Edit: household.Member{Portion: 1000}}
	var err error
	if v.Members, err = s.Household.Members(r.Context(), false); err != nil {
		s.serverError(w, r, err)
		return
	}
	if id := formInt(r.URL.Query().Get("membre"), 0); id > 0 {
		for _, m := range v.Members {
			if m.ID == id {
				v.Edit = m
			}
		}
	}
	s.render(w, r, http.StatusOK, "household", "Foyer et membres", v)
}

func (s *Server) saveMember(w http.ResponseWriter, r *http.Request) {
	if parseForm(w, r) != nil {
		s.renderError(w, r, http.StatusBadRequest, "Formulaire illisible.")
		return
	}
	ctx := r.Context()
	m := household.Member{ID: formInt(r.PostFormValue("id"), 0), Name: r.PostFormValue("name")}
	portion, err := qty.ParseDecimal(r.PostFormValue("portion"))
	if err != nil {
		s.failed(w, r, validation.Error("Portion relative illisible (ex. 1 pour un adulte, 0,5 pour un enfant)."), "/foyer")
		return
	}
	m.Portion = portion
	for _, a := range catalog.AllAllergens {
		if r.PostFormValue("all_"+a.Code) != "" {
			m.Allergens |= a.Bit
		}
	}
	for _, d := range catalog.AllDiets {
		if r.PostFormValue("diet_"+d.Code) != "" {
			m.Diets |= d.Bit
		}
	}
	for _, name := range splitList(r.PostFormValue("refused")) {
		id, err := s.Catalog.Resolve(ctx, name)
		if err != nil {
			s.failed(w, r, validation.Errorf("Aliment refusé « %s » : introuvable dans le catalogue.", name), "/foyer")
			return
		}
		m.Refused = append(m.Refused, id)
	}
	if _, err := s.Household.Save(ctx, m); err != nil {
		s.failed(w, r, err, "/foyer")
		return
	}
	s.redirect(w, r, "/foyer", "Membre « "+strings.TrimSpace(m.Name)+" » enregistré.")
}

func (s *Server) deleteMember(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		s.notFound(w, r)
		return
	}
	if err := s.Household.Delete(r.Context(), id); err != nil {
		s.failed(w, r, err, "/foyer")
		return
	}
	s.redirect(w, r, "/foyer", "Membre retiré du foyer.")
}
