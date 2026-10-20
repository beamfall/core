import type { PlaybackMarker } from "@/lib/graphql/types";
import type { PlaybackInfo, PlaybackNext, PlaybackRendition, PlaybackStream } from "./playback";

// Pure transport model helpers, ported faithfully from the vanilla
// internal/web/static/player-transport.js. These are the browser-free core of
// the custom transport (ADR-0007 transport spec): time formatting, the marker
// strip / skip-segment model, the trickplay tile lookup, resume position, and
// the Up Next trigger. The React player renders off these models.

const DEFAULT_TRICKPLAY_COLUMNS = 10;
const DEFAULT_TRICKPLAY_ROWS = 10;
const DEFAULT_TRICKPLAY_INTERVAL = 10;
const DEFAULT_TRICKPLAY_TILE_WIDTH = 320;
const DEFAULT_TRICKPLAY_TILE_HEIGHT = 180;

const SKIPPABLE_SEGMENT_KINDS = new Set([
  "intro",
  "recap",
  "credits",
  "commercial",
  "intermission",
]);

function finiteNumber(value: unknown, fallback = 0): number {
  const number = Number(value);
  return Number.isFinite(number) ? number : fallback;
}

// --- Trickplay sprite-sheet manifest (ADR-0033) ---------------------------

/** The trickplay sprite manifest shape the server serves (320w tiles, 10×10). */
export interface TrickplayManifest {
  intervalSeconds?: number;
  interval?: number;
  columns?: number;
  rows?: number;
  tileWidth?: number;
  tileHeight?: number;
  sheets?: string[];
}

export interface TrickplayTile {
  sheetIndex: number;
  sheet: string;
  index: number;
  cell: number;
  row: number;
  column: number;
  x: number;
  y: number;
  width: number;
  height: number;
}

/** Resolves the sprite tile for a scrub position; null when out of range. */
export function resolveTrickplayTile(
  manifest: TrickplayManifest | null,
  timeSeconds: number,
): TrickplayTile | null {
  if (!manifest) {
    return null;
  }
  const interval = Math.max(
    1,
    finiteNumber(manifest.intervalSeconds || manifest.interval, DEFAULT_TRICKPLAY_INTERVAL),
  );
  const columns = Math.max(1, finiteNumber(manifest.columns, DEFAULT_TRICKPLAY_COLUMNS));
  const rows = Math.max(1, finiteNumber(manifest.rows, DEFAULT_TRICKPLAY_ROWS));
  const cellsPerSheet = columns * rows;
  const tileWidth = Math.max(1, finiteNumber(manifest.tileWidth, DEFAULT_TRICKPLAY_TILE_WIDTH));
  const tileHeight = Math.max(1, finiteNumber(manifest.tileHeight, DEFAULT_TRICKPLAY_TILE_HEIGHT));
  const sheets = Array.isArray(manifest.sheets) ? manifest.sheets : [];
  const index = Math.max(0, Math.floor(finiteNumber(timeSeconds) / interval));
  const sheetIndex = Math.floor(index / cellsPerSheet);
  const sheet = sheets[sheetIndex];
  if (!sheet) {
    return null;
  }
  const cell = index % cellsPerSheet;
  const row = Math.floor(cell / columns);
  const column = cell % columns;
  return {
    sheetIndex,
    sheet,
    index,
    cell,
    row,
    column,
    x: column * tileWidth,
    y: row * tileHeight,
    width: tileWidth,
    height: tileHeight,
  };
}

// --- Time formatting ------------------------------------------------------

export function formatClockTime(seconds: number): string {
  const total = Math.max(0, Math.floor(finiteNumber(seconds)));
  const hours = Math.floor(total / 3600);
  const minutes = Math.floor((total % 3600) / 60);
  const remainingSeconds = total % 60;
  const two = (value: number) => String(value).padStart(2, "0");
  if (hours > 0) {
    return `${hours}:${two(minutes)}:${two(remainingSeconds)}`;
  }
  return `${minutes}:${two(remainingSeconds)}`;
}

export function formatRemainingTime(currentSeconds: number, durationSeconds: number): string {
  const remaining = Math.max(0, finiteNumber(durationSeconds) - finiteNumber(currentSeconds));
  return `-${formatClockTime(remaining)}`;
}

