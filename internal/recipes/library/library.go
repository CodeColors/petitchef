// Package library embarque la bibliothèque de recettes de PetitChef :
// environ 500 plats variés (entrées, plats, desserts, petits-déjeuners,
// goûters), des classiques gourmands aux assiettes équilibrées, liés au
// catalogue. Format : voir recipes.ParseLibrary et recettes/LISEZMOI.md.
package library

import (
	"embed"
	"io/fs"
)

// Version de la bibliothèque : à augmenter à chaque ajout de recettes.
// Seules les recettes jamais proposées sont ajoutées.
const Version = 1

//go:embed recettes/*.txt
var files embed.FS

// FS renvoie les fichiers de la bibliothèque.
func FS() fs.FS {
	sub, _ := fs.Sub(files, "recettes")
	return sub
}
