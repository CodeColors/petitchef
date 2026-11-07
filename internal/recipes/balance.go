package recipes

import (
	"cmp"
	"fmt"
	"slices"

	"github.com/codecolors/petitchef/internal/catalog"
)

// Grid est la grille de l'indice d'équilibre : un coefficient par groupe
// alimentaire (en dixièmes de point par pourcentage du poids) et les seuils
// des lettres A à D (en dessous du dernier : E). Paramétrable dans
// l'interface.
type Grid struct {
	Coef       map[catalog.Group]int `json:"coefficients"`
	Thresholds [4]int                `json:"seuils"` // A, B, C, D
}

// DefaultGrid est la grille par défaut, documentée dans docs/EQUILIBRE.md.
func DefaultGrid() Grid {
	return Grid{
		Coef: map[catalog.Group]int{
			catalog.Vegetables: 10, catalog.Fruits: 8, catalog.Pulses: 10, catalog.WholeStarch: 6, catalog.Fish: 8,
			catalog.Eggs: 3, catalog.Poultry: 3, catalog.Dairy: 2, catalog.Nuts: 3, catalog.RefinedStarch: 0,
			catalog.Cheese: -3, catalog.RedMeat: -5, catalog.ColdCuts: -10, catalog.Fats: -15, catalog.Sweets: -12,
			catalog.Processed: -8,
		},
		Thresholds: [4]int{35, 15, -5, -25},
	}
}

// neutral sont les groupes exclus du calcul : ils ne pèsent pas dans le
// total (épices, boissons de cuisson, produits non classés).
func neutral(g catalog.Group) bool {
	return g == catalog.Condiments || g == catalog.Drinks || g == catalog.NoGroup
}

// Contribution est la part d'un groupe dans l'indice.
type Contribution struct {
	Group  catalog.Group
	Share  int // % du poids pris en compte
	Points int // points apportés (arrondis)
}

// Balance est le résultat du calcul de l'indice d'équilibre.
type Balance struct {
	Score         int
	Grade         string
	Contributions []Contribution // triées par effet décroissant
	Weighed       int64          // grammes pris en compte
	Unweighed     []string       // ingrédients dont le poids est inconnu
}

// ComputeBalance calcule l'indice d'une recette à partir du poids de ses
// ingrédients par groupe alimentaire : score = Σ coefficient × part du
// poids, puis lettre selon les seuils.
func ComputeBalance(grams map[catalog.Group]int64, g Grid) Balance {
	var total int64
	for grp, w := range grams {
		if !neutral(grp) {
			total += w
		}
	}
	b := Balance{Weighed: total}
	if total == 0 {
		b.Grade = "C"
		return b
	}
	var score int64 // en dixièmes de point × %
	for grp, w := range grams {
		if neutral(grp) || w == 0 {
			continue
		}
		share := w * 1000 / total // pour mille
		pts := int64(g.Coef[grp]) * share
		score += pts
		b.Contributions = append(b.Contributions, Contribution{Group: grp, Share: int((share + 5) / 10), Points: int(roundDiv(pts, 100))})
	}
	b.Score = int(roundDiv(score, 100))
	slices.SortFunc(b.Contributions, func(x, y Contribution) int {
		if c := cmp.Compare(abs(y.Points), abs(x.Points)); c != 0 {
			return c
		}
		return cmp.Compare(y.Share, x.Share)
	})
	b.Grade = g.Letter(b.Score)
	return b
}

// Letter renvoie la lettre d'un score.
func (g Grid) Letter(score int) string {
	for i, t := range g.Thresholds {
		if score >= t {
			return string(rune('A' + i))
		}
	}
	return "E"
}

func roundDiv(a, b int64) int64 {
	if a < 0 {
		return -roundDiv(-a, b)
	}
	return (a + b/2) / b
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// Explain décrit une contribution en français : « Légumes : 62 % du
// poids, +62 points ».
func (c Contribution) Explain() string {
	return fmt.Sprintf("%s : %d %% du poids, %+d points", c.Group.Label(), c.Share, c.Points)
}

// Validate contrôle une grille saisie.
func (g Grid) Validate() error {
	for i := 1; i < len(g.Thresholds); i++ {
		if g.Thresholds[i] >= g.Thresholds[i-1] {
			return fmt.Errorf("les seuils doivent décroître de A à D")
		}
	}
	for grp, c := range g.Coef {
		if c < -50 || c > 50 {
			return fmt.Errorf("%s : coefficient entre -5 et 5", grp.Label())
		}
	}
	return nil
}

// Nutrition est une estimation par portion (grammes en dixièmes, énergie
// en kcal).
type Nutrition struct {
	Kcal, Proteins, Carbs, Fat, Fibres int64
}
