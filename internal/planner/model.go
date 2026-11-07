// Package planner est le moteur du générateur de menus : une heuristique
// déterministe (même graine, même menu), explicable et rapide. Il ne lit
// pas la base : le service des menus lui fournit les créneaux, les
// recettes candidates, le stock et les réglages. Le même moteur de score
// sert à « Que cuisiner ce soir ? ».
package planner

import (
	"github.com/codecolors/petitchef/internal/catalog"
	"github.com/codecolors/petitchef/internal/platform/calendar"
	"github.com/codecolors/petitchef/internal/recipes"
	"github.com/codecolors/petitchef/internal/stock"
)

// Role est le rôle d'un plat dans un repas.
type Role string

const (
	Starter Role = "entree"
	Main    Role = "plat"
	Dessert Role = "dessert"
)

// Roles liste les rôles dans l'ordre de service.
var Roles = []Role{Starter, Main, Dessert}

// Label nomme le rôle.
func (r Role) Label() string {
	switch r {
	case Starter:
		return "Entrée"
	case Dessert:
		return "Dessert"
	}
	return "Plat"
}

// Type renvoie le type de recette attendu pour le rôle.
func (r Role) Type() recipes.Type {
	switch r {
	case Starter:
		return recipes.Starter
	case Dessert:
		return recipes.Dessert
	}
	return recipes.Main
}

// Format est le format d'un repas.
type Format string

const (
	NoMeal      Format = "aucun"
	MainOnly    Format = "plat"
	StarterMain Format = "entree_plat"
	MainDessert Format = "plat_dessert"
	FullMeal    Format = "complet"
)

// Formats liste les formats dans l'ordre d'affichage.
var Formats = []Format{MainOnly, StarterMain, MainDessert, FullMeal, NoMeal}

// Label nomme le format.
func (f Format) Label() string {
	switch f {
	case NoMeal:
		return "Pas de repas à la maison"
	case MainOnly:
		return "Plat seul"
	case StarterMain:
		return "Entrée + plat"
	case MainDessert:
		return "Plat + dessert"
	case FullMeal:
		return "Entrée + plat + dessert"
	}
	return string(f)
}

// Roles renvoie les rôles du format, plat d'abord (ordre de choix : la
// cohérence de l'entrée et du dessert dépend du plat).
func (f Format) Roles() []Role {
	switch f {
	case MainOnly:
		return []Role{Main}
	case StarterMain:
		return []Role{Main, Starter}
	case MainDessert:
		return []Role{Main, Dessert}
	case FullMeal:
		return []Role{Main, Starter, Dessert}
	}
	return nil
}

// Valid indique un format connu.
func (f Format) Valid() bool {
	for _, x := range Formats {
		if x == f {
			return true
		}
	}
	return false
}

// Use est l'emploi d'un produit par une recette, pour une portion
// (millièmes d'unité de base pour 1 portion).
type Use struct {
	ProductID  int64
	PerPortion int64 // pour 1000 millièmes de portion, en unités de base × 1000
	Grams      int64 // poids pour une portion (pondération du stock)
	Staple     bool
}

// Candidate est une recette candidate, avec ce qu'il faut savoir pour la
// noter sans relire la base.
type Candidate struct {
	ID             int64
	Title          string
	Types          recipes.Type
	ActiveMin      int
	Allergens      catalog.Allergens
	Traits         catalog.Traits
	Season         catalog.Months
	Grade          string
	Protein        catalog.Group
	StarchID       int64
	Heavy          bool
	VegShare       int
	Stars          int // moyenne × 10
	Favorite       bool
	BannedBy       map[int64]bool
	LastCooked     calendar.Date
	Uses           []Use
	CostPerPortion int64 // centimes, -1 : inconnu
	BatchFriendly  bool
	Fish           bool
	Vegetarian     bool
	RedMeat        bool
}

// ProductNames nomme les produits (raisons « utilise les courgettes »).
type ProductNames map[int64]string

// Constraints sont les contraintes strictes d'un créneau.
type Constraints struct {
	Allergens catalog.Allergens
	Diets     catalog.Diet
	Refused   map[int64]bool
	Members   []int64 // membres présents (recettes « à ne plus proposer »)
}