function spokenDuration(seconds: number): string {
  const total = Math.max(0, Math.floor(finiteNumber(seconds)));
  const hours = Math.floor(total / 3600);
  const minutes = Math.floor((total % 3600) / 60);
  const remainingSeconds = total % 60;
  const parts: string[] = [];
  if (hours) {
    parts.push(`${hours} ${hours === 1 ? "hour" : "hours"}`);
  }
  if (minutes) {
    parts.push(`${minutes} ${minutes === 1 ? "minute" : "minutes"}`);
  }
  if (remainingSeconds || !parts.length) {
    parts.push(`${remainingSeconds} ${remainingSeconds === 1 ? "second" : "seconds"}`);
  }
  return parts.join(" ");
}

/** The screen-reader time announcement for the scrubber slider (a11y gate). */
export function formatAriaTime(currentSeconds: number, durationSeconds: number): string {
  return `${spokenDuration(currentSeconds)} of ${spokenDuration(durationSeconds)}`;
}

// --- Marker strip + skip segment (ADR-0033 markers) -----------------------

export interface MarkerTick {
  title: string;
  segmentKind: string;
  startSeconds: number;
  leftPercent: number;
}

export interface MarkerRange {
  title: string;
  segmentKind: string;
  startSeconds: number;
  endSeconds: number;
  leftPercent: number;
  widthPercent: number;
  skippable: boolean;
}

export interface MarkerSkip {
  title: string;
  segmentKind: string;
  endSeconds: number;
}

export interface MarkerStripModel {
  ticks: MarkerTick[];
  ranges: MarkerRange[];
  skip: MarkerSkip | null;
}

function markerTitle(marker: PlaybackMarker): string {
  return marker.title || marker.segmentKind || "";
}

function markerPercent(seconds: number, durationSeconds: number): number {
  if (!durationSeconds) {
    return 0;
  }
  return Math.max(0, Math.min(100, (seconds / durationSeconds) * 100));
}

export function buildMarkerStripModel(
  markers: PlaybackMarker[],
  options: { durationSeconds?: number; currentTimeSeconds?: number } = {},
): MarkerStripModel {
  const durationSeconds = Math.max(0, finiteNumber(options.durationSeconds));
  const currentTimeSeconds = Math.max(0, finiteNumber(options.currentTimeSeconds));
  const ticks: MarkerTick[] = [];
  const ranges: MarkerRange[] = [];
  let skip: MarkerSkip | null = null;
  for (const marker of markers || []) {
    const startSeconds = finiteNumber(marker.startSeconds);
    const endSeconds =
      marker.endSeconds === undefined || marker.endSeconds === null
        ? 0
        : finiteNumber(marker.endSeconds);
    const segmentKind = marker.segmentKind || "";
    const title = markerTitle(marker);
    if (endSeconds > startSeconds) {
      const skippable = SKIPPABLE_SEGMENT_KINDS.has(segmentKind);
      ranges.push({
        title,
        segmentKind,
        startSeconds,
        endSeconds,
        leftPercent: markerPercent(startSeconds, durationSeconds),
        widthPercent:
          markerPercent(endSeconds, durationSeconds) - markerPercent(startSeconds, durationSeconds),
        skippable,
      });
      if (skippable && currentTimeSeconds >= startSeconds && currentTimeSeconds < endSeconds) {
        skip = { title, segmentKind, endSeconds };
      }
    } else {
      ticks.push({
        title,
        segmentKind,
        startSeconds,
        leftPercent: markerPercent(startSeconds, durationSeconds),
      });
    }
  }
  return { ticks, ranges, skip };
}

// --- Auto-skip (ADR-0048 Decision 6) --------------------------------------

/** The three segment kinds the per-profile autoskip.* prefs cover. */
const AUTO_SKIP_SEGMENT_KINDS = new Set(["intro", "recap", "credits"]);

export interface AutoSkipPrefs {
  intro: boolean;
  recap: boolean;
  credits: boolean;
}

export interface AutoSkipSegment {
  segmentKind: string;
  startSeconds: number;
  endSeconds: number;
  title: string;
}

