-- CORE-2360: a manager can keep a Library off Home. 1 = shown on Home (default), 0 = hidden.
ALTER TABLE library ADD COLUMN show_on_home INTEGER NOT NULL DEFAULT 1;
