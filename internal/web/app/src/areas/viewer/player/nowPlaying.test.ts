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

  it("keeps the played prefix and shuffles only new items into the unplayed remainder when the queue grows (CORE-3177)", () => {
    const initial = Array.from({ length: 50 }, (_, i) => ({ id: String(i) }));
    const shuffled = modes({ shuffle: true, repeat: "off", seed: 1, anchor: "0" });
    const before = ids(playOrder(initial, shuffled, "44"));
    const at44 = before.indexOf("44");
    expect(at44).toBe(45);
    expect(before.slice(0, at44 + 1)).toContain("10");

    const grown = Array.from({ length: 60 }, (_, i) => ({ id: String(i) }));
    const after = ids(playOrder(grown, shuffled, "44"));
    expect(after.slice(0, at44 + 1)).toEqual(before.slice(0, at44 + 1));
    for (let i = 50; i < 60; i++) {
      expect(after.indexOf(String(i))).toBeGreaterThan(at44);
    }
    expect(modeNeighbours(grown, "44", shuffled).next?.id).not.toBe("10");

    // Walk from anchor 0 to the end across a second top-up (60..69) when 4 remain:
    // every item plays once and none repeats.
    let currentQueue = initial;
    const played: string[] = ["0"];
    let cursor = "0";
    let toppedUp50 = false;
    let toppedUp60 = false;
    while (true) {
      if (!toppedUp50 && upcomingItems(currentQueue, cursor, shuffled, 5).length < 5) {
        currentQueue = grown;
        toppedUp50 = true;
      } else if (
        toppedUp50 &&
        !toppedUp60 &&
        upcomingItems(currentQueue, cursor, shuffled, 5).length < 5
      ) {
        currentQueue = Array.from({ length: 70 }, (_, i) => ({ id: String(i) }));
        toppedUp60 = true;
      }
      const next = modeNeighbours(currentQueue, cursor, shuffled).next;
      if (!next) break;
      played.push(next.id);
      cursor = next.id;
    }
    expect(played).toHaveLength(70);
    expect(new Set(played).size).toBe(70);
  });

  it("pins an explicit Play next track immediately after the current track when shuffle is on (CORE-3250)", () => {
    const initial = [
      { id: "A", title: "A" },
      { id: "B", title: "B" },
      { id: "C", title: "C" },
      { id: "D", title: "D" },
      { id: "E", title: "E" },
    ];
    const shuffled: PlayModes = { shuffle: true, repeat: "off", seed: 1, anchor: "A" };
    expect(playOrder(initial, shuffled, "A").map((item) => item.id)).toEqual([
      "A",
      "E",
      "C",
      "B",
      "D",
    ]);

    const reordered = [
      { id: "A", title: "A" },
      { id: "D", title: "D" },
      { id: "B", title: "B" },
      { id: "C", title: "C" },
      { id: "E", title: "E" },
    ];
    expect(modeNeighbours(reordered, "A", shuffled).next?.id).toBe("D");
    expect(playOrder(reordered, shuffled, "A").map((item) => item.id)).toEqual([
      "A",
      "D",
      "E",
      "C",
      "B",
    ]);

    // A subsequent radio top-up preserves the unplayed pinned track D right after A.
    const toppedUp = [
      ...reordered,
      { id: "F", title: "F" },
      { id: "G", title: "G" },
      { id: "H", title: "H" },
      { id: "I", title: "I" },
      { id: "J", title: "J" },
    ];
    expect(modeNeighbours(toppedUp, "A", shuffled).next?.id).toBe("D");
    expect(
      playOrder(toppedUp, shuffled, "A")
        .slice(0, 2)
        .map((item) => item.id),
    ).toEqual(["A", "D"]);
  });
});
