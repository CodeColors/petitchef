// Package catalog est le référentiel unique des produits : un ingrédient
// de recette, une ligne de stock et une ligne de liste de courses pointent
// tous vers un produit du catalogue. Il porte les unités et conversions,
// les conditionnements, la conservation indicative, les groupes
// alimentaires, la saisonnalité, les allergènes et les régimes, ainsi que
// les rayons, magasins et emplacements.
package catalog

import (
	"strings"

	"github.com/codecolors/petitchef/internal/platform/qty"
)

// Universe est l'univers d'un produit.
type Universe string

const (
	Food     Universe = "alimentaire"
	Cleaning Universe = "entretien"
	Hygiene  Universe = "hygiene"
	Home     Universe = "maison"
	Pets     Universe = "animaux"
	Baby     Universe = "bebe"
)

// Universes liste les univers dans l'ordre d'affichage.
var Universes = []Universe{Food, Hygiene, Cleaning, Home, Baby, Pets}

// Label nomme l'univers.
func (u Universe) Label() string {
	switch u {
	case Food:
		return "Alimentaire"
	case Cleaning:
		return "Entretien"
	case Hygiene:
		return "Hygiène"
	case Home:
		return "Maison"
	case Pets:
		return "Animaux"
	case Baby:
		return "Bébé"
	}
	return string(u)
}

// Valid indique un univers connu.
func (u Universe) Valid() bool {
	for _, v := range Universes {
		if v == u {
			return true
		}
	}
	return false
}

// Group est le groupe alimentaire d'un produit, base de l'indice
// d'équilibre.
type Group string

const (
	Vegetables    Group = "legumes"
	Fruits        Group = "fruits"
	WholeStarch   Group = "feculents_complets"
	RefinedStarch Group = "feculents_raffines"
	Pulses        Group = "legumineuses"
	RedMeat       Group = "viande_rouge"
	Poultry       Group = "volaille"
	Fish          Group = "poisson"
	ColdCuts      Group = "charcuterie"
	Eggs          Group = "oeufs"
	Dairy         Group = "produits_laitiers"
	Cheese        Group = "fromage"
	Fats          Group = "matieres_grasses"
	Sweets        Group = "produits_sucres"
	Processed     Group = "plats_transformes"
	Nuts          Group = "fruits_coque"
	Drinks        Group = "boissons"
	Condiments    Group = "condiments"
	NoGroup       Group = ""
)

// Groups liste les groupes alimentaires dans l'ordre d'affichage.
var Groups = []Group{Vegetables, Fruits, Pulses, WholeStarch, RefinedStarch, Fish, Poultry, Eggs, RedMeat, ColdCuts,
	Dairy, Cheese, Nuts, Fats, Sweets, Processed, Drinks, Condiments}

// Label nomme le groupe.
func (g Group) Label() string {
	switch g {
	case Vegetables:
		return "Légumes"
	case Fruits:
		return "Fruits"
	case WholeStarch:
		return "Féculents complets"
	case RefinedStarch:
		return "Féculents raffinés"
	case Pulses:
		return "Légumineuses"
	case RedMeat:
		return "Viande rouge et porc"
	case Poultry:
		return "Volaille"
	case Fish:
		return "Poisson et fruits de mer"
	case ColdCuts:
		return "Charcuterie"
	case Eggs:
		return "Œufs"
	case Dairy:
		return "Produits laitiers"
	case Cheese:
		return "Fromages"
	case Fats:
		return "Matières grasses"
	case Sweets:
		return "Produits sucrés"
	case Processed:
		return "Plats transformés"
	case Nuts:
		return "Fruits à coque et graines"
	case Drinks:
		return "Boissons"
	case Condiments:
		return "Condiments et épices"
	}
	return "Non classé"
}

// Valid indique un groupe connu (le groupe vide est permis : produit non
// alimentaire ou non classé).
func (g Group) Valid() bool {
	if g == NoGroup {
		return true
	}
	for _, v := range Groups {
		if v == g {
			return true
		}
	}
	return false
}

// IsProtein indique un groupe qui sert de source de protéines principale
// (rotation des protéines du générateur de menus).
func (g Group) IsProtein() bool {
	switch g {
	case RedMeat, Poultry, Fish, ColdCuts, Eggs, Pulses:
		return true
	}
	return false
}

// IsStarch indique un féculent.
func (g Group) IsStarch() bool { return g == WholeStarch || g == RefinedStarch }

// Allergens est un ensemble d'allergènes réglementaires (bits).
type Allergens int64

// Les 14 allergènes réglementaires de l'Union européenne.
const (
	Gluten Allergens = 1 << iota
	Crustaceans
	EggsAllergen
	FishAllergen
	Peanuts
	Soy
	Milk
	TreeNuts
	Celery
	Mustard
	Sesame
	Sulphites
	Lupin
	Molluscs
)

// AllergenInfo décrit un allergène.
type AllergenInfo struct {
	Bit   Allergens
	Code  string
	Label string
}

