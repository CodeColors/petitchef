# Plan et structure

## Jalons

| # | Jalon | État |
|---|---|---|
| 1 | Socle repris de BuddyGet : dépôt, Dockerfile multi-arch, SQLite et migrations, authentification, gabarit, thèmes, navigation mobile, CI, `/healthz` | fait |
| 2 | Catalogue : produits, unités et conversions, conditionnements, rayons, magasins et ordre des rayons, emplacements, base de départ (880 produits) | fait |
| 3 | Inventaire : lots, dates limites, gestes rapides, journal, mode inventaire, à consommer en priorité, scan | fait |
| 4 | Stock minimum et consommation, modèles de stock | fait |
| 5 | Recettes : saisie, ingrédients liés, étapes, échelle, composées, avis, indice d'équilibre, FTS5, 70 recettes, import/export | fait |
| 6 | Foyer et créneaux : membres, régimes, créneaux, semaines types | fait |
| 7 | Générateur de menus : contraintes, critères, explicabilité, édition, restes | fait |
| 8 | Liste de courses : moteur unique, tri par magasin, mode magasin hors ligne, rangement, prix | fait |
| 9 | Cuisiné et décompte, mode cuisine, « que cuisiner ce soir » | fait |
| 10 | Alertes et notifications, statistiques | fait |
| 11 | Exports, impressions, iCal, sauvegardes | fait |
| 12 | Durcissement : mesures, accessibilité, e2e, documentation, recette | fait |
| 13 | Phase 2 : Open Food Facts, import de recette par URL | à faire (architecture prête, voir ci-dessous) |

## Questions bloquantes
Aucune : les choix ouverts ont été tranchés et consignés dans
`DECISIONS.md` (chargement des bases de départ après les migrations, une
photo par recette, pas de table `menus`, période de la liste, etc.).

## Phase 2 : points d'entrée prévus
- **Import de recette par URL** : `recipes.decodeRecipes` lit déjà le JSON-LD
  schema.org (objet, liste, `@graph`) ; il restera à télécharger la page
  (délai et taille bornés) et à extraire les blocs
  `<script type="application/ld+json">`, derrière un réglage désactivé par
  défaut.
- **Open Food Facts** : à l'écran de scan, un code inconnu pourra préremplir
  la fiche produit (nom, marque, valeurs nutritionnelles, allergènes) ; les
  champs `brand`, `kcal`… et `product_barcodes` existent déjà.
- Seules exceptions prévues à « aucun appel sortant », toutes deux
  désactivées par défaut.

## Structure du dépôt
Voir `ARCHITECTURE.md`.
