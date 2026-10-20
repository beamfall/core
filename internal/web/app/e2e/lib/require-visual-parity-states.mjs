import { mkdirSync, renameSync, writeFileSync } from "node:fs";
import { dirname } from "node:path";
import {
  ARCHIVED_VISUAL_PARITY_SHA256,
  REQUIRED_VISUAL_PARITY_STATE_ATTACHMENTS,
  VISUAL_PARITY_STATE_ATTACHMENT_PREFIX,
} from "./required-visual-parity-states.mjs";
import { canonicalVisualParityManifestSHA256 } from "./validate-visual-parity-provenance.mjs";

const REQUIRED_STATE_COUNT = 33;
const requiredStates = new Set(REQUIRED_VISUAL_PARITY_STATE_ATTACHMENTS);
const requiredArchiveKeys = new Set(
  REQUIRED_VISUAL_PARITY_STATE_ATTACHMENTS.map((state) =>
    state.slice(VISUAL_PARITY_STATE_ATTACHMENT_PREFIX.length),
  ),
);
const archivedKeys = Object.keys(ARCHIVED_VISUAL_PARITY_SHA256);

if (
  REQUIRED_VISUAL_PARITY_STATE_ATTACHMENTS.length !== REQUIRED_STATE_COUNT ||
  requiredStates.size !== REQUIRED_STATE_COUNT ||
  archivedKeys.length !== REQUIRED_STATE_COUNT ||
  archivedKeys.some(
    (key) =>
      !requiredArchiveKeys.has(key) || !/^[0-9a-f]{64}$/.test(ARCHIVED_VISUAL_PARITY_SHA256[key]),
  )
) {
  throw new Error(
    `visual parity manifest must contain ${REQUIRED_STATE_COUNT} unique states with pinned archive hashes`,
  );
}

function attachmentProject(name) {
  const slash = name.indexOf("/", VISUAL_PARITY_STATE_ATTACHMENT_PREFIX.length);
  return slash === -1 ? "" : name.slice(VISUAL_PARITY_STATE_ATTACHMENT_PREFIX.length, slash);
}

function publishProvenance() {
  const path = process.env.BEAMFALL_WEB_E2E_SMOKE_PROVENANCE;
  if (!path) return;

  const payload = {
    schemaVersion: 1,
    manifestSHA256: canonicalVisualParityManifestSHA256(),
    requiredCount: REQUIRED_STATE_COUNT,
    passedCount: REQUIRED_STATE_COUNT,
    states: REQUIRED_VISUAL_PARITY_STATE_ATTACHMENTS.map((id) => ({ id, status: "passed" })),
  };
  const temporary = `${path}.${process.pid}.tmp`;
  mkdirSync(dirname(path), { recursive: true });
  writeFileSync(temporary, `${JSON.stringify(payload, null, 2)}\n`, { mode: 0o600 });
  renameSync(temporary, path);
}

class RequireVisualParityStatesReporter {
  executedTestCount = 0;
  passedStateCounts = new Map();
  invalidStates = [];

  onTestEnd(test, result) {
    if (result.status !== "skipped" && result.status !== "interrupted") {
      this.executedTestCount += 1;
    }
    if (result.status !== "passed") return;

    const project = test.parent.project()?.name ?? "";
    for (const attachment of result.attachments) {
      if (!attachment.name.startsWith(VISUAL_PARITY_STATE_ATTACHMENT_PREFIX)) continue;

      if (!requiredStates.has(attachment.name)) {
        this.invalidStates.push(`unexpected=${attachment.name}`);
        continue;
      }
      if (attachment.body?.toString("utf8") !== "passed") {
        this.invalidStates.push(`invalid-payload=${attachment.name}`);
        continue;
      }
      if (attachmentProject(attachment.name) !== project) {
        this.invalidStates.push(
          `project-mismatch=${attachment.name} actual-project=${project || "<none>"}`,
        );
        continue;
      }

      this.passedStateCounts.set(
        attachment.name,
        (this.passedStateCounts.get(attachment.name) ?? 0) + 1,
      );
    }
  }

  onEnd() {
    if (this.executedTestCount === 0) {
      process.stderr.write("FAIL web-ui-e2e-smoke executed zero tests\n");
      return { status: "failed" };
    }

    const missingStates = REQUIRED_VISUAL_PARITY_STATE_ATTACHMENTS.filter(
      (state) => !this.passedStateCounts.has(state),
    );
    const duplicateStates = [...this.passedStateCounts].filter(([, count]) => count !== 1);

    if (
      missingStates.length === 0 &&
      duplicateStates.length === 0 &&
      this.invalidStates.length === 0
    ) {
      publishProvenance();
      process.stdout.write(
        `PASS web-ui-e2e-smoke required-visual-parity-states expected=${REQUIRED_STATE_COUNT} passed=${this.passedStateCounts.size}\n`,
      );
      return;
    }

    for (const state of missingStates) {
      process.stderr.write(`FAIL web-ui-e2e-smoke missing-visual-parity-state ${state}\n`);
    }
    for (const [state, count] of duplicateStates) {
      process.stderr.write(
        `FAIL web-ui-e2e-smoke duplicate-visual-parity-state ${state} count=${count}\n`,
      );
    }
    for (const invalid of this.invalidStates) {
      process.stderr.write(`FAIL web-ui-e2e-smoke invalid-visual-parity-state ${invalid}\n`);
    }
    return { status: "failed" };
  }
}

export default RequireVisualParityStatesReporter;
