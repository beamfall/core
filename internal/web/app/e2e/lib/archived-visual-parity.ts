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

// Exact approved deltas against the archive; archived PNGs remain immutable. None remain:
// every state that carried one is now in redesignedSinceMigration (CORE-1131).
const approvedVisualDeltas: Readonly<Record<string, ApprovedDelta>> = {};

// CORE-0045 redesigned the player transport after the migration. These states keep their archived
// PNG SHA-pinned in the manifest but no longer compare the live render to it; the live
// goldens under e2e/goldens/ are the regression baseline for them. CORE-1131 moved library
// navigation into a left sidebar and redesigned the Manage and Console shells, so every Manage,
// Console and desktop shared-dialog state joins them. CORE-1141 (controls over a bottom gradient,
// one settings sheet) re-recorded all 22 of the player live PNGs from the new layout; CORE-1191
// replaced the viewer header Search link with a search field, which also redesigns the shared
// dialog state.
const redesignedSinceMigration: ReadonlySet<string> = new Set([
  "console/console-alert-dialog-open-focused.png",
  "console/console-kind-select-open-focused-selected.png",
  "manage/manage-alert-dialog-open-focused.png",
  "manage/manage-collections-tab-focused-selected.png",
  "manage/manage-curation-extras-tab-focused-selected.png",
  "manage/manage-curation-relation-select-open-focused-selected.png",
  "manage/manage-curation-subject-select-open-focused-selected.png",
  "manage/manage-curation-versions-tab-focused-selected.png",
  "manage/manage-library-select-open-focused-selected.png",
  "manage/manage-smart-tab-focused-selected.png",
  "player-desktop/shared-dialog-open-focused.png",
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
