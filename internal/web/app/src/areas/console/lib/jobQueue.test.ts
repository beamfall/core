import { describe, expect, it } from "vitest";
import type { JobInfo } from "@/lib/graphql/types";
import {
  arrangeJobQueue,
  collapseJobLog,
  formatJobEventTime,
  jobRunSeconds,
  redactJobEventPayload,
  redactJobLogPayload,
  unresolvedFailedJobs,
} from "./jobQueue";

function job(overrides: Partial<JobInfo>): JobInfo {
  return {
    id: "job",
    kind: "scan",
    status: "completed",
    progress: { current: 0, total: 0, unit: "" },
    ...overrides,
  } as JobInfo;
}

const folder = { libraryID: "lib-1", libraryTitle: "Films", root: "/media/films" };

describe("arrangeJobQueue (CORE-2308)", () => {
  it("orders live work running, yielded, then queued", () => {
    const view = arrangeJobQueue([
      job({ id: "q", status: "queued" }),
      job({ id: "y", status: "yielded" }),
      job({ id: "r", status: "running" }),
    ]);
    expect(view.live.map((j) => j.id)).toEqual(["r", "y", "q"]);
  });

  it("treats a failure a later clean run of the same folder cleared as history", () => {
    const view = arrangeJobQueue([
      job({ id: "f", status: "failed", target: folder, finishedAt: "2026-11-17T10:00:00Z" }),
      job({ id: "ok", status: "completed", target: folder, finishedAt: "2026-11-17T11:00:00Z" }),
    ]);
    expect(view.failed).toEqual([]);
    expect(view.history).toEqual([{ job: expect.objectContaining({ id: "ok" }), count: 2 }]);
  });

  it.each(["queued", "running", "yielded"])(
    "treats a failure a newer %s job of the same folder superseded as history (CORE-2352)",
    (status) => {
      const jobs = [
        job({ id: "f", status: "failed", target: folder, finishedAt: "2026-11-17T14:58:00Z" }),
        job({
          id: "next",
          status,
          target: folder,
          createdAt: "2026-11-17T15:00:00Z",
          updatedAt: "2026-11-17T15:05:00Z",
        }),
      ];
      const view = arrangeJobQueue(jobs);
      expect(view.failed).toEqual([]);
      expect(view.live.map((j) => j.id)).toEqual(["next"]);
      expect(view.history.map((g) => g.job.id)).toEqual(["f"]);
      expect(unresolvedFailedJobs(jobs)).toEqual([]);
    },
  );

  it("keeps a failure under Failed when only older or other-target jobs exist (CORE-2352)", () => {
    const view = arrangeJobQueue([
      job({ id: "f", status: "failed", target: folder, finishedAt: "2026-11-17T14:58:00Z" }),
      job({ id: "older", status: "yielded", target: folder, updatedAt: "2026-11-17T14:00:00Z" }),
      job({
        id: "elsewhere",
        status: "running",
        target: { ...folder, root: "/media/more" },
        updatedAt: "2026-11-17T15:05:00Z",
      }),
      job({
        id: "other-kind",
        kind: "artwork-sweep",
        status: "completed",
        target: folder,
        finishedAt: "2026-11-17T15:05:00Z",
      }),
    ]);
    expect(view.failed.map((g) => g.job.id)).toEqual(["f"]);
  });

  it("keeps the newest unresolved failure per folder and counts the repeats", () => {
    const jobs = [
      job({ id: "ok", status: "completed", target: folder, finishedAt: "2026-11-17T09:00:00Z" }),
      job({ id: "f1", status: "failed", target: folder, finishedAt: "2026-11-17T10:00:00Z" }),
      job({ id: "f2", status: "failed", target: folder, finishedAt: "2026-11-17T11:00:00Z" }),
      job({
        id: "other",
        status: "failed",
        target: { ...folder, root: "/media/more" },
        finishedAt: "2026-11-17T08:00:00Z",
      }),
    ];
    const view = arrangeJobQueue(jobs);
    expect(view.failed.map((g) => [g.job.id, g.count])).toEqual([
      ["f2", 2],
      ["other", 1],
    ]);
    expect(unresolvedFailedJobs(jobs).map((j) => j.id)).toEqual(["f2", "other"]);
  });

  it("folds finished runs into one row per kind, newest first", () => {
    const view = arrangeJobQueue([
      job({ id: "a1", kind: "update-check", finishedAt: "2026-11-17T08:00:00Z" }),
      job({ id: "b1", kind: "artwork-sweep", finishedAt: "2026-11-17T09:00:00Z" }),
      job({ id: "a2", kind: "update-check", finishedAt: "2026-11-17T10:00:00Z" }),
      job({ id: "c1", kind: "scan", status: "canceled", finishedAt: "2026-11-17T07:00:00Z" }),
    ]);
    expect(view.history.map((g) => [g.job.id, g.count])).toEqual([
      ["a2", 2],
      ["b1", 1],
      ["c1", 1],
    ]);
  });
});

