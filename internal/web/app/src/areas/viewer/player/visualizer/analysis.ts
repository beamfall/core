// The visualizer's audio analysis: Oscillux's DSP, mapper and reaction sampler fed from a
// decoded copy of the playing track. The player never routes its <video> element through Web
// Audio (gapless playback and the audio delay own that graph), so the analysis reads the
// track's own bytes instead and runs ahead of the playhead, which it samples by media time.
import { MAX_DECODED_SECONDS, probeDuration } from "../useGaplessAudio";
import type { Oscillux } from "./oscillux";

/** The analysis rate: mono at 24 kHz keeps a decoded track near 5.8 MB a minute. */
export const ANALYSIS_RATE = 24000;
const CHUNK = 4096;
// Audio before a seek target fed to a new chain, so its levels and beat clock settle first.
const WARMUP_MS = 3000;
// A jump this far past what is analysed restarts the chain at the new position.
const JUMP_MS = 2000;

/** The mapped values a visual reads, in the sampler's output order. */
const MAPPED = [
  "amplitude",
  "level",
  "bass",
  "mid",
  "treble",
  "onset",
  "bassImpact",
  "flux",
  "spectralCentroid",
] as const;

/** Every scalar audio uniform a visual declares. */
export const AUDIO_KEYS = [
  ...MAPPED,
  "beat",
  "beatPhase",
  "beatCount",
  "barPhase",
  "downbeat",
] as const;
export type AudioKey = (typeof AUDIO_KEYS)[number];

export interface AudioFrame {
  values: Record<AudioKey, number>;
  /** 64 spectrum bands. */
  spectrum: Float32Array;
}

/** A silent frame, for a visual drawn while no analysis is ready. */
export function silentFrame(): AudioFrame {
  const values = Object.fromEntries(AUDIO_KEYS.map((k) => [k, 0])) as Record<AudioKey, number>;
  return { values, spectrum: new Float32Array(64) };
}

/**
 * Fetches a direct-play stream and decodes it to mono at the analysis rate, or null for an item
 * longer than the gapless player's decode cap (or of no declared length): decoding a long
 * recording whole would hold hundreds of megabytes and could take the tab, and playback, down.
 */
export async function decodeTrack(url: string, signal: AbortSignal): Promise<Float32Array | null> {
  const seconds = await probeDuration(url, signal);
  signal.throwIfAborted();
  if (!(seconds <= MAX_DECODED_SECONDS)) return null;
  const res = await fetch(url, { signal, credentials: "same-origin" });
  if (!res.ok) throw new Error(`visualizer: stream ${res.status}`);
  const bytes = await res.arrayBuffer();
  signal.throwIfAborted();
  const buffer = await new OfflineAudioContext(1, 1, ANALYSIS_RATE).decodeAudioData(bytes);
  if (buffer.duration > MAX_DECODED_SECONDS) return null;
  if (buffer.numberOfChannels === 1) return buffer.getChannelData(0);
  const mono = new Float32Array(buffer.length);
  for (let c = 0; c < buffer.numberOfChannels; c++) {
    const data = buffer.getChannelData(c);
    for (let i = 0; i < mono.length; i++) mono[i] += data[i];
  }
  const scale = 1 / buffer.numberOfChannels;
  for (let i = 0; i < mono.length; i++) mono[i] *= scale;
  return mono;
}

interface Chain {
  dsp: InstanceType<Oscillux["Dsp"]>;
  mapper: InstanceType<Oscillux["Mapper"]>;
  sampler: InstanceType<Oscillux["ReactionSampler"]>;
  /** The next PCM index to feed. */
  at: number;
  /** The media time of the newest analysed frame, ms. */
  last: number;
}

/** Oscillux's reaction values for one decoded track, sampled at any media time. */
export class TrackAnalysis {
  private readonly osc: Oscillux;
  private readonly pcm: Float32Array;
  private readonly config: InstanceType<Oscillux["DspConfig"]>;
  private readonly latencyMs: number;
  private chain: Chain | null = null;
  private readonly frame = silentFrame();

  constructor(osc: Oscillux, pcm: Float32Array) {
    this.osc = osc;
    this.pcm = pcm;
    const config = new osc.DspConfig();
    config.behaviorVersion = 4;
    config.sampleRateHz = ANALYSIS_RATE;
    config.spectrumMaxHz = Math.min(config.spectrumMaxHz, ANALYSIS_RATE / 2);
    const problem = config.validate();
    if (problem !== undefined) throw new Error(`visualizer: DSP config ${problem}`);
    this.config = config;
    const reaction = new osc.ReactionConfig(4, ANALYSIS_RATE);
    this.latencyMs = reaction.analysisLatencyMs;
    reaction.free();
  }

