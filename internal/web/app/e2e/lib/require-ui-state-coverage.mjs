import { mkdirSync, renameSync, writeFileSync } from "node:fs";
import { dirname } from "node:path";
import { VISUAL_PARITY_STATE_ATTACHMENT_PREFIX } from "./required-visual-parity-states.mjs";
import { UI_STATE_ANNOTATION, UI_STATE_GAPS, UI_STATES } from "./ui-states.mjs";

// Fails the smoke when a UI state in ui-states.mjs is neither covered by a passing test nor
// listed in UI_STATE_GAPS, when a listed gap has become covered, or when a test declares an id the
// inventory does not have. Only meaningful on the unfiltered run, so the smoke adds it to that
// run alone.

const rowIds = new Set(UI_STATES.map((row) => row.id));
const rowsByGolden = new Map();
for (const row of UI_STATES) {
  for (const golden of row.visualParity ?? []) {
    rowsByGolden.set(golden, [...(rowsByGolden.get(golden) ?? []), row.id]);
  }
}

export function evaluateUiStateCoverage(
  covered,
  unknown,
  inventory = UI_STATES,
  gaps = UI_STATE_GAPS,
) {
  const failures = [];
  const ids = new Set(inventory.map((row) => row.id));
  for (const id of Object.keys(gaps)) {
    if (!ids.has(id)) failures.push(`gap-not-in-inventory=${id}`);
    else if (covered.has(id)) failures.push(`gap-now-covered=${id}`);
  }
  for (const id of [...unknown.keys()].sort()) {
    const tests = unknown instanceof Map ? [...unknown.get(id)].sort().join(", ") : "";
    failures.push(tests ? `unknown-state=${id} in ${tests}` : `unknown-state=${id}`);
  }
  const rows = inventory.map((row) => ({
    id: row.id,
    status: covered.has(row.id) ? "covered" : Object.hasOwn(gaps, row.id) ? "gap" : "uncovered",
  }));
  for (const row of rows) if (row.status === "uncovered") failures.push(`uncovered=${row.id}`);
  return { failures, rows };
}

function publish(rows) {
  const path = process.env.BEAMFALL_WEB_E2E_UI_STATE_COVERAGE;
  if (!path) return;
  const count = (status) => rows.filter((row) => row.status === status).length;
  const payload = {
    schemaVersion: 1,
    total: rows.length,
    covered: count("covered"),
    gaps: count("gap"),
    uncovered: count("uncovered"),
    rows,
  };
  const temporary = `${path}.${process.pid}.tmp`;
  mkdirSync(dirname(path), { recursive: true });
  writeFileSync(temporary, `${JSON.stringify(payload, null, 2)}\n`, {
    mode: 0o600,
  });
  renameSync(temporary, path);
}

// "project › file › title" for a reporter test, or "" when the runner gives no title path.
function testName(test) {
  return typeof test.titlePath === "function" ? test.titlePath().filter(Boolean).join(" › ") : "";
}

function record(map, id, name) {
  if (!map.has(id)) map.set(id, new Set());
  if (name) map.get(id).add(name);
}

class RequireUiStateCoverageReporter {
  // Row id -> names of the passed tests that covered it, and the same for ids no row has.
  covered = new Map();
  unknown = new Map();

  onTestEnd(test, result) {
    if (result.status !== "passed") return;
    const name = testName(test);
    for (const annotation of result.annotations ?? test.annotations ?? []) {
      if (annotation.type !== UI_STATE_ANNOTATION) continue;
      const id = annotation.description ?? "";
      record(rowIds.has(id) ? this.covered : this.unknown, id, name);
    }
    for (const attachment of result.attachments ?? []) {
      if (!attachment.name.startsWith(VISUAL_PARITY_STATE_ATTACHMENT_PREFIX)) continue;
      if (attachment.body?.toString("utf8") !== "passed") continue;
      const golden = attachment.name.slice(VISUAL_PARITY_STATE_ATTACHMENT_PREFIX.length);
      for (const id of rowsByGolden.get(golden) ?? []) record(this.covered, id, name);
    }
  }

  onEnd() {
    const { failures, rows } = evaluateUiStateCoverage(this.covered, this.unknown);
    publish(
      rows.map((row) =>
        row.status === "covered" ? { ...row, tests: [...this.covered.get(row.id)].sort() } : row,
      ),
    );
    if (failures.length === 0) return undefined;
    for (const failure of failures) {
      process.stderr.write(`FAIL web-ui-e2e-smoke ui-state ${failure}\n`);
    }
    process.stderr.write(
      "FAIL web-ui-e2e-smoke ui-state coverage: cover the state with coverUiStates() in a passing " +
        "test, or list it in UI_STATE_GAPS (e2e/lib/ui-states.mjs) with a reason\n",
    );
    return { status: "failed" };
  }

  printsToStdio() {
    return false;
  }
}

export default RequireUiStateCoverageReporter;
