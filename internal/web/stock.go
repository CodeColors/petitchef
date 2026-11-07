package web

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/codecolors/petitchef/internal/catalog"
	"github.com/codecolors/petitchef/internal/platform/calendar"
	"github.com/codecolors/petitchef/internal/platform/db"
	"github.com/codecolors/petitchef/internal/platform/qty"
	"github.com/codecolors/petitchef/internal/platform/validation"
	"github.com/codecolors/petitchef/internal/stock"
)

// stockView est la donnée des listes de stock.
type stockView struct {
	Filter    stock.Filter
	Page      stock.Page
	Locations []catalog.Location
	Aisles    []catalog.Aisle
	Today     calendar.Date
	Last      *stock.LastAction
	Query     string
	Tab       string // tout, priorite
	Back      string // adresse de retour des gestes rapides
}

func (s *Server) lastAction(r *http.Request) *stock.LastAction {
	a, ok, err := s.Stock.Last(r.Context(), currentUser(r.Context()).ID)
	if err != nil || !ok {
		return nil
	}
	return &a
}

func (s *Server) stockPage(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := stock.Filter{
		LocationID: formInt(q.Get("emplacement"), 0),
		Universe:   catalog.Universe(q.Get("univers")),
		AisleID:    formInt(q.Get("rayon"), 0),
		Query:      q.Get("q"),
		Page:       int(formInt(q.Get("page"), 0)),
		Priority:   r.URL.Path == "/stock/priorite",
	}
	v := stockView{Filter: f, Today: calendar.Today(), Last: s.lastAction(r), Tab: "tout", Back: r.URL.RequestURI()}
	if f.Priority {
		v.Tab = "priorite"
	}
	ctx := r.Context()
	var err error
	if v.Page, err = s.Stock.List(ctx, f); err == nil {
		if v.Locations, err = s.Catalog.Locations(ctx); err == nil {
			v.Aisles, err = s.Catalog.Aisles(ctx)
		}
	}
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	q.Del("page")
	v.Query = q.Encode()
	if r.Header.Get("HX-Request") == "true" && r.Header.Get("HX-Target") == "resultats" {
		s.renderBlock(w, r, "stock", "results", v)
		return
	}
	title := "Stock"
	if f.Priority {
		title = "À consommer en priorité"
	}
	s.render(w, r, http.StatusOK, "stock", title, v)
}

// addStockForm est la donnée de l'ajout rapide au stock.
type addStockForm struct {
	ProductName string
	Product     *catalog.Product
	Locations   []catalog.Location
	LocationID  int64
	Qty         string
	Packages    string // nombre de conditionnements
	PackageID   int64
	BestBefore  calendar.Date
	Price       string
	Error       string
	Last        *stock.LastAction
	Scanned     string // code-barres scanné, à associer
}

func (s *Server) addStockPage(w http.ResponseWriter, r *http.Request) {
	f := addStockForm{Last: s.lastAction(r), Scanned: r.URL.Query().Get("code")}
	if id := formInt(r.URL.Query().Get("produit_id"), 0); id > 0 {
		if err := s.fillAddStock(r, &f, id); err != nil {
			s.failed(w, r, err, "/stock")
			return
		}
	} else if name := r.URL.Query().Get("produit"); name != "" {
		f.ProductName = name
		if id, err := s.Catalog.Resolve(r.Context(), name); err == nil {
			_ = s.fillAddStock(r, &f, id)
		}
	}
	if r.Header.Get("HX-Request") == "true" && r.Header.Get("HX-Target") == "details" {
		var err error
		if f.Locations, err = s.Catalog.Locations(r.Context()); err != nil {
			s.serverError(w, r, err)
			return
		}
		s.renderBlock(w, r, "stock_add", "details", f)
		return
	}
	s.renderAddStock(w, r, http.StatusOK, f)
}

