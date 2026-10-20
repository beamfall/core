-- PODAD-2-PROVENANCE-1 (ADR-0179 §2, ADR-0193 Decisions 3/5): immutable
-- detector-run receipts and their append-only lifecycle ledger. Both live in
-- catalog so a run receipt, supersession, candidate evidence, and served
-- markers commit in one transaction; the separate events database can only be
-- a replayable activity projection, never the lifecycle authority.
CREATE TABLE detection_run (
  run_id                TEXT PRIMARY KEY,
  media_id              TEXT NOT NULL REFERENCES media(id) ON DELETE CASCADE,
  detector_method       TEXT NOT NULL CHECK (length(trim(detector_method)) > 0),
  model_id              TEXT NOT NULL CHECK (length(trim(model_id)) > 0),
  model_version         TEXT NOT NULL CHECK (length(trim(model_version)) > 0),
  model_hash            TEXT NOT NULL CHECK (length(trim(model_hash)) > 0),
  artifact_identity     TEXT NOT NULL CHECK (length(trim(artifact_identity)) > 0),
  feature_set_version   TEXT NOT NULL CHECK (length(trim(feature_set_version)) > 0),
  calibration_id        TEXT NOT NULL CHECK (length(trim(calibration_id)) > 0),
  calibration_version   TEXT NOT NULL CHECK (length(trim(calibration_version)) > 0),
  calibration_hash      TEXT NOT NULL CHECK (length(trim(calibration_hash)) > 0),
  signal_tier           TEXT NOT NULL CHECK (length(trim(signal_tier)) > 0),
  candidate_count       INTEGER NOT NULL CHECK (candidate_count >= 0),
  completion_outcome    TEXT NOT NULL CHECK (completion_outcome = 'completed'),
  completed_at_ms       INTEGER NOT NULL
);
CREATE INDEX detection_run_media_method_idx
  ON detection_run(media_id, detector_method, completed_at_ms, run_id);
CREATE INDEX detection_run_identity_idx
  ON detection_run(
    media_id, detector_method,
    model_id, model_version, model_hash, artifact_identity,
    feature_set_version, calibration_id, calibration_version, calibration_hash
  );
CREATE INDEX detection_run_model_idx
  ON detection_run(model_id, model_version, model_hash);

CREATE TRIGGER detection_run_immutable_update
BEFORE UPDATE ON detection_run
BEGIN
  SELECT RAISE(ABORT, 'detection_run receipts are immutable');
END;
CREATE TRIGGER detection_run_immutable_delete
BEFORE DELETE ON detection_run
WHEN EXISTS (SELECT 1 FROM media WHERE id = OLD.media_id)
BEGIN
  SELECT RAISE(ABORT, 'detection_run receipts are immutable');
END;

-- Lifecycle is append-only and derived: supersede/reset name one receipt,
-- revoke names an exact model version/hash, and undo names one prior event.
-- No transition rewrites the immutable inference receipt.
CREATE TABLE detection_run_lifecycle (
  event_id            TEXT PRIMARY KEY,
  event_type          TEXT NOT NULL
    CHECK (event_type IN ('supersede', 'revoke', 'undo', 'reset-to-auto')),
  run_id              TEXT REFERENCES detection_run(run_id) ON DELETE CASCADE,
  replacement_run_id  TEXT REFERENCES detection_run(run_id) ON DELETE CASCADE,
  model_id            TEXT,
  model_version       TEXT,
  model_hash          TEXT,
  target_event_id     TEXT REFERENCES detection_run_lifecycle(event_id),
  occurred_at_ms      INTEGER NOT NULL,
  CHECK (
    (event_type = 'supersede'
      AND run_id IS NOT NULL AND replacement_run_id IS NOT NULL
      AND model_id IS NULL AND model_version IS NULL AND model_hash IS NULL
      AND target_event_id IS NULL)
    OR
    (event_type = 'reset-to-auto'
      AND run_id IS NOT NULL AND replacement_run_id IS NULL
      AND model_id IS NULL AND model_version IS NULL AND model_hash IS NULL
      AND target_event_id IS NULL)
    OR
    (event_type = 'revoke'
      AND run_id IS NULL AND replacement_run_id IS NULL
      AND model_id IS NOT NULL AND model_version IS NOT NULL AND model_hash IS NOT NULL
      AND target_event_id IS NULL)
    OR
    (event_type = 'undo'
      AND run_id IS NULL AND replacement_run_id IS NULL
      AND model_id IS NULL AND model_version IS NULL AND model_hash IS NULL
      AND target_event_id IS NOT NULL)
  )
);
CREATE INDEX detection_run_lifecycle_run_idx
  ON detection_run_lifecycle(run_id, event_type, occurred_at_ms);