describe("jobRunSeconds", () => {
  it.each([1, 2, 3])("measures elapsed time since first start across %i attempts", (attempts) => {
    const startedAt = "2026-11-17T10:00:00Z";
    const now = Date.parse("2026-11-17T10:02:30Z");
    expect(
      jobRunSeconds(job({ startedAt, attempts, finishedAt: "2026-11-17T10:01:00Z" }), now),
    ).toBe(60);
    expect(jobRunSeconds(job({ status: "running", startedAt, attempts }), now)).toBe(150);
    expect(jobRunSeconds(job({}), now)).toBeNull();
  });
});

describe("collapseJobLog (CORE-2352)", () => {
  it("keeps each run of consecutive progress entries as its latest one", () => {
    const events = [
      "queued",
      "running",
      "progress",
      "progress",
      "progress",
      "yielded",
      "progress",
    ].map((type, i) => ({ id: String(i), type }));
    expect(collapseJobLog(events).map((line) => [line.event.id, line.folded])).toEqual([
      ["0", 0],
      ["1", 0],
      ["4", 2],
      ["5", 0],
      ["6", 0],
    ]);
    expect(collapseJobLog([])).toEqual([]);
  });

  it("folds two or more pause and resume cycles into one summary line", () => {
    const types = [
      "queued",
      "running",
      "progress",
      "yielded",
      "running",
      "progress",
      "progress",
      "yielded",
      "running",
      "yielded",
      "running",
      "progress",
      "completed",
    ];
    const lines = collapseJobLog(types.map((type, i) => ({ id: String(i), type })));
    expect(lines.map((line) => [line.event.id, line.folded, line.pauses])).toEqual([
      ["0", 0, 0],
      ["1", 0, 0],
      ["2", 0, 0],
      // Three pauses from entry 3 to 11: the summary, then the latest
      // progress and the last transition of the run.
      ["3", 7, 3],
      ["10", 0, 0],
      ["11", 0, 0],
      ["12", 0, 0],
    ]);
  });

  it("keeps a single pause as it is", () => {
    const types = ["running", "yielded", "running", "progress", "completed"];
    const lines = collapseJobLog(types.map((type, i) => ({ id: String(i), type })));
    expect(lines.map((line) => line.pauses)).toEqual([0, 0, 0, 0, 0]);
    expect(lines).toHaveLength(5);
  });
});

describe("formatJobEventTime (CORE-2352)", () => {
  const now = new Date("2026-11-17T15:00:00Z");
  it("shows the time alone today and the date and time otherwise", () => {
    expect(formatJobEventTime("2026-11-17T14:58:07Z", now, "en-US", "UTC")).toBe("2:58:07 PM");
    expect(formatJobEventTime("2026-11-16T14:58:07Z", now, "en-US", "UTC")).toBe(
      "11/16/26, 2:58 PM",
    );
    expect(formatJobEventTime("2026-11-17T14:58:07Z", now, "de", "UTC")).toBe("14:58:07");
  });

  it("is empty for a value that is not a date", () => {
    expect(formatJobEventTime("not a date", now, "en-US", "UTC")).toBe("");
  });
});

describe("redactJobLogPayload (CORE-2725)", () => {
  it("blanks developer keys and Jellyfin compat integration keys", () => {
    const out = redactJobLogPayload("dev bfk_abc_123 compat bfj_" + "0".repeat(64) + " done");
    expect(out).not.toContain("bfk_abc_123");
    expect(out).not.toContain("bfj_");
    expect(out).toContain("done");
  });

  it("blanks a token whose ampersand is JSON-escaped", () => {
    const out = redactJobLogPayload('{"u":"/x?a=1\\u0026token=hidden-secret"}');
    expect(out).not.toContain("hidden-secret");
  });
});

describe("redactJobEventPayload (CORE-2725)", () => {
  it("redacts a token that JSON escaping hides until decoding", () => {
    const out = redactJobEventPayload('{"message":"GET /x?a=1\\u0026token=hidden-secret"}');
    expect(out).not.toContain("hidden-secret");
    expect(JSON.parse(out).message).toContain("[REDACTED]");
  });

  it("keeps the payload parseable when a message ends in a credential", () => {
    const out = redactJobEventPayload('{"type":"progress","message":"auth Bearer abc123"}');
    expect(out).not.toContain("abc123");
    expect(JSON.parse(out)).toEqual({ type: "progress", message: "auth [REDACTED]" });
  });

  it("redacts nested strings and leaves other values alone", () => {
    const out = JSON.parse(redactJobEventPayload('{"a":[{"k":"bfj_x1"}],"n":3,"b":true}'));
    expect(out).toEqual({ a: [{ k: "[REDACTED]" }], n: 3, b: true });
  });

  it("redacts a credential hidden behind an escaped tab", () => {
    expect(redactJobEventPayload('{"auth":"Bearer\\tplaceholder"}')).toBe('{"auth":"[REDACTED]"}');
  });

  it("keeps later fields, property names and number literals exactly", () => {
    expect(
      redactJobEventPayload('{"auth":"Bearer placeholder","bfj_k1":1,"recordID":9007199254740993}'),
    ).toBe('{"auth":"[REDACTED]","[REDACTED]":1,"recordID":9007199254740993}');
  });

  it("redacts a payload that is not JSON as text", () => {
    expect(redactJobEventPayload("worker=w2 bfk_abc")).toBe("worker=w2 [REDACTED]");
  });
});
