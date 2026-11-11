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
const approvedVisualDeltas: Readonly<Record<string, ApprovedDelta>> = {
  "player-desktop/shared-dialog-open-focused.png": {
    differentPixels: 1190,
    coordinateSHA256: "b98548126162a410ed554547d63e86b93bf06c23a73b32eb47d53322c4610e63",
    normalizedSHA256: "6b0800e8790f1a3c3ffbbad0af1d310826e69caeb7c6f8d756a1f463d0490364",
  },
  "manage/manage-collections-tab-focused-selected.png": {
    differentPixels: 1032,
    coordinateSHA256: "c5a9894f0485ae0e3e4bbd7749df94a39412e551364e4ba9cd4bae212e57450e",
    normalizedSHA256: "2d5ea534b16a957e4a9ab496faece5fa7f5e0eb4c59049e1e317c8bb14bb1093",
  },
  "manage/manage-smart-tab-focused-selected.png": {
    differentPixels: 1032,
    coordinateSHA256: "c5a9894f0485ae0e3e4bbd7749df94a39412e551364e4ba9cd4bae212e57450e",
    normalizedSHA256: "2d5ea534b16a957e4a9ab496faece5fa7f5e0eb4c59049e1e317c8bb14bb1093",
  },
  "manage/manage-curation-subject-select-open-focused-selected.png": {
    differentPixels: 938,
    coordinateSHA256: "90c6eb2f9c0cbd43f028f03e2e2e29a64626176e3aa2d46e087fac8e11da6c11",
    normalizedSHA256: "f2efeab6b8ac5f42bd2f9a8463d5d61ad6cbfc7f68645759119366215863b9ea",
  },
  "manage/manage-curation-versions-tab-focused-selected.png": {
    differentPixels: 938,
    coordinateSHA256: "90c6eb2f9c0cbd43f028f03e2e2e29a64626176e3aa2d46e087fac8e11da6c11",
    normalizedSHA256: "f2efeab6b8ac5f42bd2f9a8463d5d61ad6cbfc7f68645759119366215863b9ea",
  },
  "manage/manage-curation-extras-tab-focused-selected.png": {
    differentPixels: 938,
    coordinateSHA256: "90c6eb2f9c0cbd43f028f03e2e2e29a64626176e3aa2d46e087fac8e11da6c11",
    normalizedSHA256: "f2efeab6b8ac5f42bd2f9a8463d5d61ad6cbfc7f68645759119366215863b9ea",
  },
  "manage/manage-curation-relation-select-open-focused-selected.png": {
    differentPixels: 1389,
    coordinateSHA256: "deb5104dae71a5e7d261cb67c4d9fa536c2ea6ef878fa034efa157d1e91539af",
    normalizedSHA256: "943b19b4f60173f6e38fcd04f371fd1639873e044a29ed5d1fc550cecba911e9",
  },
};

// CORE-0045 redesigned the player transport after the migration. These states keep their archived
// PNG SHA-pinned in the manifest but no longer compare the live render to it; the live
// goldens under e2e/goldens/ are the regression baseline for them.
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
