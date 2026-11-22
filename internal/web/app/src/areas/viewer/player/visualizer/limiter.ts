// The host side of Oscillux's frame flash limiter, shared by every web surface that presents an
// Oscillux frame: the visualizer (renderer.ts) and the calm photo slideshow (slideshowDriver.ts).
// It links the six vendored limiter passes, allocates their targets, keeps the limiter's clock
// and refill and schedules the passes over an RGBA8 frame, as the release's reference hosts do
// (hosts/web/host.js and tools/shader/limiter-check.mjs at the pinned rev). Every shader is
// Oscillux's, unchanged; this file only binds and schedules them. The paint-lag bounds the clock
// is credited with live here too, so both hosts time the limiter alike.

import { shaderSource } from "./oscillux";

// The frame flash limiter's clock (tools/shader/limiter-model.mjs) and its WCAG 2.x reference
// field, a third of the frame each way (tools/shader/limiter-geometry.mjs).
const TICK_MS = 1000 / 15 / 64;
const TICKS = 240;
const GRID_X = 16;
const GRID_Y = 9;
const SUB = 4;
const TOKEN = 65025;
const MAX_FLASH_HZ = 3;
/** The longest interval one frame credits the limiter with: a luma token's refill (limit). */
export const CREDIT_MS = 1000 / (2 * MAX_FLASH_HZ);
// The limiter's coverage interval and a recency unit (Oscillux shaders/limiter/common.glsl): a
// stamp is recent while it is at most 5 units old, so its clock may age a stamp the coverage
// interval and one unit more.
const COVERAGE_MS = 1000 / 15;
const UNIT_MS = 16 * TICK_MS;

/**
 * The share of a draw interval the limiter is credited with when a frame is painted late
 * (CORE-2744): its clock runs slower by lagMs, the bound on the lag of the frames of about the
 * last second, a second, and by enough that a stamp under the coverage interval old on screen is
 * within the 5 units recency reaches, with recentLagMs the bound for the last coverage interval.
 */
export function creditScale(lagMs: number, recentLagMs: number): number {
  return Math.min(1000 / (1000 + lagMs), (COVERAGE_MS + UNIT_MS) / (COVERAGE_MS + recentLagMs));
}
/** The limiter's grant state with every budget just spent: two luma tokens of deficit (bit 8 of x
 * carries one), a red token and no credit (Oscillux shaders/limiter/grant.glsl). */
const SPENT = new Uint32Array([256, TOKEN, TOKEN, 0]);
/** The longest a geometry change fades the frame on screen to black through the limiter, in time
 * the limiter is credited with: longer than the slowest grant of a fall, the red token's refill of
 * 8 / (2 maxFlashHz) s, by more than the one frame's credit the fade's last frame is not judged
 * with (Oscillux shaders/limiter/README.md, "Ping-pong and reset"). A host may end the fade
 * sooner, at the first frame after one whose grant held nothing: this is the backstop. */
export const FADE_S = 2;
/** How long black is presented before every start, the first included: longer than a second by
 * the expiry's margin, so the trackers start tracked black and a red change owed from before has
 * expired (WCAG 2.3.1 counts flashes in any one second), and longer than the coverage interval,
 * 1 / (5 maxFlashHz) s. The black is credited a presented frame at a time from the first black
 * frame shown, in time the limiter is credited with and BLACK_CREDIT_MS at most a frame, and is
 * over once the credit less its longest interval reaches BLACK_S, as a credit may lead the display
 * by a frame (Oscillux hostBlackLongEnough, shaders/limiter/README.md, "Restart seeds and the black
 * hold"): 76 intervals at 60 Hz. */
export const BLACK_S = 1.25;
/** The longest interval one black frame is credited with (Oscillux "Black credit before a black
 * start"). */
export const BLACK_CREDIT_MS = 100;
/** The tracker texel a start writes after BLACK_S of black: black, its red memory counted, both
 * u'v' boxes known and empty, the 32-bit marker set (Oscillux BLACK_TEXEL). */
