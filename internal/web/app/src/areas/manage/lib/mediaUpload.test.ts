import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { droppedRelativeDir, openMediaUpload, resumeKey, sendMediaUpload } from "./mediaUpload";

function json(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

const file = new File(["abcdefghij"], "clip.mkv", { lastModified: 1 });

beforeEach(() => sessionStorage.clear());
afterEach(() => vi.unstubAllGlobals());

describe("sendMediaUpload", () => {
  it("resumes from the server offset after a refused or failed chunk", async () => {
    const offsets: string[] = [];
    let call = 0;
    const fetchMock = vi.fn(async (_url: string, init: RequestInit) => {
      const headers = init.headers as Record<string, string>;
      expect(headers["X-Beamfall-Csrf"]).toBe("1");
      if (init.method === "GET") {
        return json({ uploadID: "u1", size: 10, offset: 4, chunkSize: 4 });
      }
      offsets.push(headers["Upload-Offset"]);
      call += 1;
      if (call === 1) return json({ uploadID: "u1", size: 10, offset: 4, chunkSize: 4 });
      if (call === 2) throw new TypeError("network down");
      if (call === 3) return json({ uploadID: "u1", size: 10, offset: 8, chunkSize: 4 });
      return json({ uploadID: "u1", size: 10, offset: 10, chunkSize: 4 });
    });
    vi.stubGlobal("fetch", fetchMock);
    const progress: number[] = [];
    const done = await sendMediaUpload(
      file,
      { uploadID: "u1", fileName: "clip.mkv", relativeDir: "", size: 10, offset: 0, chunkSize: 4 },
      (sent) => progress.push(sent),
    );
    expect(done.offset).toBe(10);
    expect(offsets).toEqual(["0", "4", "4", "8"]);
    expect(progress.at(-1)).toBe(10);
  });

  it("reuses the remembered upload for the same file", async () => {
    sessionStorage.setItem(resumeKey("lib", file, ""), "u-old");
    const fetchMock = vi.fn(async (url: string) => {
      expect(url).toBe("/api/media/uploads/u-old");
      return json({ uploadID: "u-old", size: 10, offset: 6, chunkSize: 4 });
    });
    vi.stubGlobal("fetch", fetchMock);
    const status = await openMediaUpload("lib", file, "");
    expect(status.offset).toBe(6);
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });
});

describe("droppedRelativeDir", () => {
  it("keeps the folder a dropped file came from", () => {
    const nested = new File(["x"], "a.mkv");
    Object.defineProperty(nested, "webkitRelativePath", { value: "Show/Season 1/a.mkv" });
    expect(droppedRelativeDir(nested)).toBe("Show/Season 1");
    expect(droppedRelativeDir(file)).toBe("");
  });
});
