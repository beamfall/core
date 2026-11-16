// Draws one Oscillux visual on a WebGL2 canvas. A frame is, in order: the visual's pass into a
// feedback target, Oscillux's present pass (its overlay block packed by the wasm core) into an
// RGBA8 frame, the seven passes of Oscillux's frame flash limiter, and a blit of the limited
// frame to the canvas. The pass order, bindings and limiter clock follow the release's own
// reference hosts (hosts/web/host.js and tools/shader/limiter-check.mjs at the pinned rev).
// Every shader is Oscillux's, unchanged; this file only binds and schedules them.
import type { AudioFrame } from "./analysis";
import { AUDIO_KEYS } from "./analysis";
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
  rows: ["uFrame"],
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
  /** The visual's feedback pair: [current, next]. */
  feedback: Target[];
  /** The presented frame, the limiter's input. */
  frame: WebGLTexture;
  /** The present pass's history pair, each framebuffer writing the frame and one history. */
  history: Target[];
  limiter: Limiter;
}

/** A WebGL2 renderer for one canvas, or null where the browser cannot run it. */
export class Renderer {
  private readonly gl: WebGL2RenderingContext;
  private readonly osc: Oscillux;
  private readonly overlay: InstanceType<Oscillux["OverlayConfig"]>;
  private readonly regions: InstanceType<Oscillux["OverlayRegions"]>;
  private readonly feedbackFormat: { internal: number; type: number };
  private readonly quad: WebGLBuffer;
  private readonly image: WebGLTexture;
  private readonly presentUbo: WebGLBuffer;
  private readonly limiterUbo: WebGLBuffer;
  private readonly presentBlock = new Float32Array(PRESENT_FLOATS);
  private readonly limiterBlock = new Float32Array(8);
  private readonly visualVertex: string;
  private present: WebGLProgram;
  private limiter: WebGLProgram[];
  private readonly shaders: WebGLShader[] = [];
  // Every program linked, for dispose.
  private readonly linked: WebGLProgram[] = [];
  private disposed = false;
  private readonly programs = new Map<string, WebGLProgram>();
  private visual: {
    visual: Visual;
    program: WebGLProgram;
    locations: Map<string, WebGLUniformLocation | null>;
  } | null = null;
  private size: Size | null = null;
  private time = 0;
  private clockMs = 0;
  private presented = 0;

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
    const [vertex, present, ...limiter] = await Promise.all([
      shaderSource("visuals/vertex.web.vert"),
      shaderSource("post/present.frag"),
      ...LIMITER.map((name) => shaderSource(`limiter/${name}.frag`)),
    ]);
    return new Renderer(gl, osc, vertex, present, limiter);
  }

  private constructor(
    gl: WebGL2RenderingContext,
    osc: Oscillux,
    vertex: string,
    present: string,
    limiter: string[],
  ) {
    this.gl = gl;
    this.osc = osc;
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
      this.feedbackFormat = gl.getExtension("EXT_color_buffer_float")
        ? { internal: gl.RGBA16F, type: gl.HALF_FLOAT }
        : { internal: gl.RGBA8, type: gl.UNSIGNED_BYTE };

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

  /** Compiles a visual (once) and draws it from the next frame on. */
  async use(visual: Visual): Promise<void> {
    if (this.visual?.visual.id === visual.id) return;
    let program = this.programs.get(visual.id);
    if (!program) {
      const source = await shaderSource(`visuals/${visual.id}.web.frag`);
      // Disposed while the source loaded: allocate nothing more.
      if (this.disposed) return;
      program = this.link(this.visualVertex, source);
      this.programs.set(visual.id, program);
    }
    this.visual = { visual, program, locations: new Map() };
    if (this.size) for (const t of this.size.feedback) this.clear(t.fbo, [0, 0, 0, 1]);
    this.time = 0;
  }

  /** Draws one frame at width x height device pixels, dt seconds after the last. */
  draw(width: number, height: number, dt: number, audio: AudioFrame, reducedMotion: boolean): void {
    const v = this.visual;
    if (!v) return;
    const gl = this.gl;
    const size = this.resize(Math.max(MIN_WIDTH, width), Math.max(MIN_HEIGHT, height));
    const { width: W, height: H } = size;
    this.time += dt;
    gl.bindBuffer(gl.ARRAY_BUFFER, this.quad);

    // 1. The visual, reading its previous frame.
    const [prev, next] = size.feedback;
    gl.useProgram(v.program);
    this.setVisualUniforms(v, W, H, dt, audio, reducedMotion);
    bind(gl, 0, this.image);
    bind(gl, 1, prev.tex);
    gl.bindFramebuffer(gl.FRAMEBUFFER, next.fbo);
    gl.viewport(0, 0, W, H);
    gl.drawArrays(gl.TRIANGLES, 0, 3);
    size.feedback = [next, prev];

    // 2. The present pass, into the frame and the next history.
    this.packPresent(W, H, dt, reducedMotion);
    gl.useProgram(this.present);
    gl.bindBuffer(gl.UNIFORM_BUFFER, this.presentUbo);
    gl.bufferSubData(gl.UNIFORM_BUFFER, 0, this.presentBlock);
    gl.bindBufferBase(gl.UNIFORM_BUFFER, 0, this.presentUbo);
    const [histPrev, histNext] = size.history;
    bind(gl, 0, next.tex);
    bind(gl, 1, next.tex);
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

  /**
   * Sets the visual's image input: the track's artwork, or the neutral stand-in for null. The
   * texture keeps its own copy, so the caller may close the bitmap afterwards.
   */
  setImage(image: ImageBitmap | null): void {
    if (this.disposed) return;
    const gl = this.gl;
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
    this.freeSize();
    for (const p of this.linked) gl.deleteProgram(p);
    for (const s of this.shaders) gl.deleteShader(s);
    // Fields a failed constructor never reached are undefined; GL treats them as null.
    gl.deleteBuffer(this.quad ?? null);
    gl.deleteBuffer(this.presentUbo ?? null);
    gl.deleteBuffer(this.limiterUbo ?? null);
    gl.deleteTexture(this.image ?? null);
    this.linked.length = 0;
    this.shaders.length = 0;
    this.programs.clear();
    this.visual = null;
    this.regions?.free();
    this.overlay?.free();
  }

  private setVisualUniforms(
    v: NonNullable<Renderer["visual"]>,
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
    gl.uniform4fv(at("u.palette"), t.palette);
    gl.uniform4fv(at("u.presentation0"), PRESENTATION);
    gl.uniform4f(at("u.presentation1"), this.presentBlock[OVERLAY_FLOAT], 0, 0, 0);
    gl.uniform4fv(at("u.safeRect"), SAFE_RECT);
    for (const k of AUDIO_KEYS) gl.uniform1f(at(`u.${k}`), audio.values[k]);
    gl.uniform4fv(at("u.spectrum[0]"), audio.spectrum);
    gl.uniform1i(at("uImage"), 0);
    gl.uniform1i(at("uPrev"), 1);
  }

  // present0 (saturation 1, no switch, history on, no vibrance), the overlay block the wasm
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
    block.set([1, 1, 1, 0]);
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
    this.pass(rows, L.fRows, 64, H, [size.frame]);
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

  // The targets for a W x H canvas, rebuilt (and cleared) when the size changes.
  private resize(W: number, H: number): Size {
    if (this.size && this.size.width === W && this.size.height === H) return this.size;
    this.freeSize();
    const gl = this.gl;
    const canvas = gl.canvas as HTMLCanvasElement;
    canvas.width = W;
    canvas.height = H;
    const { internal, type } = this.feedbackFormat;
    const feedback = [0, 1].map(() => {
      const tex = this.texture(W, H, internal, gl.RGBA, type, gl.LINEAR);
      const fbo = this.framebuffer([tex]);
      gl.clearBufferfv(gl.COLOR, 0, [0, 0, 0, 1]);
      return { tex, fbo };
    });
    const frame = this.storage(W, H, gl.RGBA8);
    const history = [0, 1].map(() => {
      const tex = this.texture(W, H, internal, gl.RGBA, type, gl.NEAREST);
      const fbo = this.framebuffer([frame, tex]);
      gl.clearBufferfv(gl.COLOR, 1, [0, 0, 0, 0]);
      return { tex, fbo };
    });
    const u16 = (w: number, h: number) => this.storage(w, h, gl.RGBA16UI);
    const two = <T>(make: () => T) => [make(), make()];
    const rows = this.storage(64, H, gl.RGBA32UI);
    const s = u16(64, 36);
    const c = u16(16, 9);
    const w = u16(16, 9);
    const g = two(() => u16(1, 1));
    const t0 = two(() => u16(64, 36));
    const t1 = two(() => u16(64, 36));
    const t2 = two(() => u16(64, 36));
    const out = two(() => this.storage(W, H, gl.RGBA8));
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
      fOut: out.map((x) => this.framebuffer([x])),
      cur: 0,
    };
    // The limiter starts with no sub-block tracked.
    for (const f of limiter.fG) this.clearUint(f, 1);
    for (const f of limiter.fState) this.clearUint(f, 3);
    this.size = { width: W, height: H, feedback, frame, history, limiter };
    return this.size;
  }

  private freeSize(): void {
    const s = this.size;
    if (!s) return;
    const gl = this.gl;
    const L = s.limiter;
    for (const t of [...s.feedback, ...s.history]) {
      gl.deleteTexture(t.tex);
      gl.deleteFramebuffer(t.fbo);
    }
    for (const t of [s.frame, L.rows, L.s, L.c, L.w, ...L.g, ...L.t0, ...L.t1, ...L.t2, ...L.out]) {
      gl.deleteTexture(t);
    }
    for (const f of [L.fRows, L.fS, L.fC, L.fW, ...L.fG, ...L.fState, ...L.fOut])
      gl.deleteFramebuffer(f);
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

  private framebuffer(attachments: WebGLTexture[]): WebGLFramebuffer {
    const gl = this.gl;
    const f = must(gl.createFramebuffer());
    gl.bindFramebuffer(gl.FRAMEBUFFER, f);
    attachments.forEach((t, i) =>
      gl.framebufferTexture2D(gl.FRAMEBUFFER, gl.COLOR_ATTACHMENT0 + i, gl.TEXTURE_2D, t, 0),
    );
    gl.drawBuffers(attachments.map((_, i) => gl.COLOR_ATTACHMENT0 + i));
    if (gl.checkFramebufferStatus(gl.FRAMEBUFFER) !== gl.FRAMEBUFFER_COMPLETE) {
      throw new Error("visualizer: incomplete framebuffer");
    }
    return f;
  }

  private clear(fbo: WebGLFramebuffer, rgba: number[]): void {
    this.gl.bindFramebuffer(this.gl.FRAMEBUFFER, fbo);
    this.gl.clearBufferfv(this.gl.COLOR, 0, rgba);
  }

  private clearUint(fbo: WebGLFramebuffer, attachments: number): void {
    const gl = this.gl;
    gl.bindFramebuffer(gl.FRAMEBUFFER, fbo);
    for (let i = 0; i < attachments; i++) gl.clearBufferuiv(gl.COLOR, i, new Uint32Array(4));
  }

  private link(vertex: string, fragment: string): WebGLProgram {
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
      if (!gl.getShaderParameter(s, gl.COMPILE_STATUS) && !gl.isContextLost()) {
        throw new Error(`visualizer: shader: ${gl.getShaderInfoLog(s)}`);
      }
      gl.attachShader(p, s);
    }
    gl.bindAttribLocation(p, 0, "aPos");
    gl.linkProgram(p);
    if (!gl.getProgramParameter(p, gl.LINK_STATUS) && !gl.isContextLost()) {
      throw new Error(`visualizer: program: ${gl.getProgramInfoLog(p)}`);
    }
    return p;
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
