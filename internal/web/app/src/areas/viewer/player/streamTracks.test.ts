import { describe, expect, it } from "vitest";
import type { PlaybackAudioTrack, PlaybackCaptionTrack } from "../lib/playback";
import {
  attachCaptionElements,
  audioTrackLabel,
  CAPTION_TRACK_ATTRIBUTE,
  captionElement,
  captionTrackLabel,
  captionTrackLabels,
  distinctLabels,
  isBurnInCaption,
  languageName,
  matchSelectedCaption,
  orderCaptionTracks,
  preferredLanguageIndex,
  reloadCaptionElement,
  removeCaptionElements,
  sameLanguage,
  showCaptionElement,
  shownCaptionElement,
  type TrackLabelText,
  trackPreferencePolicy,
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
    // No track loads until it is shown: each first request converts on the server.
    expect(elements.map((element) => element.getAttribute("src"))).toEqual([
      null,
      null,
      null,
      null,
    ]);
    expect(elements[2].srclang).toBe("de");
    // jsdom has no TextTrack on <track>: stub the mode each element carries.
    for (const element of elements) {
      Object.defineProperty(element, "track", { value: { mode: "disabled" } });
    }
    expect(shownCaptionElement(video)).toBeNull();
    showCaptionElement(video, "sidecar-main-0");
    expect(shownCaptionElement(video)).toBe("sidecar-main-0");
    expect(elements.map((element) => element.getAttribute("src"))).toEqual([
      null,
      null,
      "/c/0.vtt",
      null,
    ]);
    showCaptionElement(video, null);
    expect(shownCaptionElement(video)).toBeNull();
    removeCaptionElements(video);
    expect(video.querySelectorAll("track")).toHaveLength(0);
  });
});

describe("track preferences (CORE-1680)", () => {
  it("reads the wire policy and ignores what is not a language list", () => {
    expect(
      trackPreferencePolicy({
        audioLanguages: ["fr", 3, ""],
        subtitleLanguages: ["en"],
        subtitleMode: "forced-only",
        preferSDH: true,
      }),
    ).toEqual({
      audioLanguages: ["fr"],
      subtitleLanguages: ["en"],
      subtitleMode: "forced-only",
      preferSDH: true,
    });
    expect(trackPreferencePolicy(undefined)).toBeNull();
    expect(trackPreferencePolicy({})?.audioLanguages).toEqual([]);
  });

  it("picks the track in the most preferred language that is present", () => {
    expect(preferredLanguageIndex(["eng", "fra"], ["it", "fr-CA", "en"])).toBe(1);
    expect(preferredLanguageIndex(["eng", "fra"], ["ja"])).toBe(-1);
    expect(preferredLanguageIndex(["eng", "fra"], [])).toBe(-1);
  });

  it("matches the server's subtitle pick by kind, language and forced flag", () => {
    const forced = matchSelectedCaption(captions, {
      track: { kind: "sidecar", language: "deu", roles: { forced: true } },
    });
    expect(forced?.id).toBe("sidecar-main-0");
    const muxed = matchSelectedCaption(captions, { track: { kind: "muxed", language: "en" } });
    expect(muxed?.id).toBe("muxed-main-4");
    const picture = matchSelectedCaption(captions, { track: { kind: "muxed", language: "es" } });
    expect(picture?.id).toBe("muxed-main-6");
    // A forced pick never lands on a full track, nor a sidecar pick on a muxed one.
    expect(
      matchSelectedCaption(captions, {
        track: { kind: "muxed", language: "en", roles: { forced: true } },
      }),
    ).toBeUndefined();
    expect(
      matchSelectedCaption(captions, { track: { kind: "sidecar", language: "fr" } }),
    ).toBeUndefined();
    expect(matchSelectedCaption(captions, undefined)).toBeUndefined();
  });

  it("prefers a text rendition over a burn-in one for the same pick", () => {
    const both: PlaybackCaptionTrack[] = [
      { id: "muxed-main-7", format: "hdmv_pgs_subtitle", streamIndex: 7, language: "eng" },
      { id: "muxed-main-8", format: "subrip", streamIndex: 8, language: "eng", url: "/s/8.vtt" },
    ];
    expect(matchSelectedCaption(both, { track: { kind: "muxed", language: "en" } })?.id).toBe(
      "muxed-main-8",
    );
  });
});

