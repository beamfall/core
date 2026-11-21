// Canonical per-Profile client preference keys and their value conventions
// (ADR-0082 viewer-resident self-service prefs over the generic
// profile_client_prefs seam, ADR-0014). This is the single source of truth for
// the key namespace so the Profile Settings page (writer) and any future client
// consumer (e.g. the web player reading auto-skip) agree byte-for-byte with the
// server conventions in graph_autoskip.go / graph_enrichment.go.
//
// Boolean values follow the server's existing convention (graph_autoskip.go):
// the string "on" means true; an absent key or any other value means false.
// Storing an empty value clears the key server-side (graph.setClientPref).
//
// ADR-0193 Decision 2/5 adds the fourth auto-skip key, autoskip.ad, for the `ad`
// skip-segment kind. It is keyed on the KIND ALONE, exactly like the other three:
// detection flavor (inserted / host-read) never reaches a client, so one `ad`
// kind means one `autoskip.ad` boolean.
//
// RESERVED, NOT IMPLEMENTED (ADR-0193 Decision 2): `sponsor` is a non-binding
// placeholder, not a live segment kind, so there is deliberately no
// autoskip.sponsor key here and no branch reading one. Its ANTICIPATED
// initialization — autoskip.sponsor resolving to the autoskip.ad value by
// READ-TIME FALLBACK when the key is absent, never a backfill write — is a
// recorded contract only, matching graph_autoskip.go. It is not code on either
// side of the seam.

import type { ClientPref } from "@/lib/graphql/types";
import { THEME_PREF_KEYS } from "@/lib/theme/runtime";

/** Canonical clientPref keys. Mirror the server's namespaced keys exactly. */
export const PREF_KEYS = {
  autoSkipIntro: "autoskip.intro",
  autoSkipRecap: "autoskip.recap",
  autoSkipCredits: "autoskip.credits",
  autoSkipAd: "autoskip.ad",
  recommendOptIn: "recommend.opt-in",
  playbackSpeedDefault: "playback.speed-default",
  trimSilence: "playback.trim-silence",
  skipInterval: "playback.skip-interval",
  downstreamAudio: "playback.downstream-audio",
  reduceMotion: "a11y.reduce-motion",
  // Pre-play queue opt-in and suggested-item count (CORE-1701, graph_preplay.go).
  prePlayEnabled: "preplay.enabled",
  prePlayExtras: "preplay.extras",
  // Subtitle appearance (CORE-1681); values and defaults live in subtitleAppearance.ts.
  subtitleSize: "subtitle.size",
  subtitleBackground: "subtitle.background",
  subtitleFont: "subtitle.font",
  subtitleColor: "subtitle.color",
  subtitleOpacity: "subtitle.opacity",
  subtitleEdge: "subtitle.edge",
  subtitlePosition: "subtitle.position",
  stillWatchingAfter: "stillwatching.after",
  // Hide finished Media from catalog shelves and library browse (CORE-2836, profile_hide_watched.go).
  hideWatched: "profile.hide-watched",
  // Theme preset and reserved accent (CORE-1179, ADR-0231 D10); the scheme stays on the device.
  themePreset: THEME_PREF_KEYS.preset,
  themeAccent: THEME_PREF_KEYS.accent,
} as const;

export type PrefKey = (typeof PREF_KEYS)[keyof typeof PREF_KEYS];

/** The "on"/absent boolean convention shared with graph_autoskip.go. */
export const PREF_ON = "on";

/** Allowed playback-speed default options (advisory, client-owned). */
export const PLAYBACK_SPEEDS = ["1", "1.25", "1.5", "1.75", "2"] as const;
export type PlaybackSpeed = (typeof PLAYBACK_SPEEDS)[number];
export const PLAYBACK_SPEED_DEFAULT: PlaybackSpeed = "1";
export const SKIP_INTERVALS = ["10", "15", "30", "45", "60"] as const;
export type SkipInterval = (typeof SKIP_INTERVALS)[number];
export const SKIP_INTERVAL_DEFAULT: SkipInterval = "10";
export const DOWNSTREAM_AUDIO_MODES = ["stereo", "surround"] as const;
export type DownstreamAudioMode = (typeof DOWNSTREAM_AUDIO_MODES)[number];
export const DOWNSTREAM_AUDIO_DEFAULT: DownstreamAudioMode = "stereo";
/** Suggested pre-play item counts; the server clamps any stored value to 0..10. */
export const PRE_PLAY_EXTRAS = ["0", "1", "2", "3", "4", "5"] as const;
export type PrePlayExtras = (typeof PRE_PLAY_EXTRAS)[number];
export const PRE_PLAY_EXTRAS_DEFAULT: PrePlayExtras = "2";

// "Are you still watching?" (CORE-1689, docs/still-watching/README.md): ask
// after this many items in a row started by the Up Next countdown. The stored
// value is "1".."10" or "off"; absent or malformed means the default. Every
// client parses the same key the same way.
export const STILL_WATCHING_OFF = "off";
export const STILL_WATCHING_DEFAULT = 3;
export const STILL_WATCHING_MAX = 10;
/** The choices the settings page offers; any stored 1..10 is still honoured. */
export const STILL_WATCHING_OPTIONS = ["off", "1", "2", "3", "4", "5"] as const;

