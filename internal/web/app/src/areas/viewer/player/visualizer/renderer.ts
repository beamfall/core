// Draws one Oscillux visual on a WebGL2 canvas. A frame is, in order: the visual's pass into a
// feedback target (during a switch, the outgoing visual's pass too and Oscillux's switch pass
// blending the two), Oscillux's present pass (its overlay block packed by the wasm core) into an
// RGBA8 frame, the six passes of Oscillux's frame flash limiter, and a blit of the limited
// frame to the canvas. The pass order, bindings and limiter clock follow the release's own
// reference hosts (hosts/web/host.js and tools/shader/limiter-check.mjs at the pinned rev).
// Every shader is Oscillux's, unchanged; this file only binds and schedules them.

import type { AudioFrame } from "./analysis";
import { AUDIO_KEYS } from "./analysis";
import { CadenceGate } from "./cadence";
import type { Oscillux } from "./oscillux";
import { shaderSource } from "./oscillux";
import type { Visual } from "./visuals";

// The frame flash limiter's clock (tools/shader/limiter-model.mjs) and its WCAG 2.x reference
// field, a third of the frame each way (tools/shader/limiter-geometry.mjs).
const TICK_MS = 1000 / 15 / 64;
const TICKS = 240;
const GRID_X = 16;
const GRID_Y = 9;
const SUB = 4;
const TOKEN = 65025;
const MAX_FLASH_HZ = 3;
// The longest interval one frame credits the limiter with: a luma token's refill (limit).
const CREDIT_MS = 1000 / (2 * MAX_FLASH_HZ);
/** The limiter's grant state with every budget just spent: two luma tokens of deficit (bit 8 of x
 * carries one), a red token and no credit (Oscillux shaders/limiter/grant.glsl). */
const SPENT = new Uint32Array([256, TOKEN, TOKEN, 0]);
/** The longest a geometry change fades the frame on screen to black through the limiter, in time
 * the limiter is credited with: longer than the slowest grant of a fall, the red token's refill of
 * 8 / (2 maxFlashHz) s, by more than the one frame's credit the fade's last frame is not judged
 * with (Oscillux shaders/limiter/README.md, "Ping-pong and reset"). The fade ends sooner, at the
 * first frame after one whose grant held nothing (draw): this is the backstop. */
export const FADE_S = 2;
/** How long black is presented before a frame starts after a fade or a restored context: longer
 * than the limiter's coverage interval, 1 / (5 maxFlashHz) s (same section). */
export const BLACK_S = 0.1;
/** What the host shows wherever the canvas shows no judged pixel (past its edge, through its clip,
 * before its first frame): the black every tracker starts at (clearOutput), whatever the theme,
 * so a pixel that comes into view rises from what its tracker holds (Codex round 25). */
export const TRACKED_BLACK = "#000";
/** The limiter's smallest frame: one pixel per sub-block. */
export const MIN_WIDTH = 64;
export const MIN_HEIGHT = 36;
/** The largest frame side the limiter supports: its sub-block counts are 16-bit (Oscillux
 * shaders/limiter/README.md, "Frame size"). Larger canvases render scaled down, keeping aspect. */
export const MAX_SIDE = 8192;

/** The render size for a canvas of `width` x `height`: at least the minimum on each side and at
 * most MAX_SIDE on the longer one, scaled down uniformly so the frame keeps its aspect. */
export function frameSize(width: number, height: number): [number, number] {
  const k = Math.min(1, MAX_SIDE / Math.max(width, height));
  return [
    Math.min(MAX_SIDE, Math.max(MIN_WIDTH, Math.round(width * k))),
    Math.min(MAX_SIDE, Math.max(MIN_HEIGHT, Math.round(height * k))),
  ];
}

const LIMITER = ["rows", "reduce", "cells", "windows", "grant", "output"] as const;
const LIMITER_SAMPLERS: Record<(typeof LIMITER)[number], readonly string[]> = {
  rows: ["uFrame", "uTrack"],
  reduce: ["uRows"],
  cells: ["uS"],
  windows: ["uC"],
  grant: ["uC", "uW", "uG"],
  output: ["uFrame", "uHist", "uTrack", "uG"],
};

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

/** What the viewer sees of a frame: its backing size in texels, the texel's side in physical
 * pixels and the whole texels in view, from the top left. */
interface Geometry {
  width: number;
  height: number;
  texel: number;
  visibleWidth: number;
  visibleHeight: number;
}

// The geometry for a slot of slotWidth x slotHeight physical pixels. A texel the slot cuts is not
// in view: it is never shown, so no pixel is ever seen that the limiter did not judge whole.
function geometry(
  width: number,
  height: number,
  texel: number,
  slotWidth: number,
  slotHeight: number,
): Geometry {
  const [W, H] = frameSize(width, height);
  const whole = (slot: number, n: number) =>
    Math.max(0, Math.min(n, Math.floor(slot / texel + 1e-6)));
  return {
    width: W,
    height: H,
    texel,
    visibleWidth: whole(slotWidth, W),
    visibleHeight: whole(slotHeight, H),
  };
}

function sameGeometry(a: Geometry, b: Geometry): boolean {
  return (
    a.width === b.width &&
    a.height === b.height &&
    a.texel === b.texel &&
    a.visibleWidth === b.visibleWidth &&
    a.visibleHeight === b.visibleHeight
  );
}

/** The geometry a draw request would show, as a key: two requests with one key are the same frame
 * to the renderer, whatever the slot's pixels past its last whole texel. A host compares the key
 * of the frame it drew with the key of the slot it then observes, so a slot that only gained or
 * lost a pixel no texel fills withdraws nothing (Codex round 26). */
export function geometryKey(
  width: number,
  height: number,
  texel: number,
  slotWidth: number,
  slotHeight: number,
): string {
  const g = geometry(width, height, texel, slotWidth, slotHeight);
  return [g.width, g.height, g.texel, g.visibleWidth, g.visibleHeight].join();
}
const LIMITER_VERTEX =
  "#version 300 es\nvoid main() { vec2 p = vec2(gl_VertexID == 1 ? 3.0 : -1.0, gl_VertexID == 2 ? 3.0 : -1.0); gl_Position = vec4(p, 0.0, 1.0); }";

// The default preset's accent and motion (tools/shader/preset.mjs).
const ACCENT = [0.15, 0.9, 0.72, 1];
const MOTION = [0, 0, 0, 0];
// presentation0 for a visual drawn on its own (no switch in progress).
const PRESENTATION = [0, 1, 0, 0];
// The safe rectangle with no overlay regions tracked (hosts/web/overlay.js).
const SAFE_RECT = [0.05, 0.05, 0.95, 0.95];
// The PresentBlock: present0, the 320-byte overlay block, post0, post1.
const PRESENT_FLOATS = 92;
const OVERLAY_FLOAT = 4;
const OVERLAY_BYTES = 320;
const POST_FLOAT = 84;

// The switch between visuals (CORE-2219): Oscillux's Luma Key at full motion, focus halfway,
// over 1.2 s with smoothstep easing, timed by the wasm core's transition scheduler
// (shaders/switch/README.md at the pinned rev). The incoming visual starts at once: no pre-roll.
const SWITCH_STYLE = 0;
const SWITCH_SECONDS = 1.2;
const SWITCH_EASING_SMOOTHSTEP = 1;
const SWITCH_FOCUS = 0.5;
const TRANSITION_DONE = 4;

