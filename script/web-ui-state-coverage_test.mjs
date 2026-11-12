// Unit checks for the web UI state inventory and its coverage reporter (CORE-1305). The inventory
// must list exactly the routes the routers declare, so a new screen cannot ship without a row.
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import Reporter, {
  evaluateUiStateCoverage,
} from "../internal/web/app/e2e/lib/require-ui-state-coverage.mjs";
import { REQUIRED_VISUAL_PARITY_STATE_ATTACHMENTS } from "../internal/web/app/e2e/lib/required-visual-parity-states.mjs";
import {
  CONSOLE_ROUTES,
  IN_PAGE_KINDS,
  IN_PAGE_STATES,
  MANAGE_ROUTES,
  SCREENS,
  TOP_LEVEL_ROUTES,
  UI_STATE_ANNOTATION,
  UI_STATE_GAPS,
  UI_STATES,
  VIEWER_ROUTES,
} from "../internal/web/app/e2e/lib/ui-states.mjs";

const src = fileURLToPath(new URL("../internal/web/app/src/", import.meta.url));
const read = (path) => readFileSync(`${src}${path}`, "utf8");
const routePaths = (text) =>
  [...text.matchAll(/<Route\s[^>]*?path="([^"]*)"/gs)]
    .map((match) => match[1])
    .filter((path) => path !== "*");

let failures = 0;
function check(name, fn) {
  try {
    fn();
    console.log(`ok - ${name}`);
  } catch (error) {
    failures += 1;
    console.error(`not ok - ${name}\n${error.stack}`);
  }
}

check("top-level routes match App.tsx", () => {
  const app = routePaths(read("App.tsx"));
  assert.deepEqual(
    app.filter((path) => !path.endsWith("/*")),
    [...TOP_LEVEL_ROUTES],
  );
  assert.deepEqual(
    app.filter((path) => path.endsWith("/*")),
    ["/manage/*", "/console/*", "/*"],
  );
});

check("viewer routes match areas/viewer/index.tsx", () => {
  assert.deepEqual(routePaths(read("areas/viewer/index.tsx")), [...VIEWER_ROUTES]);
});

check("manage routes match areas/manage/index.tsx", () => {
  assert.deepEqual(routePaths(read("areas/manage/index.tsx")), [...MANAGE_ROUTES]);
});

check("console routes match the GROUPS paths in areas/console/index.tsx", () => {
  const paths = [...read("areas/console/index.tsx").matchAll(/^\s*path: "([^"]*)",$/gm)].map(
    (match) => match[1],
  );
  assert.deepEqual(paths, [...CONSOLE_ROUTES]);
});

check("row ids are unique and every in-page state is well formed", () => {
  const ids = UI_STATES.map((row) => row.id);
  assert.equal(new Set(ids).size, ids.length);
  for (const [screen, states] of Object.entries(IN_PAGE_STATES)) {
    assert.ok(SCREENS.includes(screen), `IN_PAGE_STATES key ${screen} is not a screen`);
    for (const state of states) {
      assert.ok(IN_PAGE_KINDS.includes(state.kind), `${screen} has kind ${state.kind}`);
      assert.match(state.name, /^[a-z0-9-]+$/, `${screen} has name ${state.name}`);
    }
  }
});

check("every visualParity golden is a required visual-parity state", () => {
  const required = new Set(
    REQUIRED_VISUAL_PARITY_STATE_ATTACHMENTS.map((name) =>
      name.replace(/^visual-parity-state:/, ""),
    ),
  );
  for (const row of UI_STATES) {
    for (const golden of row.visualParity ?? []) {
      assert.ok(required.has(golden), `${row.id} names ${golden}, which no test attaches`);
    }
  }
});

check("every gap is an inventory row with a reason", () => {
  const ids = new Set(UI_STATES.map((row) => row.id));
  for (const [id, reason] of Object.entries(UI_STATE_GAPS)) {
    assert.ok(ids.has(id), `gap ${id} is not a row`);
    assert.ok(typeof reason === "string" && reason.trim().length > 0, `gap ${id} has no reason`);
  }
});

check("evaluateUiStateCoverage classifies rows and reports every failure kind", () => {
  const inventory = [{ id: "a" }, { id: "b" }, { id: "c" }, { id: "d" }];
  const gaps = { b: "listed", d: "listed but covered", z: "stale" };
  const { failures: found, rows } = evaluateUiStateCoverage(
    new Set(["a", "d"]),
    new Map([["typo", new Set(["proj › a.spec.ts › t"])]]),
    inventory,
    gaps,
  );
  assert.deepEqual(rows, [
    { id: "a", status: "covered" },
    { id: "b", status: "gap" },
    { id: "c", status: "uncovered" },
    { id: "d", status: "covered" },
  ]);
  assert.deepEqual(found.sort(), [
    "gap-not-in-inventory=z",
    "gap-now-covered=d",
    "uncovered=c",
    "unknown-state=typo in proj › a.spec.ts › t",
  ]);
  assert.deepEqual(
    evaluateUiStateCoverage(new Set(["a"]), new Set(), [{ id: "a" }], {}).failures,
    [],
  );
});

check("the reporter counts annotations and goldens only from passing tests", () => {
  const golden = UI_STATES.find((row) => row.visualParity);
  const plain = UI_STATES.find((row) => !row.visualParity && row.kind === "screen");
  const reporter = new Reporter();
  const annotate = (id) => ({ type: UI_STATE_ANNOTATION, description: id });
  reporter.onTestEnd({}, { status: "failed", annotations: [annotate(plain.id)], attachments: [] });
  assert.equal(reporter.covered.size, 0);
  reporter.onTestEnd(
    { titlePath: () => ["", "ui-states", "x.spec.ts", "covers"] },
    {
      status: "passed",
      annotations: [annotate(plain.id), annotate("no-such-row")],
      attachments: [
        {
          name: `visual-parity-state:${golden.visualParity[0]}`,
          body: Buffer.from("passed"),
        },
      ],
    },
  );
  assert.deepEqual([...reporter.covered.keys()].sort(), [golden.id, plain.id].sort());
  assert.deepEqual([...reporter.covered.get(plain.id)], ["ui-states › x.spec.ts › covers"]);
  assert.deepEqual([...reporter.unknown.keys()], ["no-such-row"]);
});

if (failures > 0) {
  console.error(`FAIL web-ui-state-coverage_test: ${failures} check(s) failed`);
  process.exit(1);
}
