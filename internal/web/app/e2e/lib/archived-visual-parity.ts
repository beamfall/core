import { createHash } from "node:crypto";
import { readFileSync } from "node:fs";
import { expect, type Locator, type Page, type TestInfo } from "@playwright/test";
import { utils as playwrightUtils } from "playwright-core/lib/coreBundle";
import { changedPixelSignature } from "./archived-visual-parity-signature.mjs";
import { ARCHIVED_VISUAL_PARITY_SHA256 } from "./required-visual-parity-states.mjs";

type ScreenshotSubject = Page | Locator;

type ApprovedDelta = {
  differentPixels: number;
  coordinateSHA256: string;
  normalizedSHA256: string;
};

// Exact approved focus and Cut Beam header deltas; archived PNGs remain immutable.
// Signatures re-recorded for the macOS 27.2.1 system-ui (SF Pro) rasterization (CORE-1109).
const approvedVisualDeltas: Readonly<Record<string, ApprovedDelta>> = {
  "manage/manage-collections-tab-focused-selected.png": {
    differentPixels: 1032,
    coordinateSHA256: "353d3f37dd5a048999149beea4ac963ce65e906132b6d2d40acf8dede71f3168",
    normalizedSHA256: "65f4d038d22b67309c8c073e02ed12261b3fd08e0f9445dfe6334da23e92e419",
  },
  "manage/manage-smart-tab-focused-selected.png": {
    differentPixels: 1032,
    coordinateSHA256: "353d3f37dd5a048999149beea4ac963ce65e906132b6d2d40acf8dede71f3168",
    normalizedSHA256: "65f4d038d22b67309c8c073e02ed12261b3fd08e0f9445dfe6334da23e92e419",
  },
  "manage/manage-curation-subject-select-open-focused-selected.png": {
    differentPixels: 939,
    coordinateSHA256: "777aad9b8856d3c160872e18c2df3203d672b656e8b622be6e5b50bfa3da1b7e",
    normalizedSHA256: "4bafa14f20e9b0a6bc39253b755b716c80185ab5ff11a64c62341cbd2075d30c",
  },
  "manage/manage-curation-versions-tab-focused-selected.png": {
    differentPixels: 939,
    coordinateSHA256: "777aad9b8856d3c160872e18c2df3203d672b656e8b622be6e5b50bfa3da1b7e",
    normalizedSHA256: "4bafa14f20e9b0a6bc39253b755b716c80185ab5ff11a64c62341cbd2075d30c",
  },
  "manage/manage-curation-extras-tab-focused-selected.png": {
    differentPixels: 939,
    coordinateSHA256: "777aad9b8856d3c160872e18c2df3203d672b656e8b622be6e5b50bfa3da1b7e",
    normalizedSHA256: "4bafa14f20e9b0a6bc39253b755b716c80185ab5ff11a64c62341cbd2075d30c",
  },
  "manage/manage-curation-relation-select-open-focused-selected.png": {
    differentPixels: 1390,
    coordinateSHA256: "4467ce0b53c47b225b66b72cea611eb09fe991064a0981c172956de243453465",
    normalizedSHA256: "207bee3a1556d8f29bd38c64686b376c51cc9acd26f250346e316b364c9dcbd6",
  },
};

