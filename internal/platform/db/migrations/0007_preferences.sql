-- Préférence d'affichage par utilisateur : taille du texte (accessibilité).
ALTER TABLE users ADD COLUMN text_size TEXT NOT NULL DEFAULT 'normal' CHECK (text_size IN ('normal', 'grand'));
