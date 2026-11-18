import { useEffect, useReducer, useRef } from "react";
import { getIdentityEpochSignal } from "@/lib/api";

/**
 * Waveform peaks for the music seek bar (CORE-2401). The server advertises
 * `assets.waveform` for every audio decision and generates the envelope on the
 * first fetch, so a track that the background sweep has not reached still gets
 * one within a few seconds.
 *
 * Peaks are cached per Media for the page's lifetime: the asset URL carries a
 * Stream Token that changes with every decision, but the peaks do not. The next
 * queued track is fetched ahead, so advancing shows its peaks at once.
 */

const MAX_CACHED = 64;

const peaks = new Map<string, number[]>();
const inflight = new Map<string, Promise<number[] | null>>();
/** URLs that failed; a later decision (new token) for the same Media retries. */
const failed = new Set<string>();

interface WaveformEnvelope {
  version?: number;
  points?: unknown;
}

function remember(mediaID: string, points: number[]) {
  peaks.delete(mediaID);
  peaks.set(mediaID, points);
  while (peaks.size > MAX_CACHED) {
    const oldest = peaks.keys().next().value;
    if (oldest === undefined) break;
    peaks.delete(oldest);
  }
}

function parsePoints(body: WaveformEnvelope): number[] | null {
  if (!Array.isArray(body.points) || body.points.length === 0) return null;
  const points = body.points.map((p) => (typeof p === "number" && Number.isFinite(p) ? p : 0));
  return points.map((p) => Math.max(0, Math.min(1, p)));
}

/** Fetches (once) and caches one Media's peaks; null when unavailable. */
export function loadWaveform(mediaID: string, url: string | undefined): Promise<number[] | null> {
  if (!mediaID || !url) return Promise.resolve(null);
  const cached = peaks.get(mediaID);
  if (cached) return Promise.resolve(cached);
  if (failed.has(url)) return Promise.resolve(null);
  const pending = inflight.get(mediaID);
  if (pending) return pending;
  const request = (async () => {
    try {
      const response = await fetch(url, {
        credentials: "same-origin",
        signal: getIdentityEpochSignal(),
      });
      if (!response.ok) throw new Error(`waveform ${response.status}`);
      const points = parsePoints((await response.json()) as WaveformEnvelope);
      if (!points) throw new Error("empty waveform");
      remember(mediaID, points);
      return points;
    } catch {
      failed.add(url);
      return null;
    } finally {
      inflight.delete(mediaID);
    }
  })();
  inflight.set(mediaID, request);
  return request;
}

/** Test seam: forget every cached, pending and failed waveform. */
export function resetWaveformCache() {
  peaks.clear();
  inflight.clear();
  failed.clear();
}

export interface WaveformInput {
  /** The Media the player is on now (the route's Media). */
  mediaID: string;
  /** The Media the current playback info describes; differs right after an advance. */
  infoMediaID?: string;
  /** That info's waveform URL. */
  url?: string;
  /** The next queued Media and its waveform URL, fetched ahead. */
  nextMediaID?: string;
  nextURL?: string;
}

export interface WaveformState {
  /** Peaks normalised to [0,1], or null while loading or when unavailable. */
  points: number[] | null;
  /**
   * True while peaks are expected but not here yet: the bar keeps its waveform
   * layout with a flat placeholder instead of falling back and jumping.
   */
  pending: boolean;
}

export function useWaveform({
  mediaID,
  infoMediaID,
  url,
  nextMediaID,
  nextURL,
}: WaveformInput): WaveformState {
  const [, rerender] = useReducer((n: number) => n + 1, 0);
  const shownRef = useRef(false);
  const infoCurrent = !!infoMediaID && infoMediaID === mediaID;
  const currentURL = infoCurrent ? url : undefined;

  useEffect(() => {
    if (!mediaID || !currentURL || peaks.has(mediaID)) return;
    let alive = true;
    void loadWaveform(mediaID, currentURL).then(() => {
      if (alive) rerender();
    });
    return () => {
      alive = false;
    };
  }, [mediaID, currentURL]);

  useEffect(() => {
    if (nextMediaID && nextURL) void loadWaveform(nextMediaID, nextURL);
  }, [nextMediaID, nextURL]);

  const points = (mediaID && peaks.get(mediaID)) || null;
  let pending = false;
  if (!points) {
    // The info for this Media is still loading: keep the waveform layout if
    // the previous track had one, so the bar does not flash between songs.
    if (!infoCurrent) pending = shownRef.current;
    else pending = !!currentURL && !failed.has(currentURL);
  }
  shownRef.current = !!points || pending;
  return { points, pending };
}
