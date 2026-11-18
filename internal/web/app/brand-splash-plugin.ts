import { readFileSync } from "node:fs";
import { join } from "node:path";
import type { Plugin } from "vite";

// brand-splash: the cold-load splash (CORE-2556). Replaces the BRAND_SPLASH marker in
// index.html with both Cut Beam logo intros vendored from the design system
// (scripts/vendor-brand-intro.mjs), so the intro plays from the first paint instead of waiting
// for the bundle. Each intro sits in its own declarative shadow root: the two SVGs reuse the
// same ids, classes and keyframe names, which would collide in one document. The layout CSS
// (src/styles/brand-splash.css) is inlined beside it; src/lib/brandSplash.ts hands off.

export const BRAND_SPLASH_MARKER = "<!-- beamfall:brand-splash -->";

const LAYOUTS = [
  { layout: "horizontal", file: "beamfall-lockup-intro.svg" },
  { layout: "stacked", file: "beamfall-stacked-intro.svg" },
] as const;

// Each intro's animations start when the parser reaches its SVG, so a response that streams in
// slowly would leave the two clocks apart and a mid-intro rotation would jump. The play state
// comes from --bf-splash-play, which brand-splash.css holds at paused until the ready sentinel
// after both intros has been parsed; both then start in the same frame. The !important beats the
// `animation` shorthand in the SVG's own stylesheet, which resets the play state to running.
const SHADOW_STYLE =
  ":host{display:block}svg{display:block;width:100%;height:auto}" +
  "svg,svg *{animation-play-state:var(--bf-splash-play,running)!important}";

export function brandSplashMarkup(appDir: string): string {
  const read = (path: string) => readFileSync(join(appDir, path), "utf8").trim();
  const layouts = LAYOUTS.map(
    ({ layout, file }) =>
      `<div class="bf-splash__layout" data-layout="${layout}">` +
      `<template shadowrootmode="open"><style>${SHADOW_STYLE}</style>` +
      `${read(`src/assets/brand/intro/${file}`)}</template></div>`,
  ).join("");
  return (
    `<style>${read("src/styles/brand-splash.css")}</style>` +
    `<div id="bf-splash" class="bf-splash" aria-hidden="true">${layouts}` +
    `<i class="bf-splash__ready" hidden></i></div>`
  );
}

export function brandSplash(options: { appDir: string }): Plugin {
  return {
    name: "beamfall-brand-splash",
    transformIndexHtml(html) {
      if (!html.includes(BRAND_SPLASH_MARKER)) {
        throw new Error(`index.html is missing the ${BRAND_SPLASH_MARKER} marker`);
      }
      return html.replace(BRAND_SPLASH_MARKER, () => brandSplashMarkup(options.appDir));
    },
  };
}