// fillAddStock préremplit l'ajout d'après un produit : emplacement par
// défaut, date limite proposée, conditionnement.
func (s *Server) fillAddStock(r *http.Request, f *addStockForm, id int64) error {
	p, err := s.Catalog.Get(r.Context(), id)
	if err != nil {
		return err
	}
	f.Product, f.ProductName = &p, p.Name
	if f.LocationID == 0 {
		f.LocationID = p.LocationID
	}
	kind := ""
	locs, err := s.Catalog.Locations(r.Context())
	if err != nil {
		return err
	}
	for _, l := range locs {
		if l.ID == f.LocationID {
			kind = l.Kind
		}
	}
	f.BestBefore = stock.SuggestBestBefore(p, kind, calendar.Today())
	if len(p.Packages) > 0 && f.Qty == "" && f.Packages == "" {
		f.Packages, f.PackageID = "1", p.Packages[0].ID
	} else if f.Qty == "" && p.Unit == qty.Piece {
		f.Qty = "1"
	}
	return nil
}

func (s *Server) renderAddStock(w http.ResponseWriter, r *http.Request, status int, f addStockForm) {
	var err error
	if f.Locations, err = s.Catalog.Locations(r.Context()); err != nil {
		s.serverError(w, r, err)
		return
	}
	s.render(w, r, status, "stock_add", "Ajouter au stock", f)
}

func (s *Server) addStock(w http.ResponseWriter, r *http.Request) {
	if parseForm(w, r) != nil {
		s.renderError(w, r, http.StatusBadRequest, "Formulaire illisible.")
		return
	}
	ctx := r.Context()
	f := addStockForm{ProductName: r.PostFormValue("produit"), Qty: r.PostFormValue("qty"), Packages: r.PostFormValue("packages"),
		PackageID: formInt(r.PostFormValue("package"), 0), LocationID: formInt(r.PostFormValue("location"), 0),
		BestBefore: calendar.Date(r.PostFormValue("best_before")), Price: r.PostFormValue("price"), Scanned: r.PostFormValue("code")}
	err := func() error {
		id, created, err := s.resolveProduct(r, "produit")
		if err != nil {
			return err
		}
		p, err := s.Catalog.Get(ctx, id)
		if err != nil {
			return err
		}
		f.Product = &p
		if f.Scanned != "" {
			if err := s.Catalog.AttachBarcode(ctx, id, f.Scanned); err != nil {
				return err
			}
		}
		in := stock.AddInput{ProductID: id, LocationID: f.LocationID, PriceCents: -1, Origin: stock.Manual, Date: calendar.Today(),
			UserID: currentUser(ctx).ID}
		if in.LocationID == 0 {
			in.LocationID = p.LocationID
		}
		switch {
		case strings.TrimSpace(f.Packages) != "" && f.PackageID > 0:
			n, err := qty.ParseDecimal(f.Packages)
			if err != nil {
				return err
			}
			for _, pk := range p.Packages {
				if pk.ID == f.PackageID {
					in.Qty = pk.Qty * n / 1000
				}
			}
		case strings.TrimSpace(f.Qty) != "":
			if in.Qty, err = baseQty(f.Qty, p.Unit); err != nil {
				return err
			}
		}
		if in.Qty <= 0 {
			return validation.Error("Indiquez une quantité ou un nombre de conditionnements.")
		}
		if f.BestBefore != "" {
			if _, err := calendar.Parse(string(f.BestBefore)); err != nil {
				return err
			}
			in.BestBefore = f.BestBefore
		} else if r.PostFormValue("no_date") == "" {
			kind := ""
			if locs, err := s.Catalog.Locations(ctx); err == nil {
				for _, l := range locs {
					if l.ID == in.LocationID {
						kind = l.Kind
					}
				}
			}
			in.BestBefore = stock.SuggestBestBefore(p, kind, in.Date)
		}
		if strings.TrimSpace(f.Price) != "" {
			if in.PriceCents, err = parseEuros(f.Price); err != nil {
				return err
			}
			in.StoreID, _ = s.Catalog.DefaultStore(ctx)
		}
		if _, _, err := s.Stock.Add(ctx, in); err != nil {
			return err
		}
		msg := fmt.Sprintf("Ajouté : %s de %s.", qty.Format(in.Qty, p.Unit), p.Name)
		if created {
			msg += " Nouveau produit créé dans le rayon « " + p.AisleName + " » : pensez à compléter sa fiche."
		}
		s.redirect(w, r, "/stock/ajouter", msg)
		return nil
	}()
	if err != nil {
		if msg, ok := s.inputError(w, r, err); ok {
			f.Error = msg
			s.renderAddStock(w, r, http.StatusUnprocessableEntity, f)
		}
	}
}

