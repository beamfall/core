import type {
  PlaybackAudioTrack,
  PlaybackCaptionTrack,
  PlaybackSelectedSubtitle,
} from "../lib/playback";

// The audio and caption tracks playbackInfo lists for the negotiated file
// (CORE-1709). The player builds its menus from these rather than from the
// live hls.js / <video> track lists, which hold only what one route carries:
// a server stream maps a single audio track and no subtitles at all.

/** The attribute that marks the <track> elements the player adds for caption tracks. */
export const CAPTION_TRACK_ATTRIBUTE = "data-bf-caption";
/** Holds a caption <track>'s WebVTT URL until the track is first shown. */
const CAPTION_SOURCE_ATTRIBUTE = "data-bf-caption-src";

/** A muxed picture-based subtitle stream: shown only by burning it into the video. */
export function isBurnInCaption(track: PlaybackCaptionTrack): boolean {
  return !track.url && typeof track.streamIndex === "number";
}

/** The provenance kind the server's subtitle pick names: "muxed" or "sidecar". */
export function captionKind(track: PlaybackCaptionTrack): "muxed" | "sidecar" {
  return typeof track.streamIndex === "number" ? "muxed" : "sidecar";
}

/** A language tag in canonical form ("eng" and "en-US" become "en", "en-us"). */
export function canonicalLanguage(tag: string | undefined): string {
  const trimmed = (tag ?? "").trim();
  if (!trimmed) return "";
  try {
    return (Intl.getCanonicalLocales(trimmed)[0] ?? trimmed).toLowerCase();
  } catch {
    return trimmed.toLowerCase();
  }
}

/**
 * Whether two language tags name the same language: case-insensitive, three-
 * and two-letter codes alike, and a region-qualified tag matches its base.
 */
export function sameLanguage(a: string | undefined, b: string | undefined): boolean {
  const left = canonicalLanguage(a);
  const right = canonicalLanguage(b);
  if (!left || !right) return left === right;
  return left === right || left.startsWith(`${right}-`) || right.startsWith(`${left}-`);
}

/** The Profile's track policy as playbackInfo forwards it (CORE-1680). */
export interface TrackPreferencePolicy {
  audioLanguages: string[];
  subtitleLanguages: string[];
  /** "off" | "smart" | "forced-only" | "always"; "" when unstated. */
  subtitleMode: string;
  preferSDH: boolean;
}

/** Reads playbackInfo.trackPreferences; null when it is absent or not an object. */
export function trackPreferencePolicy(raw: unknown): TrackPreferencePolicy | null {
  if (!raw || typeof raw !== "object") return null;
  const fields = raw as Record<string, unknown>;
  const languages = (value: unknown) =>
    Array.isArray(value)
      ? value.filter((tag): tag is string => typeof tag === "string" && tag.trim() !== "")
      : [];
  return {
    audioLanguages: languages(fields.audioLanguages),
    subtitleLanguages: languages(fields.subtitleLanguages),
    subtitleMode: typeof fields.subtitleMode === "string" ? fields.subtitleMode : "",
    preferSDH: fields.preferSDH === true,
  };
}

/** The index of the first track in the most preferred language, or -1 when none matches. */
export function preferredLanguageIndex(
  languages: ReadonlyArray<string | undefined>,
  preferred: readonly string[],
): number {
  for (const want of preferred) {
    const index = languages.findIndex((language) => sameLanguage(language, want));
    if (index >= 0) return index;
  }
  return -1;
}

/**
 * The caption track the server's start-of-playback pick names: same kind,
 * language and forced flag; among those the one whose other roles match, and
 * a text track before a burn-in one.
 */
export function matchSelectedCaption(
  tracks: readonly PlaybackCaptionTrack[] | undefined,
  selected: PlaybackSelectedSubtitle | undefined,
): PlaybackCaptionTrack | undefined {
  if (!tracks?.length || !selected?.track) return undefined;
  const { kind, language } = selected.track;
  const roles = selected.track.roles ?? {};
  const score = (track: PlaybackCaptionTrack) =>
    (!!track.sdh === !!roles.sdh ? 4 : 0) +
    (!!track.commentary === !!roles.commentary && !!track.visualImpaired === !!roles.visualImpaired
      ? 2
      : 0) +
    (isBurnInCaption(track) ? 0 : 1);
  let best: PlaybackCaptionTrack | undefined;
  for (const track of tracks) {
    if (captionKind(track) !== kind || !sameLanguage(track.language, language)) continue;
    if (!!track.forced !== !!roles.forced) continue;
    if (!best || score(track) > score(best)) best = track;
  }
  return best;
}