function autoSkipEnabled(segmentKind: string, prefs: AutoSkipPrefs): boolean {
  if (segmentKind === "intro") return prefs.intro;
  if (segmentKind === "recap") return prefs.recap;
  if (segmentKind === "credits") return prefs.credits;
  return false;
}

/**
 * The skippable segment (if any) the playhead currently sits inside, gated by
 * the per-profile autoskip.intro/recap/credits prefs. Only "intro", "recap",
 * and "credits" markers are eligible — "commercial"/"intermission" segments
 * have no autoskip preference and stay manual-skip-only. `endSeconds` is
 * always the marker's own segment end (e.g. the true credits end), never the
 * media duration/EOF.
 */
export function autoSkipSegmentAt(
  markers: PlaybackMarker[],
  currentTimeSeconds: number,
  prefs: AutoSkipPrefs,
): AutoSkipSegment | null {
  const current = finiteNumber(currentTimeSeconds);
  for (const marker of markers || []) {
    const segmentKind = marker.segmentKind || "";
    if (!AUTO_SKIP_SEGMENT_KINDS.has(segmentKind) || !autoSkipEnabled(segmentKind, prefs)) {
      continue;
    }
    const startSeconds = finiteNumber(marker.startSeconds);
    const endSeconds =
      marker.endSeconds === undefined || marker.endSeconds === null
        ? 0
        : finiteNumber(marker.endSeconds);
    if (endSeconds <= startSeconds) continue;
    if (current >= startSeconds && current < endSeconds) {
      return { segmentKind, startSeconds, endSeconds, title: markerTitle(marker) };
    }
  }
  return null;
}

/** A stable identity for a segment instance, keyed by kind + start (ADR-0048
 * Decision 6's one-shot re-fire guard: Undo seeking back into the segment must
 * not immediately re-trigger the same skip). */
export function autoSkipSegmentKey(segment: AutoSkipSegment): string {
  return `${segment.segmentKind}:${segment.startSeconds}`;
}

export function chapterStartTimes(markers: PlaybackMarker[]): number[] {
  return Array.from(
    new Set(
      (markers || [])
        .map((marker) => finiteNumber(marker.startSeconds, -1))
        .filter((seconds) => seconds >= 0),
    ),
  ).sort((a, b) => a - b);
}

export function chapterSeekTarget(
  markers: PlaybackMarker[],
  currentTimeSeconds: number,
  direction: -1 | 1,
): number | null {
  const current = finiteNumber(currentTimeSeconds);
  const starts = chapterStartTimes(markers);
  if (direction < 0) {
    for (let i = starts.length - 1; i >= 0; i -= 1) {
      if (starts[i] < current - 1) return starts[i];
    }
    return null;
  }
  return starts.find((seconds) => seconds > current + 1) ?? null;
}

// --- Up Next + resume -----------------------------------------------------

export function shouldShowUpNext(args: {
  next: PlaybackNext | null | undefined;
  duration: number;
  currentTime: number;
  ended?: boolean;
}): boolean {
  const { next, duration, currentTime, ended = false } = args;
  if (!next) {
    return false;
  }
  if (ended) {
    return true;
  }
  const durationSeconds = finiteNumber(duration);
  if (durationSeconds <= 0) {
    return false;
  }
  return durationSeconds - finiteNumber(currentTime) <= 15;
}

/**
 * The canonical resume position the player seeks to on loadedmetadata (ADR-0010
 * timeline): the playback-info timeline's positionSeconds, or 0 when there is no
 * saved position. Non-finite/negative values resolve to 0 (never seek backward
 * past the start).
 */
export function resumePositionFromInfo(info: PlaybackInfo): number {
  const position = info && info.timeline ? info.timeline.positionSeconds : 0;
  const seconds = finiteNumber(position, 0);
  return seconds > 0 ? seconds : 0;
}

export function playbackDecision(
  info: PlaybackInfo | null,
  stream: PlaybackStreamLike | null,
): string {
  if (info && info.decision) {
    return info.decision;
  }
  return stream && stream.kind ? stream.kind : "";
}

interface PlaybackStreamLike {
  kind?: string;
}

// --- Track menus (audio / subtitle) ---------------------------------------

/** One row in an audio/subtitle/quality menu (ADR-0010 multi-track selection). */
export interface TrackMenuOption {
  label: string;
  selected: boolean;
  disabled: boolean;
  reason: string;
}