const BLACK_TRACKER = new Uint32Array([0, 32768, 2147508224, 0]);
/** Bit 31 set in every word: a start writes it to each tracker copy and reads it back, and a
 * store of fewer than 32 bits presents black instead of holding every changed pixel (Oscillux
 * shaders/limiter/README.md, "Ping-pong and reset"). */
const TRACKER_CHECK = new Uint32Array([0x80000001, 0x80000002, 0x80000004, 0x80000008]);

const LIMITER = ["rows", "reduce", "cells", "windows", "grant", "output"] as const;
const LIMITER_SAMPLERS: Record<(typeof LIMITER)[number], readonly string[]> = {
  rows: ["uFrame", "uTrack", "uHist"],
  reduce: ["uRows"],
  cells: ["uS"],
  windows: ["uC"],
  grant: ["uC", "uW", "uG"],
  output: ["uFrame", "uHist", "uTrack", "uG"],
};

/** A full-screen triangle from gl_VertexID, in the offscreen targets' top-left orientation. */
export const LIMITER_VERTEX =
  "#version 300 es\nvoid main() { vec2 p = vec2(gl_VertexID == 1 ? 3.0 : -1.0, gl_VertexID == 2 ? 3.0 : -1.0); gl_Position = vec4(p, 0.0, 1.0); }";

export interface LimiterWindow {
  cols: number;
  rows: number;
  limit: number;
}

// The most cells a run of `len` pixels touches along an axis of `n` pixels split into `cells`
// cells of SUB sub-blocks each.
function span(len: number, n: number, cells: number): number {
  const cellOf = (x: number) => Math.floor(Math.floor((cells * SUB * (x + 1) - 1) / n) / SUB);
  let most = 1;
  for (let s = 0; s + len <= n; s++) most = Math.max(most, cellOf(s + len - 1) - cellOf(s) + 1);
  return most;
}

/** windowOf("wcag", W, H) from tools/shader/limiter-geometry.mjs for a W x H frame whose texels
 * are `texel` physical pixels on a side, with visibleWidth x visibleHeight of them in view: the
 * field is a third of the visible frame each way, the window the fewest whole cells that contain
 * every texel the field touches wherever it sits, and the area limit a quarter of the field's
 * physical pixels over the texel's area, so a patch over the physical limit is over it in texels
 * (Codex round 13). */
export function limiterWindow(
  W: number,
  H: number,
  texel = 1,
  visibleWidth = W,
  visibleHeight = H,
): LimiterWindow {
  const fieldPx = (px: number) => Math.min(px, Math.max(1, Math.round(px / 3)));
  const touched = (px: number, n: number) =>
    Math.min(n, texel === 1 ? px : Math.ceil((px - 1) / texel) + 1);
  const fwPx = fieldPx(visibleWidth * texel);
  const fhPx = fieldPx(visibleHeight * texel);
  return {
    cols: span(touched(fwPx, W), W, GRID_X),
    rows: span(touched(fhPx, H), H, GRID_Y),
    limit: Math.floor((fwPx * fhPx) / 4 / (texel * texel)),
  };
}

/** One frame size's limiter targets and the ping-pong index of the current state. */
export interface Limiter {
  rows: WebGLTexture;
  s: WebGLTexture;
  c: WebGLTexture;
  w: WebGLTexture;
  g: WebGLTexture[];
  /** Per-pixel tracker state, RGBA32UI at the frame size. */
  track: WebGLTexture[];
  out: WebGLTexture[];
  fRows: WebGLFramebuffer;
  fS: WebGLFramebuffer;
  fC: WebGLFramebuffer;
  fW: WebGLFramebuffer;
  fG: WebGLFramebuffer[];
  /** The output pass's targets: out[i] and track[i]. The limited frame is fOut[cur]. */
  fOut: WebGLFramebuffer[];
  window: LimiterWindow;
  cur: number;
}

// The objects an allocation has created so far, released if it throws.
interface Made {
  textures: WebGLTexture[];
  framebuffers: WebGLFramebuffer[];
}