/** The resolved, typed self-service preference set for one Profile. */
export interface ViewerPreferences {
  autoSkipIntro: boolean;
  autoSkipRecap: boolean;
  autoSkipCredits: boolean;
  autoSkipAd: boolean;
  recommendOptIn: boolean;
  reduceMotion: boolean;
  /** Leave finished Media out of catalog shelves and library browse (CORE-2836). */
  hideWatched: boolean;
  playbackSpeed: PlaybackSpeed;
  trimSilence: boolean;
  skipInterval: SkipInterval;
  downstreamAudio: DownstreamAudioMode;
  /** Autoplayed items in a row before the prompt; 0 means never ask. */
  stillWatchingAfter: number;
  prePlayEnabled: boolean;
  prePlayExtras: PrePlayExtras;
}

function bool(map: Map<string, string>, key: string): boolean {
  return map.get(key) === PREF_ON;
}

function downstreamAudio(map: Map<string, string>): DownstreamAudioMode {
  const raw = map.get(PREF_KEYS.downstreamAudio);
  return (DOWNSTREAM_AUDIO_MODES as readonly string[]).includes(raw ?? "")
    ? (raw as DownstreamAudioMode)
    : DOWNSTREAM_AUDIO_DEFAULT;
}

/** Parse the stillwatching.after value: 0 for "off", 1..10, else the default. */
export function parseStillWatchingAfter(raw: string | undefined): number {
  if (raw === STILL_WATCHING_OFF) return 0;
  // Exactly "1".."10": no whitespace, sign or leading zero, as every client parses it.
  if (raw === undefined || !/^(10|[1-9])$/.test(raw)) return STILL_WATCHING_DEFAULT;
  return Number(raw);
}

function prePlayExtras(map: Map<string, string>): PrePlayExtras {
  const raw = map.get(PREF_KEYS.prePlayExtras)?.trim();
  return (PRE_PLAY_EXTRAS as readonly string[]).includes(raw ?? "")
    ? (raw as PrePlayExtras)
    : PRE_PLAY_EXTRAS_DEFAULT;
}

function speed(map: Map<string, string>): PlaybackSpeed {
  const raw = map.get(PREF_KEYS.playbackSpeedDefault);
  return (PLAYBACK_SPEEDS as readonly string[]).includes(raw ?? "")
    ? (raw as PlaybackSpeed)
    : PLAYBACK_SPEED_DEFAULT;
}

function interval(
  map: Map<string, string>,
  key: string,
  fallback = SKIP_INTERVAL_DEFAULT,
): SkipInterval {
  const raw = map.get(key);
  return (SKIP_INTERVALS as readonly string[]).includes(raw ?? "")
    ? (raw as SkipInterval)
    : fallback;
}

export function skipIntervalOverrideKey(showID: string): string {
  return `${PREF_KEYS.skipInterval}.show.${encodeURIComponent(showID)}`;
}

export function skipIntervalOverrideValue(prefs: ClientPref[], showID: string): SkipInterval | "" {
  const value = prefs.find((pref) => pref.key === skipIntervalOverrideKey(showID))?.value ?? "";
  return (SKIP_INTERVALS as readonly string[]).includes(value) ? (value as SkipInterval) : "";
}

/**
 * resolvePreferences folds a raw clientPrefs list into the typed preference
 * set, defaulting every field to its opt-out posture. An unknown or malformed
 * value resolves to the default (fail-closed for the booleans, matching the
 * server's autoSkipPrefs).
 */
export function resolvePreferences(prefs: ClientPref[], showID = ""): ViewerPreferences {
  const map = new Map(prefs.map((p) => [p.key, p.value]));
  const globalSkipInterval = interval(map, PREF_KEYS.skipInterval);
  return {
    autoSkipIntro: bool(map, PREF_KEYS.autoSkipIntro),
    autoSkipRecap: bool(map, PREF_KEYS.autoSkipRecap),
    autoSkipCredits: bool(map, PREF_KEYS.autoSkipCredits),
    autoSkipAd: bool(map, PREF_KEYS.autoSkipAd),
    recommendOptIn: bool(map, PREF_KEYS.recommendOptIn),
    reduceMotion: bool(map, PREF_KEYS.reduceMotion),
    hideWatched: bool(map, PREF_KEYS.hideWatched),
    playbackSpeed: speed(map),
    trimSilence: bool(map, PREF_KEYS.trimSilence),
    skipInterval: showID
      ? interval(map, skipIntervalOverrideKey(showID), globalSkipInterval)
      : globalSkipInterval,
    downstreamAudio: downstreamAudio(map),
    stillWatchingAfter: parseStillWatchingAfter(map.get(PREF_KEYS.stillWatchingAfter)),
    prePlayEnabled: bool(map, PREF_KEYS.prePlayEnabled),
    prePlayExtras: prePlayExtras(map),
  };
}

/** Encode a boolean preference to its stored value ("on" or "" to clear). */
export function encodeBool(on: boolean): string {
  return on ? PREF_ON : "";
}