/** Plain track data the menu model consumes (no DOM / hls.js objects). */
export interface TrackSource {
  label: string;
  selected: boolean;
}

export interface TrackMenuSources {
  hlsAudio?: TrackSource[];
  nativeAudio?: TrackSource[];
  hlsSubtitle?: TrackSource[];
  nativeSubtitle?: TrackSource[];
}

export interface TrackMenuLabels {
  audio?: string;
  subtitles?: string;
  subtitlesOff?: string;
}

// pickTrackSource implements the hls.js-over-native precedence: when the hls.js
// track set is non-empty it wins, otherwise the native set is used.
function pickTrackSource(
  hls: TrackSource[] | undefined,
  native: TrackSource[] | undefined,
): TrackSource[] {
  const hlsTracks = Array.isArray(hls) ? hls : [];
  if (hlsTracks.length) {
    return hlsTracks;
  }
  return Array.isArray(native) ? native : [];
}

/**
 * Turns the available tracks into the audio/subtitle menu option list the player
 * renders (ADR-0010), ported from buildTrackMenuModel (player-transport.js). The
 * hls.js track set takes precedence over the native set of the same kind; each
 * option carries its display label and whether it is the current selection. The
 * subtitle menu always leads with an "Off" entry, selected when no track shows.
 */
export function buildTrackMenuModel(
  kind: "audio" | "subtitle",
  sources: TrackMenuSources = {},
  labels: TrackMenuLabels = {},
): TrackMenuOption[] {
  const audioLabel = labels.audio || "Audio";
  const subtitleLabel = labels.subtitles || "Subtitles";
  const offLabel = labels.subtitlesOff || "Off";

  const option = (label: string, selected: boolean): TrackMenuOption => ({
    label,
    selected: !!selected,
    disabled: false,
    reason: "",
  });

  if (kind === "audio") {
    const tracks = pickTrackSource(sources.hlsAudio, sources.nativeAudio);
    return tracks.map((track, index) =>
      option(track.label || `${audioLabel} ${index + 1}`, track.selected),
    );
  }

  const tracks = pickTrackSource(sources.hlsSubtitle, sources.nativeSubtitle);
  const options = tracks.map((track, index) =>
    option(track.label || `${subtitleLabel} ${index + 1}`, track.selected),
  );
  // The "Off" choice leads the subtitle menu and is selected only when no real
  // track is currently showing (subtitles are off by default).
  return [option(offLabel, !options.some((entry) => entry.selected)), ...options];
}

// --- Quality menu + route detail (honest ABR, ADR-0010) -------------------

function renditionLabel(rendition: PlaybackRendition | undefined): string {
  if (rendition && rendition.height) {
    return `${rendition.height}p`;
  }
  if (rendition && rendition.bandwidth) {
    return `${Math.round(rendition.bandwidth / 1_000_000)} Mbps`;
  }
  return "Unknown";
}

function formatBitrate(bandwidth: number | undefined): string {
  const mbps = finiteNumber(bandwidth) / 1_000_000;
  if (mbps >= 10) {
    return `${Math.round(mbps)} Mbps`;
  }
  return `${mbps.toFixed(1)} Mbps`;
}

export interface RenditionMenuOption {
  id: string;
  label: string;
  selected: boolean;
  disabled: boolean;
  reason: string;
}

export interface RenditionMenuModel {
  manualPinActive: boolean;
  options: RenditionMenuOption[];
}

export interface RenditionMenuLabels {
  auto?: string;
  directPlay?: string;
}

/**
 * The quality menu model (ADR-0010 honest ABR), ported from
 * buildRenditionMenuModel (player-transport.js): a single "Direct play" row when
 * the stream has no renditions; otherwise a leading "Auto" plus one row per
 * rendition, with the manually-pinned rendition (if any) marked selected.
 */
