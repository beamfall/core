-- CORE-3379: per-screen display settings the host chooses. qr_corner places
-- the join QR and code in one corner of the stage while a song plays (off by
-- default); muted silences the screen, since not every screen needs audio.
ALTER TABLE party_screen ADD COLUMN qr_corner TEXT NOT NULL DEFAULT 'off'
  CHECK (qr_corner IN ('off', 'top-left', 'top-right', 'bottom-left', 'bottom-right'));
ALTER TABLE party_screen ADD COLUMN muted INTEGER NOT NULL DEFAULT 0 CHECK (muted IN (0, 1));
