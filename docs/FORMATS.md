# Formats d'import et d'export

## Recettes : JSON PetitChef (version 1)

```json
{
  "format": "petitchef-recettes",
  "version": 1,
  "recettes": [
    {
      "titre": "Quiche lorraine",
      "types": ["plat"],
      "portions": 6,
      "preparation": 15,
      "cuisson": 35,
      "repos": 0,
      "difficulte": 1,
      "etiquettes": ["four", "classique"],
      "ustensiles": "moule à tarte",
      "source": "",
      "notes": "",
      "ingredients": [
        "1 [Pâte brisée maison]",
        "200 g Lardons fumés",
        "3 Œufs",
        "20 cl Crème liquide entière",
        "Poivre noir moulu"
      ],
      "etapes": ["Préchauffer le four à 200 °C…", "Cuire 35 min."],
      "indice_manuel": "C"
    }
  ]
}
```

- `types` : `entree`, `plat`, `dessert`, `petit_dejeuner`, `gouter`,
  `accompagnement`, `sauce`.
- `ingredients` : une ligne par ingrédient, au format de saisie de
  l'application : quantité (« 2 », « 1,5 », « ½ »), unité facultative (g,
  kg, ml, cl, L, c. à soupe, c. à café, pincée, gousse, sachet, tranche,
  botte, boîte…), nom du produit, mentions « (facultatif) », « (au goût) »,
  précision entre parenthèses. Une ligne terminée par « : » ouvre un groupe.
  Un nom entre crochets désigne une autre recette (recette composée) ; elle
  est exportée avant celle qui l'utilise.
- À l'import, un ingrédient inconnu crée un produit « à compléter » ; une
  recette dont le titre existe déjà est ignorée.

## Recettes : schema.org `Recipe` (JSON-LD)

Export d'une recette (`name`, `recipeYield`, `prepTime`, `cookTime`,
`totalTime` en ISO 8601, `recipeCategory`, `keywords`, `recipeIngredient`,
`recipeInstructions` en `HowToStep`). À l'import : objet seul, liste ou
`@graph`, instructions en texte, liste, `HowToStep` ou `HowToSection`.

## Exports CSV

Séparateur point-virgule, encodage UTF-8 avec BOM (ouverture directe dans un
tableur), dates JJ/MM/AAAA :

- **menus** : Date ; Repas ; Format ; Rôle ; Plat ; Portions ; Indice ; État ;
- **inventaire** : Produit ; Rayon ; Emplacement ; Quantité ; Unité ; Date
  limite ; Type de date ; Ouvert le ; Ajouté le ; Origine ;
- **historique des achats** : Date ; Produit ; Magasin ; Quantité ; Unité ;
  Prix (EUR).

### Achats pour BuddyGet
`achats-buddyget.csv` : virgule, en-tête `Date,Libellé,Montant`, dates ISO,
montant négatif à point décimal, une ligne par jour et par magasin
(« Courses Hyper »). Il s'importe dans BuddyGet avec le **profil générique**
(Importer > choisir le compte > déposer le fichier).

## Agenda iCal
`/ical/<jeton>.ics` (adresse donnée dans **Exports**, révocable) : un
`VEVENT` par repas planifié, des 7 derniers jours aux 8 semaines à venir,
fuseau Europe/Paris, heures indicatives par créneau (déjeuner 12h30, dîner
19h30).
