// Draws one Oscillux visual on a WebGL2 canvas. A frame is, in order: the visual's pass into a
// feedback target (during a switch, the outgoing visual's pass too and Oscillux's switch pass
// blending the two), Oscillux's present pass (its overlay block packed by the wasm core) into an
// RGBA8 frame, the seven passes of Oscillux's frame flash limiter, and a blit of the limited
// frame to the canvas. The pass order, bindings and limiter clock follow the release's own
// reference hosts (hosts/web/host.js and tools/shader/limiter-check.mjs at the pinned rev).
// Every shader is Oscillux's, unchanged; this file only binds and schedules them.

import type { AudioFrame } from "./analysis";
import { AUDIO_KEYS } from "./analysis";
import { CadenceGate } from "./cadence";
import type { Oscillux } from "./oscillux";
import { shaderSource } from "./oscillux";
import type { Visual } from "./visuals";

// The frame flash limiter: the WCAG 2.x reference window (5 x 3 cells, 64 sub-blocks) and its
// clock (tools/shader/limiter-model.mjs, limiter-geometry.mjs).
const LIMIT_COLS = 5;
const LIMIT_ROWS = 3;
const LIMIT_SUBBLOCKS = 64;
const TICK_MS = 1000 / 15 / 64;
const TOKEN = 65025;
const MAX_FLASH_HZ = 3;
/** The limiter's smallest frame: one pixel per sub-block. */
export const MIN_WIDTH = 64;
export const MIN_HEIGHT = 36;

const LIMITER = ["rows", "reduce", "cells", "windows", "grant", "state", "output"] as const;
const LIMITER_SAMPLERS: Record<(typeof LIMITER)[number], readonly string[]> = {
  rows: ["uFrame", "uHist"],
  reduce: ["uRows", "uT0", "uT1"],
  cells: ["uS", "uT2"],
  windows: ["uC"],
  grant: ["uC", "uW", "uG"],
  state: ["uS", "uG", "uT0", "uT1", "uT2"],
  output: ["uFrame", "uHist", "uT1"],
};
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
  t0: WebGLTexture[];
  t1: WebGLTexture[];
  t2: WebGLTexture[];
  out: WebGLTexture[];
  fRows: WebGLFramebuffer;
  fS: WebGLFramebuffer;
  fC: WebGLFramebuffer;
  fW: WebGLFramebuffer;
  fG: WebGLFramebuffer[];
  fState: WebGLFramebuffer[];
  fOut: WebGLFramebuffer[];
  cur: number;
}

interface Size {
  width: number;
  height: number;
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
  private size: Size | null = null;
  private time = 0;
  private clockMs = 0;
  private presented = 0;

  /** The feedback and history format in use, for diagnostics (CORE-2481). */
  get feedbackLabel(): FeedbackLabel {
    return this.feedbackFormat.label;
  }

  /**
   * A renderer on the canvas, or null without a hardware WebGL2 context. A software one
   * (SwiftShader, say) runs every full-frame pass on the CPU and stalls the page, so it is
   * refused rather than used for decoration.
   */
  static async create(canvas: HTMLCanvasElement, osc: Oscillux): Promise<Renderer | null> {
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
    return new Renderer(gl, osc, vertex, present, blend, limiter);
  }

