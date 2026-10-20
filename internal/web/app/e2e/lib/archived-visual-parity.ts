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
  "player-desktop/player-quality-listbox-open-focused-selected.png": {
    differentPixels: 713,
    coordinateSHA256: "1004e0680fd85221df652b14d90be92b8423d6e2d77d4d65b47d24f564c1623f",
    normalizedSHA256: "21dc2d54d4360ab60066d3b9eca320f4971aa21c0d04884f139f55d7b0fe0874",
  },
  "player-desktop/shared-dialog-open-focused.png": {
    differentPixels: 1190,
    coordinateSHA256: "b98548126162a410ed554547d63e86b93bf06c23a73b32eb47d53322c4610e63",
    normalizedSHA256: "6b0800e8790f1a3c3ffbbad0af1d310826e69caeb7c6f8d756a1f463d0490364",
  },
  "player-mobile/player-quality-listbox-open-focused-selected.png": {
    differentPixels: 336,
    coordinateSHA256: "3068bcd3fd5c5aa1fbc2afbf7157403c63a284543ae51382187de991908ca90e",
    normalizedSHA256: "9a22aacdcb7a1759d38d6d7e50838e59ccda2c272968584d0303b9109a68ffa7",
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