// The quality ladder the Oscillux governor steps through (CORE-2215), as in the reference web
// host (hosts/web/host.js at the pinned rev): the visual's render scale and its detail reduction
// (presentation1.y) per level, lowest quality first.
export const QUALITY_LADDER: readonly { scale: number; reduce: number }[] = [
  { scale: 0.5, reduce: 1 },
  { scale: 0.67, reduce: 0.6 },
  { scale: 0.83, reduce: 0.25 },
  { scale: 1, reduce: 0 },
];
// KHR_parallel_shader_compile's query, and how often a compiling visual is polled.
const COMPLETION_STATUS_KHR = 0x91b1;
const COMPILE_POLL_MS = 32;
const TOP_LEVEL = QUALITY_LADDER.length - 1;

/** The visual's render size and detail reduction for a W x H canvas at a governor level. */
export function renderQuality(
  W: number,
  H: number,
  level: number,
): { width: number; height: number; reduce: number } {
  const step = QUALITY_LADDER[Math.min(TOP_LEVEL, Math.max(0, Math.trunc(level)))];
  return {
    width: Math.max(1, Math.round(W * step.scale)),
    height: Math.max(1, Math.round(H * step.scale)),
    reduce: step.reduce,
  };
}

/**
 * What a request for a visual does: nothing ("same"), replace the visual now ("instant": the
 * first visual, before the first frame, or under reduced motion), wait for the switch in
 * progress to end ("queue"), or blend to it ("blend").
 */
export function switchAction(
  current: string | null,
  switching: boolean,
  next: string,
  reducedMotion: boolean,
): "same" | "instant" | "queue" | "blend" {
  if (switching && !reducedMotion) return "queue";
  if (current === next) return "same";
  if (current === null || reducedMotion) return "instant";
  return "blend";
}

interface Layer {
  visual: Visual;
  program: WebGLProgram;
  locations: Map<string, WebGLUniformLocation | null>;
  /** The visual's image input. */
  image: WebGLTexture;
}

interface Target {
  tex: WebGLTexture;
  fbo: WebGLFramebuffer;
}

interface Limiter {
  rows: WebGLTexture;
  s: WebGLTexture;
  c: WebGLTexture;
  w: WebGLTexture;
  g: WebGLTexture[];
  /** Per-pixel tracker state, RGBA16UI at the frame size. */
  track: WebGLTexture[];
  out: WebGLTexture[];
  fRows: WebGLFramebuffer;
  fS: WebGLFramebuffer;
  fC: WebGLFramebuffer;
  fW: WebGLFramebuffer;
  fG: WebGLFramebuffer[];
  /** The output pass's targets: out[i] and track[i]. */
  fOut: WebGLFramebuffer[];
  window: LimiterWindow;
  cur: number;
}

interface Size extends Geometry {
  /** The physical pixels of the canvas the host may show, from the top left: the smallest slot
   * since the frame started (draw). */
  viewWidth: number;
  viewHeight: number;
  /** The visual's render size at the governor's level: its feedback, and a switch's targets. */
  renderWidth: number;
  renderHeight: number;
  /** The visual's feedback pair: [current, next]. */
  feedback: Target[];
  /** During a switch, the outgoing visual's feedback pair and the switch pass's output. */
  outgoing: Target[] | null;
  blend: Target | null;
  /** The presented frame, the limiter's input. */
  frame: WebGLTexture;
  /** The present pass's history pair, each framebuffer writing the frame and one history. */
  history: Target[];
  limiter: Limiter;
}

export type FeedbackLabel = "float" | "half-float" | "8-bit";
export interface FeedbackFormat {
  internal: number;
  type: number;
  label: FeedbackLabel;
}

/**
 * The feedback format: RGBA16F where the browser renders to it, through EXT_color_buffer_float
 * or else EXT_color_buffer_half_float, otherwise RGBA8, which clamps HDR feedback and steps
 * gradients (CORE-2481). An enabled extension does not promise a complete framebuffer, so each
 * is probed on a 1x1 attachment, as the reference host does (hosts/web/host.js feedbackFormat).
 */
export function pickFeedbackFormat(gl: WebGL2RenderingContext): FeedbackFormat {
  const candidates: [string, FeedbackLabel][] = [
    ["EXT_color_buffer_float", "float"],
    ["EXT_color_buffer_half_float", "half-float"],
  ];
  for (const [ext, label] of candidates) {
    if (!gl.getExtension(ext)) continue;
    const texture = gl.createTexture();
    const fbo = gl.createFramebuffer();
    let complete = false;
    try {
      gl.bindTexture(gl.TEXTURE_2D, texture);
      gl.texStorage2D(gl.TEXTURE_2D, 1, gl.RGBA16F, 1, 1);
      gl.bindFramebuffer(gl.FRAMEBUFFER, fbo);
      gl.framebufferTexture2D(gl.FRAMEBUFFER, gl.COLOR_ATTACHMENT0, gl.TEXTURE_2D, texture, 0);
      complete = gl.checkFramebufferStatus(gl.FRAMEBUFFER) === gl.FRAMEBUFFER_COMPLETE;
    } finally {
      gl.bindFramebuffer(gl.FRAMEBUFFER, null);
      gl.bindTexture(gl.TEXTURE_2D, null);
      gl.deleteFramebuffer(fbo);
      gl.deleteTexture(texture);
    }
    if (complete) return { internal: gl.RGBA16F, type: gl.HALF_FLOAT, label };
  }
  return { internal: gl.RGBA8, type: gl.UNSIGNED_BYTE, label: "8-bit" };
}

/** A WebGL2 renderer for one canvas, or null where the browser cannot run it. */
export class Renderer {
  private readonly gl: WebGL2RenderingContext;
  private readonly osc: Oscillux;
  private readonly overlay: InstanceType<Oscillux["OverlayConfig"]>;
  private readonly regions: InstanceType<Oscillux["OverlayRegions"]>;
  private readonly feedbackFormat: FeedbackFormat;
  private readonly quad: WebGLBuffer;
  // The current visual's image input; during a switch the outgoing visual keeps the one it had.
  private image: WebGLTexture;
  private readonly transition: InstanceType<Oscillux["Transition"]>;
  // Oscillux's quality governor and the level the targets are built for (CORE-2215).
  private readonly governor: InstanceType<Oscillux["Governor"]>;
  private readonly cadence = new CadenceGate();
  private level = TOP_LEVEL;
  private readonly switchUbo: WebGLBuffer;
  private readonly switchBlock = new Float32Array(12);
  private readonly presentUbo: WebGLBuffer;
  private readonly limiterUbo: WebGLBuffer;
  private readonly presentBlock = new Float32Array(PRESENT_FLOATS);
  // Oscillux's vivid colour mode: present-pass saturation and vibrance (CORE-2369).
  private readonly vivid: Float64Array;
  private readonly limiterBlock = new Float32Array(8);
  private readonly grantWord = new Uint32Array(4);
  private grantSettled = true;
  private limiterSpent = false;
  // The limiter's state before the last frame drawn, until the next draw, when that frame was
  // judged outside a fade (withdraw). `started` marks the frame that started: before it there
  // was no frame, only black.
  private undo: {
    cur: number;
    clockMs: number;
    carry: number;
    spent: boolean;
    started: boolean;
  } | null = null;
  // The fraction of a refill unit earned and not yet granted (limiter README, Codex round 15).
  private refillCarry = 0;
  private readonly visualVertex: string;
  private present: WebGLProgram;
  private switchPass: WebGLProgram;
  private limiter: WebGLProgram[];
  private readonly shaders: WebGLShader[] = [];
  // Every program linked, for dispose.
  private readonly linked: WebGLProgram[] = [];
  private disposed = false;
  private readonly programs = new Map<string, WebGLProgram>();
  // Visuals compiling now, so a second request for one waits on the same program.
  private readonly compiling = new Map<string, Promise<WebGLProgram | null>>();
  // KHR_parallel_shader_compile: a visual compiles off the page's thread and is polled for
  // completion, instead of the page waiting on its status (CORE-2368).
  private parallel = false;
  private visual: Layer | null = null;
  private outgoing: Layer | null = null;
  // The visual asked for while a switch runs, started when it ends.
  private pending: { visual: Visual; program: WebGLProgram } | null = null;
  // The visual last asked for: an older request still compiling is dropped.
  private wanted: Visual | null = null;
  private reducedMotion = false;
  // The frame on screen: none before the first draw and while black is presented.
  private size: Size | null = null;
  // Seconds left of a geometry change's fade to black, or 0 outside one.
  private fadeLeft = 0;
  // Seconds of black left before a frame starts, counted once black is shown.
  private black: number;
  private blackShown = false;
  private time = 0;
  private clockMs = 0;
  private presented = 0;

