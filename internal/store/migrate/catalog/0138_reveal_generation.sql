-- CORE-3313 / ADR-0240 D10: bind restricted stream tokens to one Reveal.
ALTER TABLE auth_session ADD COLUMN reveal_generation BLOB NOT NULL
  DEFAULT X'00000000000000000000000000000000';