export function buildRenditionMenuModel({
  stream,
  manualRenditionID = "",
  labels = {},
}: {
  stream: PlaybackStream | null;
  manualRenditionID?: string;
  labels?: RenditionMenuLabels;
}): RenditionMenuModel {
  const renditions = stream && Array.isArray(stream.renditions) ? stream.renditions : [];
  if (!renditions.length) {
    return {
      manualPinActive: false,
      options: [
        {
          id: "direct",
          label: labels.directPlay || "Direct play",
          selected: true,
          disabled: false,
          reason: "",
        },
      ],
    };
  }
  return {
    manualPinActive: !!manualRenditionID,
    options: [
      {
        id: "auto",
        label: labels.auto || "Auto",
        selected: !manualRenditionID,
        disabled: false,
        reason: "",
      },
      ...renditions.map((rendition) => ({
        id: String(rendition.id || rendition.height || rendition.bandwidth),
        label: renditionLabel(rendition),
        selected: String(rendition.id) === String(manualRenditionID),
        disabled: false,
        reason: "",
      })),
    ],
  };
}

export interface RouteDetailRow {
  /** A strings.js key (resolved by the caller). */
  labelKey: string;
  value: string;
}

export interface RouteDetailLabels {
  directPlay?: string;
  directStream?: string;
  serverTranscode?: string;
  auto?: string;
  upTo?: string;
  engineNative?: string;
  engineNativeHls?: string;
  engineHlsJs?: string;
  reasonRequiresTranscode?: string;
}

function reasonLabel(reason: string, labels: RouteDetailLabels): string {
  if (reason === "requires-transcode") {
    return labels.reasonRequiresTranscode || "This device cannot direct play the source.";
  }
  // Unknown machine token: render it verbatim rather than inventing copy.
  return reason;
}

/**
 * The ordered rows for the info drill-in overlay, ported from
 * buildRouteDetailModel (player-transport.js): route, engine (the actual
 * playback path the browser took), quality/bitrate, and the transcode reason
 * when the server attached one. Codec/container/channel-layout are not on the
 * web playback wire, so they are intentionally absent rather than faked.
 */
export function buildRouteDetailModel({
  stream,
  info,
  manualRenditionID = "",
  hlsActive = false,
  labels = {},
}: {
  stream: PlaybackStream | null;
  info: PlaybackInfo | null;
  manualRenditionID?: string;
  hlsActive?: boolean;
  labels?: RouteDetailLabels;
}): RouteDetailRow[] {
  const rows: RouteDetailRow[] = [];
  const decision = (info && info.decision) || (stream && stream.kind) || "";
  // Route.
  let routeLabel = labels.directPlay || "Direct play";
  if (decision === "transcode") {
    routeLabel = labels.serverTranscode || "Server transcode";
  } else if (decision === "direct-stream") {
    routeLabel = labels.directStream || "Direct stream";
  }
  rows.push({ labelKey: "infoRoute", value: routeLabel });
  // Engine: the actual playback path the browser took.
  rows.push({
    labelKey: "infoEngine",
    value:
      stream?.kind === "hls" || stream?.kind === "transcode"
        ? hlsActive
          ? labels.engineHlsJs || "hls.js"
          : labels.engineNativeHls || "Native HLS"
        : labels.engineNative || "Native player",
  });
  // Quality: the active rendition for transcodes; honest direct otherwise.
  const renditions = stream && Array.isArray(stream.renditions) ? stream.renditions : [];
  if (renditions.length) {
    const pinned = renditions.find(
      (rendition) => String(rendition.id) === String(manualRenditionID),
    );
    if (pinned) {
      rows.push({ labelKey: "infoQuality", value: renditionLabel(pinned) });
      if (pinned.bandwidth) {
        rows.push({
          labelKey: "infoBitrate",
          value: formatBitrate(pinned.bandwidth),
        });
      }
    } else {
      rows.push({ labelKey: "infoQuality", value: labels.auto || "Auto" });
      const ceiling = renditions[renditions.length - 1];
      if (ceiling && ceiling.bandwidth) {
        rows.push({
          labelKey: "infoBitrate",
          value: `${labels.upTo || "up to"} ${formatBitrate(ceiling.bandwidth)}`,
        });
      }
    }
  } else {
    rows.push({
      labelKey: "infoQuality",
      value: labels.directPlay || "Direct play",
    });
  }
  // Reason: only present when the server attached one (transcode rationale).
  const reason = info && info.reason;
  if (reason) {
    rows.push({ labelKey: "infoReason", value: reasonLabel(reason, labels) });
  }
  return rows;
}
