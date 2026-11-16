import { routeSyntheticAlbum, type SyntheticTrack } from "./lib/synthetic-audio";
import { expect, test } from "./lib/test";

// Gapless album playback (CORE-1634). The player projects' testMatch
// (/player\.spec\.ts/) picks this file up. Focused run:
//
//   npx playwright test e2e/gapless-player.spec.ts --project=player-desktop
//
// Three short tone tracks, each wrapped in the silence its stream declares as
// encoder delay and padding, play through two joins. Everything the page sends
// to the Web Audio output is recorded, and the silence between the tones is
// measured at each join: the first (media element to decoded track) and the
// second (decoded to decoded, chained on the audio clock). Untrimmed, the
// declared silence alone would leave about 82 ms.
//
// The player never routes its media element through Web Audio; the decoded
// tracks play on a context of their own. To hear both on one clock, this spec
// routes the element into that context itself, as a recording tap: it measures
// where the join is scheduled, not the output latency a device adds to each.

const SAMPLE_RATE = 44100;
const gapless = { delaySamples: 2112, paddingSamples: 1500 };
const TRACKS: SyntheticTrack[] = [
  { id: "media-gapless-one", title: "Gapless One", frequency: 440, seconds: 4, gapless },
  { id: "media-gapless-two", title: "Gapless Two", frequency: 660, seconds: 4, gapless },
  { id: "media-gapless-three", title: "Gapless Three", frequency: 880, seconds: 4, gapless },
];

/** One tone peaks at 1/8 full scale; two at once go above this. */
const SINGLE_TONE_CEILING = 0.14;
/** Silence (or overlap) the spec allows at the first join, element to decoded (ms). */
const FIRST_JOIN_TOLERANCE_MS = 20;
/** Silence (or overlap) the spec allows at a later join, decoded to decoded (ms). */
const CHAINED_JOIN_TOLERANCE_MS = 2;

declare global {
  interface Window {
    __gaplessCapture?: { sampleRate: number; chunks: { frame: number; data: number[] }[] }[];
  }
}

// An AudioWorklet recorder: it runs on the audio thread, so it never misses a
// render quantum the way a main-thread recorder can under a busy page. Its
// module loads from a blob: URL, which the page's script-src would refuse and
// Playwright cannot route (worklet fetches bypass page.route), so this spec
// bypasses the CSP; nothing under test depends on it.
const RECORDER_SOURCE = `
class OutputRecorder extends AudioWorkletProcessor {
  constructor() { super(); this.blocks = []; this.frame = -1; }
  process(inputs) {
    const channel = inputs[0] && inputs[0][0];
    if (this.frame < 0) this.frame = currentFrame;
    this.blocks.push(channel ? Float32Array.from(channel) : new Float32Array(128));
    if (this.blocks.length === 32) {
      const data = new Float32Array(32 * 128);
      this.blocks.forEach((block, i) => data.set(block, i * 128));
      this.port.postMessage({ frame: this.frame, data });
      this.blocks = [];
      this.frame = -1;
    }
    return true;
  }
}
registerProcessor("output-recorder", OutputRecorder);
`;

// Every AudioContext's destination is replaced by a pass-through node that the
// recorder listens to, so whatever the page connects to the output is captured
// in order, and the page's media element is routed into the same tap.
function recordAudioOutput(recorderSource: string) {
  const Real = window.AudioContext;
  const recorderURL = URL.createObjectURL(new Blob([recorderSource], { type: "text/javascript" }));
  window.__gaplessCapture = [];
  class Recording extends Real {
    constructor(options?: AudioContextOptions) {
      super(options);
      const output = super.destination;
      const tap = this.createGain();
      tap.connect(output);
      Object.defineProperty(this, "destination", { value: tap });
      const element = document.querySelector("video");
      if (element) this.createMediaElementSource(element).connect(tap);
      const capture = {
        sampleRate: this.sampleRate,
        chunks: [] as { frame: number; data: number[] }[],
      };
      window.__gaplessCapture?.push(capture);
      void this.audioWorklet.addModule(recorderURL).then(() => {
        const recorder = new AudioWorkletNode(this, "output-recorder", {
          channelCount: 1,
          channelCountMode: "explicit",
        });
        recorder.port.onmessage = (event) => {
          capture.chunks.push({ frame: event.data.frame, data: Array.from(event.data.data) });
        };
        const sink = this.createGain();
        sink.gain.value = 0;
        tap.connect(recorder);
        recorder.connect(sink);
        sink.connect(output);
      });
    }
  }
  window.AudioContext = Recording;
}

interface Join {
  from: number;
  to: number;
  /** Longest near-silent run around the tone change (ms). */
  silenceMs: number;
  /** How long both tones sound at once around the tone change (ms). */
  overlapMs: number;
  /** Sample index of the first window of the new tone. */
  at: number;
}

