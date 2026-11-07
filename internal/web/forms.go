package web

import (
	"strconv"
	"strings"

	"github.com/codecolors/petitchef/internal/platform/money"
	"github.com/codecolors/petitchef/internal/platform/qty"
	"github.com/codecolors/petitchef/internal/platform/validation"
)

// formInt lit un entier facultatif ; def si le champ est vide ou illisible.
func formInt(v string, def int64) int64 {
	v = strings.TrimSpace(v)
	if v == "" {
		return def
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return def
	}
	return n
}

// formQty lit une quantité décimale (« 1,5 ») en millièmes ; 0 si vide.
func formQty(v string) (int64, error) {
	if strings.TrimSpace(v) == "" {
		return 0, nil
	}
	return qty.ParseDecimal(v)
}

// baseQty lit une quantité saisie dans l'unité de base d'un produit :
// grammes et millilitres entiers, pièces décimales (« 1,5 »).
func baseQty(v string, b qty.Base) (int64, error) {
	milli, err := qty.ParseDecimal(v)
	if err != nil {
		return 0, err
	}
	if b == qty.Piece {
		return milli, nil
	}
	if milli%1000 != 0 && milli > 10_000 {
		return 0, validation.Error("Quantité en grammes ou millilitres entiers.")
	}
	return (milli + 500) / 1000, nil
}

// splitList découpe une liste saisie séparée par des virgules ou des
// retours à la ligne.
func splitList(v string) []string {
	var out []string
	for _, line := range strings.Split(strings.ReplaceAll(v, "\n", ","), ",") {
		if s := strings.TrimSpace(line); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// parseEuros lit un prix positif (« 2,49 ») en centimes.
func parseEuros(v string) (int64, error) {
	c, err := money.Parse(v)
	if err != nil {
		return 0, err
	}
	if c < 0 {
		return 0, validation.Error("Le prix doit être positif.")
	}
	return c, nil
}