/** The six limiter passes' sources, in pass order. */
export function limiterSources(): Promise<string[]> {
  return Promise.all(LIMITER.map((name) => shaderSource(`limiter/${name}.frag`)));
}

/** Links the six limiter passes with the host's linker and binds their block and samplers. */
export function linkLimiter(
  gl: WebGL2RenderingContext,
  sources: readonly string[],
  link: (vertex: string, fragment: string) => WebGLProgram,
): WebGLProgram[] {
  return LIMITER.map((name, i) => {
    const p = link(LIMITER_VERTEX, sources[i] ?? "");
    gl.useProgram(p);
    gl.uniformBlockBinding(p, gl.getUniformBlockIndex(p, "LimiterBlock"), 0);
    LIMITER_SAMPLERS[name].forEach((s, unit) => gl.uniform1i(gl.getUniformLocation(p, s), unit));
    return p;
  });
}

/**
 * The limiter's clock, refill and pass schedule over linked programs, and the allocation of its
 * targets at a frame size. The host owns when a frame starts, fades and is presented; this owns
 * how the limiter is fed.
 */
export class FrameLimiter {
  /** The limiter's clock, in time it is credited with. */
  clockMs = 0;
  /** The fraction of a refill unit earned and not yet granted (limiter README, Codex round 15). */
  refillCarry = 0;
  /** Whether the grant state the next frame reads was written spent by the host (spend). */
  spent = false;
  private readonly block = new Float32Array(8);
  private readonly ubo: WebGLBuffer;
  // Whether a framebuffer the allocation in progress built is incomplete.
  private incomplete = false;

  constructor(
    private readonly gl: WebGL2RenderingContext,
    private readonly programs: readonly WebGLProgram[],
  ) {
    this.ubo = must(gl.createBuffer());
    gl.bindBuffer(gl.UNIFORM_BUFFER, this.ubo);
    gl.bufferData(gl.UNIFORM_BUFFER, this.block.byteLength, gl.DYNAMIC_DRAW);
  }

  /**
   * A fresh limiter for a W x H frame: its output targets black and tracked black, and every
   * budget written spent. Every framebuffer at the frame size is checked complete, and the
   * fixed-size ones (the same at every size) when verifyFixed; a tracker store of fewer than 32
   * bits counts as incomplete. Whatever was allocated is released if an allocation throws. On
   * `complete: false` the host frees the targets (free) and presents black.
   */
  allocate(
    W: number,
    H: number,
    window: LimiterWindow,
    verifyFixed: boolean,
  ): { limiter: Limiter; complete: boolean } {
    const gl = this.gl;
    this.incomplete = false;
    const made: Made = { textures: [], framebuffers: [] };
    try {
      const rows = this.storage(made, 64, H, gl.RGBA32UI);
      const out = [0, 1].map(() => this.storage(made, W, H, gl.RGBA8));
      const track = [0, 1].map(() => this.storage(made, W, H, gl.RGBA32UI));
      const fOut = out.map((x, i) => this.framebuffer(made, [x, track[i] as WebGLTexture]));
      for (const f of fOut) {
        if (!this.trackerKeeps32(f)) this.incomplete = true;
        this.clearOutput(f);
      }
      const s = this.storage(made, 64, 36, gl.RGBA32UI);
      const c = this.storage(made, 16, 9, gl.RGBA32UI);
      const w = this.storage(made, 16, 9, gl.RGBA16UI);
      const g = [0, 1].map(() => this.storage(made, 1, 1, gl.RGBA16UI));
      const limiter: Limiter = {
        rows,
        s,
        c,
        w,
        g,
        track,
        out,
        fRows: this.framebuffer(made, [rows]),
        fS: this.framebuffer(made, [s], verifyFixed),
        fC: this.framebuffer(made, [c], verifyFixed),
        fW: this.framebuffer(made, [w], verifyFixed),
        fG: g.map((x) => this.framebuffer(made, [x], verifyFixed)),
        fOut,
        window,
        cur: 0,
      };
      for (const f of limiter.fG) this.clearUint(f, 1);
      this.spend(limiter);
      return { limiter, complete: !this.incomplete };
    } catch (err) {
      for (const f of made.framebuffers) gl.deleteFramebuffer(f);
      for (const t of made.textures) gl.deleteTexture(t);
      throw err;
    }
  }

