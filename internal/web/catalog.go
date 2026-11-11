package web

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/codecolors/petitchef/internal/catalog"
	"github.com/codecolors/petitchef/internal/platform/db"
	"github.com/codecolors/petitchef/internal/platform/qty"
	"github.com/codecolors/petitchef/internal/platform/validation"
)

// catalogView est la donnée de la liste du catalogue.
type catalogView struct {
	Filter    catalog.Filter
	Page      catalog.ListPage
	Aisles    []catalog.Aisle
	Universes []catalog.Universe
	Query     string // chaîne de requête courante, pour la pagination
}

func catalogFilter(r *http.Request) catalog.Filter {
	q := r.URL.Query()
	return catalog.Filter{
		Query:      q.Get("q"),
		Universe:   catalog.Universe(q.Get("univers")),
		AisleID:    formInt(q.Get("rayon"), 0),
		Incomplete: q.Get("incomplets") != "",
		Page:       int(formInt(q.Get("page"), 0)),
	}
}

func (s *Server) catalogPage(w http.ResponseWriter, r *http.Request) {
	f := catalogFilter(r)
	v := catalogView{Filter: f, Universes: catalog.Universes}
	var err error
	if v.Page, err = s.Catalog.List(r.Context(), f); err != nil {
		s.serverError(w, r, err)
		return
	}
	if v.Aisles, err = s.Catalog.Aisles(r.Context()); err != nil {
		s.serverError(w, r, err)
		return
	}
	q := r.URL.Query()
	q.Del("page")
	v.Query = q.Encode()
	if r.Header.Get("HX-Request") == "true" && r.Header.Get("HX-Target") == "resultats" {
		s.renderBlock(w, r, "catalog", "results", v)
		return
	}
	s.render(w, r, http.StatusOK, "catalog", "Catalogue", v)
}

// suggestions renvoie les options d'une liste de suggestions (<datalist>)
// pour un champ produit : la saisie libre reste possible, le produit est
// alors créé à la volée.
func (s *Server) productSuggestions(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	for k, vals := range r.URL.Query() {
		if k != "q" && (strings.HasPrefix(k, "produit") || strings.HasPrefix(k, "ing_product_")) && len(vals) > 0 && vals[0] != "" {
			q = vals[0]
		}
	}
	hits, err := s.Catalog.Search(r.Context(), q, 12)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.renderBlock(w, r, "catalog", "suggestions", hits)
}

// productForm est la donnée du formulaire d'un produit.
type productForm struct {
	Product   catalog.Product
	New       bool
	Error     string
	Aisles    []catalog.Aisle
	Locations []catalog.Location
	Stores    []catalog.Store
	Prices    []catalog.Price
	Back      string
	// Champs saisis sous forme de texte.
	Synonyms, Units, Packages, Barcodes string
	SeasonFrom, SeasonTo                int
}

func (s *Server) newProductPage(w http.ResponseWriter, r *http.Request) {
	p := catalog.Product{Universe: catalog.Food, Unit: qty.Gram, DateKind: catalog.DLC, ShelfClosed: -1, ShelfOpened: -1, ShelfFrozen: -1,
		Nutrition: catalog.Nutrition{Kcal: -1, Proteins: -1, Carbs: -1, Fat: -1, Fibres: -1}}
	if name := r.URL.Query().Get("nom"); name != "" {
		p.Name = name
		if sg, err := s.Catalog.Suggest(r.Context(), name); err == nil {
			p.AisleID, p.Unit, p.Universe, p.Group = sg.AisleID, sg.Unit, sg.Universe, sg.Group
		}
	}
	s.renderProductForm(w, r, http.StatusOK, productForm{Product: p, New: true})
}

func (s *Server) editProductPage(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		s.notFound(w, r)
		return
	}
	p, err := s.Catalog.Get(r.Context(), id)
	if err != nil {
		s.failed(w, r, err, "/catalogue")
		return
	}
	f := productForm{Product: p, Back: r.URL.Query().Get("retour")}
	if f.Prices, err = s.Catalog.Prices(r.Context(), id, 10); err != nil {
		s.serverError(w, r, err)
		return
	}
	s.renderProductForm(w, r, http.StatusOK, f)
}

