package recipes

import (
	"regexp"
	"strings"

	"github.com/codecolors/petitchef/internal/platform/qty"
	"github.com/codecolors/petitchef/internal/platform/text"
)

// Line est une ligne d'ingrédient analysée, avant rapprochement du
// catalogue.
type Line struct {
	Raw       string
	Group     string // regroupement (« Pour la pâte »)
	Milli     int64  // quantité en millièmes (0 : aucune)
	Unit      string // code d'unité canonique ("" : aucune)
	UnitText  string // unité telle que saisie (« filet »)
	Name      string // nom du produit ou de la recette
	Note      string // précision (« émincé »)
	Optional  bool
	ToTaste   bool
	SubRecipe bool // nom entre crochets : une autre recette
}

var (
	quantityRe = regexp.MustCompile(`^(\d+\s+\d+/\d+|\d+\s*[½¼¾⅓⅔]|\d+(?:[.,]\d+)?\s*/\s*\d+|\d+(?:[.,]\d+)?|[½¼¾⅓⅔]|,\d+)\s*`)
	optionalRe = regexp.MustCompile(`(?i)\s*\(?\s*\b(facultatif|facultative|optionnel|optionnelle|si possible)\s*\)?\s*$`)
	tasteRe    = regexp.MustCompile(`(?i)\s*\(?\s*\b(au go[uû]t|selon le go[uû]t|à volonté|a volonte|qs|q\.s\.)\s*\)?\s*$`)
	bracketRe  = regexp.MustCompile(`\[([^\]]+)\]`)
)

// unitWords sont les unités reconnues en tête de ligne, les plus longues
// d'abord (« c. à soupe » avant « c »).
var unitWords = []string{
	"cuilleres a soupe", "cuillere a soupe", "cuil. a soupe", "cuil a soupe", "c. a soupe", "c.a soupe", "c a soupe", "c.a.s.", "c.a.s", "c. s.", "c.s.", "cas",
	"cuilleres a cafe", "cuillere a cafe", "cuil. a cafe", "cuil a cafe", "c. a cafe", "c.a cafe", "c a cafe", "c.a.c.", "c.a.c", "c. c.", "c.c.", "cac",
	"kilogrammes", "kilogramme", "grammes", "gramme", "kilos", "kilo", "kg", "gr", "g",
	"millilitres", "millilitre", "centilitres", "centilitre", "decilitres", "decilitre", "litres", "litre", "ml", "cl", "dl", "l",
	"pincees", "pincee", "tasses", "tasse", "verres", "verre", "gousses", "gousse", "tranches", "tranche", "sachets", "sachet",
	"bottes", "botte", "brins", "brin", "feuilles", "feuille", "boites", "boite", "pots", "pot", "poignees", "poignee",
	"noix", "noisette", "filet", "bouquets", "bouquet", "branches", "branche", "rouleaux", "rouleau", "tablettes", "tablette",
	"carres", "carre", "briques", "brique", "bouteilles", "bouteille", "paquets", "paquet", "lamelles", "lamelle", "cs", "cc",
}

// ParseLines analyse une liste d'ingrédients en texte libre : une ligne
// par ingrédient, ou une liste séparée par des virgules (« 200 g de farine,
// 3 œufs, 1 pincée de sel »). Une ligne qui se termine par « : » (ou
// commence par « ## ») ouvre un regroupement.
func ParseLines(src string) []Line {
	var out []Line
	group := ""
	for _, raw := range splitIngredients(src) {
		s := strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(raw), "-*•·–"))
		if s == "" {
			continue
		}
		if g, ok := strings.CutPrefix(s, "##"); ok {
			group = strings.TrimSpace(g)
			continue
		}
		if g, ok := strings.CutSuffix(s, ":"); ok && !quantityRe.MatchString(s) {
			group = strings.TrimSpace(g)
			continue
		}
		l := ParseLine(s)
		l.Group = group
		out = append(out, l)
	}
	return out
}