  /** Marks every limiter budget just spent, in the grant state the next frame reads. */
  spend(L: Limiter): void {
    const gl = this.gl;
    gl.bindFramebuffer(gl.FRAMEBUFFER, L.fG[L.cur] as WebGLFramebuffer);
    gl.clearBufferuiv(gl.COLOR, 0, SPENT);
    gl.bindFramebuffer(gl.FRAMEBUFFER, null);
    this.spent = true;
  }

  /**
   * Runs the six passes over `frame` (RGBA8, W x H), leaving the limited frame in
   * L.fOut[L.cur]. shownMs is the time between draws; lagMs and recentLagMs bound the paint lag
   * of about the last second and of the last coverage interval (lagBounds).
   */
  run(
    L: Limiter,
    frame: WebGLTexture,
    W: number,
    H: number,
    shownMs: number,
    lagMs: number,
    recentLagMs: number,
  ): void {
    const gl = this.gl;
    // shownMs is the time between draws. lagMs bounds how late any frame of about the last
    // second was painted after its draw, and recentLagMs any of the last coverage interval (the
    // host holds its largest recent refreshes), so a run of frames was on screen its draw time
    // to within those either way (CORE-2744). The limiter is given time it cannot overstate:
    // - its clock (tick and refill) runs slower by lagMs a second, and by enough that a stamp
    //   under the coverage interval old on screen is within the 5 units recency reaches. A span
    //   the clock counts as a second or more was then a second or more on screen, so lateness
    //   cannot crowd a seventh transition into a second, and only about a refresh in a second is
    //   held, not the half of all time a per-frame lower bound held;
    // - deltaMs, which clears every stamp when it is over the coverage interval, is the
    //   interval less recentLagMs, the least this frame can have been shown.
    const creditMs = shownMs * creditScale(lagMs, recentLagMs);
    const deltaMs = Math.max(0, shownMs - recentLagMs);
    this.clockMs += creditMs;
    // The interval up to a host write of the grant state cannot refill the deficit it wrote.
    // The fraction of a unit carries to the next frame, so the refill keeps the exact rate. A
    // token is TOKEN - 1 units of credit, so a carried fraction cannot finish one early (Codex
    // round 16).
    if (this.spent) this.refillCarry = 0;
    const refillMs = this.spent ? 0 : creditMs;
    const earned =
      this.refillCarry +
      Math.min(1, Math.max(0, (2 * MAX_FLASH_HZ * refillMs) / 1000)) * (TOKEN - 1);
    const refill = Math.min(TOKEN, Math.floor(earned));
    this.refillCarry = Math.min(1, earned - refill);
    const tick = Math.floor(this.clockMs / TICK_MS) % TICKS;
    this.block.set([L.window.cols, L.window.rows, L.window.limit, refill, tick, deltaMs, 0, 0]);
    gl.bindBuffer(gl.UNIFORM_BUFFER, this.ubo);
    gl.bufferSubData(gl.UNIFORM_BUFFER, 0, this.block);
    gl.bindBufferBase(gl.UNIFORM_BUFFER, 0, this.ubo);
    const a = L.cur;
    const b = a ^ 1;
    const [rows, reduce, cells, windows, grant, output] = this.programs as WebGLProgram[];
    const ga = L.g[a] as WebGLTexture;
    const gb = L.g[b] as WebGLTexture;
    const outA = L.out[a] as WebGLTexture;
    const trackA = L.track[a] as WebGLTexture;
    this.pass(rows, L.fRows, 64, H, [frame, trackA, outA]);
    this.pass(reduce, L.fS, 64, 36, [L.rows]);
    this.pass(cells, L.fC, 16, 9, [L.s]);
    this.pass(windows, L.fW, 16, 9, [L.c]);
    this.pass(grant, L.fG[b] as WebGLFramebuffer, 1, 1, [L.c, L.w, ga]);
    this.pass(output, L.fOut[b] as WebGLFramebuffer, W, H, [frame, outA, trackA, gb]);
    L.cur = b;
    this.spent = false;
  }