func (s *Server) renderProductForm(w http.ResponseWriter, r *http.Request, status int, f productForm) {
	ctx := r.Context()
	var err error
	if f.Aisles, err = s.Catalog.Aisles(ctx); err == nil {
		if f.Locations, err = s.Catalog.Locations(ctx); err == nil {
			f.Stores, err = s.Catalog.Stores(ctx)
		}
	}
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	p := f.Product
	if f.Synonyms == "" && f.Error == "" {
		f.Synonyms = strings.Join(p.Synonyms, ", ")
		f.Barcodes = strings.Join(p.Barcodes, ", ")
		var units, packs []string
		for _, u := range p.Units {
			units = append(units, fmt.Sprintf("%s=%s", u.Unit, qty.FormatInput(u.Qty, p.Unit)))
		}
		for _, pk := range p.Packages {
			item := qty.FormatInput(pk.Qty, p.Unit)
			if pk.Label != "" {
				item += " : " + pk.Label
			}
			packs = append(packs, item)
		}
		f.Units, f.Packages = strings.Join(units, ", "), strings.Join(packs, "\n")
		f.SeasonFrom, f.SeasonTo = seasonBounds(p.Season)
	}
	title := "Nouveau produit"
	if !f.New {
		title = p.Name
	}
	s.render(w, r, status, "product_form", title, f)
}

// seasonBounds renvoie le premier et le dernier mois d'une saison continue
// (0, 0 si aucune).
func seasonBounds(m catalog.Months) (int, int) {
	if m == 0 {
		return 0, 0
	}
	if m == catalog.AllYear {
		return 1, 12
	}
	for i := 1; i <= 12; i++ {
		prev := (i+10)%12 + 1
		if m.Has(i) && !m.Has(prev) {
			end := i
			for m.Has(end%12 + 1) {
				end = end%12 + 1
			}
			return i, end
		}
	}
	return 0, 0
}

// parseProductForm lit le formulaire d'un produit.
func parseProductForm(r *http.Request, p *catalog.Product, f *productForm) error {
	p.Name = r.PostFormValue("name")
	p.Universe = catalog.Universe(r.PostFormValue("universe"))
	p.AisleID = formInt(r.PostFormValue("aisle"), 0)
	p.LocationID = formInt(r.PostFormValue("location"), 0)
	p.StoreID = formInt(r.PostFormValue("store"), 0)
	p.Unit = qty.Base(r.PostFormValue("unit"))
	p.PieceG = formInt(r.PostFormValue("piece_g"), 0)
	p.Density = formInt(r.PostFormValue("density"), 0)
	p.ShelfClosed = formInt(r.PostFormValue("shelf_closed"), -1)
	p.ShelfOpened = formInt(r.PostFormValue("shelf_opened"), -1)
	p.ShelfFrozen = formInt(r.PostFormValue("shelf_frozen"), -1)
	p.DateKind = catalog.DateKind(r.PostFormValue("date_kind"))
	p.Group = catalog.Group(r.PostFormValue("group"))
	p.Brand = strings.TrimSpace(r.PostFormValue("brand"))
	p.Staple = r.PostFormValue("staple") != ""
	p.Incomplete = false
	p.Nutrition = catalog.Nutrition{
		Kcal: formInt(r.PostFormValue("kcal"), -1), Proteins: tenths(r.PostFormValue("proteins")), Carbs: tenths(r.PostFormValue("carbs")),
		Fat: tenths(r.PostFormValue("fat")), Fibres: tenths(r.PostFormValue("fibres")),
	}
	p.Allergens = 0
	for _, a := range catalog.AllAllergens {
		if r.PostFormValue("all_"+a.Code) != "" {
			p.Allergens |= a.Bit
		}
	}
	p.Traits = 0
	for code, bit := range map[string]catalog.Traits{"meat": catalog.Meat, "animal": catalog.Animal, "pork": catalog.Pork, "alcohol": catalog.Alcohol} {
		if r.PostFormValue("trait_"+code) != "" {
			p.Traits |= bit
		}
	}
	f.SeasonFrom, f.SeasonTo = int(formInt(r.PostFormValue("season_from"), 0)), int(formInt(r.PostFormValue("season_to"), 0))
	p.Season = 0
	if f.SeasonFrom >= 1 && f.SeasonFrom <= 12 && f.SeasonTo >= 1 && f.SeasonTo <= 12 {
		p.Season = catalog.MonthRange(f.SeasonFrom, f.SeasonTo)
	}
	f.Synonyms, f.Units, f.Packages, f.Barcodes = r.PostFormValue("synonyms"), r.PostFormValue("units"), r.PostFormValue("packages"), r.PostFormValue("barcodes")
	p.Synonyms = splitList(f.Synonyms)
	p.Barcodes = splitList(f.Barcodes)
	p.Units = nil
	for _, item := range splitList(f.Units) {
		u, v, ok := strings.Cut(item, "=")
		code := qty.Canonical(u)
		n, err := baseQty(v, p.Unit)
		if !ok || code == "" || err != nil || n <= 0 {
			return validation.Errorf("Conversion « %s » illisible : écrivez par exemple « cs=15, gousse=5 ».", item)
		}
		p.Units = append(p.Units, catalog.UnitConv{Unit: code, Qty: n})
	}
	p.Packages = nil
	for _, line := range strings.Split(f.Packages, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		v, label, _ := strings.Cut(line, ":")
		n, err := baseQty(v, p.Unit)
		if err != nil || n <= 0 {
			return validation.Errorf("Conditionnement « %s » illisible : une quantité par ligne, libellé facultatif après « : ».", line)
		}
		p.Packages = append(p.Packages, catalog.Package{Qty: n, Label: strings.TrimSpace(label)})
	}
	return nil
}

