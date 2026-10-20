import { join } from "node:path";
import { DatabaseSync } from "node:sqlite";
import { harnessState } from "./spec";

// Fixture for the Media-INDEPENDENT Unmatched Record inbox (ADR-0068).
//
// The create-Media chain under test starts from a PENDING Unmatched Record, and
// the running server offers no API that mints one: records are appended by the
// scan walker (internal/scan) and the migration importer (internal/migrate),
// both of which need an installed plugin (a parser / a declared Importer) that a
// stock `-seed development` server does not carry. So the PRECONDITION — one
// pending record — is written straight into the catalog the harness server owns,
// exactly as QueueUnmatchedRecord would have written it
// (internal/store/unmatched_record.go). Everything AFTER that precondition runs
// against the real server: the real inbox read, the real createMediaFromUnmatched
// mutation, the real multipart artwork route, and the real artwork bytes served
// back to the browser. Nothing in the chain is stubbed.
//
// The catalog runs in WAL mode with a 10s busy timeout
// (internal/store/sqlite_boot.go), so this second writer is safe alongside the
// live server.

export interface UnmatchedRecordFixture {
  id: string;
  libraryID: string;
  sourceHandle: string;
  candidateTitle: string;
}

function catalogPath(): string {
  const state = harnessState() as { dataDir?: string };
  if (!state.dataDir) {
    throw new Error(
      "the unmatched-record fixture needs the self-booted harness (state.json has no dataDir); " +
        "unset BEAMFALL_E2E_URL so global-setup boots its own server",
    );
  }
  return join(state.dataDir, "db", "catalog.db");
}

/**
 * queueUnmatchedRecord appends one PENDING record and returns it. Insert-only
 * and idempotent per id, so a re-run against a reused data dir converges rather
 * than duplicating the inbox row.
 */
export function queueUnmatchedRecord(record: UnmatchedRecordFixture): UnmatchedRecordFixture {
  const db = new DatabaseSync(catalogPath());
  try {
    db.exec("PRAGMA busy_timeout = 10000");
    db.prepare(
      `INSERT OR IGNORE INTO unmatched_record(
         id, origin, library_id, source_handle, candidate_title,
         failure_reason, queued_at_ms)
       VALUES (?, 'scan', ?, ?, ?, 'no-candidates', ?)`,
    ).run(record.id, record.libraryID, record.sourceHandle, record.candidateTitle, Date.now());
  } finally {
    db.close();
  }
  return record;
}

/** The record's resolution as the server committed it, or null when pending. */
export function resolvedMediaID(recordID: string): string | null {
  const db = new DatabaseSync(catalogPath());
  try {
    db.exec("PRAGMA busy_timeout = 10000");
    const row = db
      .prepare("SELECT resolved_media_id AS mediaID FROM unmatched_record WHERE id = ?")
      .get(recordID) as { mediaID?: string | null } | undefined;
    return row?.mediaID ?? null;
  } finally {
    db.close();
  }
}
