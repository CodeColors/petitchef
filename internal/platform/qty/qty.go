// Package qty manipule les quantités sans flottants : saisie décimale en
// millièmes, unités de saisie usuelles et affichage à la française.
//
// Une quantité de catalogue est un entier dans l'unité de base du produit :
// grammes, millilitres ou millièmes de pièce (pour « ½ citron »).
package qty

import (
	"strconv"
	"strings"

	"github.com/codecolors/petitchef/internal/platform/text"
	"github.com/codecolors/petitchef/internal/platform/validation"
)

// Base est l'unité de base d'un produit.
type Base string

const (
	Gram  Base = "g"
	Milli Base = "ml"
	Piece Base = "piece"
)

// Bases liste les unités de base dans l'ordre d'affichage.
var Bases = []Base{Gram, Milli, Piece}

// Label décrit l'unité de base.
func (b Base) Label() string {
	switch b {
	case Gram:
		return "grammes"
	case Milli:
		return "millilitres"
	}
	return "pièces"
}

// Valid indique une unité de base connue.
func (b Base) Valid() bool { return b == Gram || b == Milli || b == Piece }

// Dimension est la grandeur mesurée par une unité de saisie.
type Dimension int

const (
	Other  Dimension = iota // unité propre au produit (gousse, sachet…)
	Mass                    // en milligrammes
	Volume                  // en microlitres
	Count                   // en millièmes de pièce
)

// unit est une unité de saisie générique : sa grandeur et sa valeur en
// millièmes de l'unité de la grandeur (mg, µl, millième de pièce).
type unit struct {
	dim    Dimension
	factor int64
	label  string
}

// units sont les unités génériques reconnues partout.
var units = map[string]unit{
	"g":      {Mass, 1000, "g"},
	"kg":     {Mass, 1_000_000, "kg"},
	"pincee": {Mass, 500, "pincée"},
	"ml":     {Volume, 1000, "ml"},
	"cl":     {Volume, 10_000, "cl"},
	"dl":     {Volume, 100_000, "dl"},
	"l":      {Volume, 1_000_000, "L"},
	"cs":     {Volume, 15_000, "c. à soupe"},
	"cc":     {Volume, 5_000, "c. à café"},
	"tasse":  {Volume, 250_000, "tasse"},
	"verre":  {Volume, 200_000, "verre"},
	"piece":  {Count, 1000, ""},
}

// Generic renvoie la grandeur et le facteur d'une unité générique.
func Generic(code string) (Dimension, int64, bool) {
	u, ok := units[code]
	return u.dim, u.factor, ok
}

// aliases associe les écritures courantes (forme normalisée par
// text.Fold, sans point final) à leur code.
var aliases = map[string]string{
	"g": "g", "gr": "g", "gramme": "g", "grammes": "g",
	"kg": "kg", "kilo": "kg", "kilos": "kg", "kilogramme": "kg", "kilogrammes": "kg",
	"ml": "ml", "millilitre": "ml", "millilitres": "ml",
	"cl": "cl", "centilitre": "cl", "centilitres": "cl",
	"dl": "dl", "decilitre": "dl", "decilitres": "dl",
	"l": "l", "litre": "l", "litres": "l",
	"cs": "cs", "c.s": "cs", "c. s": "cs", "cas": "cs", "c. a s": "cs", "c.a.s": "cs", "c. a soupe": "cs", "c.a soupe": "cs",
	"cuillere a soupe": "cs", "cuilleres a soupe": "cs", "cuil. a soupe": "cs", "cuil a soupe": "cs", "c a soupe": "cs",
	"cc": "cc", "c.c": "cc", "c. c": "cc", "cac": "cc", "c. a c": "cc", "c.a.c": "cc", "c. a cafe": "cc", "c.a cafe": "cc",
	"cuillere a cafe": "cc", "cuilleres a cafe": "cc", "cuil. a cafe": "cc", "cuil a cafe": "cc", "c a cafe": "cc",
	"pincee": "pincee", "pincees": "pincee",
	"tasse": "tasse", "tasses": "tasse", "verre": "verre", "verres": "verre",
	"piece": "piece", "pieces": "piece", "pc": "piece", "pcs": "piece", "u": "piece", "unite": "piece", "unites": "piece",
	"gousse": "gousse", "gousses": "gousse", "tranche": "tranche", "tranches": "tranche",
	"sachet": "sachet", "sachets": "sachet", "botte": "botte", "bottes": "botte", "brin": "brin", "brins": "brin",
	"feuille": "feuille", "feuilles": "feuille", "boite": "boite", "boites": "boite", "pot": "pot", "pots": "pot",
	"poignee": "poignee", "poignees": "poignee", "noix": "noix", "noisette": "noisette", "filet": "filet",
	"bouquet": "bouquet", "bouquets": "bouquet", "branche": "branche", "branches": "branche", "rouleau": "rouleau", "rouleaux": "rouleau",
	"tablette": "tablette", "tablettes": "tablette", "carre": "carre", "carres": "carre", "brique": "brique", "briques": "brique",
	"bouteille": "bouteille", "bouteilles": "bouteille", "paquet": "paquet", "paquets": "paquet", "lamelle": "lamelle", "lamelles": "lamelle",
}