// tenths lit une valeur nutritionnelle en grammes (« 12,5 ») et la renvoie
// en dixièmes ; -1 si vide.
func tenths(v string) int64 {
	milli, err := formQty(v)
	if err != nil || strings.TrimSpace(v) == "" {
		return -1
	}
	return milli / 100
}

func (s *Server) saveProduct(w http.ResponseWriter, r *http.Request) {
	if parseForm(w, r) != nil {
		s.renderError(w, r, http.StatusBadRequest, "Formulaire illisible.")
		return
	}
	ctx := r.Context()
	f := productForm{Back: r.PostFormValue("back")}
	id, isEdit := pathID(r)
	if isEdit {
		p, err := s.Catalog.Get(ctx, id)
		if err != nil {
			s.failed(w, r, err, "/catalogue")
			return
		}
		f.Product = p
	} else {
		f.New = true
	}
	err := parseProductForm(r, &f.Product, &f)
	if err == nil {
		if isEdit {
			err = s.Catalog.Update(ctx, f.Product)
		} else {
			f.Product.ID, err = s.Catalog.Create(ctx, f.Product)
		}
	}
	if err == nil {
		for _, code := range f.Product.Barcodes {
			if err = s.Catalog.AttachBarcode(ctx, f.Product.ID, code); err != nil {
				break
			}
		}
	}
	if err != nil {
		if msg, ok := s.inputError(w, r, err); ok {
			f.Error = msg
			s.renderProductForm(w, r, http.StatusUnprocessableEntity, f)
		}
		return
	}
	back := localPath(f.Back)
	if f.Back == "" {
		back = "/catalogue?q=" + url.QueryEscape(f.Product.Name)
	}
	s.redirect(w, r, back, "Produit « "+f.Product.Name+" » enregistré.")
}

func (s *Server) archiveProduct(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		s.notFound(w, r)
		return
	}
	if err := s.Catalog.Archive(r.Context(), id); err != nil {
		s.failed(w, r, err, "/catalogue")
		return
	}
	s.redirect(w, r, "/catalogue", "Produit retiré du catalogue.")
}

// storesView est la donnée de la page des magasins.
type storesView struct {
	Stores   []catalog.Store
	Selected catalog.Store
	Aisles   []catalog.Aisle
	Error    string
}

func (s *Server) storesPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var v storesView
	var err error
	if v.Stores, err = s.Catalog.Stores(ctx); err != nil {
		s.serverError(w, r, err)
		return
	}
	sel := formInt(r.URL.Query().Get("magasin"), 0)
	for _, st := range v.Stores {
		if st.ID == sel || (sel == 0 && v.Selected.ID == 0) {
			v.Selected = st
		}
	}
	if v.Aisles, err = s.Catalog.StoreAisles(ctx, v.Selected.ID); err != nil {
		s.serverError(w, r, err)
		return
	}
	s.render(w, r, http.StatusOK, "stores", "Magasins et rayons", v)
}

