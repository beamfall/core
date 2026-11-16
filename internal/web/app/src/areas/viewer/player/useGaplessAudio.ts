import { useEffect, useRef } from "react";
import type { PlaybackGapless, PlaybackNext, PlaybackStream } from "../lib/playback";
import { elementGraph, ensureElementGraph, routeElement } from "./elementAudioGraph";
import { audioDelayRoutable, browserPlaysNativeHls } from "./useAudioDelay";

// Gapless track joins for the web player (CORE-1634). The media element loads
// one track at a time, so an album's track change waits for the next stream to
// load and buffer. Instead, the next track's direct-play stream is fetched and
// decoded ahead and scheduled on the Web Audio clock to start where the current
// track stops, with the server's encoder delay and padding (CORE-1626) trimmed.
//
// The media element stays the player: after the join it plays the same track
// silently (its gain in the shared element graph is zero), kept close to what
// is heard, so the transport, progress beacons, Media Session and the advance
// keep following it. While a decoded track plays, the one after it is chained
// on the same clock, so every later join is sample-accurate. Anything else the
// listener does (pause, seek, a rate change, another item) hands sound back to
// the element at its own position.

/** How long before a track's end the next one is fetched and decoded. */
export const PREPARE_AHEAD_SECONDS = 30;
/** How long before the element's end the first join is scheduled. */
const JOIN_AHEAD_SECONDS = 1;
/** A join scheduled closer than this to now is left to the element advance. */
const JOIN_MARGIN_SECONDS = 0.05;
/** How far the silent element may drift from what is heard. */
export const SYNC_TOLERANCE_SECONDS = 0.25;
/** A declared delay or padding is trimmed only when its samples stay below this peak. */
export const QUIET_PEAK = 0.01;
/**
 * A next track longer than this is left to the element advance: decoded, a
 * stereo track holds about 23 MB a minute at 48 kHz. At most two decoded
 * tracks (the one heard and the next) are held at once.
 */
export const MAX_DECODED_SECONDS = 12 * 60;

export interface TrimWindow {
  /** Seconds skipped at the start of the decoded track. */
  offset: number;
  /** Seconds played from the offset. */
  duration: number;
}

function declaredSeconds(samples: number, gapless: PlaybackGapless): number {
  if (!(gapless.sampleRate > 0) || !(samples > 0)) return 0;
  return samples / gapless.sampleRate;
}

/** The seconds of end padding a stream declares, or 0. */
export function declaredPadding(gapless?: PlaybackGapless): number {
  return gapless ? declaredSeconds(gapless.encoderPaddingSamples, gapless) : 0;
}

function quiet(buffer: AudioBuffer, from: number, to: number): boolean {
  for (let channel = 0; channel < buffer.numberOfChannels; channel++) {
    const samples = buffer.getChannelData(channel);
    for (let i = Math.max(0, from); i < Math.min(to, samples.length); i++) {
      if (Math.abs(samples[i]) >= QUIET_PEAK) return false;
    }
  }
  return true;
}

/**
 * The part of a decoded track to play: the declared encoder delay and padding
 * are skipped, each only where its decoded samples are near silence. A browser
 * decoder that applies the container's own gapless signalling has already
 * removed them, and the samples left in their place are music.
 */
export function trimWindow(buffer: AudioBuffer, gapless?: PlaybackGapless): TrimWindow {
  const total = buffer.duration;
  if (!gapless) return { offset: 0, duration: total };
  const rate = buffer.sampleRate;
  let lead = Math.min(declaredSeconds(gapless.encoderDelaySamples, gapless), total / 2);
  let tail = Math.min(declaredPadding(gapless), total / 2);
  if (lead > 0 && !quiet(buffer, 0, Math.round(lead * rate))) lead = 0;
  if (tail > 0 && !quiet(buffer, buffer.length - Math.round(tail * rate), buffer.length)) tail = 0;
  return { offset: lead, duration: total - lead - tail };
}

/** The next track's direct-play stream when it is served from this origin. */
export function decodableStream(next?: PlaybackNext): PlaybackStream | undefined {
  return next?.streams?.find((stream) => {
    if (stream.kind !== "direct-play" || !stream.url) return false;
    try {
      return new URL(stream.url, window.location.href).origin === window.location.origin;
    } catch {
      return false;
    }
  });
}