describe("same-language subtitle labels (CORE-2315)", () => {
  it("adds the stream title unless it only repeats the language", () => {
    const base = { id: "c", format: "subrip", language: "en", url: "/s" } as PlaybackCaptionTrack;
    expect(captionTrackLabel({ ...base, title: "Signs & Songs" }, text)).toBe(
      "English · Signs & Songs · SRT · External",
    );
    expect(captionTrackLabel({ ...base, title: "english" }, text)).toBe("English · SRT · External");
    expect(captionTrackLabel({ ...base, title: "English (SDH)", sdh: true }, text)).toBe(
      "English (SDH) · SRT · External",
    );
    // CORE-2523: a title that denies a role does not stand in for the flag.
    expect(captionTrackLabel({ ...base, title: "Not Forced", forced: true }, text)).toBe(
      "English · Not Forced · Forced · SRT · External",
    );
    expect(captionTrackLabel({ ...base, title: "English (No SDH/CC)", sdh: true }, text)).toBe(
      "English (No SDH/CC) · SDH · SRT · External",
    );
    expect(captionTrackLabel({ ...base, title: "No SDH or CC", sdh: true }, text)).toBe(
      "English · No SDH or CC · SDH · SRT · External",
    );
    expect(
      captionTrackLabel({ ...base, title: "English (non-SDH) [Forced]", forced: true }, text),
    ).toBe("English (non-SDH) [Forced] · SRT · External");
  });

  it("numbers labels that still read the same and leaves unique ones", () => {
    expect(
      distinctLabels(
        ["English · SRT", "French · SRT", "English · SRT", "English · Forced · SRT"],
        (n) => `Track ${n}`,
      ),
    ).toEqual([
      "English · SRT · Track 1",
      "French · SRT",
      "English · SRT · Track 2",
      "English · Forced · SRT",
    ]);
  });
});

describe("subtitle picker labels and order (CORE-2523)", () => {
  const muxed = (
    streamIndex: number,
    language: string,
    extra: Partial<PlaybackCaptionTrack> = {},
  ) =>
    ({
      id: `muxed-main-${streamIndex}`,
      format: "subrip",
      streamIndex,
      language,
      url: `/s/${streamIndex}.vtt`,
      ...extra,
    }) as PlaybackCaptionTrack;
  const numbered = (n: number) => `Track ${n}`;

  it("labels by language, region, script and role without format noise", () => {
    expect(
      captionTrackLabels(
        [
          muxed(2, "eng", { sdh: true }),
          muxed(3, "es-419"),
          muxed(4, "zh-Hant"),
          muxed(5, "eng", { forced: true }),
          muxed(6, "pt-BR", { title: "Commentary", commentary: true }),
        ],
        text,
        numbered,
      ),
    ).toEqual([
      "English · SDH",
      "Spanish (Latin America)",
      "Chinese (Traditional)",
      "English · Forced",
      "Portuguese (Brazil) · Commentary",
    ]);
  });

  it("adds format and source only where they tell tracks apart, then a number", () => {
    expect(
      captionTrackLabels(
        [
          muxed(2, "fre"),
          { id: "sidecar-main-0", format: "srt", language: "fr", url: "/c/0.vtt" },
          muxed(3, "eng"),
          muxed(4, "eng"),
        ],
        text,
        numbered,
      ),
    ).toEqual([
      "French · SRT",
      "French · SRT · External",
      "English · Track 1",
      "English · Track 2",
    ]);
  });

  it("puts preferred languages first, then the rest by name, forced before regular before SDH", () => {
    const tracks = [
      muxed(2, "fre"),
      muxed(3, "eng", { sdh: true }),
      muxed(4, ""),
      muxed(5, "spa", { sdh: true }),
      muxed(6, "eng"),
      muxed(7, "spa"),
      muxed(8, "eng", { forced: true }),
      muxed(9, "es-419"),
    ];
    const order = (preferred: string[]) =>
      orderCaptionTracks(tracks, preferred, "en").map((track) => track.streamIndex);
    expect(order(["es"])).toEqual([7, 5, 9, 8, 6, 3, 2, 4]);
    expect(order(["en"])).toEqual([8, 6, 3, 2, 7, 5, 9, 4]);
  });

  it("keeps a language's regional variants together under a regional preference", () => {
    const tracks = [
      muxed(2, "pt-PT"),
      muxed(3, "eng"),
      muxed(4, "pt"),
      muxed(5, "pt-BR"),
      muxed(6, "ara"),
    ];
    const order = (preferred: string[]) =>
      orderCaptionTracks(tracks, preferred, "en").map((track) => track.streamIndex);
    expect(order(["pt-BR", "en"])).toEqual([4, 5, 2, 3, 6]);
    expect(order(["en-US"])).toEqual([3, 6, 4, 5, 2]);
  });
});

describe("reloadCaptionElement (CORE-2315)", () => {
  it("replaces a failed track with a fresh one that loads the same source", () => {
    const video = document.createElement("video");
    attachCaptionElements(
      video,
      [{ id: "c1", format: "subrip", language: "en", url: "/api/media/subtitle/one" }],
      "en",
    );
    showCaptionElement(video, "c1");
    const failed = captionElement(video, "c1");
    reloadCaptionElement(video, "c1");
    const fresh = captionElement(video, "c1");
    expect(fresh).not.toBe(failed);
    expect(fresh?.getAttribute("src")).toBe("/api/media/subtitle/one");
    expect(video.querySelectorAll("track")).toHaveLength(1);
  });
});