CREATE INDEX detection_run_lifecycle_model_idx
  ON detection_run_lifecycle(model_id, model_version, model_hash, event_type);
CREATE INDEX detection_run_lifecycle_target_idx
  ON detection_run_lifecycle(target_event_id);

CREATE TRIGGER detection_run_lifecycle_immutable_update
BEFORE UPDATE ON detection_run_lifecycle
BEGIN
  SELECT RAISE(ABORT, 'detection_run_lifecycle is append-only');
END;
CREATE TRIGGER detection_run_lifecycle_immutable_delete
BEFORE DELETE ON detection_run_lifecycle
WHEN (OLD.run_id IS NULL OR EXISTS (
        SELECT 1 FROM detection_run WHERE run_id = OLD.run_id
     ))
 AND (OLD.replacement_run_id IS NULL OR EXISTS (
        SELECT 1 FROM detection_run WHERE run_id = OLD.replacement_run_id
     ))
BEGIN
  SELECT RAISE(ABORT, 'detection_run_lifecycle is append-only');
END;

CREATE TRIGGER detection_run_lifecycle_validate_supersede
BEFORE INSERT ON detection_run_lifecycle
WHEN NEW.event_type = 'supersede'
BEGIN
  SELECT CASE WHEN NEW.run_id = NEW.replacement_run_id
    THEN RAISE(ABORT, 'detection supersession must name distinct runs') END;
  SELECT CASE WHEN NOT EXISTS (
    SELECT 1
    FROM detection_run prior
    JOIN detection_run replacement
      ON replacement.run_id = NEW.replacement_run_id
    WHERE prior.run_id = NEW.run_id
      AND prior.media_id = replacement.media_id
      AND prior.detector_method = replacement.detector_method
  ) THEN RAISE(ABORT, 'detection supersession must preserve Media and method') END;
  SELECT CASE WHEN EXISTS (
    SELECT 1 FROM detection_run_lifecycle invalidation
    WHERE invalidation.run_id = NEW.run_id
      AND invalidation.event_type IN ('supersede', 'reset-to-auto')
  ) THEN RAISE(ABORT, 'prior detection run is already invalidated') END;
  SELECT CASE WHEN EXISTS (
    SELECT 1 FROM detection_run_lifecycle invalidation
    WHERE invalidation.run_id = NEW.replacement_run_id
      AND invalidation.event_type IN ('supersede', 'reset-to-auto')
  ) THEN RAISE(ABORT, 'replacement detection run is already invalidated') END;
END;

CREATE TRIGGER detection_run_lifecycle_validate_revoke
BEFORE INSERT ON detection_run_lifecycle
WHEN NEW.event_type = 'revoke'
BEGIN
  SELECT CASE WHEN length(trim(NEW.model_id)) = 0
                OR length(trim(NEW.model_version)) = 0
                OR length(trim(NEW.model_hash)) = 0
    THEN RAISE(ABORT, 'detection revocation identity is required') END;
END;

CREATE TRIGGER detection_run_lifecycle_validate_undo
BEFORE INSERT ON detection_run_lifecycle
WHEN NEW.event_type = 'undo'
BEGIN
  SELECT CASE WHEN NOT EXISTS (
    SELECT 1 FROM detection_run_lifecycle target
    WHERE target.event_id = NEW.target_event_id
      AND target.event_type = 'revoke'
  ) THEN RAISE(ABORT, 'detection undo must target a revocation') END;
  SELECT CASE WHEN EXISTS (
    SELECT 1 FROM detection_run_lifecycle prior_undo
    WHERE prior_undo.event_type = 'undo'
      AND prior_undo.target_event_id = NEW.target_event_id
  ) THEN RAISE(ABORT, 'detection revocation is already undone') END;
END;

-- Existing rows remain valid. New PODAD rows link every candidate and served
-- Marker to the originating run, while keeping class confidence separate from
-- boundary score and the actual served decision.
ALTER TABLE segment_detection
  ADD COLUMN detection_run_id TEXT REFERENCES detection_run(run_id);
ALTER TABLE segment_detection
  ADD COLUMN candidate_ordinal INTEGER NOT NULL DEFAULT 0 CHECK (candidate_ordinal >= 0);
ALTER TABLE segment_detection
  ADD COLUMN boundary_score REAL NOT NULL DEFAULT 0
    CHECK (boundary_score >= 0 AND boundary_score <= 1);
ALTER TABLE segment_detection
  ADD COLUMN boundary_eligible INTEGER NOT NULL DEFAULT 0
    CHECK (boundary_eligible IN (0, 1));
ALTER TABLE segment_detection
  ADD COLUMN served INTEGER NOT NULL DEFAULT 0 CHECK (served IN (0, 1));
