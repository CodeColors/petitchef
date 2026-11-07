package catalog

import (
	"github.com/codecolors/petitchef/internal/platform/qty"
)

// Conv rassemble les conversions d'un produit : unité de base, poids d'une
// pièce, densité et unités propres. Tous les calculs restent en entiers.
type Conv struct {
	Base    qty.Base
	PieceG  int64            // grammes pour une pièce (0 : inconnu)
	Density int64            // grammes par litre (0 : 1000, comme l'eau)
	Units   map[string]int64 // unité propre → unités de base
}

func (c Conv) density() int64 {
	if c.Density > 0 {
		return c.Density
	}
	return 1000
}

// ToBase convertit une quantité saisie (en millièmes) dans une unité de
// saisie (code canonique, "" pour l'unité de base) en unités de base du
// produit. ok vaut false si la conversion est impossible (pièce sans poids
// connu, unité inconnue).
func (c Conv) ToBase(milli int64, unit string) (v int64, ok bool) {
	if unit == "" {
		unit = defaultUnit(c.Base)
	}
	if q, ok := c.Units[unit]; ok {
		return divRound(milli*q, 1000), true
	}
	dim, factor, known := qty.Generic(unit)
	if !known {
		return 0, false
	}
	amount := divRound(milli*factor, 1000) // mg, µl ou millièmes de pièce
	switch dim {
	case qty.Mass:
		return c.fromMg(amount)
	case qty.Volume:
		switch c.Base {
		case qty.Milli:
			return divRound(amount, 1000), true
		default:
			return c.fromMg(divRound(amount*c.density(), 1000))
		}
	case qty.Count:
		if c.Base == qty.Piece {
			return amount, true
		}
		if c.PieceG <= 0 {
			return 0, false
		}
		return c.fromMg(amount * c.PieceG)
	}
	return 0, false
}

// fromMg convertit des milligrammes en unités de base.
func (c Conv) fromMg(mg int64) (int64, bool) {
	switch c.Base {
	case qty.Gram:
		return divRound(mg, 1000), true
	case qty.Milli:
		return divRound(mg, c.density()), true
	}
	if c.PieceG <= 0 {
		return 0, false
	}
	return divRound(mg, c.PieceG), true
}

// Grams renvoie le poids approximatif d'une quantité de base, en grammes
// (indice d'équilibre, nutrition) ; ok vaut false si le poids d'une pièce
// est inconnu.
func (c Conv) Grams(v int64) (int64, bool) {
	switch c.Base {
	case qty.Gram:
		return v, true
	case qty.Milli:
		return divRound(v*c.density(), 1000), true
	}
	if c.PieceG <= 0 {
		return 0, false
	}
	return divRound(v*c.PieceG, 1000), true
}

func defaultUnit(b qty.Base) string {
	switch b {
	case qty.Gram:
		return "g"
	case qty.Milli:
		return "ml"
	}
	return "piece"
}

// divRound divise en arrondissant au plus proche (a ≥ 0, b > 0).
func divRound(a, b int64) int64 {
	if a < 0 {
		return -divRound(-a, b)
	}
	return (a + b/2) / b
}

// RoundToPackages choisit le conditionnement qui couvre le besoin avec le
// moins de surplus (à surplus égal, le moins de paquets) et renvoie la
// quantité achetée, le nombre de paquets et la taille retenue. Sans
// conditionnement connu, la quantité est arrondie à la pièce entière (unité
// pièce) ou laissée telle quelle.
func RoundToPackages(need int64, base qty.Base, packages []int64) (buy, count, size int64) {
	if need <= 0 {
		return 0, 0, 0
	}
	if len(packages) == 0 {
		if base == qty.Piece {
			n := qty.CeilDiv(need, 1000)
			return n * 1000, n, 1000
		}
		return need, 0, 0
	}
	for _, p := range packages {
		if p <= 0 {
			continue
		}
		n := qty.CeilDiv(need, p)
		total := n * p
		if size == 0 || total < buy || (total == buy && n < count) {
			buy, count, size = total, n, p
		}
	}
	return buy, count, size
}

// ScalePieces arrondit une quantité de pièces mise à l'échelle : demi-pièce
// si la recette en utilisait déjà une fraction (« ½ citron »), pièce entière
// sinon, jamais moins d'une demi-pièce (« pas 1,37 œuf »).
func ScalePieces(v int64, fractional bool) int64 {
	step := int64(1000)
	if fractional {
		step = 500
	}
	r := divRound(v, step) * step
	if r == 0 {
		r = step
	}
	return r
}