// Slot est un repas à remplir.
type Slot struct {
	MealID   int64
	Date     calendar.Date
	SlotID   int64
	SlotName string
	Lunch    bool // créneau de midi (restes du dîner de la veille)
	Dinner   bool
	Format   Format
	Portions int64 // millièmes
	MaxMin   int   // 0 : sans limite
	Constraints
	Fixed map[Role]Fixed // plats verrouillés ou déjà cuisinés
}

// Fixed est un plat conservé tel quel (verrouillé, cuisiné, reste).
type Fixed struct {
	RecipeID  int64
	ProductID int64
	Leftover  bool
}

// Ready est un plat prêt en stock (fait maison, restes, congélateur).
type Ready struct {
	StockItemID int64
	RecipeID    int64
	Portions    int64 // millièmes
	BestBefore  calendar.Date
}

// Weights sont les poids des critères (0 à 10).
type Weights struct {
	Stock   int `json:"stock"`
	Balance int `json:"equilibre"`
	Variety int `json:"variete"`
	Season  int `json:"saison"`
	Rating  int `json:"avis"`
	Cost    int `json:"cout"`
	Quick   int `json:"rapidite"`
}

// Rules sont les règles de répartition sur une semaine et de variété.
type Rules struct {
	FishMin       int  `json:"poisson_min"`
	RedMeatMax    int  `json:"viande_rouge_max"`
	VegetarianMin int  `json:"vegetarien_min"`
	VegEveryMeal  bool `json:"legumes_chaque_repas"`
	NoRepeatDays  int  `json:"pas_de_repetition_jours"`
}

// Preset est un préréglage de poids.
type Preset struct {
	Code    string
	Label   string
	Weights Weights
}

// Presets sont les préréglages proposés.
var Presets = []Preset{
	{"equilibre", "Équilibré", Weights{Stock: 5, Balance: 9, Variety: 7, Season: 5, Rating: 5, Cost: 2, Quick: 2}},
	{"economique", "Économique", Weights{Stock: 6, Balance: 4, Variety: 5, Season: 6, Rating: 3, Cost: 10, Quick: 2}},
	{"anti-gaspi", "Anti-gaspi", Weights{Stock: 10, Balance: 5, Variety: 4, Season: 4, Rating: 3, Cost: 3, Quick: 2}},
	{"rapide", "Rapide", Weights{Stock: 4, Balance: 5, Variety: 5, Season: 3, Rating: 4, Cost: 2, Quick: 10}},
}

// DefaultRules sont les règles par défaut.
var DefaultRules = Rules{FishMin: 2, RedMeatMax: 2, VegetarianMin: 1, VegEveryMeal: false, NoRepeatDays: 10}

// Past est un plat déjà servi (variété avec l'historique).
type Past struct {
	Date     calendar.Date
	RecipeID int64
	Protein  catalog.Group
	StarchID int64
}

// Input est l'entrée du générateur.
type Input struct {
	Slots      []Slot
	Candidates []Candidate
	Stock      map[int64][]stock.Lot
	Ready      []Ready
	Names      ProductNames
	History    []Past
	Weights    Weights
	Rules      Rules
	Leftovers  bool // cuisiner pour deux repas (restes le lendemain midi)
	Seed       uint64
	Today      calendar.Date
}

// Placement est un plat proposé.
type Placement struct {
	Slot       int // index dans Input.Slots
	Role       Role
	RecipeID   int64
	ReadyItem  int64 // plat prêt du stock
	LeftoverOf int   // index du placement d'origine + 1 (0 : aucun)
	CookDouble bool  // à cuisiner pour deux repas
	Score      int
	Reasons    []string
	Fixed      bool
}

// Summary est le bilan d'un menu.
type Summary struct {
	Grade      string         // indice moyen
	Proteins   map[string]int // répartition des sources de protéines
	Cost       int64          // coût estimé (centimes) des recettes aux prix connus
	CostKnown  bool
	StockShare int // part (poids) des ingrédients déjà en stock, en %
	Vegetarian int
	Dishes     int
}