  /** Releases one frame size's targets. */
  free(L: Limiter): void {
    const gl = this.gl;
    for (const t of [L.rows, L.s, L.c, L.w, ...L.g, ...L.track, ...L.out]) gl.deleteTexture(t);
    for (const f of [L.fRows, L.fS, L.fC, L.fW, ...L.fG, ...L.fOut]) gl.deleteFramebuffer(f);
  }

  /** Releases the uniform buffer; the programs belong to the host that linked them. */
  dispose(): void {
    this.gl.deleteBuffer(this.ubo);
  }

  private pass(
    p: WebGLProgram | undefined,
    fbo: WebGLFramebuffer,
    w: number,
    h: number,
    inputs: WebGLTexture[],
  ): void {
    const gl = this.gl;
    gl.useProgram(p ?? null);
    gl.bindFramebuffer(gl.FRAMEBUFFER, fbo);
    gl.viewport(0, 0, w, h);
    inputs.forEach((t, unit) => bind(gl, unit, t));
    gl.drawArrays(gl.TRIANGLES, 0, 3);
  }

  private storage(made: Made, w: number, h: number, internal: number): WebGLTexture {
    const gl = this.gl;
    const t = must(gl.createTexture());
    made.textures.push(t);
    gl.bindTexture(gl.TEXTURE_2D, t);
    gl.texStorage2D(gl.TEXTURE_2D, 1, internal, w, h);
    filter(gl, gl.NEAREST);
    return t;
  }

  // checkFramebufferStatus waits on the GPU process, so the fixed-size targets, the same at every
  // size, may skip it after a host's first start (renderer.ts framebuffer).
  private framebuffer(made: Made, attachments: WebGLTexture[], verify = true): WebGLFramebuffer {
    const gl = this.gl;
    const f = must(gl.createFramebuffer());
    made.framebuffers.push(f);
    gl.bindFramebuffer(gl.FRAMEBUFFER, f);
    attachments.forEach((t, i) =>
      gl.framebufferTexture2D(gl.FRAMEBUFFER, gl.COLOR_ATTACHMENT0 + i, gl.TEXTURE_2D, t, 0),
    );
    gl.drawBuffers(attachments.map((_, i) => gl.COLOR_ATTACHMENT0 + i));
    if (verify && gl.checkFramebufferStatus(gl.FRAMEBUFFER) !== gl.FRAMEBUFFER_COMPLETE) {
      this.incomplete = true;
    }
    return f;
  }

  private clearUint(fbo: WebGLFramebuffer, attachments: number): void {
    const gl = this.gl;
    gl.bindFramebuffer(gl.FRAMEBUFFER, fbo);
    for (let i = 0; i < attachments; i++) gl.clearBufferuiv(gl.COLOR, i, new Uint32Array(4));
  }

  // Whether an output target's tracker attachment keeps bit 31 of every word: a texel stored with
  // fewer bits reads as unmarked and is held at every change. One blocking read of a texel per
  // copy, at a start only.
  private trackerKeeps32(fbo: WebGLFramebuffer): boolean {
    const gl = this.gl;
    const got = new Uint32Array(4);
    gl.bindFramebuffer(gl.FRAMEBUFFER, fbo);
    gl.clearBufferuiv(gl.COLOR, 1, TRACKER_CHECK);
    gl.readBuffer(gl.COLOR_ATTACHMENT1);
    gl.readPixels(0, 0, 1, 1, gl.RGBA_INTEGER, gl.UNSIGNED_INT, got);
    gl.readBuffer(gl.COLOR_ATTACHMENT0);
    return got.every((word, i) => word === TRACKER_CHECK[i]);
  }

