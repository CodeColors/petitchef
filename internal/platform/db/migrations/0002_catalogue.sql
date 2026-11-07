-- Catalogue de produits : référentiel unique de l'inventaire, des recettes
-- et des listes de courses (jalon 2).

-- Rayons d'hypermarché ; position = ordre par défaut de la liste de courses.
CREATE TABLE aisles (
    id       INTEGER PRIMARY KEY,
    slug     TEXT    NOT NULL UNIQUE,
    name     TEXT    NOT NULL UNIQUE COLLATE NOCASE,
    position INTEGER NOT NULL
) STRICT;

-- Emplacements de rangement ; kind sert aux durées de conservation
-- (frais, congelé, ambiant).
CREATE TABLE locations (
    id       INTEGER PRIMARY KEY,
    slug     TEXT    UNIQUE,
    name     TEXT    NOT NULL UNIQUE COLLATE NOCASE,
    kind     TEXT    NOT NULL DEFAULT 'ambiant' CHECK (kind IN ('ambiant', 'frais', 'congele')),
    position INTEGER NOT NULL
) STRICT;

CREATE TABLE stores (
    id         INTEGER PRIMARY KEY,
    name       TEXT    NOT NULL UNIQUE COLLATE NOCASE,
    is_default INTEGER NOT NULL DEFAULT 0 CHECK (is_default IN (0, 1))
) STRICT;

-- Ordre des rayons dans le parcours réel d'un magasin ; un rayon absent
-- suit l'ordre par défaut, après les rayons ordonnés.
CREATE TABLE store_aisle_order (
    store_id INTEGER NOT NULL REFERENCES stores (id) ON DELETE CASCADE,
    aisle_id INTEGER NOT NULL REFERENCES aisles (id) ON DELETE CASCADE,
    position INTEGER NOT NULL,
    PRIMARY KEY (store_id, aisle_id)
) STRICT, WITHOUT ROWID;

-- Produits. Les quantités sont des entiers dans l'unité de base : grammes,
-- millilitres ou millièmes de pièce. Durées de conservation en jours
-- (NULL : inconnue). season : bits 0 à 11 = janvier à décembre. allergens :
-- bits des 14 allergènes réglementaires. diet : bits des indicateurs de
-- régime (voir internal/catalog). Valeurs nutritionnelles pour 100 g en
-- dixièmes (énergie en kcal entières), NULL si inconnues.
CREATE TABLE products (
    id            INTEGER PRIMARY KEY,
    name          TEXT    NOT NULL,
    search        TEXT    NOT NULL UNIQUE,
    universe      TEXT    NOT NULL CHECK (universe IN ('alimentaire', 'entretien', 'hygiene', 'maison', 'animaux', 'bebe')),
    aisle_id      INTEGER NOT NULL REFERENCES aisles (id),
    location_id   INTEGER REFERENCES locations (id) ON DELETE SET NULL,
    unit          TEXT    NOT NULL CHECK (unit IN ('g', 'ml', 'piece')),
    piece_g       INTEGER CHECK (piece_g > 0),
    density       INTEGER CHECK (density > 0),
    shelf_closed  INTEGER CHECK (shelf_closed >= 0),
    shelf_opened  INTEGER CHECK (shelf_opened >= 0),
    shelf_frozen  INTEGER CHECK (shelf_frozen >= 0),
    date_kind     TEXT    NOT NULL DEFAULT 'DLC' CHECK (date_kind IN ('DLC', 'DDM')),
    food_group    TEXT    NOT NULL DEFAULT '',
    season        INTEGER NOT NULL DEFAULT 0,
    allergens     INTEGER NOT NULL DEFAULT 0,
    diet          INTEGER NOT NULL DEFAULT 0,
    staple        INTEGER NOT NULL DEFAULT 0 CHECK (staple IN (0, 1)),
    store_id      INTEGER REFERENCES stores (id) ON DELETE SET NULL,
    brand         TEXT    NOT NULL DEFAULT '',
    kcal          INTEGER,
    proteins      INTEGER,
    carbs         INTEGER,
    fat           INTEGER,
    fibres        INTEGER,
    photo         TEXT    NOT NULL DEFAULT '',
    incomplete    INTEGER NOT NULL DEFAULT 0 CHECK (incomplete IN (0, 1)),
    origin        TEXT    NOT NULL DEFAULT 'utilisateur' CHECK (origin IN ('seed', 'utilisateur')),
    seed_key      TEXT    UNIQUE,
    seed_version  INTEGER NOT NULL DEFAULT 0,
    modified      INTEGER NOT NULL DEFAULT 0 CHECK (modified IN (0, 1)),
    archived      INTEGER NOT NULL DEFAULT 0 CHECK (archived IN (0, 1))
) STRICT;

