import { useEffect, useRef } from "react";
import type { PlaybackGapless, PlaybackNext, PlaybackStream } from "../lib/playback";
import { holdMute, listenerMuted, muteHeld, releaseMute } from "./listenerMute";
import { audioContextCtor, audioDelayHolds } from "./useAudioDelay";

// Gapless track joins for the web player (CORE-1634). The media element loads
// one track at a time, so an album's track change waits for the next stream to
// load and buffer. Instead, the next track's direct-play stream is fetched and
// decoded ahead and scheduled on the Web Audio clock to start where the current
// track stops, with the server's encoder delay and padding (CORE-1626) trimmed.
//
// The decoded tracks play on an AudioContext of their own. The media element is
// never routed through Web Audio: a MediaElementSource is permanent, and the
// same element later plays items Web Audio cannot hear (native HLS, CORE-1671),
// so one path serves every browser. The element stays the player: it plays the
// same track muted (the listener's own mute is kept apart, listenerMute), close
// to what is heard, so the transport, progress beacons, Media Session and the
// advance keep following it.
//
// The element and the context reach the speakers by different paths whose
// latency no engine reports alike (WebKit's GStreamer backend differs by about
// 0.1 s), so sound never passes from the element to the context mid-play.
// Whenever the element starts from silence (its first play, a resume, a seek)
// and the track it plays is decodable, it starts held muted and the decoded
// track starts at its position; the start waits at most START_WAIT_SECONDS for
// the decode, then the element plays on its own. While a decoded track plays,
// the one after it is chained on the same clock, so every join is
// sample-accurate. A track the element did start audibly joins the next one at
// its end, timed by the element's position. A rate change, an audio delay or
// another item hands sound back to the element at its own position.

/** How long before a track's end the next one is fetched and decoded. */
export const PREPARE_AHEAD_SECONDS = 30;
/**
 * How long an element starting from silence waits, held muted, for its track to
 * decode before it plays on its own.
 */
export const START_WAIT_SECONDS = 2;
/** How long before the element's end a join from the element is scheduled. */
const JOIN_AHEAD_SECONDS = 1;
/** A join scheduled closer than this to now is left to the element advance. */
const JOIN_MARGIN_SECONDS = 0.05;
/** How far the silent element may drift from what is heard. */
export const SYNC_TOLERANCE_SECONDS = 0.25;
/**
 * The silent element is moved at most this often. An engine can report its
 * old position for a while after a seek (WebKit's GStreamer backend does), and
 * correcting every report would seek it in a loop.
 */
export const STEP_INTERVAL_SECONDS = 1;
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

/** Whether the stream is a direct-play stream served from this origin. */
export function decodable(stream?: PlaybackStream | null): boolean {
  if (stream?.kind !== "direct-play" || !stream.url) return false;
  try {
    return new URL(stream.url, window.location.href).origin === window.location.origin;
  } catch {
    return false;
  }
}

/** The next track's direct-play stream when it is served from this origin. */
export function decodableStream(next?: PlaybackNext): PlaybackStream | undefined {
  return next?.streams?.find((stream) => decodable(stream));
}

// A stream's gapless metadata as a stable effect dependency, and back.
function gaplessKey(gapless?: PlaybackGapless): string {
  return gapless
    ? `${gapless.sampleRate}:${gapless.encoderDelaySamples}:${gapless.encoderPaddingSamples}`
    : "";
}

