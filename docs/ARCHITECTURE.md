# Architecture

PetitChef est un binaire Go unique : serveur HTTP, pages, fichiers
statiques, migrations, catalogue et recettes de départ y sont embarqués.
Les données tiennent dans un fichier SQLite et un dossier de photos sous
`DATA_DIR`. Le socle serveur (base, authentification, sauvegardes, alertes)
est une copie adaptée de BuddyGet ; l'interface est propre à PetitChef ;
voir `DECISIONS.md`.

## Principes

- **Un seul mécanisme par besoin** : un référentiel de produits
  (`catalog`), un journal de stock (`stock_movements`), un moteur de menus
  (`planner`), un moteur de liste (`shopping.Compute`), un système
  d'alertes (`alerts`), un point d'entrée d'exports (`exports`).
- **Moteurs purs, services qui chargent** : générateur de menus, liste de
  courses, FEFO, estimation de consommation, indice d'équilibre, mise à
  l'échelle et analyse des ingrédients sont des fonctions pures testées sans
  base ; les services lisent la base en peu de requêtes et écrivent le
  résultat en une transaction.
- **Aucune logique métier dans les handlers HTTP**.
- **Entiers partout** : quantités dans l'unité de base (g, ml, millièmes de
  pièce), montants en centimes, dates civiles `AAAA-MM-JJ`.
- **Aucune tâche de fond permanente** : un minuteur quotidien (00h05) ; les
  alertes sont examinées après chaque action.

## Paquets

```
cmd/petitchef/        point d'entrée : configuration, restauration en attente,
                      base, chargement des bases de départ, services, tâche
                      quotidienne ; sous-commandes healthcheck, reset-password
internal/
├── config/           variables d'environnement → Config validée
├── platform/
│   ├── db/           SQLite (WAL, écriture unique, lecture query_only),
│   │   └── migrations/  migrateur et NNNN_*.sql embarqués
│   ├── qty/          quantités sans flottants : saisie décimale, unités, affichage
│   ├── money/        centimes : saisie et affichage à la française
│   ├── calendar/     dates civiles
│   ├── text/         forme de comparaison (casse, accents)
│   └── validation/   erreur de saisie destinée à l'utilisateur
├── auth/             comptes, argon2id, sessions, limitation des tentatives, thème
├── catalog/          produits, conversions, conditionnements, rayons, magasins,
│   │                 emplacements, prix ; recherche ; création à la volée
│   └── seed/         base de départ : un YAML par rayon (880 produits)
├── stock/            lots, journal des mouvements, FEFO, annulation, inventaire,
│                     stock minimum, consommation estimée, modèles de stock
├── household/        membres du foyer : portions, allergies, régimes, refus
├── recipes/          recettes : analyse des ingrédients en texte, rapprochement
│   │                 du catalogue, mise à l'échelle, recettes composées, indice
│   │                 d'équilibre, recherche FTS5, avis, import/export
│   └── recipetest/   jeu d'essai de 70 recettes, pour les tests seulement
├── planner/          moteur du générateur de menus (pur) et « ce soir »
├── menus/            créneaux, semaines types, repas, génération, édition,
│                     cuisiné (décompte FEFO), menus enregistrés
├── shopping/         moteur de la liste (pur), liste partagée, rangement
├── watch/            examens d'alertes des domaines
├── alerts/           système d'alertes unique, notifications SMTP et webhook
├── stats/            statistiques depuis le journal et les menus
├── exports/          CSV, PDF, iCal
├── photos/           réduction des images, quota
├── backup/           sauvegardes chiffrées, rotation, copie, restauration
├── scheduler/        tâche quotidienne unique
└── web/              routes, middlewares, handlers, rendu
    ├── templates/    layout.html + pages/<page>.html
    └── static/       app.css (charte propre à PetitChef), app.js, htmx, store.js (magasin), cook.js
                      (cuisine), scan.js (code-barres), sw.js, icône, manifeste
e2e/                  tests Playwright (développement et CI)
```

Dépendances entre domaines (pas de cycle) :

```
catalog ← stock ← menus ← shopping ← exports
   ↑        ↑       ↑ ↖
recipes ────┴───────┘  planner (pur)
household ─────────────┘
alerts ← watch (stock, menus, shopping)
stats (base seule)
```

## Chemin d'une requête