/**
 * The duration the media at `url` declares in its metadata (NaN when it
 * declares none), read by a detached element so nothing is downloaded whole.
 */
export function probeDuration(url: string, signal: AbortSignal): Promise<number> {
  return new Promise((resolve) => {
    if (signal.aborted) return resolve(Number.NaN);
    const probe = document.createElement("audio");
    const done = (seconds: number) => {
      probe.removeEventListener("loadedmetadata", onMetadata);
      probe.removeEventListener("error", onError);
      signal.removeEventListener("abort", onError);
      probe.removeAttribute("src");
      probe.load();
      resolve(seconds);
    };
    const onMetadata = () => done(probe.duration);
    const onError = () => done(Number.NaN);
    probe.addEventListener("loadedmetadata", onMetadata);
    probe.addEventListener("error", onError);
    signal.addEventListener("abort", onError);
    probe.preload = "metadata";
    probe.src = url;
  });
}

interface Segment {
  mediaID: string;
  node: AudioBufferSourceNode;
  /** Context time at which the segment's first trimmed sample is heard. */
  start: number;
  offset: number;
  duration: number;
}

interface Bridge {
  context: AudioContext;
  /** The element's share of the output. */
  elementGain: GainNode;
  /** The decoded tracks' share; follows the element's volume and mute. */
  output: GainNode;
  segments: Segment[];
  /** Set across a seek this module makes, so it is not taken for the listener's. */
  ownSeek: boolean;
  /** True once the element plays its current source (reset when it loads another). */
  elementPlaying: boolean;
}

const bridges = new WeakMap<HTMLMediaElement, Bridge>();

function elementVolume(video: HTMLMediaElement): number {
  return video.muted ? 0 : video.volume;
}

// The element's bridge, routing the element through the shared graph first.
// Null while the context is not running.
function bridgeFor(video: HTMLMediaElement): Bridge | null {
  const existing = bridges.get(video);
  if (existing) return existing;
  const graph = ensureElementGraph(video);
  if (!graph) return null;
  const elementGain = routeElement(video, graph);
  if (!elementGain) return null;
  const output = graph.context.createGain();
  output.gain.value = elementVolume(video);
  output.connect(graph.context.destination);
  const bridge: Bridge = {
    context: graph.context,
    elementGain,
    output,
    segments: [],
    ownSeek: false,
    elementPlaying: !video.paused,
  };
  bridges.set(video, bridge);
  graph.handBack = () => handBack(video);
  return bridge;
}

// Lets go of a finished segment's decoded audio.
function release(node: AudioBufferSourceNode) {
  node.disconnect();
  try {
    node.buffer = null;
  } catch {
    // An engine that refuses the reset frees it with the node.
  }
}

/** Stops the decoded audio and gives the element its sound back. */
export function handBack(video: HTMLMediaElement) {
  const bridge = bridges.get(video);
  if (!bridge) return;
  const segments = bridge.segments;
  bridge.segments = [];
  for (const segment of segments) {
    try {
      segment.node.stop();
    } catch {
      // Never started (or already stopped): nothing to silence.
    }
    release(segment.node);
  }
  const { gain } = bridge.elementGain;
  gain.cancelScheduledValues(0);
  gain.setValueAtTime(1, bridge.context.currentTime);
}

function schedule(
  bridge: Bridge,
  mediaID: string,
  buffer: AudioBuffer,
  trim: TrimWindow,
  at: number,
  video: HTMLMediaElement,
) {
  const node = bridge.context.createBufferSource();
  node.buffer = buffer;
  node.connect(bridge.output);
  node.start(at, trim.offset, trim.duration);
  const segment: Segment = {
    mediaID,
    node,
    start: at,
    offset: trim.offset,
    duration: trim.duration,
  };
  node.onended = () => {
    if (!bridge.segments.includes(segment)) return;
    bridge.segments = bridge.segments.filter((other) => other !== segment);
    release(node);
    // The last decoded track is over and nothing follows it: the silent element
    // has ended too, or ends within the drift, and then gets its sound back.
    if (bridge.segments.length === 0 && (video.ended || video.paused)) handBack(video);
  };
  bridge.segments.push(segment);
}

function segmentFor(bridge: Bridge, mediaID: string): Segment | undefined {
  return bridge.segments.find((segment) => segment.mediaID === mediaID);
}

