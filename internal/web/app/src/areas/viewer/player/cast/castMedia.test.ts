import { describe, expect, it } from "vitest";
import { buildCastLoad, CAST_RECEIVER_PROFILE, type CastPlaybackInfo } from "./castMedia";

const origin = "https://server.example:8443";

const info: CastPlaybackInfo = {
  mediaClass: "video",
  streams: [{ kind: "hls", url: "/api/media/hls/tok/master.m3u8" }],
  timeline: { positionSeconds: 754 },
  captionTracks: [
    { id: "sidecar-a-0", url: "/api/media/sidecar/tok/0.vtt", format: "srt", language: "en" },
    {
      id: "sidecar-a-1",
      url: "/api/media/sidecar/tok/1.vtt",
      format: "srt",
      language: "fr",
      sdh: true,
    },
    { id: "muxed-a-3", format: "hdmv_pgs_subtitle", streamIndex: 3, language: "de" },
    { id: "sidecar-a-2", url: "/api/media/sidecar/tok/2.ass", format: "ass", language: "es" },
  ],
  selectedSubtitleTrack: { track: { language: "fr", kind: "sidecar", roles: { sdh: true } } },
};

describe("CAST_RECEIVER_PROFILE", () => {
  it("declares the Default Media Receiver as a chromecast client", () => {
    expect(CAST_RECEIVER_PROFILE.client).toBe("chromecast-default-receiver");
    expect(CAST_RECEIVER_PROFILE.containers).toEqual(["mp4"]);
    expect(CAST_RECEIVER_PROFILE.videoCodecs.map((cap) => cap.codec)).toEqual(["h264"]);
    expect(CAST_RECEIVER_PROFILE.audioCodecs).toEqual([{ codec: "aac", maxChannels: 2 }]);
    expect(CAST_RECEIVER_PROFILE.subtitleFormats).toEqual(["webvtt"]);
  });
});

describe("buildCastLoad", () => {
  it("casts the server's route with the resume point and its subtitle choice", () => {
    const load = buildCastLoad(info, { origin, title: "Sample" });
    expect(load).toEqual({
      contentId: `${origin}/api/media/hls/tok/master.m3u8`,
      contentType: "application/x-mpegurl",
      hls: true,
      title: "Sample",
      startSeconds: 754,
      tracks: [
        {
          trackId: 1,
          url: `${origin}/api/media/sidecar/tok/0.vtt`,
          language: "en",
          captions: false,
        },
        {
          trackId: 2,
          url: `${origin}/api/media/sidecar/tok/1.vtt`,
          language: "fr",
          captions: true,
        },
      ],
      activeTrackIds: [2],
    });
  });

  it("starts where local playback is once it has moved", () => {
    const load = buildCastLoad(info, { origin, title: "Sample", localSeconds: 1203.5 });
    expect(load).toMatchObject({ startSeconds: 1203.5 });
  });

  it("casts a direct MP4 route as MP4 with no subtitle selected when none is chosen", () => {
    const load = buildCastLoad(
      { streams: [{ kind: "direct-play", url: "/api/media/direct/tok" }] },
      { origin, title: "Sample" },
    );
    expect(load).toMatchObject({
      contentId: `${origin}/api/media/direct/tok`,
      contentType: "video/mp4",
      hls: false,
      startSeconds: 0,
      tracks: [],
      activeTrackIds: [],
    });
  });

  it("reports when the server offers the receiver no route", () => {
    expect(buildCastLoad({ streams: [] }, { origin, title: "Sample" })).toBe("no-route");
    expect(buildCastLoad({ reason: "requires-transcode" }, { origin, title: "Sample" })).toBe(
      "no-route",
    );
  });
});
