import { AUDIO_DELAY_LIMIT } from "./playbackSync";

// One Web Audio graph per media element, shared by the audio delay (CORE-1671)
// and the gapless join (CORE-1634): createMediaElementSource may only be called
// once for an element, and routing is permanent, so whoever routes it first
// builds the whole chain
//
//   element → source → gain → [delay] → destination
//
// The gain is the element's own share of the output (the gapless join silences
// the element under the decoded track); the delay is inserted only while an
// audio delay is wanted. The source is made only while the context is running,
// because a suspended context would silence the element.

export interface ElementGraph {
  context: AudioContext;
  source: MediaElementAudioSourceNode | null;
  gain: GainNode | null;
  delay: DelayNode | null;
  /** Gives the element its sound back from the gapless join, when one runs. */
  handBack?: () => void;
}

const graphs = new WeakMap<HTMLMediaElement, ElementGraph>();

type AudioContextCtor = typeof AudioContext;

export function audioContextCtor(): AudioContextCtor | null {
  if (typeof window === "undefined") return null;
  const w = window as unknown as {
    AudioContext?: AudioContextCtor;
    webkitAudioContext?: AudioContextCtor;
  };
  return w.AudioContext ?? w.webkitAudioContext ?? null;
}

/** The element's graph, if one was made. */
export function elementGraph(video: HTMLMediaElement): ElementGraph | undefined {
  return graphs.get(video);
}

/** The element's graph, making it (with its context, not yet routed) when absent. */
export function ensureElementGraph(video: HTMLMediaElement): ElementGraph | null {
  const existing = graphs.get(video);
  if (existing) return existing;
  const Ctor = audioContextCtor();
  if (!Ctor) return null;
  const graph: ElementGraph = { context: new Ctor(), source: null, gain: null, delay: null };
  graphs.set(video, graph);
  return graph;
}

/**
 * Routes the element through the graph once the context runs. Returns the
 * element's gain, or null while the context is not running.
 */
export function routeElement(video: HTMLMediaElement, graph: ElementGraph): GainNode | null {
  if (graph.gain) return graph.gain;
  if (graph.context.state !== "running") return null;
  const source = graph.context.createMediaElementSource(video);
  const gain = graph.context.createGain();
  source.connect(gain);
  gain.connect(graph.context.destination);
  graph.source = source;
  graph.gain = gain;
  return gain;
}

/** Sets the delay line (null removes it) after the element's gain. */
export function setElementDelay(graph: ElementGraph, delay: DelayNode | null) {
  const { gain, context } = graph;
  if (!gain) return;
  gain.disconnect();
  graph.delay?.disconnect();
  graph.delay = delay;
  if (delay) {
    gain.connect(delay);
    delay.connect(context.destination);
  } else {
    gain.connect(context.destination);
  }
}

/** A fresh delay line holding the given delay. */
export function makeDelay(graph: ElementGraph, seconds: number): DelayNode {
  const delay = graph.context.createDelay(AUDIO_DELAY_LIMIT);
  delay.delayTime.setValueAtTime(seconds, graph.context.currentTime);
  return delay;
}
