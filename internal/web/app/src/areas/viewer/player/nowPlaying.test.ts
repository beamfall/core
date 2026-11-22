import { describe, expect, it } from "vitest";
import {
  audioQualityLabel,
  DEFAULT_PLAY_MODES,
  modeNeighbours,
  nextRepeat,
  type PlayModes,
  playOrder,
  upcomingItems,
} from "./nowPlaying";

const words = {
  direct: "Direct",
  transcode: "Transcode",
  bits: (bits: number) => `${bits}-bit`,
  rate: (kHz: number) => `${kHz} kHz`,
};

const queue = ["a", "b", "c", "d", "e"].map((id) => ({ id }));
const ids = (items: readonly { id: string }[]) => items.map((item) => item.id);
const modes = (change: Partial<PlayModes>): PlayModes => ({ ...DEFAULT_PLAY_MODES, ...change });

describe("audioQualityLabel", () => {
  it("names the codec, resolution and delivery", () => {
    expect(
      audioQualityLabel(
        { codec: "flac", bitDepth: 24, sampleRate: 96000, delivery: "direct" },
        words,
      ),
    ).toBe("FLAC · 24-bit / 96 kHz · Direct");
    expect(
      audioQualityLabel({ codec: "opus", sampleRate: 44100, delivery: "transcode" }, words),
    ).toBe("Opus · 44.1 kHz · Transcode");
    expect(audioQualityLabel({ codec: "pcm_s24le", delivery: "direct" }, words)).toBe(
      "PCM · Direct",
    );
    expect(audioQualityLabel({ codec: "flac", bitDepth: 16, delivery: "" }, words)).toBe(
      "FLAC · 16-bit",
    );
  });
});

describe("play modes", () => {
  it("cycles repeat off, all, one", () => {
    expect(nextRepeat("off")).toBe("all");
    expect(nextRepeat("all")).toBe("one");
    expect(nextRepeat("one")).toBe("off");
  });

  it("keeps queue order without shuffle and stops at the ends", () => {
    expect(modeNeighbours(queue, "a", DEFAULT_PLAY_MODES)).toEqual({ next: { id: "b" } });
    expect(modeNeighbours(queue, "e", DEFAULT_PLAY_MODES)).toEqual({ previous: { id: "d" } });
  });

  it("wraps under repeat all and never offers the item itself", () => {
    const all = modes({ repeat: "all" });
    expect(modeNeighbours(queue, "e", all).next).toEqual({ id: "a" });
    expect(modeNeighbours(queue, "a", all).previous).toEqual({ id: "e" });
    expect(modeNeighbours([{ id: "a" }], "a", all)).toEqual({});
    expect(ids(upcomingItems(queue, "d", all, 3))).toEqual(["e", "a", "b"]);
  });

  it("shuffles every item once with the anchor first, the same way each time", () => {
    const shuffled = modes({ shuffle: true, seed: 42, anchor: "c" });
    const order = ids(playOrder(queue, shuffled));
    expect(order[0]).toBe("c");
    expect([...order].sort()).toEqual(["a", "b", "c", "d", "e"]);
    expect(ids(playOrder(queue, shuffled))).toEqual(order);
    expect(modeNeighbours(queue, "c", shuffled).next?.id).toBe(order[1]);
    expect(ids(upcomingItems(queue, "c", shuffled, 2))).toEqual(order.slice(1, 3));
  });

  it("keeps a locked playing item's place and steps only to playable items", () => {
    const locked = new Set(["c", "d"]);
    const playable = (item: { id: string }) => !locked.has(item.id);
    expect(modeNeighbours(queue, "c", DEFAULT_PLAY_MODES, playable)).toEqual({
      previous: { id: "b" },
      next: { id: "e" },
    });
    const all = modes({ repeat: "all" });
    expect(modeNeighbours(queue, "e", all, playable).next).toEqual({ id: "a" });
    expect(ids(upcomingItems(queue, "c", all, 4, playable))).toEqual(["e", "a", "b"]);
  });
});
