# PetitChef

Cuisine, inventaire et courses du foyer, **auto-hébergé**. PetitChef tient le
catalogue des produits, le stock (ce qu'on a, où, jusqu'à quand), une base
de recettes avec un indice d'équilibre, génère les menus de la semaine et la
liste de courses triée dans l'ordre de votre magasin.

Un seul conteneur, un seul fichier SQLite, **16 Mo de mémoire au repos**,
une image de **27 Mo**, aucun appel réseau sortant par défaut. Conçu pour un
NAS Synology, un mini-PC ou un Raspberry Pi, à côté de
[BuddyGet](https://github.com/CodeColors/buddyget) dont il reprend le socle.

- **Catalogue** de 880 produits d'hypermarché (alimentaire, hygiène,
  entretien, maison, bébé, animaux) avec rayon, unité, conversions,
  conditionnements, conservation indicative, saison, allergènes ;
  entièrement modifiable, enrichi par l'usage (création à la volée,
  codes-barres).
- **Stock** par lots datés : ajout en quelques secondes (date limite
  proposée), gestes rapides (−/+, fini, ouvert, jeté, déplacé), « à
  consommer en priorité », mode inventaire, scan de code-barres, stock
  minimum et consommation estimée, annulation.
- **Recettes** typées (entrée, plat, dessert…) liées au catalogue, mises à
  l'échelle, composées, notées par chaque membre, avec un **indice
  d'équilibre de A à E** expliqué ; saisie rapide (collez une liste
  d'ingrédients), import et export JSON et schema.org. La base de recettes
  démarre vide : ce sont les vôtres.
- **Menus** générés en moins d'une seconde pour 1 à 8 semaines, au format
  choisi pour chaque repas (plat seul, entrée + plat, plat + dessert,
  complet), en respectant allergies et régimes (option « sans poisson ni
  fruits de mer » comprise), en privilégiant ce qui périme ; chaque choix
  est expliqué ; verrouiller, régénérer, échanger, restes.
- **Liste de courses** : ce qui manque pour le menu et ce qui passera sous
  le minimum avant les courses suivantes, arrondi aux conditionnements, dans
  l'ordre des rayons de votre magasin ; mode magasin utilisable **hors
  ligne** ; « ranger les courses » en une étape.
- Mode cuisine, « que cuisiner ce soir ? », alertes (cloche, e-mail ou ntfy
  facultatifs), statistiques (gaspillage, dépenses, équilibre), exports
  (CSV, PDF, iCal), sauvegardes chiffrées.

Interface en français, chaleureuse et très accessible : mobile d'abord,
thèmes clair et sombre, grande taille de texte en un geste, utilisable au
clavier et sans JavaScript.

## Installation (Docker)

### Docker Compose

```yaml
services:
  petitchef:
    image: ghcr.io/codecolors/petitchef:latest
    container_name: petitchef
    restart: unless-stopped
    ports:
      - "8081:8080"
    environment:
      TZ: Europe/Paris
    volumes:
      - ./petitchef:/data
    mem_limit: 128m
    cpus: 0.5
```

```bash
docker compose up -d
```

Ouvrez `http://<adresse-du-serveur>:8081` : l'assistant crée le premier
compte. Les données (base SQLite, photos, sauvegardes) sont dans le dossier
monté sur `/data`.

L'image est publiée pour `linux/amd64` et `linux/arm64`. Elle tourne sous un
utilisateur non-root (UID 65532 par défaut) : le dossier de données doit lui
être accessible en écriture, ou fixez `user:` à l'UID:GID propriétaire du
dossier (voir le `docker-compose.yml` du dépôt).

### Synology (Container Manager)

1. **File Station** : créez le dossier `docker/petitchef` dans `volume1`.
2. **Container Manager > Projet > Créer** : nom `petitchef`, chemin
   `/volume1/docker/petitchef`, source « Créer docker-compose.yml » ; collez
   le contenu du [`docker-compose.yml`](docker-compose.yml) du dépôt.
3. Adaptez `user:` à votre UID:GID (`id` dans une session SSH ; souvent
   `1026:100`) et le port de gauche s'il est déjà pris (BuddyGet utilise
   8080).
4. Validez : l'image est téléchargée et le conteneur démarre. L'état
   « sain » apparaît après quelques secondes (`HEALTHCHECK` intégré).
5. Facultatif, accès depuis l'extérieur en HTTPS : **Panneau de
   configuration > Portail de connexion > Avancé > Proxy inversé**, source
   `https://cuisine.mondomaine.fr`, destination `http://localhost:8081` ;
   puis renseignez `BASE_URL` et `TRUSTED_PROXIES` (ci-dessous).

### Variables d'environnement

Aucune n'est obligatoire.