  /** Whether the GL context is lost, which a draw can hit before the loss event arrives (CORE-2482). */
  get contextLost(): boolean {
    return this.gl.isContextLost();
  }

  /** The feedback and history format in use, for diagnostics (CORE-2481). */
  get feedbackLabel(): FeedbackLabel {
    return this.feedbackFormat.label;
  }

  /**
   * A renderer on the canvas, or null without a hardware WebGL2 context. A software one
   * (SwiftShader, say) runs every full-frame pass on the CPU and stalls the page, so it is
   * refused rather than used for decoration.
   */
  static async create(
    canvas: HTMLCanvasElement,
    osc: Oscillux,
    restored = false,
  ): Promise<Renderer | null> {
    const gl = canvas.getContext("webgl2", {
      failIfMajorPerformanceCaveat: true,
      alpha: false,
      antialias: false,
      depth: false,
      stencil: false,
      preserveDrawingBuffer: false,
    });
    if (!gl) return null;
    const [vertex, present, blend, ...limiter] = await Promise.all([
      shaderSource("visuals/vertex.web.vert"),
      shaderSource("post/present.frag"),
      shaderSource("switch/switch.frag"),
      ...LIMITER.map((name) => shaderSource(`limiter/${name}.frag`)),
    ]);
    return new Renderer(gl, osc, vertex, present, blend, limiter, restored);
  }

  private constructor(
    gl: WebGL2RenderingContext,
    osc: Oscillux,
    vertex: string,
    present: string,
    blend: string,
    limiter: string[],
    restored: boolean,
  ) {
    this.gl = gl;
    this.osc = osc;
    // A restored context replaces whatever the lost one left on screen: black comes first.
    this.black = restored ? BLACK_S : 0;
    this.vivid = osc.vividPresent();
    // Whatever was allocated before a failure is released here, as no caller holds the
    // instance to dispose it.
    try {
      this.overlay = new osc.OverlayConfig();
      this.overlay.outputLevels = 255;
      this.overlay.intensity = 1;
      this.overlay.alpha = 1;
      this.overlay.contrastRatio = 4.5;
      this.overlay.premultipliedAlpha = false;
      this.regions = new osc.OverlayRegions();
      const config = new osc.TransitionConfig();
      try {
        config.easing = SWITCH_EASING_SMOOTHSTEP;
        config.fallbackSeconds = SWITCH_SECONDS;
        config.preRollSeconds = 0;
        config.autoAdvanceBars = 0;
        this.transition = new osc.Transition(config);
      } finally {
        config.free();
      }
      // The reference defaults, timed by the frame interval as the reference host does without
      // a GPU timer query, starting at full quality.
      const quality = new osc.GovernorConfig();
      try {
        quality.maxLevel = TOP_LEVEL;
        quality.initialLevel = TOP_LEVEL;
        this.governor = new osc.Governor(quality);
      } finally {
        quality.free();
      }
      this.parallel = !!gl.getExtension("KHR_parallel_shader_compile");
      this.feedbackFormat = pickFeedbackFormat(gl);

      this.quad = must(gl.createBuffer());
      gl.bindBuffer(gl.ARRAY_BUFFER, this.quad);
      gl.bufferData(gl.ARRAY_BUFFER, new Float32Array([-1, -1, 3, -1, -1, 3]), gl.STATIC_DRAW);
      gl.enableVertexAttribArray(0);
      gl.vertexAttribPointer(0, 2, gl.FLOAT, false, 0, 0);

      // The visual's image input: a neutral stand-in until setImage hands it the artwork.
      this.image = must(gl.createTexture());
      this.setImage(null);

      this.presentUbo = must(gl.createBuffer());
      gl.bindBuffer(gl.UNIFORM_BUFFER, this.presentUbo);
      gl.bufferData(gl.UNIFORM_BUFFER, this.presentBlock.byteLength, gl.DYNAMIC_DRAW);
      this.limiterUbo = must(gl.createBuffer());
      gl.bindBuffer(gl.UNIFORM_BUFFER, this.limiterUbo);
      gl.bufferData(gl.UNIFORM_BUFFER, this.limiterBlock.byteLength, gl.DYNAMIC_DRAW);
      this.switchUbo = must(gl.createBuffer());
      gl.bindBuffer(gl.UNIFORM_BUFFER, this.switchUbo);
      gl.bufferData(gl.UNIFORM_BUFFER, this.switchBlock.byteLength, gl.DYNAMIC_DRAW);

      // Offscreen passes draw into targets read back with a top-left origin, so the visual's
      // vertex shader flips y; the present pass draws in canvas orientation.
      this.visualVertex = vertex.replace(
        "gl_Position = vec4(aPos, 0.0, 1.0);",
        "gl_Position = vec4(aPos.x, -aPos.y, 0.0, 1.0);",
      );
      this.present = this.link(vertex, present);
      gl.useProgram(this.present);
      gl.uniformBlockBinding(
        this.present,
        gl.getUniformBlockIndex(this.present, "PresentBlock"),
        0,
      );
      ["uA", "uB", "uH"].forEach((name, unit) =>
        gl.uniform1i(gl.getUniformLocation(this.present, name), unit),
      );
      gl.uniform1i(gl.getUniformLocation(this.present, "uBloom"), 4);

      // The switch pass works in the offscreen targets' top-left orientation, as its uv does.
      this.switchPass = this.link(LIMITER_VERTEX, blend);
      gl.useProgram(this.switchPass);
      gl.uniformBlockBinding(
        this.switchPass,
        gl.getUniformBlockIndex(this.switchPass, "SwitchBlock"),
        0,
      );
      ["uFrom", "uTo", "uPhoto"].forEach((name, unit) =>
        gl.uniform1i(gl.getUniformLocation(this.switchPass, name), unit),
      );

      this.limiter = LIMITER.map((name, i) => {
        const p = this.link(LIMITER_VERTEX, limiter[i]);
        gl.useProgram(p);
        gl.uniformBlockBinding(p, gl.getUniformBlockIndex(p, "LimiterBlock"), 0);
        LIMITER_SAMPLERS[name].forEach((s, unit) =>
          gl.uniform1i(gl.getUniformLocation(p, s), unit),
        );
        return p;
      });
    } catch (err) {
      this.dispose();
      throw err;
    }
  }