// itemAction applique un geste rapide sur un lot et revient à la page
// d'origine, avec la possibilité d'annuler.
func (s *Server) itemAction(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok || parseForm(w, r) != nil {
		s.notFound(w, r)
		return
	}
	ctx := r.Context()
	back := localPath(r.PostFormValue("back"))
	if r.PostFormValue("back") == "" {
		back = "/stock"
	}
	item, err := s.Stock.Get(ctx, id)
	if err != nil {
		s.failed(w, r, err, back)
		return
	}
	user, today := currentUser(ctx).ID, calendar.Today()
	var msg string
	switch r.PathValue("action") {
	case "moins":
		_, err = s.Stock.Adjust(ctx, id, -item.Step, stock.Consumption, today, user)
		msg = fmt.Sprintf("%s : −%s.", item.Product, qty.Format(item.Step, item.Unit))
	case "plus":
		_, err = s.Stock.Adjust(ctx, id, item.Step, stock.Correction, today, user)
		msg = fmt.Sprintf("%s : +%s.", item.Product, qty.Format(item.Step, item.Unit))
	case "fini":
		_, err = s.Stock.Empty(ctx, id, stock.Consumption, today, user)
		msg = item.Product + " : fini."
	case "jete":
		_, err = s.Stock.Empty(ctx, id, stock.Waste, today, user)
		msg = item.Product + " : jeté (compté comme gaspillage)."
	case "ouvert":
		_, err = s.Stock.Open(ctx, id, today, user)
		msg = item.Product + " : ouvert aujourd'hui."
	default:
		s.notFound(w, r)
		return
	}
	if err != nil {
		s.failed(w, r, err, back)
		return
	}
	s.redirect(w, r, back+"#lot-"+strconv.FormatInt(id, 10), msg)
}

func (s *Server) undoStock(w http.ResponseWriter, r *http.Request) {
	if parseForm(w, r) != nil {
		s.renderError(w, r, http.StatusBadRequest, "Formulaire illisible.")
		return
	}
	back := localPath(r.PostFormValue("back"))
	if err := s.Stock.Undo(r.Context(), formInt(r.PostFormValue("batch"), 0)); err != nil {
		s.failed(w, r, err, back)
		return
	}
	s.redirect(w, r, back, "Action annulée.")
}

// moveView est la donnée du déplacement d'un lot.
type moveView struct {
	Item      stock.Item
	Locations []catalog.Location
	Back      string
	Error     string
}

func (s *Server) moveItemPage(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		s.notFound(w, r)
		return
	}
	v := moveView{Back: r.URL.Query().Get("retour")}
	var err error
	if v.Item, err = s.Stock.Get(r.Context(), id); err == nil {
		v.Locations, err = s.Catalog.Locations(r.Context())
	}
	if err != nil {
		s.failed(w, r, err, "/stock")
		return
	}
	s.render(w, r, http.StatusOK, "stock_move", "Déplacer", v)
}

func (s *Server) moveItem(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok || parseForm(w, r) != nil {
		s.notFound(w, r)
		return
	}
	ctx := r.Context()
	item, err := s.Stock.Get(ctx, id)
	if err != nil {
		s.failed(w, r, err, "/stock")
		return
	}
	var q int64
	if v := r.PostFormValue("qty"); strings.TrimSpace(v) != "" {
		if q, err = baseQty(v, item.Unit); err != nil {
			s.failed(w, r, err, "/stock/lots/"+strconv.FormatInt(id, 10)+"/deplacer")
			return
		}
	}
	best := calendar.Date(r.PostFormValue("best_before"))
	if _, err := s.Stock.Move(ctx, id, formInt(r.PostFormValue("location"), 0), q, best, calendar.Today(), currentUser(ctx).ID); err != nil {
		s.failed(w, r, err, "/stock/lots/"+strconv.FormatInt(id, 10)+"/deplacer")
		return
	}
	back := localPath(r.PostFormValue("back"))
	if r.PostFormValue("back") == "" {
		back = "/stock"
	}
	s.redirect(w, r, back, item.Product+" déplacé.")
}

