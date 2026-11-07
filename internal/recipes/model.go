// Package recipes gère la base de recettes : saisie et analyse des
// ingrédients (liés au catalogue), étapes, mise à l'échelle, recettes
// composées, indice d'équilibre, avis du foyer, recherche, import et export.
package recipes

import (
	"strings"

	"github.com/codecolors/petitchef/internal/catalog"
	"github.com/codecolors/petitchef/internal/platform/calendar"
	"github.com/codecolors/petitchef/internal/platform/qty"
)

// Type est un type de recette (bits).
type Type int64

const (
	Starter Type = 1 << iota
	Main
	Dessert
	Breakfast
	Snack
	Side
	Sauce
)

// TypeInfo décrit un type de recette.
type TypeInfo struct {
	Bit   Type
	Code  string
	Label string
}

// AllTypes liste les types de recette.
var AllTypes = []TypeInfo{
	{Starter, "entree", "Entrée"}, {Main, "plat", "Plat"}, {Dessert, "dessert", "Dessert"},
	{Breakfast, "petit_dejeuner", "Petit-déjeuner"}, {Snack, "gouter", "Goûter"}, {Side, "accompagnement", "Accompagnement"},
	{Sauce, "sauce", "Sauce"},
}

// TypeByCode renvoie le bit d'un type d'après son code.
func TypeByCode(code string) (Type, bool) {
	for _, t := range AllTypes {
		if t.Code == code {
			return t.Bit, true
		}
	}
	return 0, false
}

// Has indique la présence d'un type.
func (t Type) Has(b Type) bool { return t&b != 0 }

// Labels nomme les types.
func (t Type) Labels() string {
	var out []string
	for _, x := range AllTypes {
		if t&x.Bit != 0 {
			out = append(out, x.Label)
		}
	}
	return strings.Join(out, ", ")
}

// Ingredient est une ligne d'ingrédient d'une recette.
type Ingredient struct {
	ID          int64
	Position    int
	Group       string
	ProductID   int64 // ou
	SubRecipe   int64 // recette utilisée comme ingrédient (Qty en millièmes de portion)
	Name        string
	Qty         int64 // unités de base du produit, ou millièmes de portion
	InputQty    int64 // millièmes, tel que saisi (0 : sans quantité)
	InputUnit   string
	Note        string
	Optional    bool
	ToTaste     bool
	Unit        qty.Base
	Staple      bool
	Group2      catalog.Group // groupe alimentaire du produit
	PieceFrac   bool          // la saisie utilisait une fraction de pièce
	Convertible bool          // la quantité a pu être convertie en unité de base
}

// Step est une étape.
type Step struct {
	Position int
	Text     string
	TimerSec int
}

// Recipe est une recette.
type Recipe struct {
	ID          int64
	Title       string
	Types       Type
	Servings    int
	PrepMin     int
	CookMin     int
	RestMin     int
	Difficulty  int
	Utensils    string
	Source      string
	Notes       string
	Photo       string
	VariantOf   int64
	Tags        []string
	Ingredients []Ingredient
	Steps       []Step
	// Calculés.
	Allergens     catalog.Allergens
	Traits        catalog.Traits
	Season        catalog.Months
	BalanceScore  int
	BalanceGrade  string
	BalanceManual string
	Protein       catalog.Group
	StarchID      int64
	Heavy         bool
	VegShare      int // part de légumes et fruits, en %
	CookedCount   int
	LastCooked    calendar.Date
	Origin        string
	Modified      bool
}

// TotalMin renvoie le temps total (préparation, cuisson, repos).
func (r Recipe) TotalMin() int { return r.PrepMin + r.CookMin + r.RestMin }

// ActiveMin renvoie le temps « en cuisine » (préparation et cuisson), qui
// sert au temps maximum d'un créneau.
func (r Recipe) ActiveMin() int { return r.PrepMin + r.CookMin }

// Grade renvoie l'indice affiché : correction manuelle ou calcul.
func (r Recipe) Grade() string {
	if r.BalanceManual != "" {
		return r.BalanceManual
	}
	return r.BalanceGrade
}

// Diets renvoie les régimes compatibles.
func (r Recipe) Diets() catalog.Diet { return catalog.Compatibility(r.Traits, r.Allergens) }

// InSeason indique si la recette est de saison au mois m.
func (r Recipe) InSeason(m int) bool { return r.Season.Has(m) }

// Difficulties nomme les niveaux de difficulté.
var Difficulties = [...]string{"", "Facile", "Moyenne", "Difficile"}
