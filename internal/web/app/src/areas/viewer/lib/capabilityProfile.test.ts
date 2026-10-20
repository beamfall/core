import { describe, expect, it } from "vitest";

import { probeCapabilityProfile } from "./capabilityProfile";

function fakeVideo(supportedSubstrings: string[]) {
  return {
    canPlayType(contentType: string) {
      return supportedSubstrings.some((s) => contentType.includes(s)) ? "probably" : "";
    },
  };
}

function fakeMediaCapabilities(
  predicate: (configuration: MediaDecodingConfiguration) => boolean,
): MediaCapabilities {
  return {
    decodingInfo: async (configuration: MediaDecodingConfiguration) => ({
      supported: predicate(configuration),
      smooth: true,
      powerEfficient: true,
      keySystemAccess: null,
      configuration,
    }),
    encodingInfo: async () => ({
      supported: false,
      smooth: false,
      powerEfficient: false,
      keySystemAccess: null,
      configuration: { type: "record" },
    }),
  } as MediaCapabilities;
}

describe("probeCapabilityProfile", () => {
  it("falls back to a conservative 1080p canPlayType profile", async () => {
    const profile = await probeCapabilityProfile({
      video: fakeVideo(["avc1"]),
      mediaCapabilities: null,
      clientPrefs: [],
    });

    expect(profile.containers).toEqual(["mp4"]);
    expect(profile.directCodecs).toEqual(["h264"]);
    expect(profile.videoCodecs).toHaveLength(1);
    expect(profile.videoCodecs[0]).toMatchObject({ codec: "h264", maxHeight: 1080 });
    expect(profile.hdrFormats).toBeUndefined();
    expect(profile.subtitleFormats).toEqual(["srt", "webvtt"]);
    expect(profile.downstreamAudio).toBe("stereo");
  });

  it("uses Media Capabilities for codec ceilings, HDR, and downstream audio", async () => {
    const profile = await probeCapabilityProfile({
      video: fakeVideo([]),
      clientPrefs: [{ key: "playback.downstream-audio", value: "surround" }],
      mediaCapabilities: fakeMediaCapabilities((config) => {
        if (config.audio) {
          return config.audio.contentType.includes("ec-3");
        }
        if (config.video?.transferFunction) {
          return config.video.transferFunction === "pq";
        }
        return Boolean(config.video?.contentType.includes("hev1") && config.video.height <= 1080);
      }),
    });

    expect(profile.directCodecs).toEqual(["hevc"]);
    expect(profile.videoCodecs[0]).toMatchObject({
      codec: "hevc",
      profiles: ["Main", "Main 10"],
      maxHeight: 1080,
    });
    expect(profile.audioCodecs).toEqual([{ codec: "eac3", maxChannels: 6 }]);
    expect(profile.hdrFormats).toEqual(["hdr10"]);
    expect(profile.downstreamAudio).toBe("passthrough");
  });
});
