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
// measured at each join. Untrimmed, the declared silence alone would leave
// about 82 ms.
//
// The player never routes its media element through Web Audio. A decodable
// track that starts from silence plays on the player's own context from its
// first sample, the element held muted beside it, so every track here is on
// that one context and every join is decoded to decoded, chained on its clock.
// The recording tap sits before the device: it measures the joins as
// scheduled and rendered, not what a device's output path adds afterwards. A
// first track the element played instead would be missing from the capture,
// and the element must stay muted throughout.

const SAMPLE_RATE = 44100;
const gapless = { delaySamples: 2112, paddingSamples: 1500 };
const TRACKS: SyntheticTrack[] = [
  { id: "media-gapless-one", title: "Gapless One", frequency: 440, seconds: 4, gapless },
  { id: "media-gapless-two", title: "Gapless Two", frequency: 660, seconds: 4, gapless },
  { id: "media-gapless-three", title: "Gapless Three", frequency: 880, seconds: 4, gapless },
];

/** One tone peaks at 1/8 full scale; two at once go above this. */
const SINGLE_TONE_CEILING = 0.14;
/** Silence (or overlap) the spec allows at a join between decoded tracks (ms). */
const JOIN_TOLERANCE_MS = 2;
/** How far either side of a join the single-tone peak check looks away (ms). */
const PEAK_WINDOW_MS = 20;

declare global {
  interface Window {
    __gaplessCapture?: { sampleRate: number; chunks: { frame: number; data: number[] }[] }[];
    /** Times the element played unmuted, which the player must never let it. */
    __gaplessAudible?: number;
  }
}

// An AudioWorklet recorder: it runs on the audio thread, so it never misses a
// render quantum the way a main-thread recorder can under a busy page. Its
// module loads from a blob: URL, which the page's script-src would refuse and
// Playwright cannot route (worklet fetches bypass page.route), so this spec
// bypasses the CSP; nothing under test depends on it. WebKit refuses the blob:
// module regardless and records on the main thread instead (below).
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
// in order. The element is not routed: it must stay muted, which is counted.
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
      const capture = {
        sampleRate: this.sampleRate,
        chunks: [] as { frame: number; data: number[] }[],
      };
      window.__gaplessCapture?.push(capture);
      const sink = this.createGain();
      sink.gain.value = 0;
      sink.connect(output);
      void this.audioWorklet.addModule(recorderURL).then(
        () => {
          const recorder = new AudioWorkletNode(this, "output-recorder", {
            channelCount: 1,
            channelCountMode: "explicit",
          });
          recorder.port.onmessage = (event) => {
            capture.chunks.push({ frame: event.data.frame, data: Array.from(event.data.data) });
          };
          tap.connect(recorder);
          recorder.connect(sink);
        },
        () => {
          // WebKit refuses a blob: worklet module even with the CSP bypassed. A
          // main-thread recorder stands in; each block carries its own frame
          // number, so a block it misses still shows as a break. WebKit's
          // playbackTime can be one render quantum off while the input stays
          // contiguous, so a stamp within a quantum of the next frame counts as it;
          // a missed block moves the stamp by a whole buffer.
          const size = 4096;
          const quantum = 128;
          const recorder = this.createScriptProcessor(size, 1, 1);
          let next = -1;
          recorder.onaudioprocess = (event) => {
            const stamp = Math.round(event.playbackTime * this.sampleRate);
            const frame = next >= 0 && Math.abs(stamp - next) <= quantum ? next : stamp;
            next = frame + size;
            capture.chunks.push({
              frame,
              data: Array.from(event.inputBuffer.getChannelData(0)),
            });
          };
          tap.connect(recorder);
          recorder.connect(sink);
        },
      );
    }
  }
  window.AudioContext = Recording;
  window.__gaplessAudible = 0;
  document.addEventListener(
    "timeupdate",
    (event) => {
      const element = event.target as HTMLMediaElement;
      if (!element.paused && !element.muted && window.__gaplessAudible !== undefined) {
        window.__gaplessAudible++;
      }
    },
    true,
  );
}

// Refuses media playback until the first pointer press, as a browser that
// wants a gesture for sound does. Engines differ in what they let autoplay
// (Playwright's WebKit allows it, a strict Safari setting does not), and a
// page that started on its own would race the spec's click on Play.
function blockAutoplay() {
  const play = HTMLMediaElement.prototype.play;
  let activated = false;
  document.addEventListener(
    "pointerdown",
    () => {
      activated = true;
    },
    true,
  );
  HTMLMediaElement.prototype.play = function (this: HTMLMediaElement) {
    if (activated) return play.call(this);
    return Promise.reject(new DOMException("No gesture yet", "NotAllowedError"));
  };
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
  await page.addInitScript(blockAutoplay);
  await page.addInitScript(recordAudioOutput, RECORDER_SOURCE);
  await routeSyntheticAlbum(page, TRACKS);
  await page.goto(`/web/watch/${TRACKS[0].id}`);
  const video = page.locator("video");
  await expect(page.getByRole("heading", { name: "Gapless One", level: 2 })).toBeVisible();
  const expectedDuration = 4 + (gapless.delaySamples + gapless.paddingSamples) / SAMPLE_RATE;
  await expect
    .poll(() => video.evaluate((element: HTMLVideoElement) => element.duration))
    .toBeCloseTo(expectedDuration, 2);
  // Autoplay is refused (see blockAutoplay), so the album starts the way it
  // does in a browser that wants a gesture for sound: a real click on Play,
  // which also gives the page the activation its AudioContext needs.
  await expect(video).toHaveJSProperty("paused", true);
  await page.getByRole("button", { name: "Play", exact: true }).click();

  await expect(page.getByRole("heading", { name: "Gapless Three", level: 2 })).toBeVisible({
    timeout: 20_000,
  });
  // Let the third track play past the second join's recording window.
  await expect
    .poll(() => video.evaluate((element: HTMLVideoElement) => element.currentTime), {
      timeout: 10_000,
    })
    .toBeGreaterThan(1.5);

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
      audible: window.__gaplessAudible ?? 0,
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
  expect(capture.audible, "the element played unmuted").toBe(0);
  // All three tracks were heard on the context, the first one included.
  expect(joins.map((join) => [join.from, join.to])).toEqual([
    [440, 660],
    [660, 880],
  ]);
  for (const join of joins) {
    expect(join.silenceMs).toBeLessThanOrEqual(JOIN_TOLERANCE_MS);
    expect(join.overlapMs).toBeLessThanOrEqual(JOIN_TOLERANCE_MS);
  }
  // Away from the joins, no two tracks ever sound at once: two tones would
  // peak above a single tone.
  const near = Math.round((capture.sampleRate * PEAK_WINDOW_MS) / 1000);
  let peak = 0;
  samples.forEach((sample, i) => {
    if (joins.some((join) => Math.abs(i - join.at) <= near)) return;
    peak = Math.max(peak, Math.abs(sample));
  });
  expect(peak).toBeLessThan(SINGLE_TONE_CEILING);
});