function gaplessFromKey(key: string): PlaybackGapless | undefined {
  if (!key) return undefined;
  const [sampleRate, encoderDelaySamples, encoderPaddingSamples] = key.split(":").map(Number);
  return { sampleRate, encoderDelaySamples, encoderPaddingSamples };
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

interface Track {
  buffer: AudioBuffer;
  trim: TrimWindow;
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
  /** The decoded tracks' output; follows the element's volume and the listener's mute. */
  output: GainNode;
  segments: Segment[];
  /** Decoded tracks by item: the one heard and the next. */
  tracks: Map<string, Track>;
  /** The item the element plays now, when its track is decodable. */
  lead: { mediaID: string; url: string; gapless?: PlaybackGapless } | null;
  /** The lead track's decode in flight. */
  leadDecode: { mediaID: string; controller: AbortController } | null;
  /** An element start held muted until its decoded track starts at `from`. */
  pending: { mediaID: string; from: number; timer: ReturnType<typeof setTimeout> } | null;
  /** Set from a seek this module makes until it completes, so it is not taken for the listener's. */
  ownSeek: boolean;
  /** Context time of this module's last seek. */
  steppedAt: number;
  /** True once the element plays its current source (reset when it loads another). */
  elementPlaying: boolean;
}

const bridges = new WeakMap<HTMLMediaElement, Bridge>();

function listenerVolume(video: HTMLMediaElement): number {
  return listenerMuted(video) ? 0 : video.volume;
}

// The element's bridge with its own context, made when absent. Nothing routes
// the element into it. A silent source plays on the context for its whole life:
// an engine can let its output idle while nothing plays and start it again late,
// losing the start of the first decoded track (WebKit's GStreamer backend loses
// about 0.4 s).
function bridgeFor(video: HTMLMediaElement): Bridge | null {
  const existing = bridges.get(video);
  if (existing) return existing;
  const Ctor = audioContextCtor();
  if (!Ctor) return null;
  const context = new Ctor();
  const output = context.createGain();
  output.gain.value = listenerVolume(video);
  output.connect(context.destination);
  const keepAwake = context.createConstantSource();
  keepAwake.offset.value = 0;
  keepAwake.connect(output);
  keepAwake.start();
  const bridge: Bridge = {
    context,
    output,
    segments: [],
    tracks: new Map(),
    lead: null,
    leadDecode: null,
    pending: null,
    ownSeek: false,
    steppedAt: Number.NEGATIVE_INFINITY,
    elementPlaying: !video.paused,
  };
  // A start held for a gesture's resume goes ahead once the context runs.
  context.addEventListener?.("statechange", () => startPending(bridge, video));
  bridges.set(video, bridge);
  return bridge;
}

// Starts the context if it waits for one; a browser that wants a gesture
// starts it on the listener's next one.
function wake(bridge: Bridge) {
  if (bridge.context.state === "suspended") void bridge.context.resume().catch(() => {});
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

// Stops the decoded audio; the decoded tracks are kept to start again.
function silence(bridge: Bridge) {
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
}

function clearPending(bridge: Bridge) {
  if (bridge.pending) clearTimeout(bridge.pending.timer);
  bridge.pending = null;
}

/** Stops the decoded audio and gives the element its sound back. */
export function handBack(video: HTMLMediaElement) {
  const bridge = bridges.get(video);
  if (bridge) {
    silence(bridge);
    clearPending(bridge);
  }
  releaseMute(video);
}

// The element shadows a decoded track from its own end on: muted, so only the
// decoded track is heard.
function shadow(video: HTMLMediaElement) {
  const bridge = bridges.get(video);
  if (bridge && bridge.segments.length > 0) holdMute(video);
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
    bridge.tracks.delete(mediaID);
    // The last decoded track is over and nothing follows it: the muted element
    // has ended too, or ends within the drift, and then gets its sound back.
    if (bridge.segments.length === 0 && (video.ended || video.paused)) handBack(video);
  };
  bridge.segments.push(segment);
}

function segmentFor(bridge: Bridge, mediaID: string): Segment | undefined {
  return bridge.segments.find((segment) => segment.mediaID === mediaID);
}

// Decoded tracks held: kept to start again, or still sounding.
function heldTracks(bridge: Bridge): number {
  return new Set([...bridge.tracks.keys(), ...bridge.segments.map((s) => s.mediaID)]).size;
}

/**
 * The track at `url` decoded with its trim window, or null when it is over the
 * cap, declares no duration, or cannot be fetched or decoded.
 */
async function decodeTrack(
  bridge: Bridge,
  url: string,
  gapless: PlaybackGapless | undefined,
  signal: AbortSignal,
): Promise<Track | null> {
  try {
    // Not NaN, not endless, not too long to hold decoded.
    const seconds = await probeDuration(url, signal);
    if (signal.aborted || !(seconds <= MAX_DECODED_SECONDS)) return null;
    const response = await fetch(url, { credentials: "same-origin", signal });
    if (!response.ok) return null;
    const bytes = await response.arrayBuffer();
    const buffer = await bridge.context.decodeAudioData(bytes);
    // The metadata understated it: drop it rather than hold it.
    if (signal.aborted || buffer.duration > MAX_DECODED_SECONDS) return null;
    return { buffer, trim: trimWindow(buffer, gapless) };
  } catch {
    // Aborted, refused or undecodable.
    return null;
  }
}

// Starts the held element's decoded track at its position, once it can.
function startPending(bridge: Bridge, video: HTMLMediaElement) {
  const pending = bridge.pending;
  if (!pending || bridge.segments.length > 0) return;
  const track = bridge.tracks.get(pending.mediaID);
  if (!track || bridge.context.state !== "running") return;
  if (video.paused || video.seeking || video.playbackRate !== 1 || audioDelayHolds(video)) return;
  clearPending(bridge);
  const end = track.trim.offset + track.trim.duration;
  const from = Math.max(pending.from, track.trim.offset);
  if (end - from < JOIN_MARGIN_SECONDS) {
    handBack(video);
    return;
  }
  schedule(
    bridge,
    pending.mediaID,
    track.buffer,
    { offset: from, duration: end - from },
    bridge.context.currentTime,
    video,
  );
}

// The element plays on its own: nothing was heard while it waited, so it goes
// back to where it started.
function giveUp(bridge: Bridge, video: HTMLMediaElement) {
  const pending = bridge.pending;
  if (!pending || bridge.segments.length > 0) return;
  clearPending(bridge);
  if (Math.abs(video.currentTime - pending.from) > SYNC_TOLERANCE_SECONDS) {
    bridge.ownSeek = true;
    video.currentTime = pending.from;
  }
  releaseMute(video);
}

// The element starts from silence: held muted, it waits for its decoded track,
// decoding it when that has not begun. False when its track is not decodable.
function arm(bridge: Bridge, video: HTMLMediaElement, mediaID: string): boolean {
  const lead = bridge.lead?.mediaID === mediaID ? bridge.lead : null;
  const held = bridge.tracks.has(mediaID);
  if (!held && !(lead && !(video.duration > MAX_DECODED_SECONDS))) return false;
  if (video.playbackRate !== 1 || audioDelayHolds(video)) return false;
  holdMute(video);
  clearPending(bridge);
  bridge.pending = {
    mediaID,
    from: video.currentTime,
    timer: setTimeout(() => giveUp(bridge, video), START_WAIT_SECONDS * 1000),
  };
  if (lead && !held && bridge.leadDecode?.mediaID !== mediaID) {
    bridge.leadDecode?.controller.abort();
    const controller = new AbortController();
    const decode = { mediaID, controller };
    bridge.leadDecode = decode;
    void decodeTrack(bridge, lead.url, lead.gapless, controller.signal).then((track) => {
      if (bridge.leadDecode === decode) bridge.leadDecode = null;
      if (controller.signal.aborted) return;
      if (track) {
        bridge.tracks.set(mediaID, track);
        startPending(bridge, video);
        return;
      }
      // Not decodable: the element plays it, now and on later starts.
      if (bridge.lead?.mediaID === mediaID) bridge.lead = null;
      if (bridge.pending?.mediaID === mediaID) giveUp(bridge, video);
    });
  }
  startPending(bridge, video);
  return true;
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
    if (!video || !bridge) return;
    // Another item's track is no longer wanted.
    if (bridge.leadDecode && bridge.leadDecode.mediaID !== mediaID) {
      bridge.leadDecode.controller.abort();
      bridge.leadDecode = null;
    }
    if (segmentFor(bridge, mediaID)) shadow(video);
    else if (bridge.segments.length > 0 || bridge.pending) handBack(video);
  }, [mediaID, videoRef]);

  // Leaving the player takes the sound back and closes the context.
  useEffect(() => {
    const video = videoRef.current;
    return () => {
      if (!video) return;
      handBack(video);
      const bridge = bridges.get(video);
      bridges.delete(video);
      if (!bridge) return;
      bridge.leadDecode?.controller.abort();
      void bridge.context.close().catch(() => {});
    };
  }, [videoRef]);

  // The listener's own actions, and the muted element kept in step.
  useEffect(() => {
    const video = videoRef.current;
    if (!video) return;
    const active = () => {
      const bridge = bridges.get(video);
      return bridge && bridge.segments.length > 0 ? bridge : undefined;
    };
    // Stops the decoded audio for a pause or seek. Held muted over its decoded
    // track, the element starts it again when it plays on; otherwise it takes
    // the sound back.
    const interrupt = (bridge: Bridge) => {
      clearPending(bridge);
      if (bridge.segments.length === 0) return;
      if (muteHeld(video) && bridge.tracks.has(mediaIDRef.current)) silence(bridge);
      else handBack(video);
    };
    const onPause = () => {
      const bridge = bridges.get(video);
      // The element also pauses when it reaches its end, which is the join.
      if (bridge && !video.ended) interrupt(bridge);
    };
    const onSeeking = () => {
      const bridge = bridges.get(video);
      // An engine may report one seek more than once before it completes.
      if (!bridge || bridge.ownSeek) return;
      // A resume seek while a new source loads is the engine's, not the listener's.
      if (!bridge.elementPlaying || (bridge.segments.length === 0 && !bridge.pending)) return;
      const resumes = muteHeld(video) && bridge.tracks.has(mediaIDRef.current);
      interrupt(bridge);
      if (resumes && !video.paused && !arm(bridge, video, mediaIDRef.current)) handBack(video);
    };
    const onRate = () => {
      if (video.playbackRate !== 1 && active()) handBack(video);
    };
    const onVolume = () => {
      // Only this module mutes a held element; anything unmuting it would
      // sound under the decoded track, so the element takes the sound back.
      if (muteHeld(video) && !video.muted) {
        handBack(video);
        return;
      }
      const bridge = bridges.get(video);
      if (bridge) bridge.output.gain.value = listenerVolume(video);
    };
    const onLoad = () => {
      const bridge = bridges.get(video);
      if (!bridge) return;
      bridge.elementPlaying = false;
      // A new source abandons a seek in flight, and a start held for the last.
      bridge.ownSeek = false;
      clearPending(bridge);
      // The element loads the next track while a decoded one plays: it must
      // not be heard.
      shadow(video);
    };
    const keepInStep = () => {
      const bridge = active();
      if (!bridge) return;
      // A delay line holds the element's sound back; the decoded track cannot follow it.
      if (audioDelayHolds(video)) {
        handBack(video);
        return;
      }
      if (!bridge.elementPlaying || video.seeking || bridge.ownSeek) return;
      const now = bridge.context.currentTime;
      if (now - bridge.steppedAt < STEP_INTERVAL_SECONDS) return;
      const segment = segmentFor(bridge, mediaIDRef.current);
      if (!segment) return;
      const heard = now - segment.start + segment.offset;
      if (heard <= 0 || Math.abs(video.currentTime - heard) <= SYNC_TOLERANCE_SECONDS) return;
      bridge.ownSeek = true;
      bridge.steppedAt = now;
      video.currentTime = heard;
    };
    const onSeeked = () => {
      const bridge = bridges.get(video);
      if (!bridge) return;
      bridge.ownSeek = false;
      if (bridge.pending) bridge.pending.from = video.currentTime;
      startPending(bridge, video);
    };
    const onPlaying = () => {
      const bridge = bridges.get(video);
      if (!bridge) return;
      bridge.elementPlaying = true;
      startPending(bridge, video);
      keepInStep();
    };
    // The element starts from silence; its sound has not begun yet.
    const onPlay = () => {
      const bridge = bridges.get(video);
      if (!bridge) return;
      wake(bridge);
      // Nothing to start on Web Audio: the element is heard.
      if (bridge.segments.length === 0 && !arm(bridge, video, mediaIDRef.current)) handBack(video);
    };
    const onEnded = () => {
      const bridge = bridges.get(video);
      if (!bridge) return;
      if (bridge.segments.length === 0) handBack(video);
      else shadow(video);
    };
    const onGesture = () => {
      const bridge = bridges.get(video);
      if (bridge) wake(bridge);
    };
    video.addEventListener("pause", onPause);
    video.addEventListener("seeking", onSeeking);
    video.addEventListener("seeked", onSeeked);
    video.addEventListener("ratechange", onRate);
    video.addEventListener("volumechange", onVolume);
    video.addEventListener("emptied", onLoad);
    video.addEventListener("loadstart", onLoad);
    video.addEventListener("playing", onPlaying);
    video.addEventListener("timeupdate", keepInStep);
    video.addEventListener("ended", onEnded);
    video.addEventListener("play", onPlay);
    document.addEventListener("pointerdown", onGesture, true);
    document.addEventListener("keydown", onGesture, true);
    return () => {
      video.removeEventListener("pause", onPause);
      video.removeEventListener("seeking", onSeeking);
      video.removeEventListener("seeked", onSeeked);
      video.removeEventListener("ratechange", onRate);
      video.removeEventListener("volumechange", onVolume);
      video.removeEventListener("emptied", onLoad);
      video.removeEventListener("loadstart", onLoad);
      video.removeEventListener("playing", onPlaying);
      video.removeEventListener("timeupdate", keepInStep);
      video.removeEventListener("ended", onEnded);
      video.removeEventListener("play", onPlay);
      document.removeEventListener("pointerdown", onGesture, true);
      document.removeEventListener("keydown", onGesture, true);
    };
  }, [videoRef]);

  const nextID = next && next.mediaID === advanceID ? next.mediaID : "";
  const decodableNext = nextID ? decodableStream(next) : undefined;
  const nextURL = decodableNext?.url ?? "";
  const nextTrim = gaplessKey(decodableNext?.gapless);
  const padding = declaredPadding(stream?.gapless);
  // The track the element plays, when it may start on Web Audio: something
  // follows it to join, and it is decodable.
  const leadURL = nextURL && decodable(stream) ? (stream?.url ?? "") : "";
  const leadTrim = gaplessKey(stream?.gapless);

  // The element's own track, decoded when it starts from silence (arm). Only
  // the track heard and the next are kept.
  useEffect(() => {
    const video = videoRef.current;
    if (!enabled || !video || !leadURL) return;
    const bridge = bridgeFor(video);
    if (!bridge) return;
    for (const id of [...bridge.tracks.keys()]) {
      if (id !== mediaID && id !== nextID) bridge.tracks.delete(id);
    }
    bridge.lead = { mediaID, url: leadURL, gapless: gaplessFromKey(leadTrim) };
    return () => {
      if (bridge.lead?.mediaID === mediaID) bridge.lead = null;
    };
  }, [enabled, videoRef, mediaID, leadURL, leadTrim, nextID]);

  // Decode the next track ahead, then schedule its join.
  useEffect(() => {
    const video = videoRef.current;
    if (!enabled || !video || !nextURL) return;
    const bridge = bridgeFor(video);
    if (!bridge) return;
    wake(bridge);
    const trimFor = gaplessFromKey(nextTrim);
    const controller = new AbortController();
    let decoding = false;
    let timer: ReturnType<typeof setTimeout> | undefined;

    const remaining = () => video.duration - video.currentTime;

    const prepare = async () => {
      decoding = true;
      const track = await decodeTrack(bridge, nextURL, trimFor, controller.signal);
      // Not decodable: the element advance takes over.
      if (!track || controller.signal.aborted) return;
      bridge.tracks.set(nextID, track);
      check();
    };

    const join = () => {
      const track = bridge.tracks.get(nextID);
      if (!track || segmentFor(bridge, nextID)) return;
      // A suspended context would start the track late; a delay line would not line up.
      if (bridge.context.state !== "running" || audioDelayHolds(video)) return;
      const current = segmentFor(bridge, mediaIDRef.current);
      if (current) {
        // Decoded to decoded: chained on the clock, to the sample.
        schedule(bridge, nextID, track.buffer, track.trim, current.start + current.duration, video);
        return;
      }
      // A held element is not heard: its own decoded track starts first.
      if (muteHeld(video) || bridge.pending) return;
      if (video.paused || video.seeking || video.playbackRate !== 1) return;
      // The decoded track starts where the element's declared padding begins;
      // the element's own padding plays out beneath it.
      const endsIn = remaining() - padding;
      if (!Number.isFinite(endsIn)) return;
      if (endsIn > JOIN_AHEAD_SECONDS) {
        clearTimeout(timer);
        timer = setTimeout(check, (endsIn - JOIN_AHEAD_SECONDS) * 1000);
        return;
      }
      if (endsIn < JOIN_MARGIN_SECONDS) return;
      schedule(
        bridge,
        nextID,
        track.buffer,
        track.trim,
        bridge.context.currentTime + endsIn,
        video,
      );
    };

    const check = () => {
      if (controller.signal.aborted) return;
      // The track heard and the next are the most held at once.
      const due = remaining() <= PREPARE_AHEAD_SECONDS;
      if (!decoding && !bridge.tracks.has(nextID) && heldTracks(bridge) < 2 && due) void prepare();
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
