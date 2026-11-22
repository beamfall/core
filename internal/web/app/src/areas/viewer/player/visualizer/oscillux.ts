// Loads the vendored Oscillux v0.2.15 release (vendor/oscillux, AGPL-3.0-only, ADR-0233): the
// wasm core and the GLSL ES 3.00 shader bundle. Both load on first use only, so a page that
// never shows the audio stage downloads none of it. Core adds host glue only: every visual
// effect is Oscillux's (ADR-0228).
import wasmURL from "../../../../../vendor/oscillux/wasm/oscillux_bg.wasm?url";

export type Oscillux = typeof import("../../../../../vendor/oscillux/wasm/oscillux.js");

let core: Promise<Oscillux> | null = null;

/** The initialised Oscillux wasm module, shared by every visualizer on the page. */
export function loadOscillux(): Promise<Oscillux> {
  if (!core) {
    core = (async () => {
      const mod = await import("../../../../../vendor/oscillux/wasm/oscillux.js");
      await mod.default({ module_or_path: wasmURL });
      return mod;
    })();
    // A failed load (offline, a CSP refusal) may be retried by the next visualizer.
    core.catch(() => {
      core = null;
    });
  }
  return core;
}

const sources = import.meta.glob<string>(
  "../../../../../vendor/oscillux/glsl300/**/*.{frag,vert}",
  {
    query: "?raw",
    import: "default",
  },
);

/** One shader's source by its path inside the bundle, such as "post/present.frag". */
export function shaderSource(path: string): Promise<string> {
  const load = sources[`../../../../../vendor/oscillux/glsl300/${path}`];
  if (!load) return Promise.reject(new Error(`oscillux: no shader ${path}`));
  return load();
}
