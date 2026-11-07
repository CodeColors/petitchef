-- Créneaux, semaines types et menus (jalons 6 et 7).

-- Créneaux de repas ; temps de préparation et cuisson maximum en semaine
-- et le week-end (0 : sans limite).
CREATE TABLE meal_slots (
    id              INTEGER PRIMARY KEY,
    slug            TEXT    NOT NULL UNIQUE,
    name            TEXT    NOT NULL,
    position        INTEGER NOT NULL,
    enabled         INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
    max_min_weekday INTEGER NOT NULL DEFAULT 0 CHECK (max_min_weekday >= 0),
    max_min_weekend INTEGER NOT NULL DEFAULT 0 CHECK (max_min_weekend >= 0)
) STRICT;

INSERT INTO meal_slots (slug, name, position, enabled, max_min_weekday, max_min_weekend) VALUES
    ('petit-dejeuner', 'Petit-déjeuner', 1, 0, 15, 30),
    ('dejeuner', 'Déjeuner', 2, 1, 0, 0),
    ('gouter', 'Goûter', 3, 0, 15, 30),
    ('diner', 'Dîner', 4, 1, 40, 0);

-- Semaines types : format et convives de chaque créneau, jour par jour
-- (0 = lundi). Format : aucun (pas de repas à la maison), plat,
-- entree_plat, plat_dessert, complet. all_members : tout le foyer est
-- présent ; sinon, les membres de week_template_members.
CREATE TABLE week_templates (
    id         INTEGER PRIMARY KEY,
    name       TEXT    NOT NULL UNIQUE COLLATE NOCASE,
    is_default INTEGER NOT NULL DEFAULT 0 CHECK (is_default IN (0, 1))
) STRICT;

CREATE TABLE week_template_slots (
    template_id INTEGER NOT NULL REFERENCES week_templates (id) ON DELETE CASCADE,
    weekday     INTEGER NOT NULL CHECK (weekday BETWEEN 0 AND 6),
    slot_id     INTEGER NOT NULL REFERENCES meal_slots (id) ON DELETE CASCADE,
    format      TEXT    NOT NULL CHECK (format IN ('aucun', 'plat', 'entree_plat', 'plat_dessert', 'complet')),
    all_members INTEGER NOT NULL DEFAULT 1 CHECK (all_members IN (0, 1)),
    guests      INTEGER NOT NULL DEFAULT 0 CHECK (guests BETWEEN 0 AND 50),
    PRIMARY KEY (template_id, weekday, slot_id)
) STRICT, WITHOUT ROWID;

CREATE TABLE week_template_members (
    template_id INTEGER NOT NULL,
    weekday     INTEGER NOT NULL,
    slot_id     INTEGER NOT NULL,
    member_id   INTEGER NOT NULL REFERENCES household_members (id) ON DELETE CASCADE,
    PRIMARY KEY (template_id, weekday, slot_id, member_id),
    FOREIGN KEY (template_id, weekday, slot_id) REFERENCES week_template_slots (template_id, weekday, slot_id) ON DELETE CASCADE
) STRICT, WITHOUT ROWID;

INSERT INTO week_templates (name, is_default) VALUES ('Semaine habituelle', 1);
INSERT INTO week_template_slots (template_id, weekday, slot_id, format)
    SELECT 1, d.value, s.id, CASE
        WHEN s.slug = 'dejeuner' AND d.value = 6 THEN 'complet'
        WHEN s.slug = 'dejeuner' AND d.value = 5 THEN 'plat_dessert'
        WHEN s.slug = 'diner' AND d.value IN (5, 6) THEN 'plat'
        WHEN s.slug = 'diner' THEN 'entree_plat'
        WHEN s.slug = 'dejeuner' THEN 'plat'
        ELSE 'aucun' END
    FROM meal_slots s, json_each('[0,1,2,3,4,5,6]') d;