func (s *Server) saveStore(w http.ResponseWriter, r *http.Request) {
	if parseForm(w, r) != nil {
		s.renderError(w, r, http.StatusBadRequest, "Formulaire illisible.")
		return
	}
	id := formInt(r.PostFormValue("id"), 0)
	id, err := s.Catalog.SaveStore(r.Context(), id, r.PostFormValue("name"), r.PostFormValue("default") != "")
	if err != nil {
		s.failed(w, r, err, "/magasins")
		return
	}
	s.redirect(w, r, "/magasins?magasin="+strconv.FormatInt(id, 10), "Magasin enregistré.")
}

func (s *Server) deleteStore(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		s.notFound(w, r)
		return
	}
	if err := s.Catalog.DeleteStore(r.Context(), id); err != nil {
		s.failed(w, r, err, "/magasins")
		return
	}
	s.redirect(w, r, "/magasins", "Magasin supprimé.")
}

// moveAisle monte ou descend un rayon dans le parcours d'un magasin, ou
// enregistre l'ordre complet envoyé par le glisser-déposer (champ order).
func (s *Server) moveAisle(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok || parseForm(w, r) != nil {
		s.notFound(w, r)
		return
	}
	var err error
	if order := r.PostFormValue("order"); order != "" {
		var ids []int64
		for _, v := range strings.Split(order, ",") {
			if n := formInt(v, 0); n > 0 {
				ids = append(ids, n)
			}
		}
		err = s.Catalog.SetStoreOrder(r.Context(), id, ids)
	} else {
		delta := 1
		if r.PostFormValue("dir") == "up" {
			delta = -1
		}
		err = s.Catalog.MoveAisle(r.Context(), id, formInt(r.PostFormValue("aisle"), 0), delta)
	}
	if err != nil {
		s.failed(w, r, err, "/magasins")
		return
	}
	if r.Header.Get("HX-Request") == "true" {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	s.redirect(w, r, fmt.Sprintf("/magasins?magasin=%d#rayon-%s", id, r.PostFormValue("aisle")), "")
}

// locationsView est la donnée de la page des emplacements.
type locationsView struct {
	Locations []catalog.Location
}

func (s *Server) locationsPage(w http.ResponseWriter, r *http.Request) {
	locs, err := s.Catalog.Locations(r.Context())
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.render(w, r, http.StatusOK, "locations", "Emplacements", locationsView{Locations: locs})
}

func (s *Server) saveLocation(w http.ResponseWriter, r *http.Request) {
	if parseForm(w, r) != nil {
		s.renderError(w, r, http.StatusBadRequest, "Formulaire illisible.")
		return
	}
	if err := s.Catalog.SaveLocation(r.Context(), formInt(r.PostFormValue("id"), 0), r.PostFormValue("name"), r.PostFormValue("kind")); err != nil {
		s.failed(w, r, err, "/emplacements")
		return
	}
	s.redirect(w, r, "/emplacements", "Emplacement enregistré.")
}

func (s *Server) deleteLocation(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		s.notFound(w, r)
		return
	}
	if err := s.Catalog.DeleteLocation(r.Context(), id); err != nil {
		s.failed(w, r, err, "/emplacements")
		return
	}
	s.redirect(w, r, "/emplacements", "Emplacement supprimé.")
}

// resolveProduct trouve le produit désigné par un champ de formulaire :
// identifiant (champ <name>_id) ou nom saisi, créé à la volée s'il est
// inconnu (marqué « à compléter »).
func (s *Server) resolveProduct(r *http.Request, field string) (int64, bool, error) {
	if id := formInt(r.PostFormValue(field+"_id"), 0); id > 0 {
		return id, false, nil
	}
	name := strings.TrimSpace(r.PostFormValue(field))
	if name == "" {
		return 0, false, validation.Error("Choisissez ou saisissez un produit.")
	}
	id, err := s.Catalog.Resolve(r.Context(), name)
	if err == nil {
		return id, false, nil
	}
	if !errors.Is(err, db.ErrNotFound) {
		return 0, false, err
	}
	id, err = s.Catalog.QuickCreate(r.Context(), name)
	return id, true, err
}
