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