// AllAllergens liste les 14 allergènes.
var AllAllergens = []AllergenInfo{
	{Gluten, "gluten", "Gluten"}, {Crustaceans, "crustaces", "Crustacés"}, {EggsAllergen, "oeufs", "Œufs"},
	{FishAllergen, "poissons", "Poissons"}, {Peanuts, "arachides", "Arachides"}, {Soy, "soja", "Soja"},
	{Milk, "lait", "Lait"}, {TreeNuts, "fruits_a_coque", "Fruits à coque"}, {Celery, "celeri", "Céleri"},
	{Mustard, "moutarde", "Moutarde"}, {Sesame, "sesame", "Sésame"}, {Sulphites, "sulfites", "Sulfites"},
	{Lupin, "lupin", "Lupin"}, {Molluscs, "mollusques", "Mollusques"},
}

// AllergenByCode renvoie le bit d'un allergène d'après son code.
func AllergenByCode(code string) (Allergens, bool) {
	for _, a := range AllAllergens {
		if a.Code == code {
			return a.Bit, true
		}
	}
	return 0, false
}

// Labels nomme les allergènes de l'ensemble.
func (a Allergens) Labels() []string {
	var out []string
	for _, x := range AllAllergens {
		if a&x.Bit != 0 {
			out = append(out, x.Label)
		}
	}
	return out
}

// Has indique la présence d'un allergène.
func (a Allergens) Has(b Allergens) bool { return a&b != 0 }

// Traits sont les indicateurs de régime d'un produit (bits) : ce qui le
// rend incompatible avec un régime.
type Traits int64

const (
	Meat    Traits = 1 << iota // chair animale (viande, poisson, gélatine) : pas végétarien
	Animal                     // d'origine animale (y compris œufs, lait, miel) : pas végan
	Pork                       // porc
	Alcohol                    // alcool
)

// Diet est un régime alimentaire d'un membre du foyer.
type Diet int64

const (
	Vegetarian Diet = 1 << iota
	Vegan
	GlutenFree
	PorkFree
	LactoseFree
	AlcoholFree
)

// DietInfo décrit un régime.
type DietInfo struct {
	Bit   Diet
	Code  string
	Label string
}

// AllDiets liste les régimes pris en charge.
var AllDiets = []DietInfo{
	{Vegetarian, "vegetarien", "Végétarien"}, {Vegan, "vegan", "Végan"}, {GlutenFree, "sans_gluten", "Sans gluten"},
	{PorkFree, "sans_porc", "Sans porc"}, {LactoseFree, "sans_lactose", "Sans lactose"}, {AlcoholFree, "sans_alcool", "Sans alcool"},
}

// Labels nomme les régimes de l'ensemble.
func (d Diet) Labels() []string {
	var out []string
	for _, x := range AllDiets {
		if d&x.Bit != 0 {
			out = append(out, x.Label)
		}
	}
	return out
}

// Compatible indique si un produit (traits et allergènes) convient au
// régime d.
func Compatible(d Diet, t Traits, a Allergens) bool {
	switch {
	case d&Vegetarian != 0 && t&Meat != 0,
		d&Vegan != 0 && t&(Meat|Animal) != 0,
		d&GlutenFree != 0 && a&Gluten != 0,
		d&PorkFree != 0 && t&Pork != 0,
		d&LactoseFree != 0 && a&Milk != 0,
		d&AlcoholFree != 0 && t&Alcohol != 0:
		return false
	}
	return true
}

// Compatibility renvoie l'ensemble des régimes auxquels conviennent des
// produits de traits et allergènes cumulés.
func Compatibility(t Traits, a Allergens) Diet {
	var out Diet
	for _, d := range AllDiets {
		if Compatible(d.Bit, t, a) {
			out |= d.Bit
		}
	}
	return out
}

// DefaultTraits déduit les traits d'un produit alimentaire de son groupe
// et de ses allergènes ; la base de départ peut les compléter.
func DefaultTraits(g Group, a Allergens) Traits {
	var t Traits
	switch g {
	case RedMeat, Poultry, Fish, ColdCuts:
		t |= Meat | Animal
	case Eggs, Dairy, Cheese:
		t |= Animal
	}
	if a&(Milk|EggsAllergen) != 0 {
		t |= Animal
	}
	if a&(FishAllergen|Crustaceans|Molluscs) != 0 {
		t |= Meat | Animal
	}
	return t
}

// Months est un ensemble de mois (bit 0 = janvier).
type Months int64

// AllYear désigne les douze mois.
const AllYear Months = 1<<12 - 1

// Has indique si le mois m (1 à 12) appartient à l'ensemble.
func (m Months) Has(month int) bool { return m&(1<<(month-1)) != 0 }

var monthNames = [...]string{"janv.", "févr.", "mars", "avr.", "mai", "juin", "juil.", "août", "sept.", "oct.", "nov.", "déc."}

