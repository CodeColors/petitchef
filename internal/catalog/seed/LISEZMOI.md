# Base de départ du catalogue

Un fichier YAML par rayon. Chaque fichier donne :

- `rayon` : identifiant du rayon (table `aisles`) ;
- `defauts` : valeurs par défaut des produits du fichier ;
- `produits` : une ligne par produit, `Nom | clé valeur | clé valeur…`.

| Clé | Sens | Exemple |
|---|---|---|
| `syn` | synonymes, séparés par des virgules | `syn patate, patates` |
| `univers` | `alimentaire`, `entretien`, `hygiene`, `maison`, `animaux`, `bebe` | |
| `unite` | unité de base : `g`, `ml` ou `piece` | |
| `piece` | poids d'une pièce, en grammes | `piece 150` |
| `densite` | grammes par litre (liquides et poudres) | `densite 920` |
| `u` | conversions propres : unité=quantité de base | `u cs=10, cc=3` |
| `cond` | conditionnements usuels, en unités de base ; libellé après `:` | `cond 250, 6000:6 × 1 L` |
| `cons` | conservation indicative en jours : fermé/ouvert/congelé, `-` si sans objet | `cons 7/3/240` |
| `date` | `DLC` ou `DDM` (raccourcis `dlc`, `ddm` sans valeur) | |
| `groupe` | groupe alimentaire (indice d'équilibre) | `groupe legumes` |
| `saison` | mois de saison : plage `6-9`, `11-2`, liste `3,4` ou `toute` | |
| `all` | allergènes réglementaires (codes de `catalog.AllAllergens`) | `all gluten, lait` |
| `traits` | indicateurs de régime ajoutés (`+porc`, `+alcool`, `+viande`, `+animal`) ou retirés (`-animal`) | |
| `emp` | emplacement de rangement par défaut | `emp refrigerateur` |
| `base` | produit de base (sans valeur) : non suivi au gramme | |
| `cle` | identifiant stable, si le nom doit changer | |

Règles :

- **aucun prix, code-barres ni valeur nutritionnelle** : ces champs viennent
  de l'usage ;
- les durées de conservation sont **indicatives** (usage domestique courant),
  jamais réglementaires ;
- l'identifiant stable d'un produit est son nom normalisé : renommer un
  produit crée un nouveau produit, sauf à fixer `cle` à l'ancien
  identifiant ;
- toute modification de ces fichiers s'accompagne d'une augmentation de
  `catalog.SeedVersion` ; les produits modifiés par l'utilisateur ne sont
  jamais écrasés.

`go test ./internal/catalog` vérifie la cohérence de la base (doublons,
rayons, conversions, groupes).
