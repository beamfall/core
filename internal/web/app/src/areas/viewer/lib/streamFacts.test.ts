import { describe, expect, it } from "vitest";
import type { MediaTrackVersion } from "./mediaDetail";
import { streamFacts } from "./streamFacts";

const text = { mono: "Mono", stereo: "Stereo", channels: (count: number) => `${count} ch` };

function version(over: Partial<MediaTrackVersion>): MediaTrackVersion {
  return { id: "v", label: "", audioTracks: [], captionTracks: [], ...over };
}

describe("streamFacts (CORE-2469)", () => {
  it("reads the first Version's resolution, HDR and default audio track", () => {
    const versions = [
      version({
        resolution: "4K",
        hdr: "hdr10",
        audioTracks: [
          { streamIndex: 1, codec: "aac", channels: 2 },
          { streamIndex: 2, codec: "eac3", channels: 6, default: true },
        ],
      }),
      version({ id: "w", resolution: "1080p", hdr: "dolby-vision-p8" }),
    ];
    expect(streamFacts(versions, text).map((f) => f.text)).toEqual([
      "4K",
      "HDR10",
      "Dolby Digital Plus 5.1",
    ]);
  });

  it("takes the server's label, so the row agrees with the track pickers (CORE-2485)", () => {
    // 1600x900 is 1080p by the server's thresholds; the row never re-derives it.
    expect(streamFacts([version({ resolution: "1080p" })], text).map((f) => f.text)).toEqual([
      "1080p",
    ]);
  });

  it("leaves out what is unknown", () => {
    expect(streamFacts([], text)).toEqual([]);
    const sdr = [version({ audioTracks: [{ streamIndex: 1, codec: "ac3" }] })];
    expect(streamFacts(sdr, text).map((f) => f.text)).toEqual(["Dolby Digital"]);
  });
});
