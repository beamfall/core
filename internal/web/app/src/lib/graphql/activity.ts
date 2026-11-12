// The Console Activity page's read of the activity log (ADR-0012 §11). It
// selects the acting identifiers the page turns into names, which the
// narrower `fetchActivityLog` in console.ts does not.

import { graphql } from "@/lib/api";

/** An activity-log entry with its attribution (graph.ActivityEntryInfo). */
export interface ActivityRecord {
  id: string;
  occurredAt: string;
  action: string;
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

const ACTIVITY_RECORDS_QUERY = `query ActivityRecords { activityLog(limit: ${ACTIVITY_PAGE_SIZE}) { id occurredAt action profileID deviceID targetType targetID libraryID tokenID apiKeyID } }`;

/** The latest activity entries, newest first, reveal-filtered by the server. */
export async function fetchActivityRecords(signal?: AbortSignal): Promise<ActivityRecord[]> {
  const data = await graphql<{ activityLog: ActivityRecord[] | null }>(
    ACTIVITY_RECORDS_QUERY,
    {},
    signal,
  );
  return data.activityLog ?? [];
}
