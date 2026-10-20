-- ENC-ENTITY-RELATION-UPSERT-1: match the plaintext catalog's relation
-- conflict targets inside the protected catalog plane.

CREATE UNIQUE INDEX entity_relation_entity_uq
  ON entity_relation(from_entity_id, relation, to_entity_id)
  WHERE to_entity_id IS NOT NULL;
CREATE UNIQUE INDEX entity_relation_media_uq
  ON entity_relation(from_entity_id, relation, to_media_id)
  WHERE to_media_id IS NOT NULL;
CREATE INDEX entity_relation_to_entity_idx
  ON entity_relation(to_entity_id, relation)
  WHERE to_entity_id IS NOT NULL;
CREATE INDEX entity_relation_to_media_idx
  ON entity_relation(to_media_id, relation)
  WHERE to_media_id IS NOT NULL;