  private constructor(
    gl: WebGL2RenderingContext,
    osc: Oscillux,
    vertex: string,
    present: string,
    blend: string,
    limiter: string[],
  ) {
    this.gl = gl;
    this.osc = osc;
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
  // ended (or that a resize cut short) is dropped here, and a visual queued behind it starts.
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

  /** Draws one frame at width x height device pixels, dt seconds after the last. */
  draw(
    width: number,
    height: number,
    dt: number,
    audio: AudioFrame,
    reducedMotion: boolean,
    frameMs = dt * 1000,
  ): void {
    if (!this.visual) return;
    const gl = this.gl;
    this.reducedMotion = reducedMotion;
    const size = this.resize(Math.max(MIN_WIDTH, width), Math.max(MIN_HEIGHT, height));
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
    const v = this.visual;
    const { renderWidth: rW, renderHeight: rH } = size;
    gl.bindBuffer(gl.ARRAY_BUFFER, this.quad);

    // 1. The visual at its render size, reading its previous frame; during a switch the
    // outgoing one too.
    const next = this.drawVisual(v, size.feedback, rW, rH, dt, audio, reducedMotion);
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
    size.history = [histNext, histPrev];

    // 3. The frame flash limiter.
    this.limit(size, dt);

    // 4. The limited frame to the canvas.
    const L = size.limiter;
    gl.bindFramebuffer(gl.READ_FRAMEBUFFER, L.fOut[L.cur]);
    gl.bindFramebuffer(gl.DRAW_FRAMEBUFFER, null);
    gl.blitFramebuffer(0, 0, W, H, 0, 0, W, H, gl.COLOR_BUFFER_BIT, gl.NEAREST);
    gl.bindFramebuffer(gl.READ_FRAMEBUFFER, null);
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

  private limit(size: Size, dt: number): void {
    const gl = this.gl;
    const L = size.limiter;
    this.clockMs += dt * 1000;
    const refill = Math.floor(Math.min(1, Math.max(0, 2 * MAX_FLASH_HZ * dt)) * TOKEN);
    const tick = Math.floor(this.clockMs / TICK_MS) % 255;
    this.limiterBlock.set([
      LIMIT_COLS,
      LIMIT_ROWS,
      LIMIT_SUBBLOCKS,
      refill,
      tick,
      Math.fround(dt * 1000),
      0,
      0,
    ]);
    gl.bindBuffer(gl.UNIFORM_BUFFER, this.limiterUbo);
    gl.bufferSubData(gl.UNIFORM_BUFFER, 0, this.limiterBlock);
    gl.bindBufferBase(gl.UNIFORM_BUFFER, 0, this.limiterUbo);
    const a = L.cur;
    const b = a ^ 1;
    const [rows, reduce, cells, windows, grant, state, output] = this.limiter;
    const W = size.width;
    const H = size.height;
    this.pass(rows, L.fRows, 64, H, [size.frame, L.out[a]]);
    this.pass(reduce, L.fS, 64, 36, [L.rows, L.t0[a], L.t1[a]]);
    this.pass(cells, L.fC, 16, 9, [L.s, L.t2[a]]);
    this.pass(windows, L.fW, 16, 9, [L.c]);
    this.pass(grant, L.fG[b], 1, 1, [L.c, L.w, L.g[a]]);
    this.pass(state, L.fState[b], 64, 36, [L.s, L.g[b], L.t0[a], L.t1[a], L.t2[a]]);
    this.pass(output, L.fOut[b], W, H, [size.frame, L.out[a], L.t1[b]]);
    L.cur = b;
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

  // The targets for a W x H canvas, rebuilt when the size changes. The flash limiter's
  // temporal state (trackers, hold field and tokens) lives on the fixed 64 x 36 sub-block grid,
  // so it carries over a resize, and the last output is scaled into the new history: resizing
  // never hands a strobe a fresh flash budget (CORE-2516).
  private resize(W: number, H: number): Size {
    if (this.size && this.size.width === W && this.size.height === H) return this.size;
    // A size change ends a switch, as in the reference host.
    this.endSwitch();
    const old = this.size;
    const kept = old?.limiter ?? null;
    this.freeSize(kept !== null);
    const gl = this.gl;
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
    const fOut = out.map((x) => this.framebuffer([x]));
    const limiter: Limiter = kept
      ? { ...kept, rows, out, fRows: this.framebuffer([rows]), fOut }
      : this.newLimiterState(rows, out, fOut);
    if (kept && old) {
      this.copy(
        { tex: kept.out[kept.cur], fbo: kept.fOut[kept.cur] },
        old.width,
        old.height,
        { tex: out[kept.cur], fbo: fOut[kept.cur] },
        W,
        H,
      );
      gl.deleteTexture(kept.rows);
      gl.deleteFramebuffer(kept.fRows);
      kept.out.forEach((t) => gl.deleteTexture(t));
      kept.fOut.forEach((f) => gl.deleteFramebuffer(f));
    }
    this.size = {
      width: W,
      height: H,
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

  // A fresh limiter on the fixed sub-block grid, with no sub-block tracked.
  private newLimiterState(
    rows: WebGLTexture,
    out: WebGLTexture[],
    fOut: WebGLFramebuffer[],
  ): Limiter {
    const gl = this.gl;
    const u16 = (w: number, h: number) => this.storage(w, h, gl.RGBA16UI);
    const two = <T>(make: () => T) => [make(), make()];
    const s = u16(64, 36);
    const c = u16(16, 9);
    const w = u16(16, 9);
    const g = two(() => u16(1, 1));
    const t0 = two(() => u16(64, 36));
    const t1 = two(() => u16(64, 36));
    const t2 = two(() => u16(64, 36));
    const limiter: Limiter = {
      rows,
      s,
      c,
      w,
      g,
      t0,
      t1,
      t2,
      out,
      fRows: this.framebuffer([rows]),
      fS: this.framebuffer([s]),
      fC: this.framebuffer([c]),
      fW: this.framebuffer([w]),
      fG: g.map((x) => this.framebuffer([x])),
      fState: [0, 1].map((i) => this.framebuffer([t0[i], t1[i], t2[i]])),
      fOut,
      cur: 0,
    };
    for (const f of limiter.fG) this.clearUint(f, 1);
    for (const f of limiter.fState) this.clearUint(f, 3);
    return limiter;
  }

  // Frees the size's targets. With keepLimiter the limiter's textures stay for resize to carry
  // over (and free its size-bound ones after copying the last output).
  private freeSize(keepLimiter = false): void {
    const s = this.size;
    if (!s) return;
    const gl = this.gl;
    const L = s.limiter;
    for (const t of [...s.feedback, ...s.history]) {
      gl.deleteTexture(t.tex);
      gl.deleteFramebuffer(t.fbo);
    }
    gl.deleteTexture(s.frame);
    if (!keepLimiter) {
      for (const t of [L.rows, L.s, L.c, L.w, ...L.g, ...L.t0, ...L.t1, ...L.t2, ...L.out]) {
        gl.deleteTexture(t);
      }
      for (const f of [L.fRows, L.fS, L.fC, L.fW, ...L.fG, ...L.fState, ...L.fOut])
        gl.deleteFramebuffer(f);
    }
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
  // the feedback format resize already checked.
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
