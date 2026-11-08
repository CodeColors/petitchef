// Package recipetest fournit le jeu d'essai de recettes des tests : 70
// recettes familiales liées au catalogue. Il n'est importé que par des
// tests ; le binaire de PetitChef n'embarque aucune recette.
package recipetest

import (
	"context"
	"embed"
	"io/fs"

	"github.com/codecolors/petitchef/internal/recipes"
)

//go:embed recettes/*.yaml
var files embed.FS

// FS renvoie les fichiers YAML du jeu d'essai.
func FS() fs.FS {
	sub, _ := fs.Sub(files, "recettes")
	return sub
}

// Load enregistre le jeu d'essai dans le service.
func Load(ctx context.Context, s *recipes.Service) error {
	_, err := s.LoadYAML(ctx, FS())
	return err
}
