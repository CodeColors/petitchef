package web

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/codecolors/petitchef/internal/catalog"
	"github.com/codecolors/petitchef/internal/platform/calendar"
	"github.com/codecolors/petitchef/internal/platform/qty"
	"github.com/codecolors/petitchef/internal/shopping"
	"github.com/codecolors/petitchef/internal/stock"
)

// aisleGroup regroupe les lignes d'un rayon.
type aisleGroup struct {
	Name  string
	Items []shopping.Item
	Cost  int64
	Left  int
}

// storeGroup regroupe les rayons d'un magasin (répartition par magasin).
type storeGroup struct {
	Name   string
	Aisles []aisleGroup
}

// shoppingView est la donnée de la liste de courses.
type shoppingView struct {
	List      shopping.List
	Groups    []storeGroup
	Stores    []catalog.Store
	Schedule  shopping.Schedule
	Next      calendar.Date
	Until     calendar.Date
	Total     int64
	Known     int
	Left      int
	Checked   int
	Split     bool
	StoreMode bool
	Weekdays  []int
}

func (v shoppingView) bodyClass() string {
	if v.StoreMode {
		return "store-mode"
	}
	return ""
}

func (s *Server) shoppingData(r *http.Request) (shoppingView, error) {
	ctx := r.Context()
	v := shoppingView{Split: r.URL.Query().Get("par") == "magasin", Weekdays: seq(7)}
	var err error
	if v.List, err = s.Shopping.Current(ctx); err != nil {
		return v, err
	}
	if v.Stores, err = s.Catalog.Stores(ctx); err != nil {
		return v, err
	}
	if v.Schedule, err = s.Shopping.Schedule(ctx); err != nil {
		return v, err
	}
	v.Next, v.Until = v.Schedule.Dates(calendar.Today())
	order, err := s.Shopping.Order(ctx, v.List.StoreID)
	if err != nil {
		return v, err
	}
	shopping.SortItems(v.List.Items, order)
	storeNames := map[int64]string{}
	for _, st := range v.Stores {
		storeNames[st.ID] = st.Name
	}
	groups := map[string]*storeGroup{}
	var keys []string
	for _, it := range v.List.Items {
		key := "Tous les rayons"
		if v.Split {
			key = "Magasin habituel"
			if n := storeNames[it.StoreID]; n != "" && it.StoreID != v.List.StoreID {
				key = n
			}
		}
		g, ok := groups[key]
		if !ok {
			g = &storeGroup{Name: key}
			groups[key] = g
			keys = append(keys, key)
		}
		if len(g.Aisles) == 0 || g.Aisles[len(g.Aisles)-1].Name != it.Aisle {
			g.Aisles = append(g.Aisles, aisleGroup{Name: it.Aisle})
		}
		a := &g.Aisles[len(g.Aisles)-1]
		a.Items = append(a.Items, it)
		if it.Cost >= 0 {
			a.Cost += it.Cost
			v.Total += it.Cost
			v.Known++
		}
		if it.Checked {
			v.Checked++
		} else {
			v.Left++
			a.Left++
		}
	}
	for _, k := range keys {
		v.Groups = append(v.Groups, *groups[k])
	}
	return v, nil
}

func (s *Server) shoppingPage(w http.ResponseWriter, r *http.Request) {
	v, err := s.shoppingData(r)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.render(w, r, http.StatusOK, "shopping", "Courses", v)
}

func (s *Server) storeModePage(w http.ResponseWriter, r *http.Request) {
	v, err := s.shoppingData(r)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	v.StoreMode = true
	w.Header().Set("Cache-Control", "no-cache")
	s.render(w, r, http.StatusOK, "shopping_store", "En magasin", v)
}

