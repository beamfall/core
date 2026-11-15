// The activity log read (ADR-0012 §11), shared by the Console Activity page and
// the Trash page's recent-changes list. It selects the acting identifiers the
// Activity page turns into names. The server reads `limit` and `offset` only
// from variables, so they are passed that way.

import { graphql } from "@/lib/api";

/** An activity-log entry with its attribution (graph.ActivityEntryInfo). */
export interface ActivityRecord {
  id: string;
  occurredAt: string;
  action: string;
  /** Whether the entry's change can still be undone (undoActivity). */
  reversible?: boolean;
  profileID?: string;
  deviceID?: string;
  targetType?: string;
  targetID?: string;
  libraryID?: string;
  /** Managers only: the admin API token that made the change. */
  tokenID?: string;
  /** Managers only: the scoped API key that made the change. */
  apiKeyID?: string;
}

/** The server caps one page at 200 entries (graph.activityLog). */
export const ACTIVITY_PAGE_SIZE = 200;

const ACTIVITY_RECORDS_QUERY =
  "query ActivityRecords($limit: Int, $offset: Int) { activityLog(limit: $limit, offset: $offset) { id occurredAt action reversible profileID deviceID targetType targetID libraryID tokenID apiKeyID } }";

/**
 * One page of activity entries, newest first, reveal-filtered by the server.
 * `offset` skips that many newer entries, so the next page starts at the
 * number of entries already loaded. A page shorter than `limit` is the last.
 */
export async function fetchActivityRecords(
  signal?: AbortSignal,
  offset = 0,
  limit = ACTIVITY_PAGE_SIZE,
): Promise<ActivityRecord[]> {
  const data = await graphql<{ activityLog: ActivityRecord[] | null }>(
    ACTIVITY_RECORDS_QUERY,
    { limit, offset },
    signal,
  );
  return data.activityLog ?? [];
}
