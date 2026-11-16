import { afterEach, describe, expect, it, vi } from "vitest";

const probe = vi.hoisted(() => ({ seconds: 0 }));
vi.mock("../useGaplessAudio", () => ({
  MAX_DECODED_SECONDS: 12 * 60,
  probeDuration: () => Promise.resolve(probe.seconds),
}));

import { decodeTrack } from "./analysis";

describe("decodeTrack", () => {
  afterEach(() => vi.unstubAllGlobals());

  // A long recording decoded whole would hold hundreds of megabytes (CORE-1309 review).
  it.each([4 * 3600, Number.NaN, Number.POSITIVE_INFINITY])(
    "skips an item of %s seconds without fetching it",
    async (seconds) => {
      probe.seconds = seconds;
      const fetch = vi.fn();
      vi.stubGlobal("fetch", fetch);
      await expect(decodeTrack("/media/a", new AbortController().signal)).resolves.toBeNull();
      expect(fetch).not.toHaveBeenCalled();
    },
  );
});
