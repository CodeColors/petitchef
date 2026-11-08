# Accessibilité

Cible : WCAG 2.1 niveau AA, score d'accessibilité Lighthouse ≥ 90.

## Vérifications automatiques
Les tests de bout en bout (`e2e/ui.test.mjs`) passent **axe** (le moteur
d'accessibilité de Lighthouse, règles WCAG 2.0/2.1 A et AA) sur 22 écrans
clés à 1280 px dans les deux thèmes et sur les écrans mobiles principaux à
360 px : **aucune violation**. Ils vérifient aussi, à 360, 768 et 1280 px, en
clair et en sombre :

- aucun défilement horizontal ;
- cibles tactiles d'au moins 44 × 44 px (boutons, liens-boutons, onglets,
  champs, résumés repliables) ;
- champs de saisie d'au moins 16 px (pas de zoom forcé sur iOS) ;
- thème effectivement appliqué.

## Principes appliqués
- **Grande taille de texte** en un geste (menu « Affichage » : 17 px par
  défaut, 20 px en « Grande »), mémorisée par compte, sans script ; tous les
  écrans restent sans défilement horizontal à 360 px (test e2e dédié).
- Couleurs Automatique / Clair / Sombre au même endroit.
- Préférences du système respectées : mouvement réduit, contraste
  renforcé, couleurs forcées de Windows.
- Cibles tactiles de 48 px par défaut (44 px au minimum vérifié), champs à
  bordure de 2 px, corps de texte de 17 px.
- Navigation clavier complète, lien d'évitement « Aller au contenu », focus
  visible (contour de 3 px).
- Chaque champ a son libellé ; aides reliées par `aria-describedby`.
- La couleur ne porte jamais seule l'information : l'indice d'équilibre
  affiche sa lettre (et un texte pour les lecteurs d'écran), les dates
  limites un texte (« périme demain »), les lignes cochées sont barrées et
  annoncées (`aria-pressed`).
- Contrastes AA vérifiés dans les deux thèmes ; impression toujours en clair.
- Alternatives au glisser-déposer : boutons monter/descendre (rayons),
  liste « Échanger avec » (menus).
- Graphiques SVG avec `role="img"`, titre, description et tableau de
  valeurs.
- Actions destructrices confirmées ; messages de confirmation en
  `role="status"`, erreurs en `role="alert"`.
- Mode cuisine : gros caractères, une étape à la fois, écran maintenu
  allumé ; mode magasin : lignes de 64 px, un geste par article.
