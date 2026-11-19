import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";
import * as osc from "../../../../../vendor/oscillux/wasm/oscillux.js";
import { CADENCE_WINDOW, CadenceGate } from "./cadence";

osc.initSync({
  module: readFileSync(resolve(__dirname, "../../../../../vendor/oscillux/wasm/oscillux_bg.wasm")),
});

// Runs a frame clock through the gate into the Oscillux governor, as Renderer.draw does, and
// returns the governor's level after every frame.
function levels(intervals: number[]): number[] {
  const quality = new osc.GovernorConfig();
  quality.maxLevel = 3;
  quality.initialLevel = 3;
  const governor = new osc.Governor(quality);
  quality.free();
  const gate = new CadenceGate();
  const out = intervals.map((ms) => {
    const sample = gate.sample(ms);
    if (sample !== null) governor.record(sample);
    return governor.level;
  });
  governor.free();
  return out;
}

const steady = (ms: number, n: number, jitter = 0) =>
  Array.from({ length: n }, (_, i) => ms + (i % 2 ? jitter : -jitter));

describe("CadenceGate (CORE-2483)", () => {
  it("keeps full quality at a steady 30 Hz cap", () => {
    expect(levels(steady(1000 / 30, 1200, 0.8)).at(-1)).toBe(3);
  });

  it("would demote at the cap without the gate", () => {
    const quality = new osc.GovernorConfig();
    quality.maxLevel = 3;
    quality.initialLevel = 3;
    const governor = new osc.Governor(quality);
    quality.free();
    for (const ms of steady(1000 / 30, 300)) governor.record(ms);
    expect(governor.level).toBeLessThan(3);
    governor.free();
  });

  it("still demotes a render slower than the cap", () => {
    expect(levels(steady(48, 600, 6)).at(-1)).toBeLessThan(3);
  });

  it("still demotes a steady render a little slower than the cap", () => {
    expect(levels(steady(35, 1200, 0.3)).at(-1)).toBeLessThan(3);
  });

  it("still demotes an unsteady cadence around the cap", () => {
    const rough = Array.from({ length: 600 }, (_, i) => (i % 3 === 0 ? 55 : 30));
    expect(levels(rough).at(-1)).toBeLessThan(3);
  });

  it("holds samples while it learns the cadence, and passes pauses through", () => {
    const gate = new CadenceGate();
    for (let i = 0; i < CADENCE_WINDOW - 1; i++) expect(gate.sample(16.7)).toBeNull();
    expect(gate.sample(16.7)).toBe(16.7);
    expect(gate.sample(400)).toBe(400);
  });
});