  /**
   * Compiles a visual (once) and switches to it. The incoming visual starts from the outgoing
   * one's last frame as its feedback and the clock runs on (CORE-2218); Oscillux's switch pass
   * then blends the two over 1.2 s (CORE-2219). A request during a switch waits for it to end;
   * under reduced motion the visual changes at once.
   */
  async use(visual: Visual): Promise<void> {
    this.wanted = visual;
    if (!this.outgoing && this.visual?.visual.id === visual.id) return;
    const program = await this.prepare(visual);
    if (!program || this.wanted !== visual) return;
    this.apply(visual, program);
  }

  private apply(visual: Visual, program: WebGLProgram): void {
    const size = this.size;
    const action = switchAction(
      this.visual?.visual.id ?? null,
      !!this.outgoing,
      visual.id,
      this.reducedMotion || !size,
    );
    if (action === "queue") {
      this.pending = this.visual?.visual.id === visual.id ? null : { visual, program };
      return;
    }
    this.pending = null;
    if (action === "same") return;
    if (action === "instant") {
      this.endSwitch();
      this.visual = { visual, program, locations: new Map(), image: this.image };
      return;
    }
    // blend: the current visual goes out with its feedback and image; the incoming one gets a
    // copy of that feedback and shares the image until setImage hands it its own.
    const s = size as Size;
    const { internal, type } = this.feedbackFormat;
    const feedback = this.feedbackPair(s.renderWidth, s.renderHeight, internal, type, false);
    this.copy(
      s.feedback[0],
      s.renderWidth,
      s.renderHeight,
      feedback[0],
      s.renderWidth,
      s.renderHeight,
    );
    s.outgoing = s.feedback;
    s.feedback = feedback;
    this.outgoing = this.visual;
    this.visual = { visual, program, locations: new Map(), image: this.image };
    const status = this.transition.request(this.time, 0, 0, 0, 0, 0, 0);
    if (status !== 0) {
      console.warn("visualizer: switch", this.osc.statusString(status));
      this.endSwitch();
    }
  }

  // Drops the outgoing visual, its feedback, its image (when not shared) and the blend target.
  private endSwitch(): void {
    const out = this.outgoing;
    if (!out) return;
    this.outgoing = null;
    this.transition.reset();
    const gl = this.gl;
    if (out.image !== this.image) gl.deleteTexture(out.image);
    const s = this.size;
    if (!s) return;
    for (const t of [...(s.outgoing ?? []), ...(s.blend ? [s.blend] : [])]) {
      gl.deleteTexture(t.tex);
      gl.deleteFramebuffer(t.fbo);
    }
    s.outgoing = null;
    s.blend = null;
  }

  // The switch's eased progress this frame, or null with no switch running. A switch that has
  // ended (or that a geometry change cut short) is dropped here, and a visual queued behind it starts.
  private switchProgress(reducedMotion: boolean): number | null {
    if (!this.outgoing && !this.pending) return null;
    if (this.outgoing && !reducedMotion) {
      const status = this.transition.status(this.time);
      if (status === 0 && this.transition.state !== TRANSITION_DONE) {
        return this.transition.progress;
      }
    }
    this.endSwitch();
    const next = this.pending;
    this.pending = null;
    if (next) this.apply(next.visual, next.program);
    return this.outgoing ? 0 : null;
  }

  /**
   * Compiles a visual ahead of its first use, so switching to it later does not stall a frame.
   * With KHR_parallel_shader_compile the page keeps drawing while the driver compiles.
   */
  prepare(visual: Visual): Promise<WebGLProgram | null> {
    const ready = this.programs.get(visual.id);
    if (ready) return Promise.resolve(ready);
    let compiling = this.compiling.get(visual.id);
    if (!compiling) {
      compiling = this.compileVisual(visual).finally(() => this.compiling.delete(visual.id));
      this.compiling.set(visual.id, compiling);
    }
    return compiling;
  }

  private async compileVisual(visual: Visual): Promise<WebGLProgram | null> {
    const source = await shaderSource(`visuals/${visual.id}.web.frag`);
    // Disposed while the source loaded: allocate nothing more.
    if (this.disposed) return null;
    const program = this.compile(this.visualVertex, source);
    while (this.parallel && !this.disposed && !this.gl.isContextLost()) {
      if (this.gl.getProgramParameter(program, COMPLETION_STATUS_KHR)) break;
      await new Promise((resolve) => setTimeout(resolve, COMPILE_POLL_MS));
    }
    if (this.disposed) return null;
    this.check(program);
    this.programs.set(visual.id, program);
    return program;
  }

