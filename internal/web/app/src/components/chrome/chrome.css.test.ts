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