// The track tone of each 10 ms window, by zero crossings (0 when silent or mixed).
function measureJoins(samples: Float32Array, sampleRate: number): Join[] {
  const window = Math.round(sampleRate / 100);
  const pitches: number[] = [];
  for (let start = 0; start + window <= samples.length; start += window) {
    let crossings = 0;
    let peak = 0;
    for (let i = start; i < start + window; i++) {
      peak = Math.max(peak, Math.abs(samples[i]));
      if (i > start && samples[i - 1] < 0 !== samples[i] < 0) crossings++;
    }
    // A window straddling a join (or silent) matches no tone and is skipped.
    const estimate = crossings * 50;
    const tone = TRACKS.find((track) => Math.abs(track.frequency - estimate) <= 60);
    pitches.push(peak < 0.05 || !tone ? 0 : tone.frequency);
  }
  const joins: Join[] = [];
  let last = 0;
  let lastIndex = 0;
  for (let index = 0; index < pitches.length; index++) {
    const pitch = pitches[index];
    if (pitch === 0) continue;
    if (last !== 0 && pitch !== last) {
      // Longest run below 0.01 between the last window of one tone and the first of the next.
      let longest = 0;
      let run = 0;
      for (let i = lastIndex * window; i < (index + 1) * window; i++) {
        run = Math.abs(samples[i]) < 0.01 ? run + 1 : 0;
        longest = Math.max(longest, run);
      }
      // Span of samples louder than one tone, from the old tone's last window on.
      let first = -1;
      let lastLoud = -1;
      for (let i = lastIndex * window; i < (index + 1) * window; i++) {
        if (Math.abs(samples[i]) < SINGLE_TONE_CEILING) continue;
        if (first < 0) first = i;
        lastLoud = i;
      }
      joins.push({
        from: last,
        to: pitch,
        silenceMs: (longest / sampleRate) * 1000,
        overlapMs: first < 0 ? 0 : ((lastLoud - first + 1) / sampleRate) * 1000,
        at: index * window,
      });
    }
    last = pitch;
    lastIndex = index;
  }
  return joins;
}

// The app's service worker would answer the page's API requests itself, and
// Firefox and WebKit do not let page.route see requests a service worker makes.
test.use({ bypassCSP: true, serviceWorkers: "block" });

test("album tracks join without a gap, the declared silence trimmed", async ({ page }) => {
  test.setTimeout(60_000);
  await page.addInitScript(recordAudioOutput, RECORDER_SOURCE);
  await routeSyntheticAlbum(page, TRACKS);
  await page.goto(`/web/watch/${TRACKS[0].id}`);
  const video = page.locator("video");
  await expect(page.getByRole("heading", { name: "Gapless One", level: 2 })).toBeVisible();
  const expectedDuration = 4 + (gapless.delaySamples + gapless.paddingSamples) / SAMPLE_RATE;
  await expect
    .poll(() => video.evaluate((element: HTMLVideoElement) => element.duration))
    .toBeCloseTo(expectedDuration, 2);
  // A real click gives the page the activation its AudioContext needs.
  const paused = await video.evaluate((element: HTMLVideoElement) => element.paused);
  if (paused) await page.getByRole("button", { name: "Play", exact: true }).click();
  else await page.getByRole("heading", { name: "Gapless One", level: 2 }).click();

  await expect(page.getByRole("heading", { name: "Gapless Three", level: 2 })).toBeVisible({
    timeout: 20_000,
  });
  // Let the third track play past the second join's recording window.
  await expect
    .poll(() => video.evaluate((element: HTMLVideoElement) => element.currentTime), {
      timeout: 10_000,
    })
    .toBeGreaterThan(1.5);
  // The silent element follows the heard track closely.
  await page.waitForTimeout(500);

  const capture = await page.evaluate(() => {
    const contexts = window.__gaplessCapture ?? [];
    const busiest = contexts.reduce(
      (best, context) => (context.chunks.length > (best?.chunks.length ?? 0) ? context : best),
      contexts[0],
    );
    if (!busiest) return null;
    // Render quanta are numbered; a missing one would hide or fake a gap. Each break is
    // kept with its sample offset, so only one near a join fails the run (an audio thread
    // starting up can skip quanta well before the first join).
    const breaks = busiest.chunks.flatMap((chunk, i, all) => {
      if (i === 0) return [];
      const expected = all[i - 1].frame + all[i - 1].data.length;
      return chunk.frame === expected
        ? []
        : [
            {
              at: i * chunk.data.length,
              note: `chunk ${i}: frame ${chunk.frame}, expected ${expected}`,
            },
          ];
    });
    return {
      sampleRate: busiest.sampleRate,
      breaks,
      samples: busiest.chunks.flatMap((chunk) => chunk.data),
    };
  });
  expect(capture, "the page made no AudioContext").not.toBeNull();
  if (!capture) return;
  const samples = Float32Array.from(capture.samples);
  const joins = measureJoins(samples, capture.sampleRate);
  // Logged so a run states the measured silence, not only pass or fail.
  console.log(`[gapless] joins ${JSON.stringify(joins)}`);
  const second = capture.sampleRate;
  const nearJoin = capture.breaks.filter((b) =>
    joins.some((join) => Math.abs(b.at - join.at) <= second),
  );
  expect(
    nearJoin.map((b) => b.note),
    "the recorder missed render quanta near a join",
  ).toEqual([]);
  expect(joins.map((join) => [join.from, join.to])).toEqual([
    [440, 660],
    [660, 880],
  ]);
  expect(joins[0].silenceMs).toBeLessThanOrEqual(FIRST_JOIN_TOLERANCE_MS);
  expect(joins[0].overlapMs).toBeLessThanOrEqual(FIRST_JOIN_TOLERANCE_MS);
  expect(joins[1].silenceMs).toBeLessThanOrEqual(CHAINED_JOIN_TOLERANCE_MS);
  expect(joins[1].overlapMs).toBeLessThanOrEqual(CHAINED_JOIN_TOLERANCE_MS);
  // Away from the joins, the muted element never sounds under the decoded
  // track: two tones at once would peak above a single tone.
  const near = Math.round((capture.sampleRate * 2 * FIRST_JOIN_TOLERANCE_MS) / 1000);
  let peak = 0;
  samples.forEach((sample, i) => {
    if (joins.some((join) => Math.abs(i - join.at) <= near)) return;
    peak = Math.max(peak, Math.abs(sample));
  });
  expect(peak).toBeLessThan(SINGLE_TONE_CEILING);
});
