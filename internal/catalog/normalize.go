package catalog

import (
	"strings"

	"github.com/codecolors/petitchef/internal/platform/text"
)

// Normalize renvoie la forme de recherche d'un nom de produit : casse,
// accents, ponctuation et pluriels simples ignorés (« Pommes de terre » et
// « pomme de terre » se confondent).
func Normalize(name string) string {
	f := text.Fold(name)
	f = strings.Map(func(r rune) rune {
		switch r {
		case '\'', '-', '’', ',', '.', '(', ')', '/', '"', ':', ';', '!', '?', '«', '»':
			return ' '
		}
		return r
	}, f)
	words := strings.Fields(f)
	for i, w := range words {
		words[i] = singular(w)
	}
	return strings.Join(words, " ")
}

// singular retire la marque du pluriel des mots de plus de trois lettres
// (« pommes » → « pomme », « choux » → « chou ») ; la même règle s'appliquant
// au catalogue et à la saisie, ses approximations sont sans effet.
func singular(w string) string {
	if len(w) <= 3 {
		return w
	}
	switch {
	case strings.HasSuffix(w, "eaux"), strings.HasSuffix(w, "oux"):
		return w[:len(w)-1]
	case strings.HasSuffix(w, "aux") && len(w) > 4:
		return w[:len(w)-3] + "al"
	case strings.HasSuffix(w, "s") && !strings.HasSuffix(w, "ss") && !strings.HasSuffix(w, "us"):
		return w[:len(w)-1]
	}
	return w
}
