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

  it("declares the audio containers and file-only codecs a music track direct-plays with", async () => {
    const profile = await probeCapabilityProfile({
      video: fakeVideo(["avc1", "audio/mpeg", "audio/flac", "mp4a.40.2"]),
      mediaCapabilities: fakeMediaCapabilities((config) => {
        if (config.audio) {
          return config.type === "file" && config.audio.contentType === "audio/mpeg";
        }
        return false;
      }),
      clientPrefs: [],
    });

    expect(profile.containers).toEqual(["mp4", "mp3", "m4a", "flac"]);
    expect(profile.audioCodecs).toEqual([
      { codec: "flac", maxChannels: 2 },
      { codec: "mp3", maxChannels: 2 },
    ]);
  });

  // Safari 27 answers Media Capabilities "unsupported" for a bare FLAC or mp3
  // file, and in MSE, while its audio element plays both: the music track must
  // still direct-play there, or it is transcoded and loses its visual.
  it("declares a bare audio file's codec the audio element plays though Media Capabilities refuses it", async () => {
    const safari = {
      canPlayType: (type: string) =>
        (
          ({
            'video/mp4; codecs="avc1.640028"': "probably",
            "audio/mpeg": "maybe",
            'audio/mp4; codecs="mp4a.40.2"': "probably",
            'audio/mp4; codecs="flac"': "probably",
            "audio/flac": "maybe",
          }) as Record<string, CanPlayTypeResult>
        )[type] ?? "",
    };
    const profile = await probeCapabilityProfile({
      video: safari,
      mediaCapabilities: fakeMediaCapabilities(
        (config) =>
          config.audio?.contentType === 'audio/mp4; codecs="mp4a.40.2"' ||
          Boolean(config.video?.contentType.includes("avc1")),
      ),
      clientPrefs: [],
    });

    expect(profile.containers).toEqual(["mp4", "mp3", "m4a", "flac"]);
    expect(profile.audioCodecs.map((cap) => cap.codec)).toEqual(["aac", "flac", "mp3"]);
  });

  it("leaves out a file codec the audio element cannot play", async () => {
    const profile = await probeCapabilityProfile({
      video: fakeVideo(["avc1", "mp4a.40.2"]),
      mediaCapabilities: fakeMediaCapabilities((config) =>
        Boolean(config.audio?.contentType.includes("mp4a")),
      ),
      clientPrefs: [],
    });

    expect(profile.audioCodecs.map((cap) => cap.codec)).toEqual(["aac"]);
  });
});
