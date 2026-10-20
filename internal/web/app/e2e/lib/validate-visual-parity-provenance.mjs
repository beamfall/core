import { createHash } from "node:crypto";
import { readFileSync } from "node:fs";
import { pathToFileURL } from "node:url";
import { REQUIRED_VISUAL_PARITY_STATE_ATTACHMENTS } from "./required-visual-parity-states.mjs";

const REQUIRED_STATE_COUNT = 33;
const requiredStates = new Set(REQUIRED_VISUAL_PARITY_STATE_ATTACHMENTS);

if (
  REQUIRED_VISUAL_PARITY_STATE_ATTACHMENTS.length !== REQUIRED_STATE_COUNT ||
  requiredStates.size !== REQUIRED_STATE_COUNT
) {
  throw new Error(`visual parity manifest must contain ${REQUIRED_STATE_COUNT} unique entries`);
}

export function canonicalVisualParityManifestSHA256() {
  return createHash("sha256")
    .update(JSON.stringify(REQUIRED_VISUAL_PARITY_STATE_ATTACHMENTS))
    .digest("hex");
}

export function validateVisualParityProvenance(payload) {
  if (!payload || typeof payload !== "object" || Array.isArray(payload)) {
    throw new Error("visual parity provenance must be an object");
  }
  const expectedTopLevelKeys = [
    "manifestSHA256",
    "passedCount",
    "requiredCount",
    "schemaVersion",
    "states",
  ];
  if (
    Object.keys(payload).sort().join(",") !== expectedTopLevelKeys.sort().join(",") ||
    payload.schemaVersion !== 1 ||
    payload.manifestSHA256 !== canonicalVisualParityManifestSHA256() ||
    payload.requiredCount !== REQUIRED_STATE_COUNT ||
    payload.passedCount !== REQUIRED_STATE_COUNT ||
    !Array.isArray(payload.states) ||
    payload.states.length !== REQUIRED_STATE_COUNT
  ) {
    throw new Error("visual parity provenance metadata does not match the canonical inventory");
  }

  for (const [index, expectedId] of REQUIRED_VISUAL_PARITY_STATE_ATTACHMENTS.entries()) {
    const state = payload.states[index];
    if (
      !state ||
      typeof state !== "object" ||
      Array.isArray(state) ||
      Object.keys(state).sort().join(",") !== "id,status" ||
      state.id !== expectedId ||
      state.status !== "passed"
    ) {
      throw new Error(`visual parity provenance state ${index} is not the canonical PASS entry`);
    }
  }
  return payload;
}

export function readAndValidateVisualParityProvenance(path) {
  return validateVisualParityProvenance(JSON.parse(readFileSync(path, "utf8")));
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  const path = process.argv[2];
  if (!path) {
    process.stderr.write("usage: validate-visual-parity-provenance.mjs <provenance.json>\n");
    process.exitCode = 2;
  } else {
    try {
      readAndValidateVisualParityProvenance(path);
      process.stdout.write(
        `PASS web-ui-e2e-smoke provenance required=${REQUIRED_STATE_COUNT} passed=${REQUIRED_STATE_COUNT}\n`,
      );
    } catch (error) {
      process.stderr.write(`FAIL web-ui-e2e-smoke provenance ${error.message}\n`);
      process.exitCode = 1;
    }
  }
}