// Label décrit la saison : « juin à sept. », « toute l'année », « ».
func (m Months) Label() string {
	if m == 0 || m == AllYear {
		if m == AllYear {
			return "toute l'année"
		}
		return ""
	}
	// Plage continue (éventuellement à cheval sur l'année) ?
	start := -1
	for i := range 12 {
		if m.Has(i+1) && !m.Has((i+11)%12+1) {
			if start >= 0 {
				start = -2
				break
			}
			start = i
		}
	}
	if start >= 0 {
		end := start
		for m.Has((end+1)%12 + 1) {
			end = (end + 1) % 12
		}
		if start == end {
			return monthNames[start]
		}
		return monthNames[start] + " à " + monthNames[end]
	}
	var parts []string
	for i := range 12 {
		if m.Has(i + 1) {
			parts = append(parts, monthNames[i])
		}
	}
	return strings.Join(parts, ", ")
}

// MonthRange construit l'ensemble des mois de from à to (1 à 12, à cheval
// sur l'année si to < from).
func MonthRange(from, to int) Months {
	var m Months
	for i := from; ; i = i%12 + 1 {
		m |= 1 << (i - 1)
		if i == to {
			break
		}
	}
	return m
}

// DateKind précise la nature de la date limite.
type DateKind string

const (
	DLC DateKind = "DLC" // date limite de consommation (à consommer jusqu'au)
	DDM DateKind = "DDM" // date de durabilité minimale (de préférence avant)
)

// Package est un conditionnement usuel.
type Package struct {
	ID    int64
	Qty   int64 // en unités de base
	Label string
}

// UnitConv est une conversion propre au produit.
type UnitConv struct {
	Unit string
	Qty  int64 // en unités de base pour une unité de saisie
}

// Product est un produit du catalogue.
type Product struct {
	ID          int64
	Name        string
	Search      string
	Universe    Universe
	AisleID     int64
	AisleName   string
	LocationID  int64
	Location    string
	Unit        qty.Base
	PieceG      int64 // poids d'une pièce, en grammes (0 : inconnu)
	Density     int64 // grammes par litre (0 : inconnue, l'eau sert d'approximation)
	ShelfClosed int64 // jours, -1 : inconnue
	ShelfOpened int64
	ShelfFrozen int64
	DateKind    DateKind
	Group       Group
	Season      Months
	Allergens   Allergens
	Traits      Traits
	Staple      bool
	StoreID     int64
	Brand       string
	Nutrition   Nutrition
	Photo       string
	Incomplete  bool
	Origin      string
	Modified    bool
	Archived    bool
	Synonyms    []string
	Barcodes    []string
	Units       []UnitConv
	Packages    []Package
}

// Nutrition sont les valeurs pour 100 g (dixièmes de gramme ; énergie en
// kcal) ; -1 : inconnue.
type Nutrition struct {
	Kcal, Proteins, Carbs, Fat, Fibres int64
}

// Known indique que toutes les valeurs sont renseignées.
func (n Nutrition) Known() bool {
	return n.Kcal >= 0 && n.Proteins >= 0 && n.Carbs >= 0 && n.Fat >= 0 && n.Fibres >= 0
}

// Conv renvoie les conversions du produit.
func (p Product) Conv() Conv {
	c := Conv{Base: p.Unit, PieceG: p.PieceG, Density: p.Density}
	if len(p.Units) > 0 {
		c.Units = make(map[string]int64, len(p.Units))
		for _, u := range p.Units {
			c.Units[u.Unit] = u.Qty
		}
	}
	return c
}

// Format écrit une quantité de ce produit.
func (p Product) Format(v int64) string { return qty.Format(v, p.Unit) }

// PackageQtys renvoie les tailles de conditionnement.
func (p Product) PackageQtys() []int64 {
	out := make([]int64, len(p.Packages))
	for i, pk := range p.Packages {
		out[i] = pk.Qty
	}
	return out
}

// Aisle est un rayon.
type Aisle struct {
	ID       int64
	Slug     string
	Name     string
	Position int
	Products int
}

// Location est un emplacement de rangement.
type Location struct {
	ID       int64
	Slug     string
	Name     string
	Kind     string // ambiant, frais, congele
	Position int
}

// Store est un magasin.
type Store struct {
	ID        int64
	Name      string
	IsDefault bool
}

// Has indique la présence d'un trait.
func (t Traits) Has(b Traits) bool { return t&b != 0 }

// TraitInfo décrit un indicateur de régime.
type TraitInfo struct {
	Bit   Traits
	Code  string
	Label string
}

// AllTraits liste les indicateurs de régime d'un produit.
var AllTraits = []TraitInfo{
	{Meat, "meat", "Viande ou poisson (non végétarien)"},
	{Animal, "animal", "D'origine animale (non végan)"},
	{Pork, "pork", "Contient du porc"},
	{Alcohol, "alcohol", "Contient de l'alcool"},
}

// MonthNames nomme les mois (index 0 = janvier).
var MonthNames = [...]string{"janvier", "février", "mars", "avril", "mai", "juin", "juillet", "août", "septembre", "octobre", "novembre", "décembre"}
