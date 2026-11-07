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

## Recettes (jalon 5)

### Ingrédients saisis en texte, rapprochés du catalogue
Le formulaire d'une recette prend ses ingrédients en **texte libre**, une
ligne par ingrédient ou une liste collée (« 200 g de farine, 3 œufs, 1
pincée de sel »). `recipes.ParseLine` en extrait quantité (décimale,
fraction, « ½ »), unité (g, kg, cl, c. à soupe, gousse, sachet…), nom,
précision (« émincé »), mentions « facultatif » et « au goût ». Le
rapprochement (`recipes.Service.Resolve`) essaie, dans l'ordre : le nom
exact ou un synonyme ; l'unité propre faisant partie du nom (« 4 filets de
saumon ») ; le nom raccourci (« oignon émincé » → « oignon ») ; une
proposition de la recherche. Dès qu'une ligne est incertaine, le formulaire
revient avec un **écran de validation des correspondances**, ligne par
ligne, où l'on corrige ou crée le produit à la volée. Le même format sert à
l'édition (les ingrédients enregistrés sont réécrits en texte relisible),
au jeu de départ et à l'export JSON : un seul analyseur, testé.

### Quantités enregistrées deux fois
`recipe_ingredients.qty` est en unités de base (calculs), `input_qty` et
`input_unit` gardent la saisie (affichage : « 2 c. à soupe » plutôt que
« 30 ml »). Les contenants sans conversion propre (boîte, paquet, sachet…)
valent le plus petit conditionnement du produit.

### Recettes composées
Une ligne entre crochets désigne une autre recette (« 1 [Pâte brisée
maison] », quantité en portions). `recipes.Expand` déroule récursivement
les besoins en produits ; une boucle est refusée à l'enregistrement
(`CheckCycle`) et détectée au déroulé. Modifier une recette recalcule
celles qui l'utilisent.

