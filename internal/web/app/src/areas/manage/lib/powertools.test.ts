import { beforeEach, describe, expect, it, vi } from "vitest";
import { ApiError, graphql } from "@/lib/api";
import { fetchJobs } from "@/lib/graphql";
import type { JobInfo } from "@/lib/graphql/types";
import {
  batchRenameManagedPaths,
  CONVERT_JOB_KIND,
  enqueueConvertJob,
  fetchConvertJobs,
  undoBatchRename,
} from "./powertools";

vi.mock("@/lib/api", async () => {
  const actual = await vi.importActual<typeof import("@/lib/api")>("@/lib/api");
  return { ...actual, graphql: vi.fn() };
});
vi.mock("@/lib/graphql", () => ({ fetchJobs: vi.fn() }));

function job(id: string, kind: string): JobInfo {
  return {
    id,
    kind,
    status: "running",
    progress: { current: 1, total: 2, unit: "files" },
  };
}

beforeEach(() => vi.clearAllMocks());

describe("fetchConvertJobs", () => {
  it("returns only convert-managed-file jobs from the substrate", async () => {
    vi.mocked(fetchJobs).mockResolvedValue([
      job("j1", CONVERT_JOB_KIND),
      job("j2", "scan-library"),
      job("j3", CONVERT_JOB_KIND),
    ]);
    const out = await fetchConvertJobs();
    expect(out.map((j) => j.id)).toEqual(["j1", "j3"]);
  });
});

describe("enqueueConvertJob", () => {
  it("reports available with the created job on success", async () => {
    vi.mocked(graphql).mockResolvedValue({
      enqueueConvertJob: job("jx", CONVERT_JOB_KIND),
    });
    const result = await enqueueConvertJob("library-local", "/srv/a.mov", "h264-mp4");
    expect(result.available).toBe(true);
    expect(result.job?.id).toBe("jx");
  });

  it("degrades to unavailable when the server has no enqueue field", async () => {
    vi.mocked(graphql).mockRejectedValue(
      new ApiError("Cannot query field enqueueConvertJob on type Mutation", 200),
    );
    const result = await enqueueConvertJob("library-local", "/srv/a.mov", "h264-mp4");
    expect(result.available).toBe(false);
    expect(result.job).toBeUndefined();
  });

  it("rethrows unrelated errors", async () => {
    vi.mocked(graphql).mockRejectedValue(new ApiError("network down", 500));
    await expect(enqueueConvertJob("library-local", "/srv/a.mov", "h264-mp4")).rejects.toThrow(
      "network down",
    );
  });
});

describe("batch rename + undo", () => {
  it("sends renames and returns the batch id", async () => {
    vi.mocked(graphql).mockResolvedValue({
      batchRenameManagedPaths: { batchID: "b1", kind: "rename", affected: 2 },
    });
    const out = await batchRenameManagedPaths("library-local", [
      { path: "/srv/a/one.mp4", name: "uno.mp4" },
    ]);
    expect(out.batchID).toBe("b1");
    expect(out.affected).toBe(2);
  });

  it("undoes a batch by id", async () => {
    vi.mocked(graphql).mockResolvedValue({
      undoBatch: { batchID: "b1", kind: "rename", affected: 2 },
    });
    const out = await undoBatchRename("b1");
    expect(out.affected).toBe(2);
  });
});
