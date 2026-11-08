# Jeu de départ des recettes

Recettes familiales rédigées pour PetitChef (aucune n'est copiée d'un site),
une liste YAML par fichier ; les fichiers sont lus dans l'ordre de leur nom
(`00-bases.yaml` d'abord : ses recettes servent d'ingrédients aux autres).

| Clé | Sens |
|---|---|
| `titre` | titre, unique |
| `types` | `entree`, `plat`, `dessert`, `petit_dejeuner`, `gouter`, `accompagnement`, `sauce` (séparés par des virgules) |
| `portions` | nombre de portions de base |
| `preparation`, `cuisson`, `repos` | minutes |
| `difficulte` | 1 (facile) à 3 |
| `etiquettes` | étiquettes libres, séparées par des virgules |
| `ingredients` | une ligne par ingrédient, au format de saisie de l'application : `200 g Farine de blé`, `2 c. à soupe Huile d'olive`, `3 Œufs`, `Sel fin` (au goût), `1 [Pâte brisée maison]` (recette composée) |
| `etapes` | liste d'étapes ; une durée (« 20 min ») devient une minuterie |

Chaque nom d'ingrédient doit désigner **exactement** un produit (nom ou
synonyme) du catalogue : `go test ./internal/recipes` le vérifie. Toute
modification s'accompagne d'une augmentation de `recipes.SeedVersion`.