// otherLabels nomme les unités propres aux produits.
var otherLabels = map[string][2]string{
	"gousse": {"gousse", "gousses"}, "tranche": {"tranche", "tranches"}, "sachet": {"sachet", "sachets"},
	"botte": {"botte", "bottes"}, "brin": {"brin", "brins"}, "feuille": {"feuille", "feuilles"},
	"boite": {"boîte", "boîtes"}, "pot": {"pot", "pots"}, "poignee": {"poignée", "poignées"},
	"noix": {"noix", "noix"}, "noisette": {"noisette", "noisettes"}, "filet": {"filet", "filets"},
	"bouquet": {"bouquet", "bouquets"}, "branche": {"branche", "branches"}, "rouleau": {"rouleau", "rouleaux"},
	"tablette": {"tablette", "tablettes"}, "carre": {"carré", "carrés"}, "brique": {"brique", "briques"},
	"bouteille": {"bouteille", "bouteilles"}, "paquet": {"paquet", "paquets"}, "lamelle": {"lamelle", "lamelles"},
	"pincee": {"pincée", "pincées"}, "cs": {"c. à soupe", "c. à soupe"}, "cc": {"c. à café", "c. à café"},
	"tasse": {"tasse", "tasses"}, "verre": {"verre", "verres"},
}

// Canonical renvoie le code d'une unité saisie (« c. à soupe » → « cs »),
// ou "" si elle est inconnue.
func Canonical(s string) string {
	f := strings.TrimSuffix(strings.TrimSpace(text.Fold(s)), ".")
	f = strings.ReplaceAll(f, "à", "a")
	if c, ok := aliases[f]; ok {
		return c
	}
	if _, ok := units[f]; ok {
		return f
	}
	if _, ok := otherLabels[f]; ok {
		return f
	}
	return ""
}

// UnitLabel nomme une unité de saisie, accordée à la quantité (millièmes).
func UnitLabel(code string, milli int64) string {
	if l, ok := otherLabels[code]; ok {
		if milli > 1999 {
			return l[1]
		}
		return l[0]
	}
	if u, ok := units[code]; ok {
		return u.label
	}
	return code
}

// fractions reconnues à la saisie.
var fractions = map[string]int64{"½": 500, "¼": 250, "¾": 750, "⅓": 333, "⅔": 667, "⅛": 125}

// ParseDecimal lit une quantité saisie (« 2 », « 1,5 », « 0.25 », « ½ »,
// « 1/2 », « 1 ½ », « 1 1/2 ») et la renvoie en millièmes.
func ParseDecimal(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, validation.Error("Quantité manquante.")
	}
	var total int64
	parts := strings.Fields(s)
	if len(parts) > 2 {
		return 0, validation.Errorf("Quantité « %s » illisible.", s)
	}
	for _, p := range parts {
		v, err := parsePart(p)
		if err != nil {
			return 0, validation.Errorf("Quantité « %s » illisible.", s)
		}
		total += v
	}
	if total <= 0 || total > 1_000_000_000_000 {
		return 0, validation.Errorf("Quantité « %s » hors limites.", s)
	}
	return total, nil
}

func parsePart(p string) (int64, error) {
	for sym, v := range fractions {
		if p == sym {
			return v, nil
		}
		if before, ok := strings.CutSuffix(p, sym); ok {
			n, err := strconv.ParseInt(before, 10, 64)
			return n*1000 + v, err
		}
	}
	if num, den, ok := strings.Cut(p, "/"); ok {
		n, err1 := strconv.ParseInt(num, 10, 64)
		d, err2 := strconv.ParseInt(den, 10, 64)
		if err1 != nil || err2 != nil || d <= 0 || n < 0 {
			return 0, strconv.ErrSyntax
		}
		return (n*1000 + d/2) / d, nil
	}
	p = strings.Replace(p, ",", ".", 1)
	whole, frac, _ := strings.Cut(p, ".")
	if whole == "" {
		whole = "0"
	}
	n, err := strconv.ParseInt(whole, 10, 64)
	if err != nil || n < 0 {
		return 0, strconv.ErrSyntax
	}
	if len(frac) > 3 {
		frac = frac[:3]
	}
	for len(frac) < 3 {
		frac += "0"
	}
	f, err := strconv.ParseInt(frac, 10, 64)
	if err != nil {
		return 0, err
	}
	return n*1000 + f, nil
}

