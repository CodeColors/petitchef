# Décisions techniques

Chaque décision indique son contexte et sa raison. Une décision remplacée est
mise à jour sur place (l'historique reste dans Git).

## 0. Ce que PetitChef reprend de BuddyGet

PetitChef reprend le socle de [BuddyGet](https://github.com/CodeColors/buddyget)
(même auteur, AGPL-3.0) par **copie adaptée**, sans dépendance au module
BuddyGet : les deux applications évoluent indépendamment.

| Brique BuddyGet | Reprise | Adaptation |
|---|---|---|
| `internal/platform/db` (WAL, une connexion d'écriture, 4 lectures `query_only`, migrateur embarqué, `Snapshot`, `Verify`) | telle quelle | `db.ErrNotFound` commun à tous les domaines |
| `internal/platform/{text,validation,money,calendar}` | tels quels | jours fériés retirés (inutiles ici) |
| `internal/auth` (argon2id 19 Mio/2 passes, sessions SHA-256 glissantes, limitation des tentatives, thème) | tel quel | — |
| `internal/config` (variables d'environnement) | tel quel | `PHOTOS_QUOTA_MB` remplace `ATTACHMENTS_QUOTA_MB` ; base `petitchef.db` |
| `internal/backup` (archive tar.gz chiffrée `age`, rotation, copie dossier/WebDAV, restauration au démarrage) | tel quel | les photos (`/data/photos`) remplacent les pièces jointes |
| `internal/scheduler` (un minuteur réarmé chaque jour à 00h05) | tel quel | — |
| `internal/alerts` (alertes à clé unique, acquittement, SMTP, webhook/ntfy, discrétion, rythme) | adapté | les examens sont **fournis par les domaines** (`alerts.Check`) : le paquet ne dépend plus d'aucun domaine ; six types propres à PetitChef |
| `internal/attachments/image.go` (réduction mémoire-économe à 1600 px, EXIF) | adapté | devient `internal/photos` |
| `internal/web` (middlewares, CSP stricte, `CrossOriginProtection`, rendu en tampon, assets embarqués compressés et versionnés, thème sans flash) | adapté | navigation Accueil / Menus / Courses / Stock / Plus, bouton flottant contextuel, palette verte, styles d'impression |
| `Dockerfile`, `docker-compose.yml`, CI, `.golangci.yml`, e2e Playwright + axe | adaptés | mêmes contrôles de taille d'image et de mémoire au repos |

## Socle (jalon 1)

Les décisions du socle de BuddyGet s'appliquent sans changement ; elles sont
rappelées ici pour que ce document se suffise.

- **Go 1.27**, binaire statique `CGO_ENABLED=0`, bibliothèque standard d'abord.
- **SQLite `modernc.org/sqlite`** : WAL, `synchronous=NORMAL`,
  `busy_timeout=5000`, `foreign_keys=1`, cache borné à 2 Mio par connexion ;
  une connexion d'écriture (`_txlock=immediate`), quatre connexions de
  lecture `query_only` fermées après 5 minutes d'inactivité.
- **Migrations** `NNNN_description.sql` embarquées, une transaction chacune,
  numérotation continue, base plus récente que le programme refusée.
- **CSRF** par `http.CrossOriginProtection` (Go ≥ 1.25) et cookies
  `SameSite=Lax` ; aucun jeton à glisser dans les formulaires ni dans HTMX.
- **Sessions** : jeton de 128 bits, seule son empreinte SHA-256 en base,
  30 jours glissants, `Secure` dès que `BASE_URL` est en HTTPS.
- **Mots de passe** : argon2id 19 Mio / 2 passes / 1 fil, deux calculs
  simultanés au plus, tampon rendu au système après chaque calcul.
- **Anti-bruteforce** : 5 échecs en 15 minutes par IP ou par identifiant.
- **Thèmes** : `data-theme` rendu côté serveur, couleurs `light-dark()`,
  aucun script ni flash ; l'impression est toujours en clair.
- **Image** `scratch`, utilisateur 65532, `GOMEMLIMIT=100MiB`,
  `HEALTHCHECK` par `petitchef healthcheck`, compilation croisée amd64/arm64.
- **Journaux** `slog` texte ; les requêtes ne sont journalisées qu'en `debug`,
  chemin seul.
- **Réglages** divers dans une table `settings` (clé → JSON) plutôt qu'une
  table par réglage : générateur, grille d'équilibre, jour des courses, jeton
  iCal. Une seule ligne lue à la demande, sans cache.

### Alertes : examens fournis par les domaines
Dans BuddyGet, `alerts` importait tous les domaines. Ici, chaque domaine
(stock, menus, courses) fournit une fonction `alerts.Check` inscrite au
démarrage : le système d'alertes reste **unique** (une table, une cloche, un
relais de notifications) sans dépendre des domaines, ce qui évite les cycles
d'import (le stock déclenche des alertes, les alertes lisent le stock).

## Catalogue (jalon 2)

### Un seul référentiel : `products`
Il n'existe pas de table d'ingrédients : une ligne de recette, une ligne de
stock et une ligne de liste de courses référencent toutes `products.id`.
Les rapprochements (menu ↔ stock ↔ courses) sont donc des égalités
d'identifiants, sans heuristique.

### Quantités entières dans l'unité de base
Grammes, millilitres ou **millièmes de pièce** (« ½ citron » = 500), en
`int64`. La saisie décimale (« 1,5 », « ½ », « 1 1/2 ») est lue en
millièmes par `internal/platform/qty`, sans flottant. Les conversions d'un
produit (`catalog.Conv`) combinent :

- les unités génériques (g, kg, ml, cl, dl, L, c. à soupe = 15 ml,
  c. à café = 5 ml, pincée = 0,5 g, tasse, verre) ;
- le poids d'une pièce (`piece_g`) : « 1 oignon » → 120 g ;
- la densité (`density`, g/L) entre volume et masse ; à défaut, celle de
  l'eau, approximation assumée et signalée dans la documentation ;
- des unités propres au produit (`product_units` : gousse, sachet,
  tranche, ou une cuillère de farine = 10 g plutôt que 15 ml × densité).

Une conversion impossible (pièce sans poids connu) est signalée, jamais
devinée.

### Arrondis
- **Conditionnement** : le paquet qui couvre le besoin avec le moins de
  surplus, puis le moins de paquets (300 g avec 250 g et 500 g → 1 × 500 g).
- **Pièces mises à l'échelle** : pièce entière, ou demi-pièce si la recette
  utilisait déjà une fraction ; jamais moins d'une demi-pièce.

### Base de départ : YAML par rayon, chargée après les migrations
Le cahier des charges demandait un chargement « par la première
migration ». Les migrations étant du SQL pur (migrateur de BuddyGet),
la base est chargée par `catalog.SyncSeed`, **juste après les migrations**,
dans une transaction, et seulement si `settings.catalog_seed_version` est
inférieure à `catalog.SeedVersion`. Avantages : format source lisible
(`internal/catalog/seed/*.yaml`, une ligne par produit, valeurs par défaut
par rayon), contrôlé par un test, et mises à jour possibles :

- un produit de la base porte `origin = 'seed'` et un identifiant stable
  `seed_key` (nom normalisé) ;
- toute modification dans l'interface pose `modified = 1` : les versions
  suivantes de la base ne touchent plus ce produit ;
- un nom déjà pris par un produit de l'utilisateur n'est pas réimporté.

880 produits (19 rayons), sans prix, code-barres ni valeur nutritionnelle
(laissés vides). Durées de conservation indicatives, modifiables. Coût :
160 ms au premier démarrage, rien ensuite.

### Recherche de produits
Forme normalisée (`catalog.Normalize` : casse, accents, ponctuation,
pluriels simples) stockée dans `products.search` et
`product_synonyms.search`, toutes deux `UNIQUE` : un nom ou un synonyme ne
peut désigner qu'un produit. La recherche instantanée est un `LIKE` préfixe
sur ces colonnes (≈ 1 000 lignes, moins d'une milliseconde) ; FTS5 est
réservé aux recettes, où le texte libre le justifie.

### Sélection d'un produit : `<datalist>` + création à la volée
Partout où l'on choisit un produit, un champ texte propose le catalogue
par une liste native du navigateur, remplie par HTMX à la frappe. Sans
script, la saisie libre fonctionne : le serveur résout le nom (nom ou
synonyme) et, s'il est inconnu, **crée le produit en une étape** avec le
rayon, l'unité, l'univers et le groupe du produit existant le plus proche
(mot commun), marqué « à compléter ». Aucun composant JavaScript de liste
déroulante n'est nécessaire.

### Régimes : traits des produits, compatibilité calculée
Un produit porte des **traits** (chair animale, origine animale, porc,
alcool) et ses **allergènes** (14 allergènes réglementaires, en bits). Les
régimes (végétarien, végan, sans gluten, sans porc, sans lactose, sans
alcool) s'en déduisent (`catalog.Compatible`) : une recette n'a jamais à
déclarer sa compatibilité, elle la calcule de ses ingrédients. La base de
départ déduit les traits du groupe alimentaire et des allergènes, avec
des corrections explicites (`traits +porc`, `-animal` pour les boissons
végétales).

### Magasins : ordre des rayons
`store_aisle_order` donne la position de chaque rayon dans le parcours
d'un magasin. Glisser-déposer (amélioration progressive dans `app.js`) et
boutons « monter / descendre » (formulaires, accessibles au clavier et
sans script) utilisent le même service.

## Inventaire et stock minimum (jalons 3 et 4)

### Lots et journal unique
Le stock est fait de **lots** (`stock_items`) : produit, emplacement,
quantité, date limite (DLC ou DDM), date d'ouverture, prix payé, origine.
Toute variation passe par `stock_movements`, journal **unique** :
achat, ajout, consommation par recette, consommation manuelle, perte,
correction d'inventaire, transfert, ouverture. Statistiques (gaspillage,
consommation), estimation de consommation et annulation lisent ce seul
journal.

### Annulation par action
Chaque action reçoit un numéro (`batch`) commun à ses mouvements. Annuler
rejoue les mouvements à l'envers (quantités, dates d'un lot ouvert) et les
marque `undone`, en refusant si le stock a changé depuis d'une manière qui
rendrait le résultat négatif. Un lot vidé n'est pas supprimé tout de suite
(`emptied_on`) : il peut être restauré ; la tâche quotidienne purge les lots
vides depuis plus de 30 jours, leurs mouvements restant au journal.
L'interface propose « Annuler » pour la dernière action de l'utilisateur
(15 minutes) : un bandeau, sans pile d'historique à gérer.

### FEFO
« Premier périmé, premier sorti » : `stock.PlanFEFO` (pure, testée)
ordonne les lots par date limite, lots sans date en dernier. Un lot périmé
à la date du repas ne compte pas comme disponible pour le menu ou la liste ;
lors du décompte réel d'une recette cuisinée, il sert en dernier recours
(on a cuisiné avec ce qu'on avait).

### Dates limites proposées
À l'ajout : aujourd'hui + conservation « fermé » du produit, ou
« congelé » si l'emplacement est de type congelé. « Ouvert aujourd'hui »
ramène la date à aujourd'hui + conservation « ouvert » si c'est plus tôt.
Un déplacement vers le congélateur propose la conservation au congélateur.
Valeurs indicatives et modifiables à chaque étape.

### Gestes rapides sans script
−/+ (un conditionnement, ou une pièce), Fini, Ouvert, Jeté, Déplacer :
chaque geste est un petit formulaire POST qui revient à la page d'origine
(ancre sur le lot). Pas de JavaScript nécessaire, pas d'état à synchroniser.

### Valeur du gaspillage
La valeur d'une perte est calculée au moment du mouvement : prix payé du
lot au prorata, sinon dernier prix connu du produit, sinon inconnue. Elle
est figée dans `value_cents` : les statistiques n'ont pas à reconstituer
des prix passés.

### Stock minimum et consommation estimée
`stock_settings` porte minimum, cible, suivi et signalement « presque
vide ». La consommation moyenne est estimée sur 180 jours à partir des
mouvements de consommation et de cuisine (ni pertes, ni corrections), dès
deux consommations sur au moins 14 jours : total consommé ÷ durée depuis la
première consommation observée. Calcul quotidien par la tâche planifiée,
stocké en millièmes d'unité par jour. La date de rupture prévue en découle
(`stock.RunOutDate`) ; la liste de courses l'utilise (jalon 8).

### Modèles de stock
Trois modèles (salle de bain, ménage, placard de base) décrits en Go
(noms du catalogue, minimum, cible) : les appliquer règle les produits
correspondants, modifiables ensuite. Pas de table : ce sont des suggestions,
pas des données.

### Scan de code-barres
`BarcodeDetector` du navigateur (Chrome Android notamment), chargé par
`scan.js` uniquement sur l'écran de scan ; ailleurs ou sans cette API, la
saisie du code fonctionne. Aucune bibliothèque de décodage n'est
embarquée. Un code inconnu est associé au produit choisi lors de l'ajout :
il sera reconnu ensuite. La politique `Permissions-Policy` autorise la
caméra pour l'application elle-même.
