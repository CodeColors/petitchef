-- Recettes et membres du foyer (jalons 5 et 6).

-- Membres du foyer, distincts des comptes de connexion : portion relative
-- (millièmes : 1000 = adulte), allergènes et régimes (bits, voir
-- internal/catalog).
CREATE TABLE household_members (
    id        INTEGER PRIMARY KEY,
    name      TEXT    NOT NULL,
    portion   INTEGER NOT NULL DEFAULT 1000 CHECK (portion BETWEEN 100 AND 5000),
    allergens INTEGER NOT NULL DEFAULT 0,
    diets     INTEGER NOT NULL DEFAULT 0,
    archived  INTEGER NOT NULL DEFAULT 0 CHECK (archived IN (0, 1))
) STRICT;

-- Aliments refusés par un membre (produits du catalogue).
CREATE TABLE member_refusals (
    member_id  INTEGER NOT NULL REFERENCES household_members (id) ON DELETE CASCADE,
    product_id INTEGER NOT NULL REFERENCES products (id) ON DELETE CASCADE,
    PRIMARY KEY (member_id, product_id)
) STRICT, WITHOUT ROWID;

-- Recettes. types : bits (entrée, plat, dessert, petit-déjeuner, goûter,
-- accompagnement, sauce). Les champs « calculés » (allergènes, traits,
-- saison, indice, protéine principale…) sont recalculés depuis les
-- ingrédients à chaque enregistrement, jamais saisis.
CREATE TABLE recipes (
    id             INTEGER PRIMARY KEY,
    title          TEXT    NOT NULL,
    search         TEXT    NOT NULL,
    types          INTEGER NOT NULL CHECK (types > 0),
    servings       INTEGER NOT NULL CHECK (servings BETWEEN 1 AND 100),
    prep_min       INTEGER NOT NULL DEFAULT 0 CHECK (prep_min >= 0),
    cook_min       INTEGER NOT NULL DEFAULT 0 CHECK (cook_min >= 0),
    rest_min       INTEGER NOT NULL DEFAULT 0 CHECK (rest_min >= 0),
    difficulty     INTEGER NOT NULL DEFAULT 1 CHECK (difficulty BETWEEN 1 AND 3),
    utensils       TEXT    NOT NULL DEFAULT '',
    source         TEXT    NOT NULL DEFAULT '',
    notes          TEXT    NOT NULL DEFAULT '',
    photo          TEXT    NOT NULL DEFAULT '',
    variant_of     INTEGER REFERENCES recipes (id) ON DELETE SET NULL,
    -- calculés
    allergens      INTEGER NOT NULL DEFAULT 0,
    traits         INTEGER NOT NULL DEFAULT 0,
    season         INTEGER NOT NULL DEFAULT 4095,
    balance_score  INTEGER NOT NULL DEFAULT 0,
    balance_grade  TEXT    NOT NULL DEFAULT 'C',
    balance_manual TEXT    CHECK (balance_manual IN ('A', 'B', 'C', 'D', 'E')),
    protein        TEXT    NOT NULL DEFAULT '',
    starch_id      INTEGER,
    heavy          INTEGER NOT NULL DEFAULT 0 CHECK (heavy IN (0, 1)),
    veg_share      INTEGER NOT NULL DEFAULT 0,
    -- historique (mis à jour quand un repas est marqué cuisiné)
    cooked_count   INTEGER NOT NULL DEFAULT 0,
    last_cooked    TEXT,
    origin         TEXT    NOT NULL DEFAULT 'utilisateur' CHECK (origin IN ('seed', 'utilisateur')),
    seed_key       TEXT    UNIQUE,
    modified       INTEGER NOT NULL DEFAULT 0 CHECK (modified IN (0, 1)),
    created_at     TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
) STRICT;

CREATE INDEX recipes_types ON recipes (types);

-- Ingrédients : un produit du catalogue OU une autre recette (recette
-- composée, quantité en millièmes de portion). qty est en unités de base du
-- produit ; input_qty (millièmes) et input_unit gardent la saisie.
CREATE TABLE recipe_ingredients (
    id            INTEGER PRIMARY KEY,
    recipe_id     INTEGER NOT NULL REFERENCES recipes (id) ON DELETE CASCADE,
    position      INTEGER NOT NULL,
    group_name    TEXT    NOT NULL DEFAULT '',
    product_id    INTEGER REFERENCES products (id),
    sub_recipe_id INTEGER REFERENCES recipes (id),
    qty           INTEGER NOT NULL DEFAULT 0 CHECK (qty >= 0),
    input_qty     INTEGER NOT NULL DEFAULT 0,
    input_unit    TEXT    NOT NULL DEFAULT '',
    note          TEXT    NOT NULL DEFAULT '',
    optional      INTEGER NOT NULL DEFAULT 0 CHECK (optional IN (0, 1)),
    to_taste      INTEGER NOT NULL DEFAULT 0 CHECK (to_taste IN (0, 1)),
    CHECK ((product_id IS NULL) <> (sub_recipe_id IS NULL))
) STRICT;

CREATE INDEX recipe_ingredients_recipe ON recipe_ingredients (recipe_id, position);
CREATE INDEX recipe_ingredients_product ON recipe_ingredients (product_id);
CREATE INDEX recipe_ingredients_sub ON recipe_ingredients (sub_recipe_id) WHERE sub_recipe_id IS NOT NULL;

CREATE TABLE recipe_steps (
    recipe_id INTEGER NOT NULL REFERENCES recipes (id) ON DELETE CASCADE,
    position  INTEGER NOT NULL,
    text      TEXT    NOT NULL,
    timer_sec INTEGER NOT NULL DEFAULT 0 CHECK (timer_sec >= 0),
    PRIMARY KEY (recipe_id, position)
) STRICT, WITHOUT ROWID;

CREATE TABLE recipe_tags (
    recipe_id INTEGER NOT NULL REFERENCES recipes (id) ON DELETE CASCADE,
    tag       TEXT    NOT NULL,
    PRIMARY KEY (recipe_id, tag)
) STRICT, WITHOUT ROWID;

CREATE INDEX recipe_tags_tag ON recipe_tags (tag);

-- Avis de chaque membre : note (0 = aucune), favori, « à ne plus proposer ».
CREATE TABLE recipe_ratings (
    recipe_id INTEGER NOT NULL REFERENCES recipes (id) ON DELETE CASCADE,
    member_id INTEGER NOT NULL REFERENCES household_members (id) ON DELETE CASCADE,
    stars     INTEGER NOT NULL DEFAULT 0 CHECK (stars BETWEEN 0 AND 5),
    favorite  INTEGER NOT NULL DEFAULT 0 CHECK (favorite IN (0, 1)),
    banned    INTEGER NOT NULL DEFAULT 0 CHECK (banned IN (0, 1)),
    comment   TEXT    NOT NULL DEFAULT '',
    PRIMARY KEY (recipe_id, member_id)
) STRICT, WITHOUT ROWID;

-- Recherche plein texte, insensible aux accents ; tenue à jour par
-- l'application à chaque enregistrement (rowid = recipes.id).
CREATE VIRTUAL TABLE recipes_fts USING fts5 (title, ingredients, tags, tokenize = 'unicode61 remove_diacritics 2');
