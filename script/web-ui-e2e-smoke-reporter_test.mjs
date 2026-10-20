import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { createHash } from "node:crypto";
import { existsSync, mkdtempSync, readFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { changedPixelSignatureFromEntries } from "../internal/web/app/e2e/lib/archived-visual-parity-signature.mjs";
import Reporter from "../internal/web/app/e2e/lib/require-visual-parity-states.mjs";
import {
  ARCHIVED_VISUAL_PARITY_SHA256,
  REQUIRED_VISUAL_PARITY_STATE_ATTACHMENTS as requiredStates,
} from "../internal/web/app/e2e/lib/required-visual-parity-states.mjs";
import {
  canonicalVisualParityManifestSHA256,
  readAndValidateVisualParityProvenance,
  validateVisualParityProvenance,
} from "../internal/web/app/e2e/lib/validate-visual-parity-provenance.mjs";

const prefix = "visual-parity-state:";
const repoRoot = fileURLToPath(new URL("../", import.meta.url));
const sha256 = (value) => createHash("sha256").update(value).digest("hex");
const projectFor = (state) => state.slice(prefix.length).split("/", 1)[0];
const testFor = (project) => ({ parent: { project: () => ({ name: project }) } });

function record(reporter, state, status = "passed", project = projectFor(state)) {
  reporter.onTestEnd(testFor(project), {
    status,
    attachments: [{ name: state, body: Buffer.from("passed") }],
  });
}

function finish(reporter) {
  const stdout = [];
  const stderr = [];
  const stdoutWrite = process.stdout.write;
  const stderrWrite = process.stderr.write;
  process.stdout.write = (chunk) => {
    stdout.push(String(chunk));
    return true;
  };
  process.stderr.write = (chunk) => {
    stderr.push(String(chunk));
    return true;
  };
  try {
    return { result: reporter.onEnd(), stdout: stdout.join(""), stderr: stderr.join("") };
  } finally {
    process.stdout.write = stdoutWrite;
    process.stderr.write = stderrWrite;
  }
}

function completeReporter() {
  const reporter = new Reporter();
  for (const state of requiredStates) record(reporter, state);
  return reporter;
}

assert.equal(requiredStates.length, 33);
assert.equal(new Set(requiredStates).size, 33);
assert.deepEqual(
  requiredStates.reduce((counts, state) => {
    const project = projectFor(state);
    counts[project] = (counts[project] ?? 0) + 1;
    return counts;
  }, {}),
  { "player-desktop": 12, "player-mobile": 11, manage: 8, console: 2 },
);
const archiveKeys = requiredStates.map((state) => state.slice(prefix.length));
assert.deepEqual(Object.keys(ARCHIVED_VISUAL_PARITY_SHA256), archiveKeys);
for (const archiveKey of archiveKeys) {
  const [project, fileName] = archiveKey.split("/");
  const specFile = project.startsWith("player-") ? "player.spec.ts" : `${project}.spec.ts`;
  const archived = readFileSync(
    join(repoRoot, "artifacts", "techdur-web-2", "visual-baselines", project, specFile, fileName),
  );
  assert.equal(sha256(archived), ARCHIVED_VISUAL_PARITY_SHA256[archiveKey]);
}

const plantedArchive = readFileSync(
  join(
    repoRoot,
    "artifacts",
    "techdur-web-2",
    "visual-baselines",
    "console",
    "console.spec.ts",
    "console-kind-select-open-focused-selected.png",
  ),
);
const mutatedArchive = Buffer.from(plantedArchive);
mutatedArchive[Math.floor(mutatedArchive.length / 2)] ^= 1;
assert.notEqual(
  sha256(mutatedArchive),
  ARCHIVED_VISUAL_PARITY_SHA256["console/console-kind-select-open-focused-selected.png"],
);

const approvedEntry = [{ pixel: 17, current: [210, 220, 230, 255], delta: [90, 80, 70, 0] }];
const sameCoordinatesDifferentRgba = [
  { pixel: 17, current: [211, 220, 230, 255], delta: [91, 80, 70, 0] },
];
const approvedSignature = changedPixelSignatureFromEntries(approvedEntry);
const plantedSignature = changedPixelSignatureFromEntries(sameCoordinatesDifferentRgba);
assert.equal(approvedSignature.coordinateSHA256, plantedSignature.coordinateSHA256);
assert.notEqual(approvedSignature.normalizedSHA256, plantedSignature.normalizedSHA256);

const receiptDirectory = mkdtempSync(join(tmpdir(), "beamfall-smoke-reporter-test."));
process.on("exit", () => rmSync(receiptDirectory, { force: true, recursive: true }));
const receiptPath = join(receiptDirectory, "full-inventory.json");
process.env.BEAMFALL_WEB_E2E_SMOKE_PROVENANCE = receiptPath;
const complete = finish(completeReporter());
delete process.env.BEAMFALL_WEB_E2E_SMOKE_PROVENANCE;
assert.equal(complete.result, undefined);
assert.match(complete.stdout, /expected=33 passed=33/);
assert.equal(complete.stderr, "");
const receipt = JSON.parse(readFileSync(receiptPath, "utf8"));
assert.equal(receipt.requiredCount, 33);
assert.equal(receipt.passedCount, 33);
assert.equal(receipt.states.length, 33);
assert.deepEqual(
  receipt.states.map(({ id, status }) => [id, status]),
  requiredStates.map((id) => [id, "passed"]),
);
assert.match(receipt.manifestSHA256, /^[0-9a-f]{64}$/);
assert.equal(receipt.manifestSHA256, canonicalVisualParityManifestSHA256());
assert.equal(validateVisualParityProvenance(receipt), receipt);
assert.deepEqual(readAndValidateVisualParityProvenance(receiptPath), receipt);
const validatorPath = fileURLToPath(
  new URL("../internal/web/app/e2e/lib/validate-visual-parity-provenance.mjs", import.meta.url),
);
const validatorResult = spawnSync(process.execPath, [validatorPath, receiptPath], {
  encoding: "utf8",
});
assert.equal(validatorResult.status, 0, validatorResult.stderr);
assert.match(validatorResult.stdout, /required=33 passed=33/);

assert.throws(
  () => validateVisualParityProvenance({}),
  /metadata does not match the canonical inventory/,
);
assert.throws(
  () =>
    validateVisualParityProvenance({
      ...receipt,
      states: receipt.states.slice(0, 1),
    }),
  /metadata does not match the canonical inventory/,
);
assert.throws(
  () =>
    validateVisualParityProvenance({
      ...receipt,
      states: receipt.states.map((state, index) =>
        index === 7 ? { ...state, status: "skipped" } : state,
      ),
    }),
  /state 7 is not the canonical PASS entry/,
);

const empty = finish(new Reporter());
assert.deepEqual(empty.result, { status: "failed" });
assert.equal(empty.stderr, "FAIL web-ui-e2e-smoke executed zero tests\n");

const missingReporter = new Reporter();
for (const state of requiredStates.slice(0, -1)) record(missingReporter, state);
const partialReceiptPath = join(receiptDirectory, "partial-inventory.json");
process.env.BEAMFALL_WEB_E2E_SMOKE_PROVENANCE = partialReceiptPath;
const missing = finish(missingReporter);
delete process.env.BEAMFALL_WEB_E2E_SMOKE_PROVENANCE;
assert.deepEqual(missing.result, { status: "failed" });
assert.match(missing.stderr, /missing-visual-parity-state/);
assert.equal(existsSync(partialReceiptPath), false);

for (const status of ["skipped", "failed"]) {
  const reporter = new Reporter();
  for (const state of requiredStates.slice(0, -1)) record(reporter, state);
  record(reporter, requiredStates.at(-1), status);
  const incomplete = finish(reporter);
  assert.deepEqual(incomplete.result, { status: "failed" });
  assert.match(incomplete.stderr, /missing-visual-parity-state/);
}

const duplicateReporter = completeReporter();
record(duplicateReporter, requiredStates[0]);
const duplicate = finish(duplicateReporter);
assert.deepEqual(duplicate.result, { status: "failed" });
assert.match(duplicate.stderr, /duplicate-visual-parity-state/);

const mismatchReporter = new Reporter();
for (const state of requiredStates.slice(1)) record(mismatchReporter, state);
record(mismatchReporter, requiredStates[0], "passed", "player-mobile");
const mismatch = finish(mismatchReporter);
assert.deepEqual(mismatch.result, { status: "failed" });
assert.match(mismatch.stderr, /project-mismatch=/);
assert.match(mismatch.stderr, /missing-visual-parity-state/);

const invalidPayloadReporter = new Reporter();
for (const state of requiredStates.slice(1)) record(invalidPayloadReporter, state);
invalidPayloadReporter.onTestEnd(testFor(projectFor(requiredStates[0])), {
  status: "passed",
  attachments: [{ name: requiredStates[0], body: Buffer.from("pending") }],
});
const invalidPayload = finish(invalidPayloadReporter);
assert.deepEqual(invalidPayload.result, { status: "failed" });
assert.match(invalidPayload.stderr, /invalid-payload=/);
assert.match(invalidPayload.stderr, /missing-visual-parity-state/);

rmSync(receiptDirectory, { recursive: true });
console.log("PASS web-ui-e2e-smoke reporter requires exactly 33 passed state attachments");