/** The display name of a language tag, or "" when it has none. */
export function languageName(tag: string | undefined, locale: string): string {
  const canonical = canonicalLanguage(tag);
  if (!canonical || canonical === "und") return "";
  try {
    const names = new Intl.DisplayNames([locale], { type: "language", fallback: "code" });
    return names.of(canonical) ?? tag ?? "";
  } catch {
    return tag ?? "";
  }
}

/** The menu wording for track labels, from the i18n catalog. */
export interface TrackLabelText {
  locale: string;
  unknownLanguage: string;
  mono: string;
  stereo: string;
  channels: (count: number) => string;
  commentary: string;
  described: string;
  forced: string;
  sdh: string;
  external: string;
  burnedIn: string;
}

const CODEC_NAMES: Record<string, string> = {
  aac: "AAC",
  ac3: "Dolby Digital",
  eac3: "Dolby Digital Plus",
  truehd: "Dolby TrueHD",
  dts: "DTS",
  flac: "FLAC",
  opus: "Opus",
  vorbis: "Vorbis",
  mp3: "MP3",
  alac: "ALAC",
  subrip: "SRT",
  srt: "SRT",
  ass: "ASS",
  ssa: "SSA",
  webvtt: "WebVTT",
  vtt: "WebVTT",
  mov_text: "Text",
  text: "Text",
  dvd_subtitle: "DVD",
  hdmv_pgs_subtitle: "PGS",
  dvb_subtitle: "DVB",
};

export function codecName(codec: string): string {
  const key = codec.toLowerCase();
  if (key.startsWith("pcm_")) return "PCM";
  return CODEC_NAMES[key] ?? codec.toUpperCase();
}

const HDR_NAMES: Record<string, string> = {
  hdr10: "HDR10",
  hdr10plus: "HDR10+",
  hlg: "HLG",
};

/** The display name of a Version's HDR token ("hdr10", "dolby-vision-p8"); unknown tokens show as sent. */
export function hdrName(token: string): string {
  const key = token.toLowerCase();
  if (key.startsWith("dolby-vision")) return "Dolby Vision";
  return HDR_NAMES[key] ?? token;
}

/** "Stereo", "5.1": a track's channel count as a viewer reads it; "" when unknown. */
export function channelLayout(
  channels: number | undefined,
  text: Pick<TrackLabelText, "mono" | "stereo" | "channels">,
): string {
  if (!channels) return "";
  if (channels === 1) return text.mono;
  if (channels === 2) return text.stereo;
  if (channels === 6) return "5.1";
  if (channels === 8) return "7.1";
  return text.channels(channels);
}

function joinLabel(parts: string[]): string {
  return parts.filter(Boolean).join(" · ");
}

/** "French · Dolby Digital · 5.1", with the track's roles after. */
export function audioTrackLabel(track: PlaybackAudioTrack, text: TrackLabelText): string {
  return joinLabel([
    languageName(track.language, text.locale) || text.unknownLanguage,
    codecName(track.codec || ""),
    channelLayout(track.channels, text),
    track.commentary ? text.commentary : "",
    track.visualImpaired ? text.described : "",
  ]);
}

/** "German · Forced · SRT · External"; a burned-in stream says so. */
export function captionTrackLabel(track: PlaybackCaptionTrack, text: TrackLabelText): string {
  const language = languageName(track.language, text.locale) || text.unknownLanguage;
  const title = track.title?.trim() ?? "";
  return joinLabel([
    language,
    // The stream's own title ("Signs & Songs") tells same-language tracks apart.
    title.toLocaleLowerCase() === language.toLocaleLowerCase() ? "" : title,
    track.forced ? text.forced : "",
    track.sdh ? text.sdh : "",
    track.commentary ? text.commentary : "",
    track.visualImpaired ? text.described : "",
    codecName(track.format || ""),
    captionKind(track) === "sidecar" ? text.external : "",
    isBurnInCaption(track) ? text.burnedIn : "",
  ]);
}

/**
 * distinctLabels numbers labels that would otherwise read the same, so two
 * "English · SRT" tracks with no role or title to tell them apart become
 * "English · SRT · Track 1" and "… · Track 2" (CORE-2315). Unique labels are
 * left as they are.
 */
export function distinctLabels(
  labels: readonly string[],
  numbered: (n: number) => string,
): string[] {
  const total = new Map<string, number>();
  for (const label of labels) total.set(label, (total.get(label) ?? 0) + 1);
  const seen = new Map<string, number>();
  return labels.map((label) => {
    if ((total.get(label) ?? 0) < 2) return label;
    const n = (seen.get(label) ?? 0) + 1;
    seen.set(label, n);
    return joinLabel([label, numbered(n)]);
  });
}

