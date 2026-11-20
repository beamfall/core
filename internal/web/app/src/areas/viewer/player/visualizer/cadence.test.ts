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

  // CORE-2720: a page the browser throttles without hiding it draws 30 to 250 ms apart with the
  // GPU idle. Those intervals are not the render's.
  it("records slow intervals after a frame the GPU kept up with as the target", () => {
    const gate = new CadenceGate();
    for (let i = 0; i < CADENCE_WINDOW; i++) gate.sample(16.7);
    expect(gate.sample(200, 3)).toBeCloseTo(1000 / 60);
    expect(gate.sample(16.7, 3)).toBe(16.7);
    expect(gate.sample(200, null)).toBe(200);
    expect(gate.sample(400, 3)).toBe(400);
  });

  // Codex review of CORE-2720: a check that ran late bounds the GPU's time only by when it ran.
  it("records a slow interval as the bound a late fence check gives", () => {
    const gate = new CadenceGate();
    for (let i = 0; i < CADENCE_WINDOW; i++) gate.sample(16.7);
    expect(gate.sample(50, 45)).toBe(45);
    expect(gate.sample(40, 45)).toBe(40);
  });

  it("keeps full quality through a throttled page", () => {
    const quality = new osc.GovernorConfig();
    quality.maxLevel = 3;
    quality.initialLevel = 3;
    const governor = new osc.Governor(quality);
    quality.free();
    const gate = new CadenceGate();
    for (const ms of [...steady(16.7, 60), ...steady(120, 600, 80)]) {
      const sample = gate.sample(ms, ms > 20 ? 3 : null);
      if (sample !== null) governor.record(sample);
    }
    expect(governor.level).toBe(3);
    governor.free();
  });

  // Codex review of CORE-2720: skipping those intervals starved a demoted governor of the fast
  // samples it recovers on, so a steady 30 Hz cap left it at the bottom for good.
  it("recovers from a demotion at a steady cap the GPU keeps up with", () => {
    const quality = new osc.GovernorConfig();
    quality.maxLevel = 3;
    quality.initialLevel = 0;
    const governor = new osc.Governor(quality);
    quality.free();
    const gate = new CadenceGate();
    for (let i = 0; i < 3000; i++) {
      const sample = gate.sample(1000 / 30, 3);
      if (sample !== null) governor.record(sample);
    }
    expect(governor.level).toBe(3);
    governor.free();
  });
});