  /**
   * Draws one frame at width x height texels, each `texel` physical pixels on a side, dt seconds
   * after the last, in a slot slotWidth x slotHeight physical pixels.
   *
   * One rule covers every change to what the viewer sees, whether the backing size, the texel size
   * or the texels in view: the frame on screen fades to black through the limiter (until a frame
   * of the fade showed its black input exactly, FADE_S at most), black is presented (BLACK_S),
   * and a frame starts at the geometry asked for then, black with every
   * budget spent (start). A change during the fade or the black only moves that target: nothing
   * is reallocated, spent or restarted, so a slot dragged to a new size costs one fade and one
   * start. No pixel is carried from one geometry to the next (CORE-2516).
   */
  draw(
    width: number,
    height: number,
    dt: number,
    audio: AudioFrame,
    reducedMotion: boolean,
    frameMs = dt * 1000,
    texel = 1,
    shownMs = frameMs,
    slotWidth = width * texel,
    slotHeight = height * texel,
  ): void {
    if (!this.visual) return;
    const gl = this.gl;
    this.reducedMotion = reducedMotion;
    this.undo = null;
    const want = geometry(width, height, texel, slotWidth, slotHeight);
    // The fade and the black run on the time the limiter is credited with, shownMs up to one
    // token's refill a frame, so the fall's grant has come by the end of the fade whatever the
    // frame rate (Codex rounds 18, 22).
    const shownS = Math.min(shownMs, CREDIT_MS) / 1000;
    let size = this.size;
    let started = false;
    if (size && this.fadeLeft > 0) {
      this.fadeLeft = Math.max(0, this.fadeLeft - shownS);
      // The fade is over once its last frame showed its input, black, at every pixel: the grant
      // held no direction, so every fall has been granted. A dark frame gets there in one frame, a
      // frame with luma to drop when the luma tokens are back, and one with saturated red when the
      // red token is. FADE_S, by when the slowest grant has come, is the backstop.
      if (this.fadeLeft === 0 || this.exact(size)) {
        this.fadeLeft = 0;
        // The frame is black by now. A size change ends a switch, as in the reference host.
        this.endSwitch();
        this.freeSize();
        this.black = BLACK_S;
        this.blackShown = false;
        size = null;
      }
    } else if (size && !sameGeometry(size, want)) {
      // The fade starts: from this frame the limiter is fed black, with its budgets written spent
      // and an area limit of zero, so every transition down to black waits for a grant however
      // little of the frame is still in view.
      this.fadeLeft = FADE_S;
      this.spend(size.limiter);
      size.limiter.window = { ...size.limiter.window, limit: 0 };
    }
    if (!size) {
      // Black for BLACK_S from the first black frame shown, then the start. A slot with no whole
      // texel in view stays black.
      if (this.black > 0 && this.blackShown) this.black = Math.max(0, this.black - shownS);
      if (this.black > 0 || want.visibleWidth < 1 || want.visibleHeight < 1) {
        this.blackShown = true;
        gl.bindFramebuffer(gl.FRAMEBUFFER, null);
        gl.clearColor(0, 0, 0, 1);
        gl.clear(gl.COLOR_BUFFER_BIT);
        return;
      }
      size = this.start(want);
      started = true;
    }
    // The part of the canvas the host may show only shrinks (shown): a pixel the slot hid would
    // come back unjudged, so it stays hidden until the start.
    size.viewWidth = Math.min(size.viewWidth, Math.max(0, Math.floor(slotWidth)));
    size.viewHeight = Math.min(size.viewHeight, Math.max(0, Math.floor(slotHeight)));
    const fading = this.fadeLeft > 0;
    const { width: W, height: H } = size;
    this.time += dt;
    const p = this.switchProgress(reducedMotion);
    // The governor times frames outside a switch only, and its level changes between switches,
    // as in the reference host. It reads the raw frame interval, not the clamped step, so a
    // stall or a throttled page is a pause it ignores rather than a slow frame that steps the
    // render scale down.
    // A steady browser cap at 30 Hz is not overload: the cadence gate scales it to the target
    // (CORE-2483).
    if (p === null) {
      const sample = this.cadence.sample(frameMs);
      if (sample !== null) this.governor.record(sample);
      this.setLevel(size, this.governor.level);
    }
    const layer = this.visual;
    const { renderWidth: rW, renderHeight: rH } = size;
    gl.bindBuffer(gl.ARRAY_BUFFER, this.quad);

    // 1. The visual at its render size, reading its previous frame; during a switch the
    // outgoing one too.
    const next = this.drawVisual(layer, size.feedback, rW, rH, dt, audio, reducedMotion);
    size.feedback.reverse();
    let shown = next;
    if (p !== null && this.outgoing && size.outgoing) {
      const from = this.drawVisual(this.outgoing, size.outgoing, rW, rH, dt, audio, reducedMotion);
      size.outgoing.reverse();
      shown = this.blend(size, from, next, p, audio);
    }

    // 2. The present pass at the canvas size, into the frame and the next history. It samples
    // the visual with normalized coordinates, so a smaller render size scales up here.
    this.packPresent(W, H, dt, reducedMotion);
    gl.viewport(0, 0, W, H);
    gl.useProgram(this.present);
    gl.bindBuffer(gl.UNIFORM_BUFFER, this.presentUbo);
    gl.bufferSubData(gl.UNIFORM_BUFFER, 0, this.presentBlock);
    gl.bindBufferBase(gl.UNIFORM_BUFFER, 0, this.presentUbo);
    const [histPrev, histNext] = size.history;
    bind(gl, 0, shown.tex);
    bind(gl, 1, shown.tex);
    bind(gl, 2, histPrev.tex);
    bind(gl, 3, null);
    bind(gl, 4, null);
    gl.bindFramebuffer(gl.FRAMEBUFFER, histNext.fbo);
    gl.drawArrays(gl.TRIANGLES, 0, 3);
    // While fading, the limiter is fed black, so the output falls to black through its grant.
    if (fading) gl.clearBufferfv(gl.COLOR, 0, [0, 0, 0, 1]);
    size.history = [histNext, histPrev];

    // 3. The frame flash limiter, on the time the frames are shown apart (shownMs). A frame of
    // the fade is never withdrawn: under its zero area limit it is safe in any view.
    const L = size.limiter;
    if (!fading) {
      this.undo = {
        cur: L.cur,
        clockMs: this.clockMs,
        carry: this.refillCarry,
        spent: this.limiterSpent,
        started,
      };
    }
    this.limit(size, shownMs);

    // 4. The limited frame to the canvas.
    this.showLimited(size);
  }

  /** Withdraws the last frame drawn, before it is painted: the canvas shows the limiter's previous
   * output again and the limiter's state goes back to before the frame. A host calls it when the
   * slot's size changes between a draw and its paint, so a frame judged against the old area limit
   * is never shown in the new slot (Codex round 18); the next draw starts the fade from the frame
   * that was on screen. Black and the frames of a fade stand. Before the frame that started there
   * was no frame, only black, so withdrawing it leaves black and no frame: the next draw starts
   * at the geometry asked for then, with no fade of a frame nobody saw (Codex round 26). */
  withdraw(): void {
    const u = this.undo;
    const size = this.size;
    this.undo = null;
    if (!u || !size) return;
    this.clockMs = u.clockMs;
    this.refillCarry = u.carry;
    this.limiterSpent = u.spent;
    this.grantSettled = false;
    if (u.started) {
      const gl = this.gl;
      this.endSwitch();
      this.freeSize();
      gl.bindFramebuffer(gl.FRAMEBUFFER, null);
      gl.clearColor(0, 0, 0, 1);
      gl.clear(gl.COLOR_BUFFER_BIT);
      return;
    }
    size.limiter.cur = u.cur;
    this.showLimited(size);
  }

  // The limited frame to the canvas: its texels in view only, the rest black, so the canvas shows
  // no texel the limiter has not judged in view (Codex round 17). GL rows run bottom-up, so the
  // top-left anchored visible rows are the top ones.
  private showLimited(size: Size): void {
    const gl = this.gl;
    const L = size.limiter;
    const { height: H, visibleWidth: vw, visibleHeight: vh } = size;
    gl.bindFramebuffer(gl.DRAW_FRAMEBUFFER, null);
    gl.clearColor(0, 0, 0, 1);
    gl.clear(gl.COLOR_BUFFER_BIT);
    gl.bindFramebuffer(gl.READ_FRAMEBUFFER, L.fOut[L.cur]);
    gl.blitFramebuffer(0, H - vh, vw, H, 0, H - vh, vw, H, gl.COLOR_BUFFER_BIT, gl.NEAREST);
    gl.bindFramebuffer(gl.READ_FRAMEBUFFER, null);
  }

  /** The frame on screen, or null while there is none: its size in texels, the texel's side in
   * physical pixels, and the physical pixels of the canvas the host may show, from the top left.
   * The host keeps the canvas at this size and position, clipped to this view, until the next
   * frame starts: through a fade the slot may shrink and grow, and a pixel it hid must not come
   * back (Codex round 24). */
  get shown(): {
    width: number;
    height: number;
    texel: number;
    viewWidth: number;
    viewHeight: number;
  } | null {
    const size = this.size;
    return size
      ? {
          width: size.width,
          height: size.height,
          texel: size.texel,
          viewWidth: size.viewWidth,
          viewHeight: size.viewHeight,
        }
      : null;
  }

  /** Whether the picture can be held for this request while playback is paused. */
  settled(
    width: number,
    height: number,
    texel: number,
    slotWidth = width * texel,
    slotHeight = height * texel,
  ): boolean {
    const want = geometry(width, height, texel, slotWidth, slotHeight);
    const size = this.size;
    // With no frame the canvas is black: all a slot with no whole texel in view ever shows.
    if (!size) return this.black <= 0 && (want.visibleWidth < 1 || want.visibleHeight < 1);
    if (this.fadeLeft > 0 || !sameGeometry(size, want)) return false;
    return this.exact(size);
  }

