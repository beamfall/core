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

/**
 * The display name of a language tag, or "" when it has none. A region or
 * script follows the language ("Spanish (Latin America)", "Chinese
 * (Traditional)"), so regional tracks sort and group under their language.
 */
export function languageName(tag: string | undefined, locale: string): string {
  const canonical = canonicalLanguage(tag);
  if (!canonical || canonical === "und") return "";
  try {
    const names = new Intl.DisplayNames([locale], {
      type: "language",
      fallback: "code",
      languageDisplay: "standard",
    });
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

function channelLayout(channels: number | undefined, text: TrackLabelText): string {
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

// Words in a stream title that already name a role, so the label does not
// repeat it ("English (SDH)" needs no "· SDH").
const TITLE_ROLE_WORDS: Record<"forced" | "sdh" | "commentary" | "visualImpaired", RegExp> = {
  forced: /\bforced\b/i,
  sdh: /\b(sdh|cc|hearing impaired)\b/i,
  commentary: /\bcommentary\b/i,
  visualImpaired: /\b(described|audio description)\b/i,
};

/**
 * A caption track's label without the parts that only tell same-label tracks
 * apart: "English · SDH", "Spanish (Latin America)", "English · Forced". The
 * stream title replaces the language name when it names the language and adds
 * more ("English (SDH)"), and follows it when it names something else
 * ("English · Signs & Songs"); roles the title already names are not repeated.
 * A burned-in stream says so, after any format and source.
 */
function captionBaseLabel(
  track: PlaybackCaptionTrack,
  text: TrackLabelText,
  extra: string[] = [],
): string {
  const language = languageName(track.language, text.locale) || text.unknownLanguage;
  const title = track.title?.trim() ?? "";
  const titleLower = title.toLocaleLowerCase();
  const languageLower = language.toLocaleLowerCase();
  const named = (role: keyof typeof TITLE_ROLE_WORDS) => TITLE_ROLE_WORDS[role].test(title);
  let head: string[];
  if (!title || languageLower.includes(titleLower)) head = [language];
  else if (titleLower.includes(languageLower) || !languageName(track.language, text.locale)) {
    head = [title];
  } else head = [language, title];
  return joinLabel([
    ...head,
    track.forced && !named("forced") ? text.forced : "",
    track.sdh && !named("sdh") ? text.sdh : "",
    track.commentary && !named("commentary") ? text.commentary : "",
    track.visualImpaired && !named("visualImpaired") ? text.described : "",
    ...extra,
    isBurnInCaption(track) ? text.burnedIn : "",
  ]);
}

/** "German · Forced · SRT · External": the base label with the track's format and source. */
export function captionTrackLabel(track: PlaybackCaptionTrack, text: TrackLabelText): string {
  return captionBaseLabel(track, text, [
    codecName(track.format || ""),
    captionKind(track) === "sidecar" ? text.external : "",
  ]);
}

/**
 * The menu labels of a file's caption tracks (CORE-2523): each track's base
 * label, with its format and source added only where they tell tracks that
 * share a base label apart, and a number only where those still read the same.
 */
export function captionTrackLabels(
  tracks: readonly PlaybackCaptionTrack[],
  text: TrackLabelText,
  numbered: (n: number) => string,
): string[] {
  const base = tracks.map((track) => captionBaseLabel(track, text));
  const full = tracks.map((track) => captionTrackLabel(track, text));
  const variants = new Map<string, Set<string>>();
  base.forEach((label, index) => {
    const seen = variants.get(label) ?? new Set<string>();
    seen.add(full[index]);
    variants.set(label, seen);
  });
  return distinctLabels(
    base.map((label, index) => ((variants.get(label)?.size ?? 0) > 1 ? full[index] : label)),
    numbered,
  );
}

// Within one language: forced first, then regular, then SDH, then the
// commentary and described tracks.
function captionRoleRank(track: PlaybackCaptionTrack): number {
  if (track.forced) return 0;
  if (track.commentary) return 3;
  if (track.visualImpaired) return 4;
  if (track.sdh) return 2;
  return 1;
}

/**
 * A file's caption tracks in menu order (CORE-2523): tracks in the preferred
 * languages first, in preference order, then the rest by language name, then
 * untagged tracks. A language's tracks stay together whatever region a
 * preference names (a pt-BR preference still lists Portuguese (Portugal) with
 * the Portuguese tracks); within it the preferred variants come first, then
 * the plain language before regional variants, forced before regular before
 * SDH; ties keep file order.
 */
export function orderCaptionTracks(
  tracks: readonly PlaybackCaptionTrack[],
  preferred: readonly string[],
  locale: string,
): PlaybackCaptionTrack[] {
  const collator = new Intl.Collator([locale]);
  const primaryOf = (tag: string | undefined) => canonicalLanguage(tag).split("-")[0];
  const preferredPrimaries = preferred.map(primaryOf);
  const keys = tracks.map((track, index) => {
    const canonical = canonicalLanguage(track.language);
    const tagged = canonical !== "" && canonical !== "und";
    const primary = primaryOf(track.language);
    const rank = tagged ? preferredPrimaries.indexOf(primary) : -1;
    const variant = preferred.findIndex((want) => sameLanguage(track.language, want));
    return {
      track,
      index,
      tagged,
      rank: rank < 0 ? preferred.length : rank,
      variant: variant < 0 ? preferred.length : variant,
      group: tagged ? languageName(primary, locale) : "",
      name: tagged ? languageName(track.language, locale) : "",
      role: captionRoleRank(track),
    };
  });
  keys.sort(
    (a, b) =>
      Number(b.tagged) - Number(a.tagged) ||
      a.rank - b.rank ||
      collator.compare(a.group, b.group) ||
      a.variant - b.variant ||
      collator.compare(a.name, b.name) ||
      a.role - b.role ||
      a.index - b.index,
  );
  return keys.map((key) => key.track);
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