func (s *Server) recomputeShopping(w http.ResponseWriter, r *http.Request) {
	if parseForm(w, r) != nil {
		s.renderError(w, r, http.StatusBadRequest, "Formulaire illisible.")
		return
	}
	var until calendar.Date
	if v := r.PostFormValue("until"); v != "" {
		d, err := calendar.Parse(v)
		if err != nil {
			s.failed(w, r, err, "/courses")
			return
		}
		until = d
	}
	l, err := s.Shopping.Recompute(r.Context(), until)
	defer s.db.ReleaseMemory(r.Context())
	if err != nil {
		s.failed(w, r, err, "/courses")
		return
	}
	s.redirect(w, r, "/courses", fmt.Sprintf("Liste recalculée jusqu'au %s : %s. Les cases cochées et vos ajouts sont conservés.",
		l.Until.Format(), plural(len(l.Items), "ligne", "lignes")))
}

func (s *Server) addShoppingItem(w http.ResponseWriter, r *http.Request) {
	if parseForm(w, r) != nil {
		s.renderError(w, r, http.StatusBadRequest, "Formulaire illisible.")
		return
	}
	ctx := r.Context()
	back := "/courses"
	if b := r.PostFormValue("back"); b != "" {
		back = localPath(b)
	}
	id, created, err := s.resolveProduct(r, "produit")
	if err != nil {
		s.failed(w, r, err, back)
		return
	}
	p, err := s.Catalog.Get(ctx, id)
	if err != nil {
		s.failed(w, r, err, back)
		return
	}
	var q int64
	if v := strings.TrimSpace(r.PostFormValue("qty")); v != "" {
		if q, err = baseQty(v, p.Unit); err != nil {
			s.failed(w, r, err, back)
			return
		}
	}
	if err := s.Shopping.AddManual(ctx, id, q); err != nil {
		s.failed(w, r, err, back)
		return
	}
	msg := p.Name + " ajouté à la liste."
	if created {
		msg += " Nouveau produit, rangé dans « " + p.AisleName + " »."
	}
	s.redirect(w, r, back+"#ajout", msg)
}

// toggleItem coche ou décoche une ligne ; appelée par formulaire (sans
// script) ou par fetch depuis le mode magasin (réponse 204), y compris
// pour rejouer des gestes faits hors ligne.
func (s *Server) toggleItem(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok || parseForm(w, r) != nil {
		s.notFound(w, r)
		return
	}
	price := int64(-1)
	if v := strings.TrimSpace(r.PostFormValue("price")); v != "" {
		p, err := parseEuros(v)
		if err != nil {
			s.failed(w, r, err, "/courses/magasin")
			return
		}
		price = p
	}
	if err := s.Shopping.Toggle(r.Context(), id, r.PostFormValue("checked") == "1", price); err != nil {
		if r.Header.Get("X-Requested-With") == "fetch" {
			http.Error(w, "introuvable", http.StatusNotFound)
			return
		}
		s.failed(w, r, err, "/courses")
		return
	}
	if r.Header.Get("X-Requested-With") == "fetch" {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	back := "/courses"
	if b := r.PostFormValue("back"); b != "" {
		back = localPath(b)
	}
	s.redirect(w, r, back+"#ligne-"+strconv.FormatInt(id, 10), "")
}

func (s *Server) removeShoppingItem(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		s.notFound(w, r)
		return
	}
	if err := s.Shopping.Remove(r.Context(), id); err != nil {
		s.failed(w, r, err, "/courses")
		return
	}
	s.redirect(w, r, "/courses", "Ligne retirée de la liste.")
}

func (s *Server) chooseStore(w http.ResponseWriter, r *http.Request) {
	if parseForm(w, r) != nil {
		s.renderError(w, r, http.StatusBadRequest, "Formulaire illisible.")
		return
	}
	if err := s.Shopping.SetStore(r.Context(), formInt(r.PostFormValue("store"), 0)); err != nil {
		s.failed(w, r, err, "/courses")
		return
	}
	back := localPath(r.PostFormValue("back"))
	if r.PostFormValue("back") == "" {
		back = "/courses"
	}
	s.redirect(w, r, back, "Liste triée dans l'ordre de ce magasin.")
}

func (s *Server) saveSchedule(w http.ResponseWriter, r *http.Request) {
	if parseForm(w, r) != nil {
		s.renderError(w, r, http.StatusBadRequest, "Formulaire illisible.")
		return
	}
	sc := shopping.Schedule{Weekday: int(formInt(r.PostFormValue("weekday"), 5)), Every: int(formInt(r.PostFormValue("every"), 7))}
	if err := s.Shopping.SaveSchedule(r.Context(), sc); err != nil {
		s.failed(w, r, err, "/courses")
		return
	}
	s.redirect(w, r, "/courses", "Jour des courses enregistré : recalculez la liste pour en tenir compte.")
}