-- Repas planifiés : un par date et créneau. Le format et les convives
-- viennent de la semaine type et restent modifiables ponctuellement.
CREATE TABLE meals (
    id          INTEGER PRIMARY KEY,
    date        TEXT    NOT NULL,
    slot_id     INTEGER NOT NULL REFERENCES meal_slots (id),
    format      TEXT    NOT NULL CHECK (format IN ('aucun', 'plat', 'entree_plat', 'plat_dessert', 'complet')),
    all_members INTEGER NOT NULL DEFAULT 1 CHECK (all_members IN (0, 1)),
    guests      INTEGER NOT NULL DEFAULT 0 CHECK (guests BETWEEN 0 AND 50),
    max_min     INTEGER NOT NULL DEFAULT -1,
    UNIQUE (date, slot_id)
) STRICT;

CREATE TABLE meal_members (
    meal_id   INTEGER NOT NULL REFERENCES meals (id) ON DELETE CASCADE,
    member_id INTEGER NOT NULL REFERENCES household_members (id) ON DELETE CASCADE,
    PRIMARY KEY (meal_id, member_id)
) STRICT, WITHOUT ROWID;

-- Plats d'un repas : une recette, un produit simple (« pizza surgelée »),
-- ou un plat prêt du stock. portions en millièmes. status : prevu,
-- cuisine, reporte. leftovers_from : ce plat est le reste d'un autre.
-- cooked_batch : action de stock du décompte (annulation).
CREATE TABLE menu_entries (
    id             INTEGER PRIMARY KEY,
    meal_id        INTEGER NOT NULL REFERENCES meals (id) ON DELETE CASCADE,
    role           TEXT    NOT NULL CHECK (role IN ('entree', 'plat', 'dessert')),
    recipe_id      INTEGER REFERENCES recipes (id),
    product_id     INTEGER REFERENCES products (id),
    stock_item_id  INTEGER REFERENCES stock_items (id) ON DELETE SET NULL,
    portions       INTEGER NOT NULL DEFAULT 0 CHECK (portions >= 0),
    locked         INTEGER NOT NULL DEFAULT 0 CHECK (locked IN (0, 1)),
    status         TEXT    NOT NULL DEFAULT 'prevu' CHECK (status IN ('prevu', 'cuisine', 'reporte')),
    leftovers_from INTEGER REFERENCES menu_entries (id) ON DELETE SET NULL,
    cook_double    INTEGER NOT NULL DEFAULT 0 CHECK (cook_double IN (0, 1)),
    reasons        TEXT    NOT NULL DEFAULT '[]',
    cooked_batch   INTEGER,
    cooked_on      TEXT,
    UNIQUE (meal_id, role),
    CHECK (recipe_id IS NOT NULL OR product_id IS NOT NULL)
) STRICT;

CREATE INDEX meals_date ON meals (date);
CREATE INDEX menu_entries_recipe ON menu_entries (recipe_id);
CREATE INDEX menu_entries_leftovers ON menu_entries (leftovers_from) WHERE leftovers_from IS NOT NULL;

-- Menus enregistrés comme modèles réutilisables (« semaine d'hiver
-- express ») : jour relatif, créneau, rôle, recette ou produit.
CREATE TABLE saved_menus (
    id         INTEGER PRIMARY KEY,
    name       TEXT    NOT NULL UNIQUE COLLATE NOCASE,
    days       INTEGER NOT NULL CHECK (days BETWEEN 1 AND 56),
    created_at TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
) STRICT;

CREATE TABLE saved_menu_entries (
    saved_id   INTEGER NOT NULL REFERENCES saved_menus (id) ON DELETE CASCADE,
    day        INTEGER NOT NULL,
    slot_id    INTEGER NOT NULL REFERENCES meal_slots (id) ON DELETE CASCADE,
    format     TEXT    NOT NULL,
    role       TEXT    NOT NULL,
    recipe_id  INTEGER REFERENCES recipes (id) ON DELETE CASCADE,
    product_id INTEGER REFERENCES products (id) ON DELETE CASCADE
) STRICT;

-- Plats « fait maison » et restes rangés en stock : la recette d'origine.
CREATE INDEX stock_items_recipe ON stock_items (recipe_id) WHERE recipe_id IS NOT NULL AND qty > 0;