  // Whether the last frame judged showed its input exactly, at every pixel.
  private exact(size: Size): boolean {
    // A grant state the host wrote spent has judged no frame, so its clear held bits say nothing
    // about the input: a withdrawn first draw would otherwise hold black (Codex round 24).
    if (this.limiterSpent) return false;
    if (this.grantSettled) return true;
    // A denied direction can leave a steady input held. Read the last grant only when deciding
    // whether to stop drawing or to end a fade, and reuse an all-admitted result until the next
    // draw.
    const gl = this.gl;
    const L = size.limiter;
    this.grantWord[0] = 0;
    gl.bindFramebuffer(gl.READ_FRAMEBUFFER, L.fG[L.cur]);
    gl.readPixels(0, 0, 1, 1, gl.RGBA_INTEGER, gl.UNSIGNED_INT, this.grantWord);
    gl.bindFramebuffer(gl.READ_FRAMEBUFFER, null);
    // Bits 4 to 7 are the directions some pixel fires in that the grant did not admit (Oscillux
    // v0.2.9): clear, the output shows the input exactly (Codex round 18).
    this.grantSettled = (this.grantWord[0] & 0xf0) === 0;
    return this.grantSettled;
  }

  // Draws a visual from feedback[0] into feedback[1] and returns that target.
  private drawVisual(
    v: Layer,
    feedback: Target[],
    W: number,
    H: number,
    dt: number,
    audio: AudioFrame,
    reducedMotion: boolean,
  ): Target {
    const gl = this.gl;
    const [prev, next] = feedback;
    gl.useProgram(v.program);
    this.setVisualUniforms(v, W, H, dt, audio, reducedMotion);
    bind(gl, 0, v.image);
    bind(gl, 1, prev.tex);
    gl.bindFramebuffer(gl.FRAMEBUFFER, next.fbo);
    gl.viewport(0, 0, W, H);
    gl.drawArrays(gl.TRIANGLES, 0, 3);
    return next;
  }

  // Oscillux's switch pass from the outgoing frame to the incoming one at progress p, into the
  // blend target at the render size: SwitchBlock switch0 = (p, beatPhase, energy, focus),
  // switch1 = (motion, aspect, style, 0), size = (W, H, 0, 0); textures from, to and photo (the
  // incoming image, which Luma Key does not read).
  private blend(size: Size, from: Target, to: Target, p: number, audio: AudioFrame): Target {
    const gl = this.gl;
    const W = size.renderWidth;
    const H = size.renderHeight;
    if (!size.blend) {
      const { internal, type } = this.feedbackFormat;
      const tex = this.texture(W, H, internal, gl.RGBA, type, gl.LINEAR);
      size.blend = { tex, fbo: this.framebuffer([tex], false) };
    }
    const energy = Math.min(1, Math.max(0, audio.values.level));
    this.switchBlock.set([
      p,
      audio.values.beatPhase,
      energy,
      SWITCH_FOCUS,
      1,
      W / H,
      SWITCH_STYLE,
      0,
      W,
      H,
      0,
      0,
    ]);
    gl.bindBuffer(gl.UNIFORM_BUFFER, this.switchUbo);
    gl.bufferSubData(gl.UNIFORM_BUFFER, 0, this.switchBlock);
    gl.bindBufferBase(gl.UNIFORM_BUFFER, 0, this.switchUbo);
    this.pass(this.switchPass, size.blend.fbo, W, H, [from.tex, to.tex, this.image]);
    return size.blend;
  }

  /**
   * Sets the visual's image input: the track's artwork, or the neutral stand-in for null. The
   * texture keeps its own copy, so the caller may close the bitmap afterwards. During a switch
   * the outgoing visual keeps the image it had.
   */
  setImage(image: ImageBitmap | null): void {
    if (this.disposed) return;
    const gl = this.gl;
    if (this.outgoing && this.outgoing.image === this.image) {
      this.image = must(gl.createTexture());
      if (this.visual) this.visual.image = this.image;
    }
    gl.bindTexture(gl.TEXTURE_2D, this.image);
    if (image) {
      gl.texImage2D(gl.TEXTURE_2D, 0, gl.RGBA8, gl.RGBA, gl.UNSIGNED_BYTE, image);
    } else {
      const grey = new Uint8Array([128, 128, 128, 255]);
      gl.texImage2D(gl.TEXTURE_2D, 0, gl.RGBA8, 1, 1, 0, gl.RGBA, gl.UNSIGNED_BYTE, grey);
    }
    filter(gl, gl.LINEAR);
  }

  /** Releases every GL and Oscillux resource; safe on a partly built renderer, and twice. */
  dispose(): void {
    if (this.disposed) return;
    this.disposed = true;
    const gl = this.gl;
    this.endSwitch();
    this.freeSize();
    for (const p of this.linked) gl.deleteProgram(p);
    for (const s of this.shaders) gl.deleteShader(s);
    // Fields a failed constructor never reached are undefined; GL treats them as null.
    gl.deleteBuffer(this.quad ?? null);
    gl.deleteBuffer(this.presentUbo ?? null);
    gl.deleteBuffer(this.limiterUbo ?? null);
    gl.deleteBuffer(this.switchUbo ?? null);
    gl.deleteTexture(this.image ?? null);
    this.linked.length = 0;
    this.shaders.length = 0;
    this.programs.clear();
    this.visual = null;
    this.pending = null;
    this.transition?.free();
    this.governor?.free();
    this.regions?.free();
    this.overlay?.free();
  }

  private setVisualUniforms(
    v: Layer,
    W: number,
    H: number,
    dt: number,
    audio: AudioFrame,
    reducedMotion: boolean,
  ): void {
    const gl = this.gl;
    const at = (name: string) => {
      let loc = v.locations.get(name);
      if (loc === undefined) {
        loc = gl.getUniformLocation(v.program, name);
        v.locations.set(name, loc);
      }
      return loc;
    };
    const t = v.visual;
    gl.uniform2f(at("u.resolution"), W, H);
    gl.uniform1f(at("u.time"), this.time);
    gl.uniform1f(at("u.dt"), dt);
    gl.uniform1f(at("u.sensitivity"), 1);
    gl.uniform1f(at("u.param0"), t.params[0]);
    gl.uniform1f(at("u.param1"), t.params[1]);
    gl.uniform1f(at("u.param2"), t.params[2]);
    gl.uniform1f(at("u.trailDecay"), reducedMotion ? 0 : t.trailDecay);
    gl.uniform4fv(at("u.accent"), ACCENT);
    gl.uniform4fv(at("u.motion"), MOTION);
    // Oscillux's palette tour: the visual cross-fades through the vivid palettes (CORE-2369).
    gl.uniform4f(at("u.palette"), -1, 0, this.osc.paletteTour(this.time, t.paletteOffset), 0);
    gl.uniform4fv(at("u.presentation0"), PRESENTATION);
    gl.uniform4f(
      at("u.presentation1"),
      this.presentBlock[OVERLAY_FLOAT],
      QUALITY_LADDER[this.level].reduce,
      0,
      0,
    );
    gl.uniform4fv(at("u.safeRect"), SAFE_RECT);
    for (const k of AUDIO_KEYS) gl.uniform1f(at(`u.${k}`), audio.values[k]);
    gl.uniform4fv(at("u.spectrum[0]"), audio.spectrum);
    gl.uniform1i(at("uImage"), 0);
    gl.uniform1i(at("uPrev"), 1);
  }

