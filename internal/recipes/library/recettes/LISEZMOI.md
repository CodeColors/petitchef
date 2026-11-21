# Bibliothèque de recettes

Environ 500 recettes, chargées une fois au premier démarrage (puis à chaque
nouvelle version de `library.Version`, pour les seules recettes jamais
proposées). Une recette supprimée par l'utilisateur ne revient pas.

Format, un bloc par recette :

```
= Titre
types | portions | préparation | cuisson[/repos] | étiquettes | difficulté
200 g Farine de blé
> Première étape.
```

- `types` : `entree`, `plat`, `dessert`, `petit_dejeuner`, `gouter`,
  `accompagnement`, `sauce` (plusieurs, séparés par des virgules) ;
- durées en minutes ; difficulté 1 à 3 (1 par défaut) ;
- ingrédients au format de saisie de PetitChef ; chaque nom doit être un
  produit (ou un synonyme) du catalogue ; `[Titre]` désigne une recette
  de base (fichier `00-bases.txt`, chargé en premier) ;
- étiquettes utiles : `gourmand` (plats riches, à réserver), `équilibré`,
  `rapide`, `végétarien`, `enfants`, `four`, `batch cooking`, `été`,
  `hiver`, `convivial`.

`go test ./internal/recipes/library` vérifie que chaque ingrédient
correspond exactement au catalogue, que chaque quantité se convertit dans
l'unité de base du produit et s'achète dans un conditionnement du commerce.