/**
 * Joins the next track to the current one without a gap. `enabled` is the
 * player's verdict that the current item is a playing music track the join may
 * follow; `advanceID` is the item the player advances to when it ends.
 */
export function useGaplessAudio({
  videoRef,
  mediaID,
  stream,
  next,
  advanceID,
  enabled,
}: {
  videoRef: React.RefObject<HTMLVideoElement | null>;
  mediaID: string;
  /** The stream the element plays now. */
  stream: PlaybackStream | null;
  /** The next item playback-info pre-minted. */
  next?: PlaybackNext;
  advanceID?: string;
  enabled: boolean;
}) {
  const mediaIDRef = useRef(mediaID);
  mediaIDRef.current = mediaID;

  // A new item: the decoded track it names carries on (the element now
  // shadows it); any other item takes the sound back.
  useEffect(() => {
    const video = videoRef.current;
    const bridge = video ? bridges.get(video) : undefined;
    if (!video || !bridge || bridge.segments.length === 0) return;
    if (!segmentFor(bridge, mediaID)) handBack(video);
  }, [mediaID, videoRef]);

  // Leaving the player takes the sound back.
  useEffect(() => {
    const video = videoRef.current;
    return () => {
      if (video) handBack(video);
    };
  }, [videoRef]);

  // The listener's own actions, and the silent element kept in step.
  useEffect(() => {
    const video = videoRef.current;
    if (!video) return;
    const active = () => {
      const bridge = bridges.get(video);
      return bridge && bridge.segments.length > 0 ? bridge : undefined;
    };
    const onPause = () => {
      // The element also pauses when it reaches its end, which is the join.
      if (!video.ended && active()) handBack(video);
    };
    const onSeeking = () => {
      const bridge = bridges.get(video);
      if (!bridge) return;
      if (bridge.ownSeek) {
        bridge.ownSeek = false;
        return;
      }
      // A resume seek while a new source loads is the engine's, not the listener's.
      if (bridge.elementPlaying && bridge.segments.length > 0) handBack(video);
    };
    const onRate = () => {
      if (video.playbackRate !== 1 && active()) handBack(video);
    };
    const onVolume = () => {
      const bridge = bridges.get(video);
      if (bridge) bridge.output.gain.value = elementVolume(video);
    };
    const onLoad = () => {
      const bridge = bridges.get(video);
      if (bridge) bridge.elementPlaying = false;
    };
    const keepInStep = () => {
      const bridge = active();
      if (!bridge || !bridge.elementPlaying || video.seeking) return;
      const segment = segmentFor(bridge, mediaIDRef.current);
      if (!segment) return;
      const heard = bridge.context.currentTime - segment.start + segment.offset;
      if (heard <= 0 || Math.abs(video.currentTime - heard) <= SYNC_TOLERANCE_SECONDS) return;
      bridge.ownSeek = true;
      video.currentTime = heard;
    };
    const onPlaying = () => {
      const bridge = bridges.get(video);
      if (bridge) bridge.elementPlaying = true;
      keepInStep();
    };
    const onEnded = () => {
      const bridge = bridges.get(video);
      if (bridge && bridge.segments.length === 0) handBack(video);
    };
    const onPlay = () => {
      const graph = elementGraph(video);
      if (graph && graph.context.state === "suspended") void graph.context.resume().catch(() => {});
    };
    video.addEventListener("pause", onPause);
    video.addEventListener("seeking", onSeeking);
    video.addEventListener("ratechange", onRate);
    video.addEventListener("volumechange", onVolume);
    video.addEventListener("emptied", onLoad);
    video.addEventListener("loadstart", onLoad);
    video.addEventListener("playing", onPlaying);
    video.addEventListener("timeupdate", keepInStep);
    video.addEventListener("ended", onEnded);
    video.addEventListener("play", onPlay);
    return () => {
      video.removeEventListener("pause", onPause);
      video.removeEventListener("seeking", onSeeking);
      video.removeEventListener("ratechange", onRate);
      video.removeEventListener("volumechange", onVolume);
      video.removeEventListener("emptied", onLoad);
      video.removeEventListener("loadstart", onLoad);
      video.removeEventListener("playing", onPlaying);
      video.removeEventListener("timeupdate", keepInStep);
      video.removeEventListener("ended", onEnded);
      video.removeEventListener("play", onPlay);
    };
  }, [videoRef]);

  const nextID = next && next.mediaID === advanceID ? next.mediaID : "";
  const decodable = nextID ? decodableStream(next) : undefined;
  const nextURL = decodable?.url ?? "";
  const nextGapless = decodable?.gapless;
  const nextTrim = nextGapless
    ? `${nextGapless.sampleRate}:${nextGapless.encoderDelaySamples}:${nextGapless.encoderPaddingSamples}`
    : "";
  const padding = declaredPadding(stream?.gapless);

  // Decode the next track ahead, then schedule its join.
  useEffect(() => {
    const video = videoRef.current;
    if (!enabled || !video || !nextURL) return;
    if (!audioDelayRoutable(video) || browserPlaysNativeHls(video)) return;
    const existing = bridges.get(video);
    if (existing && segmentFor(existing, nextID)) return;
    const [rate, delay, pad] = nextTrim.split(":").map(Number);
    const trimFor: PlaybackGapless | undefined = nextTrim
      ? { sampleRate: rate, encoderDelaySamples: delay, encoderPaddingSamples: pad }
      : undefined;
    const controller = new AbortController();
    let decoding = false;
    let decoded: { buffer: AudioBuffer; trim: TrimWindow } | null = null;
    let scheduled = false;
    let timer: ReturnType<typeof setTimeout> | undefined;

    const remaining = () => video.duration - video.currentTime;

    const prepare = async () => {
      decoding = true;
      const graph = ensureElementGraph(video);
      if (!graph) return;
      try {
        if (graph.context.state !== "running") await graph.context.resume();
        // Not NaN, not endless, not too long to hold decoded.
        const seconds = await probeDuration(nextURL, controller.signal);
        if (controller.signal.aborted || !(seconds <= MAX_DECODED_SECONDS)) return;
        const response = await fetch(nextURL, {
          credentials: "same-origin",
          signal: controller.signal,
        });
        if (!response.ok) return;
        const bytes = await response.arrayBuffer();
        const buffer = await graph.context.decodeAudioData(bytes);
        // The metadata understated it: drop it rather than hold it.
        if (controller.signal.aborted || buffer.duration > MAX_DECODED_SECONDS) return;
        decoded = { buffer, trim: trimWindow(buffer, trimFor) };
        check();
      } catch {
        // Aborted, refused or undecodable: the element advance takes over.
      }
    };

    const join = () => {
      if (!decoded || scheduled) return;
      // An audio delay holds the element's sound back; a join would not line up.
      if ((elementGraph(video)?.delay?.delayTime.value ?? 0) > 0) return;
      const bridge = bridgeFor(video);
      if (!bridge) return;
      const current = segmentFor(bridge, mediaIDRef.current);
      if (current) {
        // Decoded to decoded: chained on the clock, to the sample.
        schedule(
          bridge,
          nextID,
          decoded.buffer,
          decoded.trim,
          current.start + current.duration,
          video,
        );
        scheduled = true;
        decoded = null;
        return;
      }
      if (video.paused || video.seeking || video.playbackRate !== 1) return;
      const endsIn = remaining() - padding;
      if (!Number.isFinite(endsIn)) return;
      if (endsIn > JOIN_AHEAD_SECONDS) {
        clearTimeout(timer);
        timer = setTimeout(check, (endsIn - JOIN_AHEAD_SECONDS) * 1000);
        return;
      }
      if (endsIn < JOIN_MARGIN_SECONDS) return;
      const at = bridge.context.currentTime + endsIn;
      schedule(bridge, nextID, decoded.buffer, decoded.trim, at, video);
      bridge.elementGain.gain.setValueAtTime(0, at);
      scheduled = true;
      decoded = null;
    };

    const check = () => {
      if (controller.signal.aborted) return;
      // The track heard and one scheduled after it are the most held at once.
      const held = bridges.get(video)?.segments.length ?? 0;
      if (!decoding && held < 2 && remaining() <= PREPARE_AHEAD_SECONDS) void prepare();
      join();
    };

    video.addEventListener("timeupdate", check);
    check();
    return () => {
      controller.abort();
      clearTimeout(timer);
      video.removeEventListener("timeupdate", check);
    };
  }, [enabled, videoRef, nextID, nextURL, nextTrim, padding]);
}