// CORE-0045 redesigned the player transport after the migration. These states keep their archived
// PNG SHA-pinned in the manifest but no longer compare the live render to it; the live
// goldens under e2e/goldens/ are the regression baseline for them. CORE-1141 (controls over a
// bottom gradient, one settings sheet) re-recorded all 22 of those live PNGs from the new layout.
// CORE-1191 replaced the viewer header Search link with a search field, so the shared dialog
// state joins them.
const redesignedSinceMigration: ReadonlySet<string> = new Set([
  "player-desktop/player-audio-listbox-open-focused-selected.png",
  "player-desktop/player-details-dialog-open-focused.png",
  "player-desktop/player-playback-slider-focused-selected.png",
  "player-desktop/player-quality-listbox-open-focused-selected.png",
  "player-desktop/player-skip-interval-select-open-focused-selected.png",
  "player-desktop/player-speed-select-open-focused-selected.png",
  "player-desktop/player-subtitle-background-open-focused-selected.png",
  "player-desktop/player-subtitle-dialog-open-focused.png",
  "player-desktop/player-subtitle-size-open-focused-selected.png",
  "player-desktop/player-subtitles-listbox-open-focused-selected.png",
  "player-desktop/player-volume-slider-focused-selected.png",
  "player-desktop/shared-dialog-open-focused.png",
  "player-mobile/player-audio-listbox-open-focused-selected.png",
  "player-mobile/player-details-dialog-open-focused.png",
  "player-mobile/player-playback-slider-focused-selected.png",
  "player-mobile/player-quality-listbox-open-focused-selected.png",
  "player-mobile/player-skip-interval-select-open-focused-selected.png",
  "player-mobile/player-speed-select-open-focused-selected.png",
  "player-mobile/player-subtitle-background-open-focused-selected.png",
  "player-mobile/player-subtitle-dialog-open-focused.png",
  "player-mobile/player-subtitle-size-open-focused-selected.png",
  "player-mobile/player-subtitles-listbox-open-focused-selected.png",
  "player-mobile/player-volume-slider-focused-selected.png",
]);

function sha256(value: Buffer): string {
  return createHash("sha256").update(value).digest("hex");
}

export async function expectArchivedVisualParity(
  subject: ScreenshotSubject,
  testInfo: TestInfo,
  state: string,
): Promise<void> {
  expect(
    testInfo.config.updateSnapshots,
    "archived visual baselines are immutable; snapshot updates must stay disabled",
  ).toBe("none");
  expect(
    testInfo.project.ignoreSnapshots,
    "archived visual comparisons cannot run with --ignore-snapshots",
  ).toBe(false);

  const fileName = `${state}.png`;
  const current = await subject.screenshot({
    animations: "disabled",
    caret: "hide",
    scale: "css",
  });
  const archiveKey = `${testInfo.project.name}/${fileName}`;
  const expectedArchivedSHA256 = ARCHIVED_VISUAL_PARITY_SHA256[archiveKey];
  expect(
    expectedArchivedSHA256,
    `${state}: archived pre-migration PNG is absent from the immutable manifest`,
  ).toMatch(/^[0-9a-f]{64}$/);
  const archived = readFileSync(testInfo.snapshotPath(fileName));
  expect(sha256(archived), `${state}: archived pre-migration PNG changed`).toBe(
    expectedArchivedSHA256,
  );
  if (redesignedSinceMigration.has(archiveKey)) return;
  const approvedDelta = approvedVisualDeltas[archiveKey];

  if (approvedDelta) {
    const comparator = playwrightUtils.getComparator("image/png");
    const comparison = comparator(current, archived, { maxDiffPixels: 0, threshold: 0.2 });
    expect(
      comparison?.diff,
      `${state}: approved visual delta unexpectedly disappeared`,
    ).toBeDefined();
    const signature = changedPixelSignature(current, archived, comparison!.diff!);
    expect(signature.count, `${state}: approved visual delta pixel count changed`).toBe(
      approvedDelta.differentPixels,
    );
    expect(
      signature.coordinateSHA256,
      `${state}: approved visual delta pixel coordinates changed`,
    ).toBe(approvedDelta.coordinateSHA256);
    expect(
      signature.normalizedSHA256,
      `${state}: approved visual delta post-RGBA/delta signature changed`,
    ).toBe(approvedDelta.normalizedSHA256);
  }

  await expect(current).toMatchSnapshot(fileName, {
    maxDiffPixels: approvedDelta?.differentPixels ?? 0,
    threshold: 0.2,
  });
}
