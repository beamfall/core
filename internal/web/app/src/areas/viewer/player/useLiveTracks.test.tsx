import { act, renderHook } from "@testing-library/react";
import type Hls from "hls.js";
import { createRef, type ReactNode, useState } from "react";
import { describe, expect, it } from "vitest";
import { I18nProvider } from "@/lib/i18n";
import type { PlaybackInfo, PlaybackStream } from "../lib/playback";
import { SUBTITLES_UNAVAILABLE_ID, useLiveTracks } from "./useLiveTracks";
import type { StreamTrackControl } from "./usePlayer";

const hlsStream: PlaybackStream = {
  kind: "hls",
  url: "/stream.m3u8",
  renditions: [
    { id: "1080", height: 1080 },
    { id: "720", height: 720 },
  ],
};

function wrapper({ children }: { children: ReactNode }) {
  return <I18nProvider initialLocale="en">{children}</I18nProvider>;
}

function renderTracks(hls: Hls | null) {
  const videoRef = createRef<HTMLVideoElement>();
  const hlsRef = { current: hls };
  return renderHook(
    () => {
      const [manualRenditionID, setManualRenditionID] = useState("");
      return useLiveTracks({
        videoRef,
        hlsRef,
        stream: hlsStream,
        manualRenditionID,
        setManualRenditionID,
        onSubtitleChanged: () => {},
      });
    },
    { wrapper },
  );
}

describe("useLiveTracks quality selection", () => {
  it("keeps native HLS on Auto because the element cannot apply a pin", () => {
    const { result } = renderTracks(null);
    act(() => result.current.selectQuality("720"));
    expect(result.current.manualPinActive).toBe(false);
    const selected = result.current.qualityOptions.filter((option) => option.selected);
    expect(selected.map((option) => option.id)).toEqual(["auto"]);
  });

  it("pins the rendition when hls.js can apply it", () => {
    // hls.js orders levels ascending: 720p is level 0, 1080p is level 1.
    const hls = {
      currentLevel: -1,
      levels: [{ height: 720 }, { height: 1080 }],
    } as unknown as Hls;
    const { result } = renderTracks(hls);
    act(() => result.current.selectQuality("720"));
    expect(hls.currentLevel).toBe(0);
    expect(result.current.manualPinActive).toBe(true);
    const selected = result.current.qualityOptions.filter((option) => option.selected);
    expect(selected.map((option) => option.id)).toEqual(["720"]);
  });

  it("accepts a pick made while the manifest is still loading", () => {
    const hls = { currentLevel: -1, levels: [] } as unknown as Hls;
    const { result } = renderTracks(hls);
    act(() => result.current.selectQuality("720"));
    expect(result.current.manualPinActive).toBe(true);
    // Held for the manifest parse, not applied to a level list that is not there yet.
    expect(hls.currentLevel).toBe(-1);
  });
});

describe("useLiveTracks file track menus (CORE-1709)", () => {
  const info: PlaybackInfo = {
    decision: "transcode",
    audioTracks: [
      { streamIndex: 1, codec: "aac", language: "eng", channels: 2, selected: true },
      { streamIndex: 2, codec: "ac3", language: "fra", channels: 6 },
    ],
    captionTracks: [
      { id: "muxed-main-3", format: "subrip", streamIndex: 3, language: "eng", url: "/s/3.vtt" },
      { id: "muxed-main-4", format: "dvd_subtitle", streamIndex: 4, language: "fra" },
      { id: "sidecar-main-0", format: "srt", language: "de", url: "/c/0.vtt" },
    ],
    subtitlesUnavailableReason: "no-client-renderable-subtitle-track",
  } as PlaybackInfo;

  function renderFileTracks(control: StreamTrackControl) {
    const videoRef = createRef<HTMLVideoElement>();
    // An hls.js stream with one audio track and no subtitles, as a server
    // transcode carries: the menus must still list the whole file.
    const hlsRef = {
      current: {
        audioTracks: [{ name: "a" }],
        audioTrack: 0,
        subtitleTracks: [],
      } as unknown as Hls,
    };
    return renderHook(
      () =>
        useLiveTracks({
          videoRef,
          hlsRef,
          stream: hlsStream,
          info,
          trackControl: control,
          manualRenditionID: "",
          setManualRenditionID: () => {},
          onSubtitleChanged: () => {},
        }),
      { wrapper },
    );
  }

  function control(active: string | null = null) {
    const calls: Array<[string, unknown]> = [];
    let shown = active;
    const trackControl: StreamTrackControl = {
      activeCaptionID: () => shown,
      selectAudio: (streamIndex) => calls.push(["audio", streamIndex]),
      selectCaption: (id) => {
        shown = id;
        calls.push(["caption", id]);
      },
    };
    return { trackControl, calls };
  }

  it("lists every audio stream of the file and switches by stream index", () => {
    const { trackControl, calls } = control();
    const { result } = renderFileTracks(trackControl);
    expect(
      result.current.audioOptions.map((option) => [option.id, option.label, option.selected]),
    ).toEqual([
      ["0", "English · AAC · Stereo", true],
      ["1", "French · Dolby Digital · 5.1", false],
    ]);
    act(() => result.current.selectAudio("1"));
    expect(calls).toEqual([["audio", 2]]);
  });

  it("lists text, burn-in and sidecar subtitles plus why some are missing", () => {
    const { trackControl, calls } = control("sidecar-main-0");
    const { result } = renderFileTracks(trackControl);
    const rows = result.current.subtitleOptions.map((option) => [
      option.id,
      option.label,
      option.selected,
      option.disabled,
    ]);
    expect(rows).toEqual([
      ["off", "Subtitles off", false, false],
      ["0", "English · SRT", false, false],
      ["1", "French · DVD · Burned in", false, false],
      ["2", "German · SRT · External", true, false],
      [SUBTITLES_UNAVAILABLE_ID, "Some subtitles can't be shown in this browser", false, true],
    ]);
    act(() => result.current.selectSubtitle("1"));
    act(() => result.current.selectSubtitle("off"));
    expect(calls).toEqual([
      ["caption", "muxed-main-4"],
      ["caption", null],
    ]);
  });
});