// FormatDecimal écrit des millièmes à la française : « 1,5 », « 0,25 »,
// « 12 ».
func FormatDecimal(milli int64) string {
	neg := milli < 0
	if neg {
		milli = -milli
	}
	s := strconv.FormatInt(milli/1000, 10)
	if r := milli % 1000; r != 0 {
		f := strings.TrimRight(strconv.FormatInt(1000+r, 10)[1:], "0")
		s += "," + f
	}
	if neg {
		s = "−" + s
	}
	return s
}

// FormatPieces écrit un nombre de pièces (millièmes) avec les fractions
// usuelles : « ½ », « 1 ½ », « 2 », « 0,3 ».
func FormatPieces(milli int64) string {
	whole, r := milli/1000, milli%1000
	var frac string
	switch r {
	case 0:
		return strconv.FormatInt(whole, 10)
	case 500:
		frac = "½"
	case 250:
		frac = "¼"
	case 750:
		frac = "¾"
	default:
		return FormatDecimal(milli)
	}
	if whole == 0 {
		return frac
	}
	return strconv.FormatInt(whole, 10) + " " + frac
}

// Format écrit une quantité dans l'unité de base : « 250 g », « 1,2 kg »,
// « 75 cl », « 1,5 L », « 3 pièces ».
func Format(v int64, b Base) string {
	const nb = " "
	switch b {
	case Gram:
		if v >= 1000 || v <= -1000 {
			return FormatDecimal(round(v, 10)) + nb + "kg"
		}
		return strconv.FormatInt(v, 10) + nb + "g"
	case Milli:
		if v >= 1000 || v <= -1000 {
			return FormatDecimal(round(v, 10)) + nb + "L"
		}
		if v >= 100 && v%10 == 0 {
			return strconv.FormatInt(v/10, 10) + nb + "cl"
		}
		return strconv.FormatInt(v, 10) + nb + "ml"
	}
	n := FormatPieces(v)
	if v > 1999 || v < -1999 {
		return n + nb + "pièces"
	}
	return n + nb + "pièce"
}

// FormatShort écrit une quantité sans le mot « pièce » : « 3 », « 250 g ».
func FormatShort(v int64, b Base) string {
	if b == Piece {
		return FormatPieces(v)
	}
	return Format(v, b)
}

// round arrondit v au multiple de step le plus proche.
func round(v, step int64) int64 {
	if v < 0 {
		return -round(-v, step)
	}
	return (v + step/2) / step * step
}

// CeilDiv renvoie le quotient de a par b arrondi au supérieur (a, b > 0).
func CeilDiv(a, b int64) int64 { return (a + b - 1) / b }

// FormatInput écrit une quantité de base pour un champ de saisie, dans
// l'unité de saisie naturelle (g, ml, pièces) : « 250 », « 1,5 ».
func FormatInput(v int64, b Base) string {
	if b == Piece {
		return FormatDecimal(v)
	}
	return strconv.FormatInt(v, 10)
}

// Choice est une unité proposée à la saisie d'un ingrédient.
type Choice struct {
	Code  string
	Label string
}

// KitchenUnits liste les unités de cuisine proposées à la saisie d'un
// ingrédient, dans l'ordre d'affichage ; « piece » vaut « à la pièce »
// (« 3 œufs »).
var KitchenUnits = []Choice{
	{"piece", "pièce(s)"}, {"g", "grammes (g)"}, {"kg", "kilogrammes (kg)"}, {"ml", "millilitres (ml)"}, {"cl", "centilitres (cL)"},
	{"dl", "décilitres (dL)"}, {"l", "litres (L)"}, {"cs", "cuillère(s) à soupe"}, {"cc", "cuillère(s) à café"},
	{"pincee", "pincée(s)"}, {"tasse", "tasse(s)"}, {"verre", "verre(s)"}, {"gousse", "gousse(s)"}, {"tranche", "tranche(s)"},
	{"sachet", "sachet(s)"}, {"botte", "botte(s)"}, {"bouquet", "bouquet(s)"}, {"brin", "brin(s)"}, {"branche", "branche(s)"},
	{"feuille", "feuille(s)"}, {"poignee", "poignée(s)"}, {"noix", "noix (de beurre…)"}, {"noisette", "noisette(s)"},
	{"filet", "filet(s)"}, {"boite", "boîte(s)"}, {"pot", "pot(s)"}, {"brique", "brique(s)"}, {"bouteille", "bouteille(s)"},
	{"paquet", "paquet(s)"}, {"rouleau", "rouleau(x)"}, {"tablette", "tablette(s)"}, {"carre", "carré(s)"}, {"lamelle", "lamelle(s)"},
}

// IsKitchenUnit indique une unité de la liste proposée.
func IsKitchenUnit(code string) bool {
	for _, c := range KitchenUnits {
		if c.Code == code {
			return true
		}
	}
	return false
}