/** The label a <track> element carries, for the browser's own caption menu. */
export function captionElementLabel(track: PlaybackCaptionTrack, locale: string): string {
  return joinLabel([
    languageName(track.language, locale) || track.language || "",
    codecName(track.format || ""),
  ]);
}

function captionElements(video: HTMLVideoElement): HTMLTrackElement[] {
  return Array.from(video.querySelectorAll<HTMLTrackElement>(`track[${CAPTION_TRACK_ATTRIBUTE}]`));
}

/** Removes the <track> elements attachCaptionElements added. */
export function removeCaptionElements(video: HTMLVideoElement): void {
  for (const element of captionElements(video)) element.remove();
}

/**
 * Adds one <track> per caption track that has a WebVTT URL (muxed text and
 * sidecar alike), all disabled. They play on every route: direct play, the
 * audio-copy direct stream and the HLS transcode, none of which carry the
 * file's text subtitles in the stream itself.
 *
 * A track gets its src only when showCaptionElement first shows it. The server
 * converts a muxed stream on first request, which reads the whole file, so
 * loading every track up front holds one connection each for that long and can
 * leave none for the stream itself over HTTP/1.1.
 */
export function attachCaptionElements(
  video: HTMLVideoElement,
  tracks: PlaybackCaptionTrack[] | undefined,
  locale: string,
): void {
  removeCaptionElements(video);
  for (const track of tracks ?? []) {
    if (!track.url) continue;
    const element = document.createElement("track");
    element.kind = "subtitles";
    element.label = captionElementLabel(track, locale);
    if (track.language && track.language !== "und") element.srclang = track.language;
    element.setAttribute(CAPTION_SOURCE_ATTRIBUTE, track.url);
    element.setAttribute(CAPTION_TRACK_ATTRIBUTE, track.id);
    video.appendChild(element);
    if (element.track) element.track.mode = "disabled";
  }
}

/**
 * CAPTION_SHOWN_EVENT fires on the video each time showCaptionElement picks a
 * track (detail: its id, or null), so a load watcher can time that track's
 * fetch without polling.
 */
export const CAPTION_SHOWN_EVENT = "bf-caption-shown";

/** Shows the caption track with this id and hides the rest; null hides them all. */
export function showCaptionElement(video: HTMLVideoElement, id: string | null): void {
  for (const element of captionElements(video)) {
    const shown = element.getAttribute(CAPTION_TRACK_ATTRIBUTE) === id;
    const source = element.getAttribute(CAPTION_SOURCE_ATTRIBUTE);
    if (shown && source && !element.getAttribute("src")) element.src = source;
    if (!element.track) continue;
    element.track.mode = shown ? "showing" : "disabled";
  }
  video.dispatchEvent(new CustomEvent<string | null>(CAPTION_SHOWN_EVENT, { detail: id }));
}

/** The <track> element for a caption id, if one is attached. */
export function captionElement(video: HTMLVideoElement, id: string): HTMLTrackElement | null {
  return (
    captionElements(video).find(
      (element) => element.getAttribute(CAPTION_TRACK_ATTRIBUTE) === id,
    ) ?? null
  );
}

/**
 * reloadCaptionElement fetches a caption track again after a failed load. A
 * <track> never refetches once its load has failed, so it is replaced by a
 * fresh element with the same source, then shown.
 */
export function reloadCaptionElement(video: HTMLVideoElement, id: string): void {
  const failed = captionElement(video, id);
  if (!failed) return;
  const fresh = document.createElement("track");
  fresh.kind = failed.kind;
  fresh.label = failed.label;
  if (failed.srclang) fresh.srclang = failed.srclang;
  for (const name of [CAPTION_SOURCE_ATTRIBUTE, CAPTION_TRACK_ATTRIBUTE]) {
    const value = failed.getAttribute(name);
    if (value !== null) fresh.setAttribute(name, value);
  }
  failed.replaceWith(fresh);
  if (fresh.track) fresh.track.mode = "disabled";
  showCaptionElement(video, id);
}

/** The id of the caption track showing now, or null. */
export function shownCaptionElement(video: HTMLVideoElement): string | null {
  for (const element of captionElements(video)) {
    if (element.track?.mode === "showing") return element.getAttribute(CAPTION_TRACK_ATTRIBUTE);
  }
  return null;
}

/** The locale track labels are written in: the page's own language. */
export function captionLocale(): string {
  if (typeof document !== "undefined" && document.documentElement.lang) {
    return document.documentElement.lang;
  }
  return typeof navigator !== "undefined" && navigator.language ? navigator.language : "en";
}
