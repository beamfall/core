// The raw-colour lint (CORE-1179): app CSS and TSX carry no colour literal outside the
// generated tokens.css, so every preset and theme document reaches every surface.
import { describe, expect, it } from "vitest";
import { checkCss, checkTsx, scan } from "../../../scripts/check-colour-literals.mjs";

describe("raw colour lint", () => {
  it("finds no colour literal in app CSS or TSX", () => {
    expect(scan()).toEqual([]);
  });

  it("flags colour literals in CSS declarations", () => {
    const css = [
      ".a { color: #fff; }",
      ".b { background: oklch(50% 0.1 200); }",
      ".c { border-color: rgba(0, 0, 0, 0.4); }",
      ".d { outline-color: white; }",
    ].join("\n");
    expect(checkCss(css).map((p) => p.match)).toEqual(["#fff", "oklch(", "rgba(", "white"]);
    expect(checkCss(css).map((p) => p.line)).toEqual([1, 2, 3, 4]);
  });

  it("accepts roles, mixes of roles and non-colour values", () => {
    const css = [
      "/* #fff in a comment */",
      ".a { color: var(--text); background: color-mix(in oklab, var(--accent) 20%, transparent); }",
      '.b { font-family: "White Rabbit", sans-serif; content: "#1"; }',
      '.c { grid-template-areas: "red green"; animation-name: fade-white; }',
      ":root { --x: var(--bg-surface); }",
    ].join("\n");
    expect(checkCss(css)).toEqual([]);
  });

  it("flags colour functions, hex strings and Tailwind palette classes in TSX", () => {
    const tsx = [
      'const a = <div className="bg-[oklch(8%_0.01_255)]" />;',
      'const b = <div style={{ color: "#a1b2c3" }} />;',
      'const c = <div className="text-white/70 border-slate-700" />;',
    ].join("\n");
    expect(checkTsx(tsx).map((p) => p.line)).toEqual([1, 2, 3]);
  });

  it("accepts role utilities and ignores comments in TSX", () => {
    const tsx = [
      "// text-white used to be here",
      'const a = <div className="bg-scrim text-on-scrim/70 border-[var(--line)]" />;',
      'const id = "#section";',
    ].join("\n");
    expect(checkTsx(tsx)).toEqual([]);
  });
});