CREATE INDEX products_aisle ON products (aisle_id);
CREATE INDEX products_incomplete ON products (incomplete) WHERE incomplete = 1;

CREATE TABLE product_synonyms (
    id         INTEGER PRIMARY KEY,
    product_id INTEGER NOT NULL REFERENCES products (id) ON DELETE CASCADE,
    name       TEXT    NOT NULL,
    search     TEXT    NOT NULL UNIQUE
) STRICT;

CREATE INDEX product_synonyms_product ON product_synonyms (product_id);

CREATE TABLE product_barcodes (
    code       TEXT    PRIMARY KEY,
    product_id INTEGER NOT NULL REFERENCES products (id) ON DELETE CASCADE
) STRICT, WITHOUT ROWID;

CREATE INDEX product_barcodes_product ON product_barcodes (product_id);

-- Conversions propres au produit : une unité de saisie (cs, cc, gousse,
-- sachet, tranche…) vaut qty unités de base.
CREATE TABLE product_units (
    product_id INTEGER NOT NULL REFERENCES products (id) ON DELETE CASCADE,
    unit       TEXT    NOT NULL,
    qty        INTEGER NOT NULL CHECK (qty > 0),
    PRIMARY KEY (product_id, unit)
) STRICT, WITHOUT ROWID;

-- Conditionnements usuels, en unités de base (beurre 250 g, lait 6 × 1 L).
CREATE TABLE product_packages (
    id         INTEGER PRIMARY KEY,
    product_id INTEGER NOT NULL REFERENCES products (id) ON DELETE CASCADE,
    qty        INTEGER NOT NULL CHECK (qty > 0),
    label      TEXT    NOT NULL DEFAULT '',
    UNIQUE (product_id, qty)
) STRICT;

-- Historique des prix relevés : prix payé pour qty unités de base.
CREATE TABLE prices (
    id          INTEGER PRIMARY KEY,
    product_id  INTEGER NOT NULL REFERENCES products (id) ON DELETE CASCADE,
    store_id    INTEGER REFERENCES stores (id) ON DELETE SET NULL,
    date        TEXT    NOT NULL,
    price_cents INTEGER NOT NULL CHECK (price_cents >= 0),
    qty         INTEGER NOT NULL CHECK (qty > 0)
) STRICT;

CREATE INDEX prices_product ON prices (product_id, date);

INSERT INTO locations (slug, name, kind, position) VALUES
    ('refrigerateur', 'Réfrigérateur', 'frais', 1),
    ('congelateur', 'Congélateur', 'congele', 2),
    ('placard', 'Placard', 'ambiant', 3),
    ('cave', 'Cave', 'ambiant', 4),
    ('fruits', 'Corbeille à fruits', 'ambiant', 5),
    ('salle-de-bain', 'Salle de bain', 'ambiant', 6),
    ('buanderie', 'Buanderie', 'ambiant', 7),
    ('garage', 'Garage', 'ambiant', 8);

INSERT INTO aisles (slug, name, position) VALUES
    ('fruits-legumes', 'Fruits et légumes', 1),
    ('boulangerie', 'Boulangerie', 2),
    ('boucherie', 'Boucherie et volaille', 3),
    ('poissonnerie', 'Poissonnerie', 4),
    ('charcuterie-traiteur', 'Charcuterie et traiteur', 5),
    ('cremerie', 'Crèmerie', 6),
    ('fromages', 'Fromages', 7),
    ('surgeles', 'Surgelés', 8),
    ('epicerie-salee', 'Épicerie salée', 9),
    ('condiments', 'Condiments, sauces et épices', 10),
    ('epicerie-sucree', 'Épicerie sucrée', 11),
    ('petit-dejeuner', 'Petit-déjeuner', 12),
    ('monde', 'Produits du monde', 13),
    ('boissons', 'Boissons', 14),
    ('bebe', 'Bébé', 15),
    ('hygiene', 'Hygiène et beauté', 16),
    ('entretien', 'Entretien et ménage', 17),
    ('maison', 'Maison et papeterie', 18),
    ('animaux', 'Animaux', 19);
