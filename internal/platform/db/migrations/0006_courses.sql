-- Liste de courses (jalon 8).

-- Une liste en cours partagée par le foyer ; les lignes rangées en stock
-- la quittent, les produits non trouvés y restent pour les courses
-- suivantes.
CREATE TABLE shopping_lists (
    id          INTEGER PRIMARY KEY,
    store_id    INTEGER REFERENCES stores (id) ON DELETE SET NULL,
    until_date  TEXT,
    computed_at TEXT,
    created_at  TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
) STRICT;

-- Lignes : besoin calculé (unité de base) et quantité à acheter arrondie
-- au conditionnement. sources : bits menu (1), stock minimum (2), ajout
-- manuel (4), produit de base presque vide (8). manual_qty : quantité d'un
-- ajout manuel (0 : sans quantité). dismissed : ligne calculée retirée à
-- la main, ignorée jusqu'au rangement des courses.
CREATE TABLE shopping_items (
    id          INTEGER PRIMARY KEY,
    list_id     INTEGER NOT NULL REFERENCES shopping_lists (id) ON DELETE CASCADE,
    product_id  INTEGER NOT NULL REFERENCES products (id),
    need        INTEGER NOT NULL DEFAULT 0 CHECK (need >= 0),
    buy         INTEGER NOT NULL DEFAULT 0 CHECK (buy >= 0),
    pack_count  INTEGER NOT NULL DEFAULT 0,
    pack_size   INTEGER NOT NULL DEFAULT 0,
    sources     INTEGER NOT NULL DEFAULT 0,
    detail      TEXT    NOT NULL DEFAULT '[]',
    manual      INTEGER NOT NULL DEFAULT 0 CHECK (manual IN (0, 1)),
    manual_qty  INTEGER NOT NULL DEFAULT 0 CHECK (manual_qty >= 0),
    dismissed   INTEGER NOT NULL DEFAULT 0 CHECK (dismissed IN (0, 1)),
    checked     INTEGER NOT NULL DEFAULT 0 CHECK (checked IN (0, 1)),
    checked_at  TEXT,
    price_cents INTEGER CHECK (price_cents >= 0),
    UNIQUE (list_id, product_id)
) STRICT;

CREATE INDEX shopping_items_list ON shopping_items (list_id);

-- Jour des courses : la liste couvre jusqu'aux courses suivantes.
INSERT INTO settings (key, value) VALUES ('shopping', '{"jour":5,"tous_les":7}');
