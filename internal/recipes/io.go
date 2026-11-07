package recipes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"

	"github.com/codecolors/petitchef/internal/platform/db"
	"github.com/codecolors/petitchef/internal/platform/validation"
)

// Format JSON propre à PetitChef (documenté dans docs/FORMATS.md).
const (
	ExportFormat  = "petitchef-recettes"
	ExportVersion = 1
)

// File est un fichier d'export de recettes.
type File struct {
	Format  string       `json:"format"`
	Version int          `json:"version"`
	Recipes []RecipeJSON `json:"recettes"`
}

// RecipeJSON est une recette exportée ; les ingrédients sont au format de
// saisie (« 200 g Farine de blé », « 1 [Pâte brisée maison] »).
type RecipeJSON struct {
	Title       string   `json:"titre"`
	Types       []string `json:"types"`
	Servings    int      `json:"portions"`
	PrepMin     int      `json:"preparation,omitempty"`
	CookMin     int      `json:"cuisson,omitempty"`
	RestMin     int      `json:"repos,omitempty"`
	Difficulty  int      `json:"difficulte,omitempty"`
	Tags        []string `json:"etiquettes,omitempty"`
	Utensils    string   `json:"ustensiles,omitempty"`
	Source      string   `json:"source,omitempty"`
	Notes       string   `json:"notes,omitempty"`
	Ingredients []string `json:"ingredients"`
	Steps       []string `json:"etapes"`
	Grade       string   `json:"indice_manuel,omitempty"`
}

// ToJSON convertit une recette au format d'export.
func ToJSON(r Recipe) RecipeJSON {
	out := RecipeJSON{Title: r.Title, Servings: r.Servings, PrepMin: r.PrepMin, CookMin: r.CookMin, RestMin: r.RestMin,
		Difficulty: r.Difficulty, Tags: r.Tags, Utensils: r.Utensils, Source: r.Source, Notes: r.Notes, Grade: r.BalanceManual}
	for _, t := range AllTypes {
		if r.Types.Has(t.Bit) {
			out.Types = append(out.Types, t.Code)
		}
	}
	group := ""
	for _, in := range r.Ingredients {
		if in.Group != group {
			group = in.Group
			if group != "" {
				out.Ingredients = append(out.Ingredients, group+" :")
			}
		}
		out.Ingredients = append(out.Ingredients, FormatLine(in))
	}
	for _, st := range r.Steps {
		out.Steps = append(out.Steps, st.Text)
	}
	return out
}

