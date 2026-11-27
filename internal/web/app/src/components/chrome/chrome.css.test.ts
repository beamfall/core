import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";

const css = readFileSync(resolve("src/components/chrome/chrome.css"), "utf8");

// The body of every rule whose selector (or last selector of a list) is this one.
function rules(selector: string): string[] {
  const bodies: string[] = [];
  for (
    let at = css.indexOf(`\n${selector} {`);
    at >= 0;
    at = css.indexOf(`\n${selector} {`, at + 1)
  ) {
    bodies.push(css.slice(at, css.indexOf("}", at)));
  }
  return bodies;
}

// Forced-colors mode drops box-shadows and fills, so a focus style drawn with
// either must keep an outline (a transparent one shows there in a system color).
describe("chrome focus styles in forced-colors mode", () => {
  it("never removes the outline", () => {
    expect(css).not.toMatch(/outline:\s*(none|0)\b/);
  });

  it.each([".bf-account-item:focus-visible"])("keeps an outline on %s", (selector) => {
    expect(rules(selector).join("\n")).toMatch(/outline:\s*2px solid transparent;/);
  });
});

// CORE-2882: under viewport-fit=cover the phone tab bar must clear the home
// indicator, and everything it would otherwise cover must move up by the same
// inset.
describe("phone tab bar safe-area insets", () => {
  it.each([
    [
      ".bf-tabbar",
      /padding: 0 env\(safe-area-inset-right, 0px\) env\(safe-area-inset-bottom, 0px\)\s+env\(safe-area-inset-left, 0px\);/,
    ],
    [
      ".bf-shell.has-tabbar .bf-content",
      /padding-bottom: calc\(var\(--bf-tabbar-height\) \+ env\(safe-area-inset-bottom, 0px\)\)/,
    ],
    [
      ":root:has(.bf-tabbar) .bf-mini-player",
      /bottom: calc\(var\(--bf-tabbar-height\) \+ env\(safe-area-inset-bottom, 0px\)\)/,
    ],
  ])("%s includes the bottom inset", (selector, pattern) => {
    expect(rules(selector).join("\n")).toMatch(pattern);
  });
});

// CORE-1228: on landscape phones the fixed account menu is taller than the
// viewport, so it must cap its height and scroll to keep Activity and Console
// reachable.
describe("account menu viewport scroll", () => {
  it("caps .bf-account-panel height within the viewport and scrolls", () => {
    const base = rules(".bf-account-panel").join("\n");
    expect(base).toMatch(/overflow-y:\s*auto;/);
    expect(base).toMatch(/max-height:\s*calc\(100dvh - \d+px\);/);

    const phoneRules: string[] = [];
    for (
      let at = css.indexOf("\n  .bf-account-panel {");
      at >= 0;
      at = css.indexOf("\n  .bf-account-panel {", at + 1)
    ) {
      phoneRules.push(css.slice(at, css.indexOf("}", at)));
    }
    const phoneRule = phoneRules.find((rule) => rule.includes("position: fixed;")) ?? "";
    expect(phoneRule).toMatch(/max-height:\s*calc\(100dvh - 68px/);
  });
});

describe("folder list wrapping for reduced motion and touch pointers", () => {
  it("wraps .bf-folder-name instead of animating marquee truncation", () => {
    const marker = "@media (prefers-reduced-motion: reduce), (pointer: coarse) {";
    const at = css.indexOf(marker);
    expect(at).toBeGreaterThan(-1);
    const blockEnd = css.indexOf("\n}\n", at);
    const block = css.slice(at, blockEnd > at ? blockEnd : undefined);
    expect(block).toMatch(/\[data-register="viewer"\]\s+\.bf-folder-name/);
    expect(block).toMatch(/white-space:\s*normal;/);
    expect(block).toMatch(/overflow:\s*visible;/);
    expect(block).toMatch(/text-overflow:\s*clip;/);
    expect(block).toMatch(/overflow-wrap:\s*anywhere;/);
    expect(block).toMatch(/animation:\s*none;/);
  });

  it("provides a wrapping desktop fallback when scroll-driven marquee is unsupported", () => {
    const marker = "@supports not (animation-timeline: scroll(self inline)) {";
    const at = css.indexOf(marker);
    expect(at).toBeGreaterThan(-1);
    const block = css.slice(at, css.indexOf("\n}\n", at));
    expect(block).toMatch(/\.bf-folder:is\(:hover,\s*:focus-visible\)\s+\.bf-folder-name/);
    expect(block).toMatch(/white-space:\s*normal;/);
    expect(block).toMatch(/overflow:\s*visible;/);
    expect(block).toMatch(/text-overflow:\s*clip;/);
    expect(block).toMatch(/overflow-wrap:\s*anywhere;/);
  });

  it("clips .bf-folder-name outside the @container self-style query when scroll-driven marquee is supported", () => {
    const marker = "@supports (animation-timeline: scroll(self inline)) {";
    const at = css.indexOf(marker);
    expect(at).toBeGreaterThan(-1);
    const containerAt = css.indexOf("@container style(--bf-truncated: 1)", at);
    expect(containerAt).toBeGreaterThan(at);
    const beforeContainer = css.slice(at, containerAt);
    expect(beforeContainer).toMatch(
      /\[data-register="viewer"\]\s+\.bf-folder:is\(:hover,\s*:focus-visible\)\s+\.bf-folder-name\s*\{[^}]*text-overflow:\s*clip;/,
    );
  });
});