  // present0 (Oscillux's vivid saturation, no switch, history on, vivid vibrance), the overlay block the wasm
  // core packs for this frame, post0 (no bloom, aberration or grain; dither of one code) and
  // post1 (the frame seed).
  private packPresent(W: number, H: number, dt: number, reducedMotion: boolean): void {
    const o = this.overlay;
    o.viewportWidth = W;
    o.viewportHeight = H;
    o.frameIntervalMs = Math.min(Math.max(dt * 1000, 1e-3), 1000);
    o.reducedMotion = reducedMotion;
    const status = this.regions.pack(o);
    if (status !== 0) throw new Error(`visualizer: overlay ${this.osc.statusString(status)}`);
    const block = this.presentBlock;
    block.set([this.vivid[0], 1, 1, this.vivid[1]]);
    block.set(
      new Float32Array(this.osc.wasmMemory().buffer, this.regions.uniformsPtr(), OVERLAY_BYTES / 4),
      OVERLAY_FLOAT,
    );
    block.set([0, 0, 0, 1, this.presented++ % 16777216, 0, 0, 0], POST_FLOAT);
  }

  private limit(size: Size, frameMs: number): void {
    const gl = this.gl;
    const L = size.limiter;
    this.clockMs += frameMs;
    // The interval up to a host write of the grant state cannot refill the deficit it wrote.
    // The fraction of a unit carries to the next frame, so the refill keeps the exact rate. A
    // token is TOKEN - 1 units of credit, so a carried fraction cannot finish one early (Codex
    // round 16).
    if (this.limiterSpent) this.refillCarry = 0;
    const refillMs = this.limiterSpent ? 0 : frameMs;
    const earned =
      this.refillCarry +
      Math.min(1, Math.max(0, (2 * MAX_FLASH_HZ * refillMs) / 1000)) * (TOKEN - 1);
    const refill = Math.min(TOKEN, Math.floor(earned));
    this.refillCarry = Math.min(1, earned - refill);
    const tick = Math.floor(this.clockMs / TICK_MS) % TICKS;
    this.limiterBlock.set([
      L.window.cols,
      L.window.rows,
      L.window.limit,
      refill,
      tick,
      frameMs,
      0,
      0,
    ]);
    gl.bindBuffer(gl.UNIFORM_BUFFER, this.limiterUbo);
    gl.bufferSubData(gl.UNIFORM_BUFFER, 0, this.limiterBlock);
    gl.bindBufferBase(gl.UNIFORM_BUFFER, 0, this.limiterUbo);
    const a = L.cur;
    const b = a ^ 1;
    const [rows, reduce, cells, windows, grant, output] = this.limiter;
    const W = size.width;
    const H = size.height;
    this.pass(rows, L.fRows, 64, H, [size.frame, L.track[a]]);
    this.pass(reduce, L.fS, 64, 36, [L.rows]);
    this.pass(cells, L.fC, 16, 9, [L.s]);
    this.pass(windows, L.fW, 16, 9, [L.c]);
    this.pass(grant, L.fG[b], 1, 1, [L.c, L.w, L.g[a]]);
    this.pass(output, L.fOut[b], W, H, [size.frame, L.out[a], L.track[a], L.g[b]]);
    L.cur = b;
    this.limiterSpent = false;
    this.grantSettled = false;
  }

  private pass(
    p: WebGLProgram,
    fbo: WebGLFramebuffer,
    w: number,
    h: number,
    inputs: WebGLTexture[],
  ): void {
    const gl = this.gl;
    gl.useProgram(p);
    gl.bindFramebuffer(gl.FRAMEBUFFER, fbo);
    gl.viewport(0, 0, w, h);
    inputs.forEach((t, unit) => bind(gl, unit, t));
    gl.drawArrays(gl.TRIANGLES, 0, 3);
  }

  // Starts a frame at a geometry: new targets, the limiter's output black and tracked black and
  // every budget written spent. This is the only way a frame comes onto the screen, at the first
  // draw and after every black. The host cannot know what the canvas replaces on screen, so the
  // first frame is rationed like any change after a host write, and a resize never hands a strobe
  // a fresh flash budget (CORE-2516, Oscillux shaders/limiter/README.md, "Ping-pong and reset").
  private start(g: Geometry): Size {
    const gl = this.gl;
    const { width: W, height: H } = g;
    const canvas = gl.canvas as HTMLCanvasElement;
    canvas.width = W;
    canvas.height = H;
    const { internal, type } = this.feedbackFormat;
    const render = renderQuality(W, H, this.level);
    const feedback = this.feedbackPair(render.width, render.height, internal, type);
    const frame = this.storage(W, H, gl.RGBA8);
    const history = [0, 1].map(() => {
      const tex = this.texture(W, H, internal, gl.RGBA, type, gl.NEAREST);
      const fbo = this.framebuffer([frame, tex]);
      gl.clearBufferfv(gl.COLOR, 1, [0, 0, 0, 0]);
      return { tex, fbo };
    });
    const rows = this.storage(64, H, gl.RGBA32UI);
    const out = [0, 1].map(() => this.storage(W, H, gl.RGBA8));
    const track = [0, 1].map(() => this.storage(W, H, gl.RGBA16UI));
    const fOut = out.map((x, i) => this.framebuffer([x, track[i]]));
    for (const f of fOut) this.clearOutput(f);
    const window = limiterWindow(W, H, g.texel, g.visibleWidth, g.visibleHeight);
    const limiter = this.newLimiterState(rows, out, track, fOut, window);
    this.spend(limiter);
    this.size = {
      ...g,
      viewWidth: W * g.texel,
      viewHeight: H * g.texel,
      renderWidth: render.width,
      renderHeight: render.height,
      feedback,
      outgoing: null,
      blend: null,
      frame,
      history,
      limiter,
    };
    return this.size;
  }

  // Marks every limiter budget just spent, in the grant state the next frame reads.
  private spend(L: Limiter): void {
    const gl = this.gl;
    gl.bindFramebuffer(gl.FRAMEBUFFER, L.fG[L.cur]);
    gl.clearBufferuiv(gl.COLOR, 0, SPENT);
    gl.bindFramebuffer(gl.FRAMEBUFFER, null);
    this.limiterSpent = true;
  }

  // Moves the visual to a governor level between switches: only its feedback pair is rebuilt,
  // at the new render size, with the last frame scaled in so the trails carry on. The present
  // history and the flash limiter stay at the canvas size and keep their state.
  private setLevel(size: Size, level: number): void {
    if (level === this.level) return;
    this.level = Math.min(TOP_LEVEL, Math.max(0, level));
    const render = renderQuality(size.width, size.height, this.level);
    if (render.width === size.renderWidth && render.height === size.renderHeight) return;
    const { internal, type } = this.feedbackFormat;
    const feedback = this.feedbackPair(render.width, render.height, internal, type, false);
    this.copy(
      size.feedback[0],
      size.renderWidth,
      size.renderHeight,
      feedback[0],
      render.width,
      render.height,
    );
    for (const t of size.feedback) {
      this.gl.deleteTexture(t.tex);
      this.gl.deleteFramebuffer(t.fbo);
    }
    size.feedback = feedback;
    size.renderWidth = render.width;
    size.renderHeight = render.height;
  }

