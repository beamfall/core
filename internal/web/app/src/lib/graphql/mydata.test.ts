import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  deleteMyEngagement,
  fetchMyDataSummary,
  fetchMyEngagementLog,
  fetchNotifications,
  fetchPlaybackMarkers,
  markNotificationRead,
} from "./viewer";

// Request-shape tests for the ADR-0036 §2.2 My-Data Summary + Event-log client
// calls. We stub the global `fetch` (the single network boundary all api.ts
// calls funnel through) and assert the exact on-wire GraphQL operation + the
// variables object — the server-side wire tests pin the resolver behavior, so
// these only guard that the client sends the agreed query and filter/page args.

interface Recorded {
  query: string;
  variables: Record<string, unknown>;
}

const recorded: Recorded[] = [];

beforeEach(() => {
  recorded.length = 0;
  vi.stubGlobal(
    "fetch",
    vi.fn((_url: string, init?: RequestInit) => {
      const body = init?.body
        ? (JSON.parse(init.body as string) as Recorded)
        : { query: "", variables: {} };
      recorded.push(body);
      // Echo a minimal valid payload for whichever field was requested.
      let data: Record<string, unknown>;
      if (body.query.includes("myDataSummary")) {
        data = {
          myDataSummary: {
            plays: 0,
            watched: 0,
            favorites: 0,
            queueEntries: 0,
            libraryCount: 0,
            libraryBreakdown: [],
          },
        };
      } else if (body.query.includes("markNotificationRead")) {
        data = {
          markNotificationRead: { id: body.variables.id, found: true },
        };
      } else if (body.query.includes("notifications")) {
        data = {
          notifications: [
            {
              id: "notification-1",
              kind: "system",
              title: "Ready",
              body: "Library scan complete.",
              ref: "",
              createdAt: "2026-06-17T12:00:00Z",
              read: false,
            },
          ],
        };
      } else if (body.query.includes("markers")) {
        data = {
          markers: [
            {
              startSeconds: 1.5,
              endSeconds: 30,
              segmentKind: "intro",
              title: "Intro",
            },
          ],
        };
      } else if (body.query.includes("deleteMyEngagement")) {
        data = { deleteMyEngagement: { scope: body.variables.scope } };
      } else {
        data = { myEngagementLog: { total: 0, events: [] } };
      }
      return Promise.resolve({
        ok: true,
        status: 200,
        json: () => Promise.resolve({ data }),
      } as Response);
    }),
  );
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("fetchMyDataSummary", () => {
  it("sends the myDataSummary query with no variables", async () => {
    await fetchMyDataSummary();
    expect(recorded).toHaveLength(1);
    expect(recorded[0].query).toContain("myDataSummary");
    expect(recorded[0].query).toContain("libraryBreakdown");
    expect(recorded[0].variables).toEqual({});
  });
});

describe("fetchNotifications", () => {
  it("sends the notifications query with unread filter and limit", async () => {
    const rows = await fetchNotifications({ unreadOnly: true, limit: 100 });
    expect(rows).toHaveLength(1);
    expect(recorded).toHaveLength(1);
    expect(recorded[0].query).toContain("notifications");
    expect(recorded[0].query).toContain("$unreadOnly");
    expect(recorded[0].query).toContain("$limit");
    expect(recorded[0].variables).toEqual({
      unreadOnly: true,
      limit: 100,
    });
  });
});

describe("markNotificationRead", () => {
  it("sends the markNotificationRead mutation with the notification id", async () => {
    const result = await markNotificationRead("notification-1");
    expect(result).toEqual({ id: "notification-1", found: true });
    expect(recorded).toHaveLength(1);
    expect(recorded[0].query).toContain("markNotificationRead");
    expect(recorded[0].query).toContain("$id");
    expect(recorded[0].variables).toEqual({ id: "notification-1" });
  });
});

describe("fetchPlaybackMarkers", () => {
  it("requests marker seconds fields from the server schema", async () => {
    const markers = await fetchPlaybackMarkers("media-1");
    expect(markers[0]).toEqual({
      startSeconds: 1.5,
      endSeconds: 30,
      segmentKind: "intro",
      title: "Intro",
    });
    expect(recorded[0].query).toContain("markers(mediaID: $mediaID)");
    expect(recorded[0].query).toContain("startSeconds endSeconds");
    expect(recorded[0].query).not.toContain("startMillis");
    expect(recorded[0].variables).toEqual({ mediaID: "media-1" });
  });
});

describe("deleteMyEngagement", () => {
  it("sends the optional reveal PIN variable for retry deletes", async () => {
    await deleteMyEngagement("all", "1234");
    expect(recorded[0].query).toContain("deleteMyEngagement(scope: $scope, pin: $pin)");
    expect(recorded[0].variables).toEqual({ scope: "all", pin: "1234" });
  });
});

describe("fetchMyEngagementLog", () => {
  it("forwards Library + date-range filter and pagination variables verbatim", async () => {
    await fetchMyEngagementLog({
      libraryID: "library-local",
      from: "2026-01-01T00:00:00Z",
      to: "2026-01-31T23:59:59Z",
      limit: 25,
      offset: 50,
    });
    expect(recorded).toHaveLength(1);
    expect(recorded[0].query).toContain("myEngagementLog");
    expect(recorded[0].query).toContain("$libraryID");
    expect(recorded[0].query).toContain("$from");
    expect(recorded[0].query).toContain("$to");
    expect(recorded[0].variables).toEqual({
      libraryID: "library-local",
      from: "2026-01-01T00:00:00Z",
      to: "2026-01-31T23:59:59Z",
      limit: 25,
      offset: 50,
    });
  });

  it("omits absent filters (only paging) so the server sees no Library/date bound", async () => {
    await fetchMyEngagementLog({ limit: 25, offset: 0 });
    expect(recorded[0].variables).toEqual({ limit: 25, offset: 0 });
  });
});
