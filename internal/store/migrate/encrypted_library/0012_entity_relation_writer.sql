-- CORE-3152: mirrors catalog 0131's entity_relation.writer in the encrypted
-- library plane. NULL means an operator or an unattributed writer.
ALTER TABLE entity_relation ADD COLUMN writer TEXT;
