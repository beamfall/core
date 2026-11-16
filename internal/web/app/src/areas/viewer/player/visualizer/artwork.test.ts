import { afterEach, describe, expect, it, vi } from "vitest";
import { loadArtwork, sameOrigin } from "./artwork";

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("visual image input (CORE-2217)", () => {
  it("accepts only non-empty same-origin artwork", () => {
    expect(sameOrigin("/api/media/m1/artwork/poster")).toBe(true);
    expect(sameOrigin(`${window.location.origin}/a.jpg`)).toBe(true);
    expect(sameOrigin("")).toBe(false);
    expect(sameOrigin("https://elsewhere.example/a.jpg")).toBe(false);
  });

  it("fetches same-origin artwork and decodes it downscaled", async () => {
    const bitmap = { close: vi.fn() };
    const fetchMock = vi.fn(async () => new Response(new Blob(["x"]), { status: 200 }));
    const decode = vi.fn(async () => bitmap);
    vi.stubGlobal("fetch", fetchMock);
    vi.stubGlobal("createImageBitmap", decode);
    const got = await loadArtwork("/api/media/m1/artwork/poster", new AbortController().signal);
    expect(got).toBe(bitmap);
    expect(fetchMock).toHaveBeenCalledWith(
      "/api/media/m1/artwork/poster",
      expect.objectContaining({ credentials: "same-origin" }),
    );
    expect(decode).toHaveBeenCalledWith(
      expect.anything(),
      expect.objectContaining({ resizeWidth: 256, resizeHeight: 256 }),
    );
  });

  it("never fetches empty or cross-origin artwork", async () => {
    const fetchMock = vi.fn();
    vi.stubGlobal("fetch", fetchMock);
    vi.stubGlobal("createImageBitmap", vi.fn());
    const signal = new AbortController().signal;
    expect(await loadArtwork("", signal)).toBeNull();
    expect(await loadArtwork("https://elsewhere.example/a.jpg", signal)).toBeNull();
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it("rejects a failed fetch so the caller keeps the stand-in", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => new Response("", { status: 404 })),
    );
    vi.stubGlobal("createImageBitmap", vi.fn());
    await expect(loadArtwork("/a.jpg", new AbortController().signal)).rejects.toThrow("404");
  });
});
