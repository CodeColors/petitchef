# Performance

Mesures du 7 novembre 2026. Machine de développement : Intel Xeon, 4 cœurs ;
conteneur limité comme dans `docker-compose.yml` (`--memory 128m --cpus 0.5`).

## Image et conteneur

| Mesure | Valeur | Cible |
|---|---|---|
| Image `linux/amd64` | 26,6 Mo (7,8 Mo compressée) | < 50 Mo |
| Image `linux/arm64` | 7,2 Mo compressée (construite par compilation croisée) | — |
| Démarrage à froid (première fois : migrations + 880 produits + 70 recettes) | 843 ms | < 3 s |
| Démarrage suivant | 22 ms | < 3 s |
| Mémoire au repos (démarrage) | 16,5 Mio | < 64 Mo (objectif 30) |
| Mémoire au repos après la charge ci-dessous | 31 Mio | < 64 Mo |
| CPU au repos | 0 % (aucune interrogation périodique) | ~0 % |

La CI reconstruit l'image et échoue si elle dépasse 50 Mo ou 64 Mo de
mémoire au repos (étape « Taille, démarrage et mémoire au repos »).

## Pics (conteneur 128 Mo / 0,5 CPU)

Pic mesuré par le cgroup (`memory.max_usage_in_bytes`, cache disque compris),
remis à zéro avant chaque opération.

| Opération | Durée | Pic mémoire | Cible |
|---|---|---|---|
| Import de 1 000 recettes (JSON, 8 ingrédients chacune) | 4,8 s | 38 Mio | < 128 Mo |
| Génération de 4 semaines, 2 repas/jour, 1 070 recettes | 320 ms | 42 Mio | < 1 s, < 128 Mo |
| Liste de courses sur 4 semaines | 162 ms (requête HTTP complète) | 46 Mio | < 300 ms |
| Photo de 12 Mpx (7,6 Mo) réduite à 1 600 px | 900 ms | 74 Mio | < 128 Mo |

Après une génération, un import ou un recalcul de liste, la mémoire de
travail est rendue au système (`PRAGMA shrink_memory` et
`debug.FreeOSMemory`, comme dans BuddyGet) : sans cela, le repos après charge
restait à 46 Mio.

## Bancs d'essai (`go test -bench`)

| Banc | Résultat | Remarque |
|---|---|---|
| `planner.BenchmarkGenerate4Weeks500Recipes` | 83 ms, 11 Mo alloués | moteur seul : 4 semaines, 2 repas/jour, entrée + plat, 500 recettes, 1 000 lots |
| `shopping.BenchmarkCompute` | 0,49 ms | 840 besoins de menu, 1 000 lots, 300 produits |
| `menus.BenchmarkGenerateSeed` | 26 ms | génération complète (base comprise), 4 semaines, 70 recettes |
| `catalog.BenchmarkSyncSeed` | 152 ms | création de la base et chargement des 880 produits |

Sur un Raspberry Pi 4 (cœur environ trois fois plus lent), le moteur reste
nettement sous la seconde pour 500 recettes.

### Optimisation notable
Le premier profil du générateur montrait deux tiers du temps dans l'analyse
des dates (`time.Parse`). Les dates sont désormais converties une fois en
numéros de jour et les raisons (textes) ne sont construites qu'au moment de
l'affichage : de 221 ms à 68–83 ms.

## Garde-fous
- Requêtes paginées (catalogue, stock, recettes) ; jamais de table chargée
  entière, sauf les recettes et lots pour les moteurs (quelques milliers de
  lignes, chargés en trois requêtes).
- Une seule réduction de photo à la fois ; images de plus de 40 Mpx refusées.
- Index : lots par produit, emplacement et date limite ; mouvements par
  produit et date ; ingrédients par recette ; repas par date.
- Pages < 150 Ko hors photos : la plus lourde (liste de courses de 37 lignes)
  fait 52 Ko non compressés ; CSS et JS servis compressés (gzip) et mis en
  cache un an.