### Indicateurs calculés, jamais saisis
Allergènes, traits de régime, saison (mois où tous les ingrédients frais
saisonniers le sont), indice d'équilibre, protéine principale, féculent
principal, « copieux » et part de végétaux sont recalculés à chaque
enregistrement (et après la modification d'un produit) puis stockés dans
`recipes` : la recherche et le générateur filtrent en SQL sans relire les
ingrédients.

### Indice d'équilibre
Score = Σ (coefficient du groupe × part du poids en %), groupes neutres
(condiments, boissons, non classés) exclus du poids ; lettre par seuils.
Grille par défaut : légumes et légumineuses +1, fruits et poisson +0,8,
féculents complets +0,6, volaille et œufs +0,3, produits laitiers +0,2,
féculents raffinés 0, fromage −0,3, viande rouge −0,5, plats transformés
−0,8, charcuterie −1, produits sucrés −1,2, matières grasses ajoutées −1,5 ;
seuils A ≥ 35, B ≥ 15, C ≥ −5, D ≥ −25. Paramétrable (écran « Grille de
l'indice d'équilibre », tout est recalculé), expliqué sur chaque recette
(contribution de chaque groupe), corrigeable à la main (« ajusté à la
main »). Ce n'est pas un Nutri-Score et l'interface le dit. L'estimation
nutritionnelle par portion n'apparaît que si tous les ingrédients pesés ont
leurs valeurs renseignées.

### Recherche plein texte
`recipes_fts` (FTS5, `unicode61 remove_diacritics 2`) indexe titre,
ingrédients et étiquettes ; tenue à jour par l'application à chaque
enregistrement (le contenu combine plusieurs tables, des déclencheurs
seraient fragiles). Chaque mot saisi devient un préfixe obligatoire.

### Avis par membre
Note, favori, « à ne plus proposer », commentaire : une ligne par recette
et par membre (`recipe_ratings`). L'historique « cuisinée » est un compteur
et une date tenus à jour quand un repas est marqué cuisiné (jalon 9).

### Jeu de départ
70 recettes familiales rédigées pour PetitChef (YAML, même format de
saisie), dont 3 bases réutilisées (pâte brisée, béchamel, vinaigrette).
Chargé comme le catalogue, après lui, sans écraser une recette modifiée ;
un test exige que chaque ingrédient corresponde **exactement** à un produit.

### Une photo par recette
Une seule photo (`recipes.photo`) plutôt qu'une table `recipe_photos` :
suffisant pour une fiche et le mode cuisine, et plus simple à sauvegarder.
Les photos sont réduites à 1600 px (code de BuddyGet, mémoire bornée),
dans `/data/photos`, sous quota ; une variante partage la photo de
l'original jusqu'à ce qu'on la remplace.

### Import et export
JSON propre (`petitchef-recettes`, version 1, documenté dans
`docs/FORMATS.md`) et schema.org `Recipe` (JSON-LD, objet, liste ou
`@graph`, durées ISO 8601, sections d'instructions). À l'import, un
ingrédient inconnu crée un produit « à compléter » plutôt que d'échouer ;
une recette dont le titre existe est ignorée. Le lecteur schema.org servira
tel quel à l'import par URL (phase 2).

## Foyer, créneaux et générateur de menus (jalons 6 et 7)

### Repas datés plutôt qu'une table « menus »
Un menu est l'ensemble des repas (`meals`, un par date et créneau) et de
leurs plats (`menu_entries`, un par rôle). Pas de table `menus`
intermédiaire : la semaine affichée, la génération, l'historique et la
liste de courses travaillent tous sur des plages de dates. Les « menus
enregistrés » (`saved_menus`) sont des modèles relatifs (jour 0, 1…).

### Semaine type → repas, puis réglages ponctuels
La semaine type fixe, pour chaque jour et créneau, le format (plat seul,
entrée + plat, plat + dessert, complet, pas de repas à la maison), les
membres présents et les invités. Un repas est créé d'après elle **une
seule fois** (`EnsureMeals`) ; ses réglages restent ensuite modifiables
sans que la semaine type ne les écrase.

### Portions
Portions d'un repas = Σ portions relatives des membres présents
(adulte 1, enfant 0,5, ado 1,25…) + invités (1 chacun) ; 2 par défaut tant
que le foyer n'a pas de membre. Un plat cuisiné pour deux repas porte les
portions des deux.

### Moteur pur, service qui charge
`internal/planner` ne lit pas la base : il reçoit créneaux, recettes
candidates (besoins par portion, avis, coût), stock, historique et
réglages. Le service des menus charge tout en une dizaine de requêtes
(`recipes.All` charge toutes les recettes et leurs ingrédients en trois),
appelle le moteur et écrit le résultat en une transaction.

### Algorithme
1. **Contraintes strictes** (jamais violées) : type adapté au rôle, temps
   de préparation + cuisson ≤ maximum du créneau, allergies et régimes des
   présents, aliments refusés, recettes « à ne plus proposer ». La règle
   « pas la même recette avant N jours » est stricte tant qu'il reste des
   recettes admissibles, levée sinon (une case n'est jamais vide par excès
   de variété).
2. **Score pondéré** (poids 0 à 10, préréglages Équilibré, Économique,
   Anti-gaspi, Rapide) : stock (part du poids des ingrédients disponible à
   la date du repas, bonus si un produit périme dans les 3 jours), indice
   d'équilibre, saison, notes et favoris, coût (par rapport au coût médian),
   rapidité, variété (protéine et féculent différents des repas voisins,
   recette peu cuisinée récemment) ; ajustements : règles de la semaine
   (poisson au moins N fois, viande rouge au plus N fois, au moins N repas
   végétariens), cohérence du repas (entrée et dessert légers si le plat est
   copieux), légumes à chaque repas.
3. **Affectation gloutonne** dans l'ordre chronologique, plat d'abord (la
   cohérence de l'entrée et du dessert en dépend), avec un **stock virtuel**
   consommé au fil du menu (deux recettes ne comptent pas deux fois les
   mêmes courgettes).
4. **Amélioration locale bornée** (au plus 240 essais, 8 alternatives) qui
   remplace un plat quand la note globale y gagne, règles de la semaine
   comprises.
5. Égalités départagées par un bruit déterministe dérivé de la **graine** :
   même graine, même menu (testé).

Pas de solveur, pas d'IA, pas d'appel réseau. Mesure : 68 ms pour 4
semaines, 2 repas par jour, entrée + plat, 500 recettes et 1 000 lots en
stock sur la machine de développement (voir `PERFORMANCE.md`). Les dates
sont converties une fois en numéros de jour : leur analyse sur chaînes
représentait d'abord les deux tiers du temps.

### Explicabilité
Chaque plat garde ses raisons (`menu_entries.reasons`, JSON) : « utilise
les courgettes qui périment jeudi », « favori », « de saison », « 25 min »,
« poisson de la semaine », « restes du dîner de lundi »… Le bilan de la
semaine (indice moyen, répartition des protéines, coût estimé, part des
ingrédients déjà en stock) est recalculé à l'affichage par le même moteur.

### Restes, plats prêts, verrouillage
- « Cuisiner pour deux repas » : option du générateur (recettes étiquetées
  batch cooking ou mijoté, servies au dîner) et action manuelle ; les restes
  vont au repas du lendemain midi, le plat d'origine porte les portions des
  deux, les restes n'ont rien à acheter.
- Les plats prêts en stock (lots liés à une recette : fait maison,
  restes, congélateur) sont proposés en priorité quand ils suffisent.
- Verrouillé ou cuisiné : conservé tel quel à la régénération.
- « Pas cuisiné / reporté » : le plat reste au menu (ses ingrédients restent
  réservés pour la liste de courses) jusqu'à ce qu'on le replace.

### « Que cuisiner ce soir ? »
Même moteur (`planner.RankTonight`), préréglage anti-gaspi, contraintes du
foyer entier ; classement « faisable maintenant » puis par part en stock.
Depuis un lot qui périme, « Trouver une recette » filtre sur ce produit.

### Échange de plats
Glisser-déposer sur ordinateur (amélioration progressive) et liste
« Échanger avec » sur mobile et sans script : même action serveur.