  // An output target: the frame black and the trackers seeded black, counted with nothing owed,
  // as every start follows BLACK_S of black, so the limiter counts the first frame's rises from
  // black (Oscillux shaders/limiter/README.md).
  private clearOutput(fbo: WebGLFramebuffer): void {
    const gl = this.gl;
    gl.bindFramebuffer(gl.FRAMEBUFFER, fbo);
    gl.clearBufferfv(gl.COLOR, 0, new Float32Array(4));
    gl.clearBufferuiv(gl.COLOR, 1, BLACK_TRACKER);
  }
}

export function bind(gl: WebGL2RenderingContext, unit: number, tex: WebGLTexture | null): void {
  gl.activeTexture(gl.TEXTURE0 + unit);
  gl.bindTexture(gl.TEXTURE_2D, tex);
}

export function filter(gl: WebGL2RenderingContext, mode: number): void {
  gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, mode);
  gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, mode);
  gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_S, gl.CLAMP_TO_EDGE);
  gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_T, gl.CLAMP_TO_EDGE);
}

function must<T>(value: T | null): T {
  if (value === null) throw new Error("limiter: WebGL object refused");
  return value;
}

// The paint-lag bound: the largest animation-frame interval measured lately, plus LAG_MARGIN_MS
// for a paint later than any interval measured (a maximum, not a median, so one late paint is
// not underestimated). A frame's lag matters to the limiter for as long as the frames after it
// can be compared with it: the 1 s flash budget for about a second after it, recency and the
// stamps' clearing for the coverage interval. So the budget's bound holds an interval
// LAG_HOLD_MS and recency's RECENT_HOLD_MS; a stall then slows the clock's recency share only
// for the coverage interval after it (Codex rounds 2 and 3, CORE-2744). The intervals are kept
// as maxima of LAG_BUCKET_MS buckets, so no rate of animation frames evicts one early. Before an
// interval is measured, the bound is the slowest cadence a page is drawn at.
const LAG_HOLD_MS = 1250;
const RECENT_HOLD_MS = 1000 / 15 + 1000 / 60;
const LAG_MARGIN_MS = 2;
const LAG_BUCKET_MS = 50;
const LAG_BUCKETS = 32;
const DEFAULT_LAG_MS = 1000 / 15;

/** Recent animation-frame intervals, as the largest in each LAG_BUCKET_MS of time. */
export type LagHistory = { bucket: Float64Array; max: Float64Array; at: Float64Array };

export function lagHistory(): LagHistory {
  return {
    bucket: new Float64Array(LAG_BUCKETS).fill(-1),
    max: new Float64Array(LAG_BUCKETS),
    at: new Float64Array(LAG_BUCKETS),
  };
}

/** Records an animation-frame interval that ended at now. Allocates nothing. */
export function recordLag(h: LagHistory, interval: number, now: number): void {
  const b = Math.floor(now / LAG_BUCKET_MS);
  const i = b % LAG_BUCKETS;
  if (h.bucket[i] !== b) {
    h.bucket[i] = b;
    h.max[i] = interval;
  } else {
    h.max[i] = Math.max(h.max[i] ?? 0, interval);
  }
  h.at[i] = now;
}

/**
 * The paint-lag bound at now: the largest interval that ended within holdMs, plus LAG_MARGIN_MS,
 * or DEFAULT_LAG_MS when none did. A bucket counts from its last interval's end, so it is held
 * at least holdMs. Allocates nothing.
 */
export function lagBound(h: LagHistory, now: number, holdMs: number): number {
  let max = -1;
  for (let i = 0; i < LAG_BUCKETS; i++) {
    if ((h.bucket[i] ?? -1) >= 0 && now - (h.at[i] ?? 0) <= holdMs) {
      max = Math.max(max, h.max[i] ?? 0);
    }
  }
  return max < 0 ? DEFAULT_LAG_MS : max + LAG_MARGIN_MS;
}

/** The budget's and recency's paint-lag bounds at now. */
export function lagBounds(h: LagHistory, now: number): [number, number] {
  return [lagBound(h, now, LAG_HOLD_MS), lagBound(h, now, RECENT_HOLD_MS)];
}
