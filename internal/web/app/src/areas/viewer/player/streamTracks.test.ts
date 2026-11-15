import { describe, expect, it } from "vitest";
import type { PlaybackAudioTrack, PlaybackCaptionTrack } from "../lib/playback";
import {
  attachCaptionElements,
  audioTrackLabel,
  CAPTION_TRACK_ATTRIBUTE,
  captionTrackLabel,
  isBurnInCaption,
  languageName,
  removeCaptionElements,
  sameLanguage,
  showCaptionElement,
  shownCaptionElement,
  type TrackLabelText,
} from "./streamTracks";

const text: TrackLabelText = {
  locale: "en",
  unknownLanguage: "Unknown language",
  mono: "Mono",
  stereo: "Stereo",
  channels: (count) => `${count} channels`,
  commentary: "Commentary",
  described: "Audio description",
  forced: "Forced",
  sdh: "SDH",
  external: "External",
  burnedIn: "Burned in",
};

// The synthetic multi-track file of CORE-1709: three audio streams, three
// muxed subtitle streams (text, ASS, picture-based) and three sidecars.
const audio: PlaybackAudioTrack[] = [
  { streamIndex: 1, codec: "aac", language: "eng", channels: 2, default: true, selected: true },
  { streamIndex: 2, codec: "ac3", language: "fra", channels: 6 },
  { streamIndex: 3, codec: "aac", language: "eng", channels: 2, commentary: true },
];
const captions: PlaybackCaptionTrack[] = [
  { id: "muxed-main-4", format: "subrip", streamIndex: 4, language: "eng", url: "/s/4.vtt" },
  { id: "muxed-main-5", format: "ass", streamIndex: 5, language: "fra", url: "/s/5.vtt" },
  { id: "muxed-main-6", format: "dvd_subtitle", streamIndex: 6, language: "spa" },
  { id: "sidecar-main-0", format: "srt", language: "de", forced: true, url: "/c/0.vtt" },
  { id: "sidecar-main-1", format: "ass", language: "ja", url: "/c/1.vtt" },
];

describe("stream track labels", () => {
  it("names each audio track by language, codec, layout and role", () => {
    expect(audio.map((track) => audioTrackLabel(track, text))).toEqual([
      "English · AAC · Stereo",
      "French · Dolby Digital · 5.1",
      "English · AAC · Stereo · Commentary",
    ]);
  });

  it("names caption tracks, marking sidecars external and picture subtitles burned in", () => {
    expect(captions.map((track) => captionTrackLabel(track, text))).toEqual([
      "English · SRT",
      "French · ASS",
      "Spanish · DVD · Burned in",
      "German · Forced · SRT · External",
      "Japanese · ASS · External",
    ]);
  });

  it("falls back to the unknown-language wording for an untagged track", () => {
    expect(audioTrackLabel({ streamIndex: 1, codec: "opus", language: "und" }, text)).toBe(
      "Unknown language · Opus",
    );
    expect(languageName("", "en")).toBe("");
  });

  it("treats three- and two-letter language codes as the same language", () => {
    expect(sameLanguage("eng", "en")).toBe(true);
    expect(sameLanguage("en-US", "en")).toBe(true);
    expect(sameLanguage("fra", "en")).toBe(false);
  });

  it("burns in only muxed streams without a WebVTT URL", () => {
    expect(captions.map(isBurnInCaption)).toEqual([false, false, true, false, false]);
  });
});

describe("caption <track> elements", () => {
  it("adds one disabled track per caption with a URL and toggles them by id", () => {
    const video = document.createElement("video");
    attachCaptionElements(video, captions, "en");
    const elements = Array.from(video.querySelectorAll("track"));
    expect(elements.map((element) => element.getAttribute(CAPTION_TRACK_ATTRIBUTE))).toEqual([
      "muxed-main-4",
      "muxed-main-5",
      "sidecar-main-0",
      "sidecar-main-1",
    ]);
    expect(elements[2].getAttribute("src")).toBe("/c/0.vtt");
    expect(elements[2].srclang).toBe("de");
    // jsdom has no TextTrack on <track>: stub the mode each element carries.
    for (const element of elements) {
      Object.defineProperty(element, "track", { value: { mode: "disabled" } });
    }
    expect(shownCaptionElement(video)).toBeNull();
    showCaptionElement(video, "sidecar-main-0");
    expect(shownCaptionElement(video)).toBe("sidecar-main-0");
    showCaptionElement(video, null);
    expect(shownCaptionElement(video)).toBeNull();
    removeCaptionElements(video);
    expect(video.querySelectorAll("track")).toHaveLength(0);
  });
});