// Export écrit les recettes demandées (toutes si ids est vide) au format
// JSON de PetitChef, les recettes utilisées par d'autres en premier.
func (s *Service) Export(ctx context.Context, w io.Writer, ids []int64) error {
	if len(ids) == 0 {
		var err error
		if ids, err = db.Collect(ctx, s.db.R, func(row db.Scanner) (int64, error) {
			var id int64
			return id, row.Scan(&id)
		}, `SELECT id FROM recipes ORDER BY id`); err != nil {
			return err
		}
	}
	f := File{Format: ExportFormat, Version: ExportVersion}
	seen := map[int64]bool{}
	var add func(id int64) error
	add = func(id int64) error {
		if seen[id] {
			return nil
		}
		seen[id] = true
		r, err := s.Get(ctx, id)
		if err != nil {
			return err
		}
		for _, in := range r.Ingredients {
			if in.SubRecipe > 0 {
				if err := add(in.SubRecipe); err != nil {
					return err
				}
			}
		}
		f.Recipes = append(f.Recipes, ToJSON(r))
		return nil
	}
	for _, id := range ids {
		if err := add(id); err != nil {
			return err
		}
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(f)
}

// SchemaOrg écrit une recette au format schema.org Recipe (JSON-LD).
func SchemaOrg(r Recipe) map[string]any {
	var cats []string
	for _, t := range AllTypes {
		if r.Types.Has(t.Bit) {
			cats = append(cats, t.Label)
		}
	}
	steps := make([]map[string]string, 0, len(r.Steps))
	for _, st := range r.Steps {
		steps = append(steps, map[string]string{"@type": "HowToStep", "text": st.Text})
	}
	ings := make([]string, 0, len(r.Ingredients))
	for _, in := range r.Ingredients {
		ings = append(ings, FormatLine(in))
	}
	out := map[string]any{
		"@context":           "https://schema.org",
		"@type":              "Recipe",
		"name":               r.Title,
		"recipeYield":        fmt.Sprintf("%d portions", r.Servings),
		"recipeCategory":     strings.Join(cats, ", "),
		"keywords":           strings.Join(r.Tags, ", "),
		"recipeIngredient":   ings,
		"recipeInstructions": steps,
	}
	if r.PrepMin > 0 {
		out["prepTime"] = isoDuration(r.PrepMin)
	}
	if r.CookMin > 0 {
		out["cookTime"] = isoDuration(r.CookMin)
	}
	out["totalTime"] = isoDuration(r.TotalMin())
	if r.Source != "" {
		out["isBasedOn"] = r.Source
	}
	return out
}

func isoDuration(min int) string {
	if min >= 60 {
		if min%60 == 0 {
			return fmt.Sprintf("PT%dH", min/60)
		}
		return fmt.Sprintf("PT%dH%dM", min/60, min%60)
	}
	return fmt.Sprintf("PT%dM", min)
}

var durationRe = regexp.MustCompile(`^P(?:(\d+)D)?(?:T(?:(\d+)H)?(?:(\d+)M)?(?:\d+S)?)?$`)

// parseDuration lit une durée ISO 8601 (« PT1H30M ») en minutes.
func parseDuration(s string) int {
	m := durationRe.FindStringSubmatch(strings.ToUpper(strings.TrimSpace(s)))
	if m == nil {
		return 0
	}
	d, _ := strconv.Atoi(m[1])
	h, _ := strconv.Atoi(m[2])
	mi, _ := strconv.Atoi(m[3])
	return d*1440 + h*60 + mi
}

// ImportResult résume un import.
type ImportResult struct {
	Created  int
	Products int // produits créés à la volée (« à compléter »)
	Skipped  []string
}

// Import lit un fichier de recettes : format JSON de PetitChef, ou
// schema.org Recipe (objet, liste ou @graph). Les ingrédients sont
// rapprochés du catalogue ; un produit inconnu est créé à la volée (à
// compléter). Une recette dont le titre existe déjà est ignorée.
func (s *Service) Import(ctx context.Context, r io.Reader) (ImportResult, error) {
	var res ImportResult
	raw, err := io.ReadAll(io.LimitReader(r, 20<<20))
	if err != nil {
		return res, err
	}
	list, err := decodeRecipes(raw)
	if err != nil {
		return res, err
	}
	for _, rj := range list {
		if _, err := s.ByTitle(ctx, rj.Title); err == nil {
			res.Skipped = append(res.Skipped, rj.Title)
			continue
		} else if !errors.Is(err, db.ErrNotFound) {
			return res, err
		}
		in, created, err := s.inputFromJSON(ctx, rj)
		if err != nil {
			return res, fmt.Errorf("« %s » : %w", rj.Title, err)
		}
		if _, err := s.Save(ctx, 0, in); err != nil {
			if msg, ok := validation.Message(err); ok {
				return res, validation.Errorf("« %s » : %s", rj.Title, msg)
			}
			return res, err
		}
		res.Created++
		res.Products += created
	}
	return res, nil
}

func (s *Service) inputFromJSON(ctx context.Context, rj RecipeJSON) (Input, int, error) {
	in := Input{Title: rj.Title, Servings: rj.Servings, PrepMin: rj.PrepMin, CookMin: rj.CookMin, RestMin: rj.RestMin,
		Difficulty: rj.Difficulty, Tags: rj.Tags, Utensils: rj.Utensils, Source: rj.Source, Notes: rj.Notes, Steps: rj.Steps, BalanceManual: rj.Grade}
	if in.Servings <= 0 {
		in.Servings = 4
	}
	for _, t := range rj.Types {
		if bit, ok := TypeByCode(strings.TrimSpace(t)); ok {
			in.Types |= bit
		}
	}
	if in.Types == 0 {
		in.Types = Main
	}
	resolved, err := s.Resolve(ctx, ParseLines(strings.Join(rj.Ingredients, "\n")))
	if err != nil {
		return in, 0, err
	}
	created := 0
	for i, x := range resolved {
		if x.SubRecipe == 0 && x.Line.SubRecipe {
			return in, 0, validation.Errorf("la recette « %s » utilisée comme ingrédient est introuvable", x.Name)
		}
		if x.Confidence < Partial && !x.Line.SubRecipe {
			id, err := s.catalog.QuickCreate(ctx, x.Name)
			if err != nil {
				return in, 0, err
			}
			resolved[i].ProductID, resolved[i].Confidence = id, Exact
			created++
		}
	}
	in.Ingredients = resolved
	return in, created, nil
}

// decodeRecipes reconnaît le format d'un fichier importé.
func decodeRecipes(raw []byte) ([]RecipeJSON, error) {
	var f File
	if err := json.Unmarshal(raw, &f); err == nil && f.Format == ExportFormat {
		if f.Version > ExportVersion {
			return nil, validation.Error("Fichier d'une version plus récente de PetitChef.")
		}
		return f.Recipes, nil
	}
	var any any
	if err := json.Unmarshal(raw, &any); err != nil {
		return nil, validation.Error("Fichier illisible : JSON attendu (export PetitChef ou schema.org Recipe).")
	}
	var out []RecipeJSON
	collectSchemaRecipes(any, &out)
	if len(out) == 0 {
		return nil, validation.Error("Aucune recette reconnue dans ce fichier.")
	}
	return out, nil
}

// collectSchemaRecipes parcourt un document JSON-LD et convertit chaque
// objet de type Recipe.
func collectSchemaRecipes(v any, out *[]RecipeJSON) {
	switch x := v.(type) {
	case []any:
		for _, e := range x {
			collectSchemaRecipes(e, out)
		}
	case map[string]any:
		if isRecipeType(x["@type"]) {
			*out = append(*out, fromSchema(x))
			return
		}
		if g, ok := x["@graph"]; ok {
			collectSchemaRecipes(g, out)
		}
	}
}

func isRecipeType(t any) bool {
	switch x := t.(type) {
	case string:
		return x == "Recipe"
	case []any:
		for _, e := range x {
			if s, ok := e.(string); ok && s == "Recipe" {
				return true
			}
		}
	}
	return false
}

func str(v any) string {
	switch x := v.(type) {
	case string:
		return strings.TrimSpace(x)
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case []any:
		if len(x) > 0 {
			return str(x[0])
		}
	case map[string]any:
		return str(x["name"])
	}
	return ""
}

var firstNumber = regexp.MustCompile(`\d+`)

func fromSchema(m map[string]any) RecipeJSON {
	r := RecipeJSON{Title: str(m["name"]), PrepMin: parseDuration(str(m["prepTime"])), CookMin: parseDuration(str(m["cookTime"])),
		Source: str(m["url"])}
	if r.Source == "" {
		r.Source = str(m["isBasedOn"])
	}
	if n := firstNumber.FindString(str(m["recipeYield"])); n != "" {
		r.Servings, _ = strconv.Atoi(n)
	}
	if total := parseDuration(str(m["totalTime"])); total > r.PrepMin+r.CookMin {
		r.RestMin = total - r.PrepMin - r.CookMin
	}
	cat := strings.ToLower(str(m["recipeCategory"]))
	for _, t := range AllTypes {
		if strings.Contains(cat, strings.ToLower(t.Label)) || strings.Contains(cat, t.Code) {
			r.Types = append(r.Types, t.Code)
		}
	}
	for k := range strings.SplitSeq(str(m["keywords"]), ",") {
		if k = strings.TrimSpace(k); k != "" && len(r.Tags) < 10 {
			r.Tags = append(r.Tags, k)
		}
	}
	if ings, ok := m["recipeIngredient"].([]any); ok {
		for _, i := range ings {
			if s := str(i); s != "" {
				r.Ingredients = append(r.Ingredients, s)
			}
		}
	}
	r.Steps = instructions(m["recipeInstructions"])
	return r
}

func instructions(v any) []string {
	var out []string
	switch x := v.(type) {
	case string:
		for line := range strings.SplitSeq(x, "\n") {
			if line = strings.TrimSpace(line); line != "" {
				out = append(out, line)
			}
		}
	case []any:
		for _, e := range x {
			out = append(out, instructions(e)...)
		}
	case map[string]any:
		if items, ok := x["itemListElement"]; ok {
			return instructions(items)
		}
		if t := str(x["text"]); t != "" {
			out = append(out, t)
		}
	}
	return out
}