func (s *Server) shoppingText(w http.ResponseWriter, r *http.Request) {
	v, err := s.shoppingData(r)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	if r.URL.Query().Get("telecharger") != "" {
		w.Header().Set("Content-Disposition", `attachment; filename="liste-de-courses.txt"`)
	}
	_, _ = w.Write([]byte(shopping.Text(v.List.Items, v.List.Until)))
}

// putAwayLine est une ligne de l'écran « ranger les courses ».
type putAwayLine struct {
	shopping.Item
	BestBefore calendar.Date
	Qty        string
	Price      string
}

// putAwayView est la donnée de l'écran « ranger les courses ».
type putAwayView struct {
	Lines     []putAwayLine
	Locations []catalog.Location
	Left      int
}

func (s *Server) putAwayPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	l, err := s.Shopping.Current(ctx)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	v := putAwayView{}
	if v.Locations, err = s.Catalog.Locations(ctx); err != nil {
		s.serverError(w, r, err)
		return
	}
	kinds := map[int64]string{}
	for _, loc := range v.Locations {
		kinds[loc.ID] = loc.Kind
	}
	ids := make([]int64, 0, len(l.Items))
	for _, it := range l.Items {
		ids = append(ids, it.ProductID)
	}
	products, err := s.Catalog.Many(ctx, ids)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	today := calendar.Today()
	for _, it := range l.Items {
		if !it.Checked {
			v.Left++
			continue
		}
		line := putAwayLine{Item: it, Qty: qty.FormatInput(it.Buy, it.Unit),
			BestBefore: stock.SuggestBestBefore(products[it.ProductID], kinds[it.LocationID], today)}
		if it.PriceCents >= 0 {
			line.Price = moneyInput(it.PriceCents)
		}
		v.Lines = append(v.Lines, line)
	}
	s.render(w, r, http.StatusOK, "shopping_putaway", "Ranger les courses", v)
}

func (s *Server) putAway(w http.ResponseWriter, r *http.Request) {
	if parseForm(w, r) != nil {
		s.renderError(w, r, http.StatusBadRequest, "Formulaire illisible.")
		return
	}
	ctx := r.Context()
	l, err := s.Shopping.Current(ctx)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	var lines []shopping.PutAway
	for _, it := range l.Items {
		if !it.Checked {
			continue
		}
		key := strconv.FormatInt(it.ID, 10)
		pa := shopping.PutAway{ItemID: it.ID, LocationID: formInt(r.PostFormValue("location_"+key), it.LocationID), PriceCents: -1}
		if v := strings.TrimSpace(r.PostFormValue("qty_" + key)); v != "" && v != "0" {
			if pa.Qty, err = baseQty(v, it.Unit); err != nil {
				s.failed(w, r, fmt.Errorf("%s : %w", it.Name, err), "/courses/ranger")
				return
			}
		}
		if v := r.PostFormValue("best_" + key); v != "" {
			d, err := calendar.Parse(v)
			if err != nil {
				s.failed(w, r, err, "/courses/ranger")
				return
			}
			pa.BestBefore = d
		}
		if v := strings.TrimSpace(r.PostFormValue("price_" + key)); v != "" {
			if pa.PriceCents, err = parseEuros(v); err != nil {
				s.failed(w, r, err, "/courses/ranger")
				return
			}
		}
		lines = append(lines, pa)
	}
	n, err := s.Shopping.Store(ctx, lines, currentUser(ctx).ID)
	if err != nil {
		s.failed(w, r, err, "/courses/ranger")
		return
	}
	s.redirect(w, r, "/stock", fmt.Sprintf("Courses rangées : %s en stock. Les produits non trouvés restent sur la liste.", plural(n, "produit", "produits")))
}

func moneyInput(c int64) string {
	return strconv.FormatInt(c/100, 10) + "," + fmt.Sprintf("%02d", c%100)
}