```
securityHeaders → logRequests → CrossOriginProtection → ServeMux
                                                         ├─ /healthz, /static/{fichier}, /sw.js
                                                         ├─ /ical/{jeton}.ics (jeton secret)
                                                         ├─ /setup, /login, /logout
                                                         └─ requireUser → pages
                                                                 └─ POST : examen des alertes après une modification réussie
```

Toute action renvoie (303) vers la page d'origine avec un message de
confirmation ; les requêtes HTMX (recherche instantanée, suggestions de
produits) ne reçoivent qu'un bloc de la page.

## Base de données

| Table | Rôle |
|---|---|
| `users`, `sessions` | comptes et sessions (empreinte du jeton) |
| `settings` | réglages clé → JSON : versions des bases de départ, générateur, grille d'équilibre, jour des courses, jeton iCal |
| `alerts`, `alert_settings`, `notification_settings` | alertes et canaux sortants |
| `backup_settings` | sauvegardes |
| `aisles`, `locations`, `stores`, `store_aisle_order` | rayons, emplacements, magasins et parcours |
| `products`, `product_synonyms`, `product_barcodes`, `product_units`, `product_packages` | catalogue |
| `prices` | prix relevés par produit et magasin |
| `stock_items` | lots (quantité, date limite, ouverture, prix, origine, recette) |
| `stock_settings` | minimum, cible, suivi, consommation estimée, « presque vide » |
| `stock_movements` | journal unique des variations (achat, recette, consommation, perte, correction, transfert, ouverture), annulation par action |
| `household_members`, `member_refusals` | foyer |
| `recipes`, `recipe_ingredients`, `recipe_steps`, `recipe_tags`, `recipe_ratings`, `recipes_fts` | recettes (champs calculés stockés), recherche plein texte |
| `meal_slots`, `week_templates`, `week_template_slots`, `week_template_members` | créneaux et semaines types |
| `meals`, `meal_members`, `menu_entries` | menus : un repas par date et créneau, un plat par rôle |
| `saved_menus`, `saved_menu_entries` | menus enregistrés comme modèles |
| `shopping_lists`, `shopping_items` | liste de courses en cours |

Index : produit, emplacement et date limite des lots ; produit et date des
mouvements ; recette des ingrédients ; date des repas ; liste des lignes de
courses.

## Moteurs

### Générateur de menus (`planner`)
Contraintes strictes, score pondéré, affectation gloutonne avec stock
virtuel, amélioration locale bornée, graine ; détail dans `DECISIONS.md`.
Entrée : créneaux (format, portions, contraintes des présents, temps
maximum, plats fixés), recettes candidates (besoins par portion, avis,
coût, saison, protéine, féculent), lots, historique ; sortie : plats et
raisons, bilan.

### Liste de courses (`shopping.Compute`)
Besoins du menu − stock disponible à la date de chaque repas (FEFO,
réservations), puis stock minimum et consommation prévue jusqu'aux courses
suivantes, ajouts manuels ; fusion, arrondi, tri par rayon.

### Recettes
`ParseLine` (texte → quantité, unité, nom), `Resolve` (→ produit),
`Expand` (recettes composées → besoins), `ScaleQty` (mise à l'échelle,
arrondi des pièces), `ComputeBalance` (indice), `Analyze` (indicateurs
calculés).

## Interface

- Rendu serveur `html/template` ; une page = `templates/pages/<nom>.html`
  insérée dans `layout.html` (bloc `content`).
- Mobile d'abord : barre d'onglets en bas sous 768 px (Accueil, Menus,
  Courses, Stock, Plus), sous l'en-tête au-delà ; Recettes et Catalogue
  rejoignent la barre à partir de 1024 px. Bouton d'action flottant
  contextuel (Produit, Article, Recette).
- Thème et taille du texte rendus côté serveur (`data-theme`,
  `data-text`), couleurs `light-dark()`, impression toujours en clair.
- Amélioration progressive : chaque écran fonctionne sans script ; HTMX
  pour la recherche instantanée et les suggestions ; `app.js`
  (confirmations, glisser-déposer, impression, copie) ; scripts propres à
  un écran chargés seulement sur celui-ci (`store.js`, `cook.js`,
  `scan.js`).
- Fichiers statiques embarqués, servis depuis la mémoire, compressés une
  fois, URL versionnées par empreinte ; CSP `default-src 'self'`.
