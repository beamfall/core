ALTER TABLE device
  ADD COLUMN distribution_class TEXT NOT NULL DEFAULT 'direct'
  CHECK (distribution_class IN ('direct', 'appStore'));
