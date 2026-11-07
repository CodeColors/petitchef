-- Inventaire : lots, réglages de stock minimum et journal unique des
-- mouvements (jalons 3 et 4).

-- Un lot de stock : un produit, un emplacement, une quantité (unité de
-- base), une date limite. Un lot épuisé (qty = 0) reste quelque temps pour
-- permettre l'annulation, puis est purgé par la tâche quotidienne.
CREATE TABLE stock_items (
    id          INTEGER PRIMARY KEY,
    product_id  INTEGER NOT NULL REFERENCES products (id),
    location_id INTEGER REFERENCES locations (id),
    qty         INTEGER NOT NULL CHECK (qty >= 0),
    best_before TEXT,
    date_kind   TEXT    NOT NULL DEFAULT 'DDM' CHECK (date_kind IN ('DLC', 'DDM')),
    opened_on   TEXT,
    added_on    TEXT    NOT NULL,
    price_cents INTEGER CHECK (price_cents >= 0),
    price_qty   INTEGER CHECK (price_qty > 0),
    origin      TEXT    NOT NULL DEFAULT 'manuel' CHECK (origin IN ('courses', 'manuel', 'restes', 'fait_maison')),
    recipe_id   INTEGER,
    emptied_on  TEXT
) STRICT;

CREATE INDEX stock_items_product ON stock_items (product_id) WHERE qty > 0;
CREATE INDEX stock_items_expiry ON stock_items (best_before) WHERE qty > 0;
CREATE INDEX stock_items_location ON stock_items (location_id) WHERE qty > 0;

-- Réglages par produit : stock minimum et cible (unité de base), suivi de
-- la consommation, consommation estimée (millièmes d'unité de base par
-- jour) et signalement « presque vide » des produits de base.
CREATE TABLE stock_settings (
    product_id  INTEGER PRIMARY KEY REFERENCES products (id) ON DELETE CASCADE,
    min_qty     INTEGER NOT NULL DEFAULT 0 CHECK (min_qty >= 0),
    target_qty  INTEGER NOT NULL DEFAULT 0 CHECK (target_qty >= 0),
    track       INTEGER NOT NULL DEFAULT 0 CHECK (track IN (0, 1)),
    daily_use   INTEGER NOT NULL DEFAULT 0 CHECK (daily_use >= 0),
    low         INTEGER NOT NULL DEFAULT 0 CHECK (low IN (0, 1)),
    low_since   TEXT
) STRICT;

-- Journal unique de toutes les variations de stock. batch regroupe les
-- mouvements d'une même action (annulation d'un bloc) ; qty est signée.
CREATE TABLE stock_movements (
    id               INTEGER PRIMARY KEY,
    at               TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    date             TEXT    NOT NULL,
    batch            INTEGER NOT NULL,
    product_id       INTEGER NOT NULL REFERENCES products (id),
    stock_item_id    INTEGER REFERENCES stock_items (id) ON DELETE SET NULL,
    kind             TEXT    NOT NULL CHECK (kind IN ('achat', 'ajout', 'recette', 'consommation', 'perte', 'correction', 'transfert', 'ouverture')),
    qty              INTEGER NOT NULL,
    value_cents      INTEGER,
    menu_entry_id    INTEGER,
    user_id          INTEGER REFERENCES users (id) ON DELETE SET NULL,
    undone           INTEGER NOT NULL DEFAULT 0 CHECK (undone IN (0, 1)),
    prev_best_before TEXT,
    prev_opened_on   TEXT
) STRICT;

CREATE INDEX stock_movements_product ON stock_movements (product_id, date);
CREATE INDEX stock_movements_batch ON stock_movements (batch);
CREATE INDEX stock_movements_kind_date ON stock_movements (kind, date);