// inventoryView est la donnée du mode inventaire.
type inventoryView struct {
	Locations []catalog.Location
	Location  catalog.Location
	Items     []stock.Item
	Today     calendar.Date
}

func (s *Server) inventoryPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	v := inventoryView{Today: calendar.Today()}
	var err error
	if v.Locations, err = s.Catalog.Locations(ctx); err != nil {
		s.serverError(w, r, err)
		return
	}
	sel := formInt(r.URL.Query().Get("emplacement"), 0)
	for _, l := range v.Locations {
		if l.ID == sel {
			v.Location = l
		}
	}
	if v.Location.ID > 0 {
		page, err := s.Stock.List(ctx, stock.Filter{LocationID: v.Location.ID})
		if err != nil {
			s.serverError(w, r, err)
			return
		}
		v.Items = page.Items
		for page.More {
			if page, err = s.Stock.List(ctx, stock.Filter{LocationID: v.Location.ID, Page: page.Page + 1}); err != nil {
				s.serverError(w, r, err)
				return
			}
			v.Items = append(v.Items, page.Items...)
		}
	}
	s.render(w, r, http.StatusOK, "stock_inventory", "Mode inventaire", v)
}

func (s *Server) saveInventory(w http.ResponseWriter, r *http.Request) {
	if parseForm(w, r) != nil {
		s.renderError(w, r, http.StatusBadRequest, "Formulaire illisible.")
		return
	}
	ctx := r.Context()
	back := "/stock/inventaire?emplacement=" + r.PostFormValue("location")
	counted := map[int64]int64{}
	for key, vals := range r.PostForm {
		idStr, ok := strings.CutPrefix(key, "qty_")
		if !ok || len(vals) == 0 || strings.TrimSpace(vals[0]) == "" {
			continue
		}
		id := formInt(idStr, 0)
		item, err := s.Stock.Get(ctx, id)
		if err != nil {
			continue
		}
		var q int64
		if strings.TrimSpace(vals[0]) != "0" {
			if q, err = baseQty(vals[0], item.Unit); err != nil {
				s.failed(w, r, validation.Errorf("%s : %s", item.Product, err.Error()), back)
				return
			}
		}
		counted[id] = q
	}
	_, n, err := s.Stock.Correct(ctx, counted, calendar.Today(), currentUser(ctx).ID)
	if err != nil {
		s.failed(w, r, err, back)
		return
	}
	s.redirect(w, r, back, fmt.Sprintf("Inventaire enregistré : %s.", plural(n, "écart corrigé", "écarts corrigés")))
}

// productStockView est la donnée de la fiche de stock d'un produit.
type productStockView struct {
	Product  catalog.Product
	Items    []stock.Item
	Settings stock.Settings
	Total    int64
	History  []stock.Movement
	RunOut   calendar.Date
	Today    calendar.Date
	Back     string
}

func (s *Server) productStockPage(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		s.notFound(w, r)
		return
	}
	ctx := r.Context()
	v := productStockView{Today: calendar.Today(), Back: r.URL.RequestURI()}
	var err error
	if v.Product, err = s.Catalog.Get(ctx, id); err != nil {
		s.failed(w, r, err, "/stock")
		return
	}
	page, err := s.Stock.List(ctx, stock.Filter{ProductID: id, Priority: true})
	if err == nil {
		v.Items = page.Items
		if v.Settings, err = s.Stock.Settings(ctx, id); err == nil {
			if v.Total, err = s.Stock.Total(ctx, id, v.Today); err == nil {
				v.History, err = s.Stock.History(ctx, id, 20)
			}
		}
	}
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	if d, ok := stock.RunOutDate(v.Total, v.Settings.Min, v.Settings.DailyUse, v.Today); ok {
		v.RunOut = d
	}
	s.render(w, r, http.StatusOK, "stock_product", v.Product.Name, v)
}