  // Copies one target into another, scaling with linear filtering when the sizes differ.
  private copy(from: Target, fw: number, fh: number, to: Target, tw: number, th: number): void {
    const gl = this.gl;
    gl.bindFramebuffer(gl.READ_FRAMEBUFFER, from.fbo);
    gl.bindFramebuffer(gl.DRAW_FRAMEBUFFER, to.fbo);
    const scaled = fw !== tw || fh !== th;
    gl.blitFramebuffer(
      0,
      0,
      fw,
      fh,
      0,
      0,
      tw,
      th,
      gl.COLOR_BUFFER_BIT,
      scaled ? gl.LINEAR : gl.NEAREST,
    );
    gl.bindFramebuffer(gl.READ_FRAMEBUFFER, null);
    gl.bindFramebuffer(gl.DRAW_FRAMEBUFFER, null);
  }

  private feedbackPair(
    W: number,
    H: number,
    internal: number,
    type: number,
    verify = true,
  ): Target[] {
    const gl = this.gl;
    return [0, 1].map(() => {
      const tex = this.texture(W, H, internal, gl.RGBA, type, gl.LINEAR);
      const fbo = this.framebuffer([tex], verify);
      gl.clearBufferfv(gl.COLOR, 0, [0, 0, 0, 1]);
      return { tex, fbo };
    });
  }

  // A fresh limiter: its output targets arrive black and tracked black, and start writes its
  // budgets spent.
  private newLimiterState(
    rows: WebGLTexture,
    out: WebGLTexture[],
    track: WebGLTexture[],
    fOut: WebGLFramebuffer[],
    window: LimiterWindow,
  ): Limiter {
    const gl = this.gl;
    const s = this.storage(64, 36, gl.RGBA32UI);
    const c = this.storage(16, 9, gl.RGBA32UI);
    const w = this.storage(16, 9, gl.RGBA16UI);
    const g = [0, 1].map(() => this.storage(1, 1, gl.RGBA16UI));
    const limiter: Limiter = {
      rows,
      s,
      c,
      w,
      g,
      track,
      out,
      fRows: this.framebuffer([rows]),
      fS: this.framebuffer([s]),
      fC: this.framebuffer([c]),
      fW: this.framebuffer([w]),
      fG: g.map((x) => this.framebuffer([x])),
      fOut,
      window,
      cur: 0,
    };
    for (const f of limiter.fG) this.clearUint(f, 1);
    return limiter;
  }

  // Frees the frame's targets.
  private freeSize(): void {
    const s = this.size;
    if (!s) return;
    const gl = this.gl;
    const L = s.limiter;
    for (const t of [...s.feedback, ...s.history]) {
      gl.deleteTexture(t.tex);
      gl.deleteFramebuffer(t.fbo);
    }
    gl.deleteTexture(s.frame);
    for (const t of [L.rows, L.s, L.c, L.w, ...L.g, ...L.track, ...L.out]) gl.deleteTexture(t);
    for (const f of [L.fRows, L.fS, L.fC, L.fW, ...L.fG, ...L.fOut]) gl.deleteFramebuffer(f);
    this.size = null;
  }

  private texture(
    w: number,
    h: number,
    internal: number,
    format: number,
    type: number,
    mode: number,
  ) {
    const gl = this.gl;
    const t = must(gl.createTexture());
    gl.bindTexture(gl.TEXTURE_2D, t);
    gl.texImage2D(gl.TEXTURE_2D, 0, internal, w, h, 0, format, type, null);
    filter(gl, mode);
    return t;
  }

  private storage(w: number, h: number, internal: number): WebGLTexture {
    const gl = this.gl;
    const t = must(gl.createTexture());
    gl.bindTexture(gl.TEXTURE_2D, t);
    gl.texStorage2D(gl.TEXTURE_2D, 1, internal, w, h);
    filter(gl, gl.NEAREST);
    return t;
  }

  // checkFramebufferStatus waits on the GPU process, including any shader still compiling in
  // parallel (over 500 ms seen on a switch), so the targets a switch adds skip it: they reuse
  // the feedback format start already checked.
  private framebuffer(attachments: WebGLTexture[], verify = true): WebGLFramebuffer {
    const gl = this.gl;
    const f = must(gl.createFramebuffer());
    gl.bindFramebuffer(gl.FRAMEBUFFER, f);
    attachments.forEach((t, i) =>
      gl.framebufferTexture2D(gl.FRAMEBUFFER, gl.COLOR_ATTACHMENT0 + i, gl.TEXTURE_2D, t, 0),
    );
    gl.drawBuffers(attachments.map((_, i) => gl.COLOR_ATTACHMENT0 + i));
    if (verify && gl.checkFramebufferStatus(gl.FRAMEBUFFER) !== gl.FRAMEBUFFER_COMPLETE) {
      throw new Error("visualizer: incomplete framebuffer");
    }
    return f;
  }

  private clearUint(fbo: WebGLFramebuffer, attachments: number): void {
    const gl = this.gl;
    gl.bindFramebuffer(gl.FRAMEBUFFER, fbo);
    for (let i = 0; i < attachments; i++) gl.clearBufferuiv(gl.COLOR, i, new Uint32Array(4));
  }

  // An output target: the frame black and the trackers tracked black (zero), so the limiter
  // counts the first frame's rises from black (Oscillux shaders/limiter/README.md).
  private clearOutput(fbo: WebGLFramebuffer): void {
    const gl = this.gl;
    gl.bindFramebuffer(gl.FRAMEBUFFER, fbo);
    gl.clearBufferfv(gl.COLOR, 0, new Float32Array(4));
    gl.clearBufferuiv(gl.COLOR, 1, new Uint32Array(4));
  }

  private link(vertex: string, fragment: string): WebGLProgram {
    const p = this.compile(vertex, fragment);
    this.check(p);
    return p;
  }

  // Starts compiling and linking a program; check() reads the result, which waits for the
  // driver unless it has finished.
  private compile(vertex: string, fragment: string): WebGLProgram {
    const gl = this.gl;
    const p = must(gl.createProgram());
    // Owned from creation, so a failure below leaves them to dispose.
    this.linked.push(p);
    for (const [type, source] of [
      [gl.VERTEX_SHADER, vertex],
      [gl.FRAGMENT_SHADER, fragment],
    ] as const) {
      const s = must(gl.createShader(type));
      this.shaders.push(s);
      gl.shaderSource(s, source);
      gl.compileShader(s);
      gl.attachShader(p, s);
    }
    gl.bindAttribLocation(p, 0, "aPos");
    gl.linkProgram(p);
    return p;
  }

  private check(p: WebGLProgram): void {
    const gl = this.gl;
    if (gl.getProgramParameter(p, gl.LINK_STATUS) || gl.isContextLost()) return;
    for (const s of gl.getAttachedShaders(p) ?? []) {
      if (!gl.getShaderParameter(s, gl.COMPILE_STATUS)) {
        throw new Error(`visualizer: shader: ${gl.getShaderInfoLog(s)}`);
      }
    }
    throw new Error(`visualizer: program: ${gl.getProgramInfoLog(p)}`);
  }
}

function bind(gl: WebGL2RenderingContext, unit: number, tex: WebGLTexture | null): void {
  gl.activeTexture(gl.TEXTURE0 + unit);
  gl.bindTexture(gl.TEXTURE_2D, tex);
}

function filter(gl: WebGL2RenderingContext, mode: number): void {
  gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, mode);
  gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, mode);
  gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_S, gl.CLAMP_TO_EDGE);
  gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_T, gl.CLAMP_TO_EDGE);
}

function must<T>(value: T | null): T {
  if (value === null) throw new Error("visualizer: WebGL object refused");
  return value;
}
