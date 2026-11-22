import type { AudioQuality } from "../lib/playback";

// Now Playing for audio (CORE-1637): the quality badge's words and the
// shuffle and repeat modes. The modes are a view over the queue that already
// drives playback (a card queue or the server Play Queue): they reorder or wrap
// it, never hold a second list.

export type RepeatMode = "off" | "all" | "one";

export interface PlayModes {
  shuffle: boolean;
  repeat: RepeatMode;
  /** Fixed when shuffle turns on, so the shuffled order stays put as items change. */
  seed: number;
  /** The item playing when shuffle turned on: it leads the shuffled order. */
  anchor: string;
}

export const DEFAULT_PLAY_MODES: PlayModes = {
  shuffle: false,
  repeat: "off",
  seed: 1,
  anchor: "",
};

const REPEAT_CYCLE: readonly RepeatMode[] = ["off", "all", "one"];

export function nextRepeat(mode: RepeatMode): RepeatMode {
  return REPEAT_CYCLE[(REPEAT_CYCLE.indexOf(mode) + 1) % REPEAT_CYCLE.length] ?? "off";
}

/** Whether the modes change which item comes before or after another. */
export function modesReorder(modes: PlayModes): boolean {
  return modes.shuffle || modes.repeat === "all";
}

/**
 * The order the queue plays in: as given, or shuffled by the seed with the
 * anchor first, so turning shuffle on keeps the playing item and plays every
 * other one once before any repeats.
 */
export function playOrder<T extends { id: string }>(
  queue: readonly T[],
  modes: PlayModes,
): readonly T[] {
  if (!modes.shuffle || queue.length < 2) return queue;
  const rest = queue.filter((item) => item.id !== modes.anchor);
  const random = mulberry32(modes.seed);
  for (let i = rest.length - 1; i > 0; i--) {
    const j = Math.floor(random() * (i + 1));
    [rest[i], rest[j]] = [rest[j]!, rest[i]!];
  }
  const anchor = queue.find((item) => item.id === modes.anchor);
  return anchor ? [anchor, ...rest] : rest;
}

/** The items before and after `id` under the modes; never `id` itself. */
export function modeNeighbours<T extends { id: string }>(
  queue: readonly T[] | undefined,
  id: string | undefined,
  modes: PlayModes,
): { previous?: T; next?: T } {
  if (!queue || !id) return {};
  const order = playOrder(queue, modes);
  const index = order.findIndex((item) => item.id === id);
  if (index < 0) return {};
  const wrap = modes.repeat === "all";
  const previous = order[index - 1] ?? (wrap ? order[order.length - 1] : undefined);
  const next = order[index + 1] ?? (wrap ? order[0] : undefined);
  return {
    ...(previous && previous.id !== id ? { previous } : {}),
    ...(next && next.id !== id ? { next } : {}),
  };
}

/** Up to `count` items that play after `id`, wrapping under repeat all. */
export function upcomingItems<T extends { id: string }>(
  queue: readonly T[] | undefined,
  id: string | undefined,
  modes: PlayModes,
  count: number,
): T[] {
  if (!queue || !id) return [];
  const order = playOrder(queue, modes);
  const index = order.findIndex((item) => item.id === id);
  if (index < 0) return [];
  const after = order.slice(index + 1);
  const ahead = modes.repeat === "all" ? [...after, ...order.slice(0, index)] : after;
  return ahead.slice(0, count);
}

// A small seeded generator: the same seed gives the same shuffle on every render.
function mulberry32(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a = (a + 0x6d2b79f5) >>> 0;
    let t = a;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

const MODES_KEY = "beamfall.player.playModes";

/** The modes this browser last used; defaults when storage is empty or blocked. */
export function readPlayModes(): PlayModes {
  try {
    const raw = window.localStorage.getItem(MODES_KEY);
    if (!raw) return DEFAULT_PLAY_MODES;
    const parsed = JSON.parse(raw) as Partial<PlayModes>;
    return {
      shuffle: parsed.shuffle === true,
      repeat: REPEAT_CYCLE.includes(parsed.repeat as RepeatMode)
        ? (parsed.repeat as RepeatMode)
        : "off",
      seed: Number.isInteger(parsed.seed) ? (parsed.seed as number) : 1,
      anchor: typeof parsed.anchor === "string" ? parsed.anchor : "",
    };
  } catch {
    return DEFAULT_PLAY_MODES;
  }
}

export function writePlayModes(modes: PlayModes): void {
  try {
    window.localStorage.setItem(MODES_KEY, JSON.stringify(modes));
  } catch {
    /* blocked storage: the modes last for this page only */
  }
}

export interface QualityWords {
  direct: string;
  transcode: string;
  /** "24-bit" */
  bits: (bits: number) => string;
  /** "96 kHz" from 96; "44.1 kHz" from 44.1 */
  rate: (kHz: number) => string;
}

/**
 * The quality badge: the source codec, its bit depth and sample rate when the
 * probe recorded them, and whether the stream delivers it unchanged.
 */
export function audioQualityLabel(quality: AudioQuality, words: QualityWords): string {
  const parts: string[] = [];
  if (quality.codec) parts.push(codecName(quality.codec));
  const resolution = [
    quality.bitDepth ? words.bits(quality.bitDepth) : "",
    quality.sampleRate ? words.rate(Math.round(quality.sampleRate / 10) / 100) : "",
  ].filter(Boolean);
  if (resolution.length) parts.push(resolution.join(" / "));
  parts.push(quality.delivery === "direct" ? words.direct : words.transcode);
  return parts.join(" · ");
}

const CODEC_NAMES: Record<string, string> = {
  opus: "Opus",
  vorbis: "Vorbis",
  wavpack: "WavPack",
  pcm_s16le: "PCM",
  pcm_s24le: "PCM",
  pcm_s32le: "PCM",
  pcm_f32le: "PCM",
};

function codecName(codec: string): string {
  const key = codec.toLowerCase();
  return CODEC_NAMES[key] ?? (key.startsWith("pcm_") ? "PCM" : key.toUpperCase());
}
