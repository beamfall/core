import { describe, expect, it } from "vitest";
import type { JobInfo } from "@/lib/graphql/types";
import { arrangeJobQueue, jobRunSeconds, unresolvedFailedJobs } from "./jobQueue";

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
  it("measures start to finish, or start to now while running", () => {
    const startedAt = "2026-11-17T10:00:00Z";
    const now = Date.parse("2026-11-17T10:02:30Z");
    expect(jobRunSeconds(job({ startedAt, finishedAt: "2026-11-17T10:01:00Z" }), now)).toBe(60);
    expect(jobRunSeconds(job({ status: "running", startedAt }), now)).toBe(150);
    expect(jobRunSeconds(job({}), now)).toBeNull();
  });
});
