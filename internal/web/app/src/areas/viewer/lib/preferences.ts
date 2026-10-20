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

/** The resolved, typed self-service preference set for one Profile. */
export interface ViewerPreferences {
  autoSkipIntro: boolean;
  autoSkipRecap: boolean;
  autoSkipCredits: boolean;
  autoSkipAd: boolean;
  recommendOptIn: boolean;
  reduceMotion: boolean;
  playbackSpeed: PlaybackSpeed;
  trimSilence: boolean;
  skipInterval: SkipInterval;
  downstreamAudio: DownstreamAudioMode;
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
    playbackSpeed: speed(map),
    trimSilence: bool(map, PREF_KEYS.trimSilence),
    skipInterval: showID
      ? interval(map, skipIntervalOverrideKey(showID), globalSkipInterval)
      : globalSkipInterval,
    downstreamAudio: downstreamAudio(map),
  };
}

/** Encode a boolean preference to its stored value ("on" or "" to clear). */
export function encodeBool(on: boolean): string {
  return on ? PREF_ON : "";
}