CREATE TRIGGER segment_detection_validate_run_link
BEFORE INSERT ON segment_detection
WHEN NEW.detection_run_id IS NOT NULL
BEGIN
  SELECT CASE WHEN NOT EXISTS (
    SELECT 1 FROM detection_run
    WHERE run_id = NEW.detection_run_id
      AND media_id = NEW.media_id
      AND detector_method = NEW.method
  ) THEN RAISE(ABORT, 'candidate run must match Media and method') END;
  SELECT CASE WHEN NEW.candidate_ordinal >= (
    SELECT candidate_count
    FROM detection_run
    WHERE run_id = NEW.detection_run_id
  ) THEN RAISE(ABORT, 'candidate ordinal exceeds completed receipt') END;
  SELECT CASE WHEN NEW.served = 1
                   AND (NEW.boundary_eligible <> 1 OR NEW.flavor = 'host-read')
    THEN RAISE(ABORT, 'served candidate contradicts admission evidence') END;
END;
CREATE UNIQUE INDEX segment_detection_run_ordinal_idx
  ON segment_detection(detection_run_id, candidate_ordinal)
  WHERE detection_run_id IS NOT NULL;

CREATE TRIGGER segment_detection_run_immutable_update
BEFORE UPDATE ON segment_detection
WHEN OLD.detection_run_id IS NOT NULL
BEGIN
  SELECT RAISE(ABORT, 'detection-run candidate evidence is immutable');
END;
CREATE TRIGGER segment_detection_reject_run_attachment
BEFORE UPDATE ON segment_detection
WHEN OLD.detection_run_id IS NULL AND NEW.detection_run_id IS NOT NULL
BEGIN
  SELECT RAISE(ABORT, 'existing candidate evidence cannot attach to a detection run');
END;
CREATE TRIGGER segment_detection_run_immutable_delete
BEFORE DELETE ON segment_detection
WHEN OLD.detection_run_id IS NOT NULL
 AND EXISTS (SELECT 1 FROM media WHERE id = OLD.media_id)
BEGIN
  SELECT RAISE(ABORT, 'detection-run candidate evidence is immutable');
END;

ALTER TABLE marker
  ADD COLUMN detection_run_id TEXT REFERENCES detection_run(run_id);
ALTER TABLE marker
  ADD COLUMN detection_candidate_ordinal INTEGER CHECK (detection_candidate_ordinal >= 0);
CREATE TRIGGER marker_validate_detection_run_link
BEFORE INSERT ON marker
WHEN NEW.detection_run_id IS NOT NULL
BEGIN
  SELECT CASE WHEN NEW.provenance <> 'detected'
    THEN RAISE(ABORT, 'run-linked markers must be detected') END;
  SELECT CASE WHEN NEW.detection_candidate_ordinal IS NULL
    THEN RAISE(ABORT, 'run-linked markers require a candidate ordinal') END;
  SELECT CASE WHEN NOT EXISTS (
    SELECT 1
    FROM detection_run
    JOIN segment_detection
      ON segment_detection.detection_run_id = detection_run.run_id
     AND segment_detection.candidate_ordinal = NEW.detection_candidate_ordinal
    WHERE detection_run.run_id = NEW.detection_run_id
      AND detection_run.media_id = NEW.media_id
      AND detection_run.detector_method = NEW.method
      AND segment_detection.media_id = NEW.media_id
      AND segment_detection.segment_kind = NEW.segment_kind
      AND segment_detection.start_seconds = NEW.start_seconds
      AND segment_detection.end_seconds = NEW.end_seconds
      AND segment_detection.flavor = NEW.flavor
      AND segment_detection.confidence = NEW.confidence
      AND segment_detection.served = 1
  ) THEN RAISE(ABORT, 'marker must match its served detection candidate') END;
END;
CREATE TRIGGER marker_reject_detection_run_attachment
BEFORE UPDATE ON marker
WHEN OLD.detection_run_id IS NULL AND NEW.detection_run_id IS NOT NULL
BEGIN
  SELECT RAISE(ABORT, 'existing markers cannot attach to a detection run');
END;
CREATE TRIGGER marker_detection_run_update_guard
BEFORE UPDATE ON marker
WHEN OLD.detection_run_id IS NOT NULL
BEGIN
  SELECT CASE WHEN NEW.detection_run_id IS NOT NULL
    THEN RAISE(ABORT, 'run-linked detected markers are immutable') END;
  SELECT CASE WHEN NEW.provenance <> 'manual'
    THEN RAISE(ABORT, 'run-linked markers may detach only by manual promotion') END;
END;
CREATE UNIQUE INDEX marker_detection_run_ordinal_idx
  ON marker(detection_run_id, detection_candidate_ordinal)
  WHERE detection_run_id IS NOT NULL;
