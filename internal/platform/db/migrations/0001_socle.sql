-- Socle repris de BuddyGet : utilisateurs, sessions, réglages, alertes,
-- notifications et sauvegardes (jalon 1).

CREATE TABLE users (
    id            INTEGER PRIMARY KEY,
    username      TEXT    NOT NULL UNIQUE COLLATE NOCASE,
    password_hash TEXT    NOT NULL,
    theme         TEXT    NOT NULL DEFAULT 'auto' CHECK (theme IN ('auto', 'light', 'dark')),
    created_at    TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
) STRICT;

-- Seule l'empreinte SHA-256 du jeton est stockée : une copie de la base ne
-- permet pas de réutiliser une session. expires_at est en secondes Unix.
CREATE TABLE sessions (
    token_hash BLOB    PRIMARY KEY,
    user_id    INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    expires_at INTEGER NOT NULL
) STRICT, WITHOUT ROWID;

CREATE INDEX sessions_user_id ON sessions (user_id);
CREATE INDEX sessions_expires_at ON sessions (expires_at);

-- Réglages divers de l'application, en JSON (générateur, grille de
-- l'indice d'équilibre, jour des courses, jeton iCal…).
CREATE TABLE settings (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
) STRICT, WITHOUT ROWID;

-- Alertes : une par situation (clé unique), acquittables.
CREATE TABLE alerts (
    id           INTEGER PRIMARY KEY,
    kind         TEXT    NOT NULL,
    key          TEXT    NOT NULL UNIQUE,
    title        TEXT    NOT NULL,
    detail       TEXT    NOT NULL DEFAULT '',
    link         TEXT    NOT NULL DEFAULT '',
    created_on   TEXT    NOT NULL,
    acknowledged INTEGER NOT NULL DEFAULT 0 CHECK (acknowledged IN (0, 1)),
    notified     INTEGER NOT NULL DEFAULT 0 CHECK (notified IN (0, 1))
) STRICT;

CREATE INDEX alerts_open ON alerts (acknowledged, created_on);
CREATE INDEX alerts_pending ON alerts (notified) WHERE notified = 0;

-- Réglage de chaque type d'alerte : activé, et son paramètre (jours).
CREATE TABLE alert_settings (
    kind    TEXT    PRIMARY KEY,
    enabled INTEGER NOT NULL CHECK (enabled IN (0, 1)),
    param   INTEGER NOT NULL CHECK (param > 0)
) STRICT, WITHOUT ROWID;

INSERT INTO alert_settings (kind, enabled, param) VALUES
    ('peremption', 1, 3),
    ('perime', 1, 1),
    ('stock_minimum', 1, 1),
    ('rupture', 1, 7),
    ('menu_absent', 1, 2),
    ('liste_prete', 1, 1);

-- Canaux sortants, désactivés par défaut (une seule ligne).
CREATE TABLE notification_settings (
    id              INTEGER PRIMARY KEY CHECK (id = 1),
    email_enabled   INTEGER NOT NULL DEFAULT 0 CHECK (email_enabled IN (0, 1)),
    smtp_host       TEXT    NOT NULL DEFAULT '',
    smtp_port       INTEGER NOT NULL DEFAULT 587,
    smtp_security   TEXT    NOT NULL DEFAULT 'starttls' CHECK (smtp_security IN ('starttls', 'tls')),
    smtp_user       TEXT    NOT NULL DEFAULT '',
    smtp_password   TEXT    NOT NULL DEFAULT '',
    email_from      TEXT    NOT NULL DEFAULT '',
    email_to        TEXT    NOT NULL DEFAULT '',
    webhook_enabled INTEGER NOT NULL DEFAULT 0 CHECK (webhook_enabled IN (0, 1)),
    webhook_url     TEXT    NOT NULL DEFAULT '',
    frequency       TEXT    NOT NULL DEFAULT 'immediate' CHECK (frequency IN ('immediate', 'daily', 'weekly')),
    discreet        INTEGER NOT NULL DEFAULT 1 CHECK (discreet IN (0, 1))
) STRICT;

INSERT INTO notification_settings (id) VALUES (1);

CREATE TABLE backup_settings (
    id              INTEGER PRIMARY KEY CHECK (id = 1),
    auto            INTEGER NOT NULL DEFAULT 0 CHECK (auto IN (0, 1)),
    keep            INTEGER NOT NULL DEFAULT 7 CHECK (keep BETWEEN 1 AND 90),
    copy_kind       TEXT    NOT NULL DEFAULT 'aucune' CHECK (copy_kind IN ('aucune', 'dossier', 'webdav')),
    copy_dir        TEXT    NOT NULL DEFAULT '',
    webdav_url      TEXT    NOT NULL DEFAULT '',
    webdav_user     TEXT    NOT NULL DEFAULT '',
    webdav_password TEXT    NOT NULL DEFAULT '',
    last_at         TEXT    NOT NULL DEFAULT '',
    last_error      TEXT    NOT NULL DEFAULT ''
) STRICT;

INSERT INTO backup_settings (id) VALUES (1);