func (s *Server) saveStockSettings(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok || parseForm(w, r) != nil {
		s.notFound(w, r)
		return
	}
	ctx := r.Context()
	back := "/stock/produit/" + strconv.FormatInt(id, 10)
	p, err := s.Catalog.Get(ctx, id)
	if err != nil {
		s.failed(w, r, err, "/stock")
		return
	}
	st := stock.Settings{ProductID: id, Track: r.PostFormValue("track") != ""}
	for field, dst := range map[string]*int64{"min": &st.Min, "target": &st.Target} {
		if v := strings.TrimSpace(r.PostFormValue(field)); v != "" && v != "0" {
			if *dst, err = baseQty(v, p.Unit); err != nil {
				s.failed(w, r, err, back)
				return
			}
		}
	}
	if err := s.Stock.SaveSettings(ctx, st); err != nil {
		s.failed(w, r, err, back)
		return
	}
	s.redirect(w, r, back, "Réglages de stock enregistrés.")
}

func (s *Server) toggleLow(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok || parseForm(w, r) != nil {
		s.notFound(w, r)
		return
	}
	low := r.PostFormValue("low") == "1"
	if err := s.Stock.SetLow(r.Context(), id, low, calendar.Today()); err != nil {
		s.failed(w, r, err, "/stock")
		return
	}
	back := localPath(r.PostFormValue("back"))
	msg := "Signalé presque vide : il sera ajouté à la prochaine liste de courses."
	if !low {
		msg = "Signalement retiré."
	}
	s.redirect(w, r, back, msg)
}

// watchedView est la donnée du suivi des stocks minimum.
type watchedView struct {
	Items     []watchedLine
	Templates []stock.Template
	Today     calendar.Date
}

type watchedLine struct {
	stock.Watched
	Below  bool
	RunOut calendar.Date
}

func (s *Server) watchedPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	v := watchedView{Templates: stock.Templates, Today: calendar.Today()}
	list, err := s.Stock.Watched(ctx, v.Today)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	for _, w := range list {
		l := watchedLine{Watched: w, Below: w.Low || (w.Min > 0 && w.Stock < w.Min)}
		if d, ok := stock.RunOutDate(w.Stock, w.Min, w.DailyUse, v.Today); ok {
			l.RunOut = d
		}
		v.Items = append(v.Items, l)
	}
	s.render(w, r, http.StatusOK, "stock_watched", "Stock minimum", v)
}

func (s *Server) applyStockTemplate(w http.ResponseWriter, r *http.Request) {
	n, err := s.Stock.ApplyTemplate(r.Context(), r.PathValue("slug"))
	if err != nil {
		s.failed(w, r, err, "/stock/suivi")
		return
	}
	s.redirect(w, r, "/stock/suivi", fmt.Sprintf("Modèle appliqué : %s.", plural(n, "produit réglé", "produits réglés")))
}

// scanPage est l'écran de scan : la bibliothèque de lecture n'est chargée
// qu'ici (BarcodeDetector du navigateur, sinon saisie du code).
func (s *Server) scanPage(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, http.StatusOK, "stock_scan", "Scanner un produit", nil)
}

// lookupBarcode cherche un code-barres dans le catalogue local : connu, il
// mène à l'ajout au stock du produit ; inconnu, à l'association du code à
// un produit (existant ou nouveau).
func (s *Server) lookupBarcode(w http.ResponseWriter, r *http.Request) {
	code := strings.TrimSpace(r.URL.Query().Get("code"))
	id, err := s.Catalog.ByBarcode(r.Context(), code)
	switch {
	case errors.Is(err, db.ErrNotFound):
		s.redirect(w, r, "/stock/ajouter?code="+url.QueryEscape(code), "Code "+code+" inconnu : choisissez le produit, il sera reconnu au prochain scan.")
	case err != nil:
		s.serverError(w, r, err)
	default:
		s.redirect(w, r, "/stock/ajouter?produit_id="+strconv.FormatInt(id, 10), "")
	}
}
