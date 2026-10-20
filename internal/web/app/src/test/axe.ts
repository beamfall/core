import axe from "axe-core";

// ADR-0100 cross-surface accessibility contract — the web (Core Web UI) arm of
// the per-renderer a11y conformance check (docs/design/accessibility-contract.md).
// axe-core runs the WCAG 2.x / 2.2 Level A + AA *structural* success criteria —
// reachability, accessible names, roles, ARIA validity, and tabindex order —
// over a rendered surface in jsdom.
//
// `color-contrast` is DISABLED on purpose: jsdom has no layout/paint engine, so
// axe cannot resolve computed colors against rendered backgrounds — the rule
// reports `incomplete`, never a real pass. ADR-0100 behavior 4 (contrast) is
// owned and verified at the design-token layer in beamfall-design-system
// (`tokens/README.md` "Light-theme accessibility") and in a real-browser pass;
// see docs/design/accessibility-contract.md §Tooling. Asserting it here would be
// a green check that proves nothing, which the gate philosophy forbids.
const RULESET: axe.RunOptions = {
  runOnly: {
    type: "tag",
    values: ["wcag2a", "wcag2aa", "wcag21a", "wcag21aa", "wcag22aa"],
  },
  rules: {
    "color-contrast": { enabled: false },
  },
};

export async function runAxe(container: Element): Promise<axe.AxeResults> {
  return axe.run(container, RULESET);
}

function format(results: axe.AxeResults): string {
  return results.violations
    .map(
      (v) =>
        `  ✗ ${v.id} (${v.impact ?? "n/a"}): ${v.help}\n` +
        v.nodes.map((n) => `      at ${n.target.join(" ")}`).join("\n"),
    )
    .join("\n");
}

/**
 * Assert a rendered surface has no axe-core violations under the WCAG A+AA
 * ruleset above. Throws with a per-node report on failure so a regression names
 * the offending element, not just a count.
 */
export async function expectNoAxeViolations(container: Element): Promise<void> {
  const results = await runAxe(container);
  if (results.violations.length > 0) {
    throw new Error(
      `axe-core found ${results.violations.length} accessibility violation(s):\n${format(results)}`,
    );
  }
}

/**
 * ADR-0100 behavior 2 (deterministic focus order). A positive tabindex breaks
 * the document's natural, predictable focus order; the contract allows only `0`
 * (in natural order) and `-1` (programmatically focusable, out of tab order).
 */
export function expectNoPositiveTabindex(container: Element): void {
  const offenders = Array.from(container.querySelectorAll("[tabindex]")).filter(
    (el) => Number(el.getAttribute("tabindex")) > 0,
  );
  if (offenders.length > 0) {
    throw new Error(
      `positive tabindex breaks deterministic focus order (ADR-0100 behavior 2):\n` +
        offenders
          .map(
            (el) => `  ✗ <${el.tagName.toLowerCase()} tabindex="${el.getAttribute("tabindex")}">`,
          )
          .join("\n"),
    );
  }
}
