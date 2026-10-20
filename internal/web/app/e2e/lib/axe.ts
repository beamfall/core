import { readFileSync } from "node:fs";
import { createRequire } from "node:module";
import { expect, type Page } from "@playwright/test";

const require = createRequire(import.meta.url);
const axeSource = readFileSync(require.resolve("axe-core/axe.min.js"), "utf8");

interface AxeViolation {
  id: string;
  impact?: string;
  help: string;
  nodes: { target: string[] }[];
}

export async function runAxe(page: Page): Promise<AxeViolation[]> {
  // The production `default-src 'self'` CSP blocks an injected inline script.
  // Playwright evaluation runs the same axe bundle in the page's main world
  // without adding a script element, and works in Chromium, Firefox, and WebKit.
  await page.evaluate(axeSource);
  return page.evaluate(async () => {
    const axe = (
      window as unknown as {
        axe: {
          run: (context: { exclude: string[][] }) => Promise<{ violations: AxeViolation[] }>;
        };
      }
    ).axe;
    // Base UI gives its visually-hidden focus sentinels role="button" only in
    // WebKit so VoiceOver's virtual cursor reaches the focus trap. They are
    // library plumbing rather than unnamed app commands; their behavior is
    // covered by the keyboard round-trip, so omit only those exact sentinels.
    const result = await axe.run({ exclude: [["[data-base-ui-focus-guard]"]] });
    // WCAG 2.1 SC 1.4.3 exempts text that is "part of an inactive user
    // interface component" from the CONTRAST minimum only. A disabled control
    // is dimmed with `disabled:opacity-50`, which blends foreground and
    // background by the same factor, so axe measures a ratio no colour token
    // can satisfy. Drop only those nodes, only from color-contrast, and only
    // when the element is genuinely inactive (disabled / aria-disabled) —
    // never by opacity, which would also exempt active translucent controls,
    // and never from any other rule.
    const inactive = (target: string[]): boolean => {
      const el = document.querySelector(target.join(" "));
      return el instanceof Element && el.closest("[disabled],[aria-disabled='true']") !== null;
    };
    for (const violation of result.violations) {
      if (violation.id !== "color-contrast") continue;
      violation.nodes = violation.nodes.filter((node) => !inactive(node.target));
    }
    result.violations = result.violations.filter(
      (violation) => violation.id !== "color-contrast" || violation.nodes.length > 0,
    );
    return result.violations.filter((violation) =>
      ["serious", "critical"].includes(violation.impact || ""),
    );
  });
}

export async function expectNoAxeViolations(page: Page): Promise<void> {
  const violations = await runAxe(page);
  expect(
    violations,
    violations
      .map(
        (violation) =>
          `${violation.id}: ${violation.help} ${JSON.stringify(
            violation.nodes.map((node) => node.target),
          )}`,
      )
      .join("\n"),
  ).toEqual([]);
}
