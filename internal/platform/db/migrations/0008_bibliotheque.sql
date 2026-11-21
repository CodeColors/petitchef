-- Bibliothèque de recettes embarquée : clés des recettes déjà proposées.
-- Une recette de la bibliothèque n'est ajoutée qu'une fois : supprimée par
-- l'utilisateur, elle ne revient pas aux versions suivantes.
CREATE TABLE recipe_library (
    key      TEXT PRIMARY KEY,
    added_on TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
) STRICT, WITHOUT ROWID;
