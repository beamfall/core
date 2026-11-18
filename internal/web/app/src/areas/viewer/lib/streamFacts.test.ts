import { describe, expect, it } from "vitest";
import type { MediaTrackVersion } from "./mediaDetail";
import { resolutionName, streamFacts } from "./streamFacts";

const text = { mono: "Mono", stereo: "Stereo", channels: (count: number) => `${count} ch` };

function version(over: Partial<MediaTrackVersion>): MediaTrackVersion {
  return { id: "v", label: "", audioTracks: [], captionTracks: [], ...over };
}

describe("resolutionName (CORE-2469)", () => {
  it("names the frame by its larger class, scope and cropped frames included", () => {
    expect(resolutionName(3840, 2160)).toBe("4K");
    expect(resolutionName(3840, 1600)).toBe("4K");
    expect(resolutionName(1920, 800)).toBe("1080p");
    expect(resolutionName(1280, 544)).toBe("720p");
    expect(resolutionName(720, 480)).toBe("480p");
    expect(resolutionName(7680, 4320)).toBe("8K");
    expect(resolutionName()).toBe("");
  });
});

describe("streamFacts (CORE-2469)", () => {
  it("reads resolution, the first Version's HDR and its default audio track", () => {
    const versions = [
      version({
        hdr: "hdr10",
        audioTracks: [
          { streamIndex: 1, codec: "aac", channels: 2 },
          { streamIndex: 2, codec: "eac3", channels: 6, default: true },
        ],
      }),
      version({ id: "w", hdr: "dolby-vision-p8" }),
    ];
    expect(
      streamFacts({ videoWidth: 3840, videoHeight: 2160 }, versions, text).map((f) => f.text),
    ).toEqual(["4K", "HDR10", "Dolby Digital Plus 5.1"]);
  });

  it("leaves out what is unknown", () => {
    expect(streamFacts(null, [], text)).toEqual([]);
    const sdr = [version({ audioTracks: [{ streamIndex: 1, codec: "ac3" }] })];
    expect(streamFacts({}, sdr, text).map((f) => f.text)).toEqual(["Dolby Digital"]);
  });
});