  /** The reaction values at media time t (ms), or null before the analysis reaches it. */
  sample(t: number): AudioFrame | null {
    const durationMs = (this.pcm.length / ANALYSIS_RATE) * 1000;
    if (t < 0 || t > durationMs) return null;
    let chain = this.chain;
    if (!chain || t < chain.last - JUMP_MS || t > chain.last + JUMP_MS) chain = this.restart(t);
    this.feed(chain, t);
    const s = chain.sampler;
    if (s.sample(t) !== 0) return null;
    const memory = this.osc.wasmMemory().buffer;
    const mapped = new Float64Array(memory, s.outputPtr(), MAPPED.length);
    const values = this.frame.values;
    for (let i = 0; i < MAPPED.length; i++) values[MAPPED[i]] = mapped[i];
    this.frame.spectrum.set(new Float32Array(memory, s.outputPtr() + MAPPED.length * 8, 64));
    values.beat = s.beat();
    values.beatPhase = s.beatPhase();
    values.beatCount = s.beatCount();
    values.barPhase = s.barPhase();
    values.downbeat = s.downbeat();
    return this.frame;
  }

  free(): void {
    this.dropChain();
    this.config.free();
  }

  // A new chain that starts WARMUP_MS before t.
  private restart(t: number): Chain {
    this.dropChain();
    const osc = this.osc;
    const startMs = Math.max(0, t - WARMUP_MS);
    const dsp = new osc.Dsp(this.config, CHUNK, Math.ceil(CHUNK / this.config.hopSamples) + 2);
    if (dsp.beginSegment(startMs, false) !== 0) {
      dsp.free();
      throw new Error("visualizer: DSP segment refused");
    }
    const mapping = osc.MappingConfig.fromDsp(this.config);
    const mapper = new osc.Mapper(mapping);
    mapping.free();
    mapper.setLevelSource(osc.LevelSource.Relative);
    const reaction = new osc.ReactionConfig(4, ANALYSIS_RATE);
    reaction.capacity = 1024;
    reaction.outputLatencyMs = 0;
    const sampler = new osc.ReactionSampler(reaction);
    reaction.free();
    const at = Math.floor((startMs / 1000) * ANALYSIS_RATE);
    this.chain = { dsp, mapper, sampler, at, last: startMs };
    return this.chain;
  }

  // Feeds PCM until the analysis covers t or the track ends.
  private feed(chain: Chain, t: number): void {
    const { dsp, mapper, sampler } = chain;
    while (chain.last - this.latencyMs <= t && chain.at < this.pcm.length) {
      const n = Math.min(CHUNK, this.pcm.length - chain.at);
      new Float32Array(this.osc.wasmMemory().buffer, dsp.inputPtr(), n).set(
        this.pcm.subarray(chain.at, chain.at + n),
      );
      if (dsp.pushExt(n, 1) !== 0) throw new Error("visualizer: DSP push refused");
      const view = new DataView(this.osc.wasmMemory().buffer);
      const frameBytes = dsp.frameBytes();
      for (let i = 0; i < dsp.produced(); i++) {
        mapper.processExt(dsp, i, false);
        const mediaTime = view.getFloat64(dsp.outputPtr() + i * frameBytes + 16, true);
        if (sampler.pushWithBeat(mediaTime, mapper, dsp, i) !== 0) {
          sampler.reset();
          sampler.pushWithBeat(mediaTime, mapper, dsp, i);
        }
        chain.last = mediaTime;
      }
      const consumed = dsp.consumed();
      if (consumed <= 0) break;
      chain.at += consumed;
    }
  }

  private dropChain(): void {
    if (!this.chain) return;
    this.chain.sampler.free();
    this.chain.mapper.free();
    this.chain.dsp.free();
    this.chain = null;
  }
}

/**
 * Eases `shown` toward `target` by weight w (0..1) in place: the levels and spectrum glide so a
 * track change or a late analysis does not snap the visual. Beat phase and count are clock
 * values, not levels, so they are taken from the target as they are.
 */
export function easeFrame(shown: AudioFrame, target: AudioFrame, w: number): AudioFrame {
  for (const key of MAPPED) shown.values[key] += (target.values[key] - shown.values[key]) * w;
  for (const key of ["beat", "beatPhase", "beatCount", "barPhase", "downbeat"] as const) {
    shown.values[key] = target.values[key];
  }
  for (let i = 0; i < shown.spectrum.length; i++) {
    shown.spectrum[i] += (target.spectrum[i] - shown.spectrum[i]) * w;
  }
  return shown;
}