// splitIngredients découpe par lignes ; une ligne unique contenant des
// virgules hors parenthèses est découpée aux virgules qui précèdent une
// quantité ou un nouveau produit.
func splitIngredients(src string) []string {
	lines := strings.Split(strings.ReplaceAll(src, "\r\n", "\n"), "\n")
	nonEmpty := 0
	for _, l := range lines {
		if strings.TrimSpace(l) != "" {
			nonEmpty++
		}
	}
	if nonEmpty != 1 {
		return lines
	}
	var out []string
	depth, start := 0, 0
	for i, r := range src {
		switch r {
		case '(', '[':
			depth++
		case ')', ']':
			depth--
		case ',', ';':
			if depth == 0 && !(r == ',' && i > 0 && i+1 < len(src) && isDigit(src[i-1]) && isDigit(src[i+1])) {
				out = append(out, src[start:i])
				start = i + 1
			}
		}
	}
	return append(out, src[start:])
}

func isDigit(b byte) bool { return b >= '0' && b <= '9' }

// ParseLine analyse une ligne d'ingrédient : quantité, unité, nom,
// mentions « facultatif » et « au goût ».
func ParseLine(s string) Line {
	l := Line{Raw: s}
	if optionalRe.MatchString(s) {
		l.Optional = true
		s = optionalRe.ReplaceAllString(s, "")
	}
	if tasteRe.MatchString(s) {
		l.ToTaste = true
		s = tasteRe.ReplaceAllString(s, "")
	}
	s = strings.TrimSpace(s)
	if m := quantityRe.FindStringSubmatch(s); m != nil {
		if v, err := qty.ParseDecimal(strings.ReplaceAll(strings.TrimSpace(m[1]), " /", "/")); err == nil {
			l.Milli = v
			s = strings.TrimSpace(s[len(m[0]):])
		}
	}
	if l.Milli > 0 {
		folded := strings.ReplaceAll(text.Fold(s), "à", "a")
		for _, w := range unitWords {
			if rest, ok := strings.CutPrefix(folded, w); ok && (rest == "" || rest[0] == ' ' || rest[0] == '\'' || strings.HasPrefix(w, "c.") && rest[0] != ' ') {
				if code := qty.Canonical(w); code != "" {
					l.Unit = code
					l.UnitText = strings.TrimSpace(string([]rune(s)[:min(len([]rune(w)), len([]rune(s)))]))
					s = strings.TrimSpace(cutRunes(s, len([]rune(w))))
					break
				}
			}
		}
	}
	s = strings.TrimSpace(s)
	for _, p := range []string{"de ", "d'", "d’", "des ", "du "} {
		if rest, ok := cutPrefixFold(s, p); ok {
			s = strings.TrimSpace(rest)
			break
		}
	}
	if m := bracketRe.FindStringSubmatch(s); m != nil {
		l.SubRecipe = true
		l.Name = strings.TrimSpace(m[1])
		l.Note = strings.TrimSpace(bracketRe.ReplaceAllString(s, ""))
		return l
	}
	// Précision entre parenthèses ou après une virgule : « oignon (émincé) ».
	if i := strings.IndexAny(s, "(,"); i > 0 {
		l.Note = strings.Trim(strings.TrimSpace(s[i:]), "(), ")
		s = strings.TrimSpace(s[:i])
	}
	l.Name = s
	if l.Milli == 0 && !l.Optional {
		l.ToTaste = true
	}
	return l
}

// cutRunes retire les n premières lettres (forme normalisée et saisie ont
// le même nombre de lettres, hors ligatures).
func cutRunes(s string, n int) string {
	r := []rune(s)
	// « œ » se normalise en « oe » : une lettre de moins dans la saisie.
	extra := 0
	for i := 0; i < n && i < len(r); i++ {
		if r[i] == 'œ' || r[i] == 'æ' || r[i] == 'Œ' || r[i] == 'Æ' {
			extra++
		}
	}
	n -= extra
	if n > len(r) {
		return ""
	}
	return string(r[n:])
}

func cutPrefixFold(s, prefix string) (string, bool) {
	if len(s) >= len(prefix) && strings.EqualFold(s[:len(prefix)], prefix) {
		return s[len(prefix):], true
	}
	return s, false
}