| Variable | Défaut | Rôle |
|---|---|---|
| `PORT` | `8080` | port d'écoute dans le conteneur |
| `DATA_DIR` | `/data` | dossier des données (base, photos, sauvegardes) |
| `TZ` | `UTC` | fuseau horaire (`Europe/Paris`) : dates du jour, tâche de nuit |
| `LOG_LEVEL` | `info` | `debug`, `info`, `warn` ou `error` |
| `BASE_URL` | — | adresse publique derrière un reverse proxy HTTPS (cookies `Secure`, HSTS, liens des notifications et de l'agenda) |
| `TRUSTED_PROXIES` | — | réseaux du reverse proxy (CIDR, séparés par des virgules), pour lire l'IP réelle des clients |
| `MAX_UPLOAD_MB` | `10` | taille maximale d'un fichier envoyé (photo, import) |
| `PHOTOS_QUOTA_MB` | `1024` | place maximale des photos |
| `BACKUP_PASSPHRASE` | — | chiffre les sauvegardes (`age`, 12 caractères au moins) ; à conserver hors du serveur |
| `GOMEMLIMIT` | `100MiB` (image) | plafond mémoire du ramasse-miettes Go |

## Premiers pas

1. **Plus > Paramètres > Foyer et membres** : ajoutez les membres (portion
   relative : 1 adulte, 0,5 enfant), leurs allergies, régimes et aliments
   refusés.
2. **Créneaux et semaines types** : qui mange à la maison, quel repas et
   sous quel format, jour par jour ; temps maximum (30 min en semaine le
   soir, par exemple).
3. **Magasins et rayons** : créez votre magasin et rangez ses rayons dans
   l'ordre de votre parcours.
4. **Recettes > Nouvelle recette** : saisissez ou collez vos recettes
   (« 200 g de farine, 3 œufs… »), ou importez un fichier JSON ou
   schema.org.
5. **Stock > Stock minimum** : appliquez un modèle (salle de bain, ménage,
   placard de base), puis ajoutez ce que vous avez (**Stock > Ajouter**).
6. **Menus > Générer** : trois clics pour la semaine.
7. **Courses** : recalculez la liste, passez en **mode magasin** pour faire
   les courses, puis **Ranger les courses** au retour.

## Sauvegarde et restauration

**Plus > Paramètres > Sauvegardes** :

- « Sauvegarder maintenant » produit une archive cohérente (base et photos)
  dans `/data/backups`, chiffrée si `BACKUP_PASSPHRASE` est définie ;
- la sauvegarde automatique de nuit garde les N dernières archives et peut
  être copiée vers un second emplacement (dossier monté ou WebDAV) ;
- « Restaurer » accepte une archive de la liste ou un fichier envoyé :
  PetitChef la vérifie, redémarre et l'applique ; les données remplacées
  sont gardées dans `/data/avant-restauration`.

Procédure manuelle (serveur arrêté) : copier le dossier de données suffit
(`petitchef.db`, `photos/`). Après une restauration manuelle, supprimez les
fichiers `petitchef.db-wal` et `petitchef.db-shm` s'ils sont présents.

Mot de passe oublié :

```bash
docker exec -it petitchef /petitchef reset-password <identifiant>
```

## Mise à jour

```bash
docker compose pull && docker compose up -d
```

Les migrations de la base s'appliquent au démarrage. Les mises à jour du
catalogue n'écrasent jamais un produit que vous avez modifié. Si votre
installation avait reçu les anciennes recettes de départ, celles que vous
n'avez ni modifiées ni utilisées sont retirées automatiquement. Faites une sauvegarde avant une mise à jour
majeure.

## Développement

Go 1.27 ou plus récent ; aucune autre dépendance système.

```bash
make run     # serveur sur :8080, données dans ./data, journaux détaillés
make test    # go test -race ./...
make lint    # go vet + golangci-lint
make e2e     # tests Playwright (Node 22)
```

Documentation :

- [docs/PLAN.md](docs/PLAN.md) : plan par jalons et structure du dépôt ;
- [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) : paquets, base de données,
  chemin d'une requête, moteurs ;
- [docs/DECISIONS.md](docs/DECISIONS.md) : choix techniques, ce qui vient
  de BuddyGet ;
- [docs/PERFORMANCE.md](docs/PERFORMANCE.md) : mesures ;
- [docs/EQUILIBRE.md](docs/EQUILIBRE.md) : la grille de l'indice
  d'équilibre ;
- [docs/FORMATS.md](docs/FORMATS.md) : formats d'import et d'export ;
- [docs/ACCESSIBILITE.md](docs/ACCESSIBILITE.md) ;
- [docs/RECETTE.md](docs/RECETTE.md) : preuve des critères d'acceptation.

## Licence

[AGPL-3.0](LICENSE), comme BuddyGet.
