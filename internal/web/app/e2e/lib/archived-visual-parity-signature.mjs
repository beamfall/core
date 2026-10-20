import { createHash } from "node:crypto";
import { PNG } from "playwright-core/lib/utilsBundle";

function sha256(value) {
  return createHash("sha256").update(value).digest("hex");
}

export function changedPixelSignatureFromEntries(entries) {
  return {
    count: entries.length,
    coordinateSHA256: sha256(Buffer.from(entries.map(({ pixel }) => pixel).join(","))),
    normalizedSHA256: sha256(
      Buffer.from(
        entries
          .map(({ pixel, current, delta }) => `${pixel}:${current.join(",")}:${delta.join(",")}`)
          .join("|"),
      ),
    ),
  };
}

export function changedPixelSignature(current, archived, diff) {
  const currentPng = PNG.sync.read(current);
  const archivedPng = PNG.sync.read(archived);
  const diffPng = PNG.sync.read(diff);
  if (
    currentPng.width !== archivedPng.width ||
    currentPng.height !== archivedPng.height ||
    currentPng.width !== diffPng.width ||
    currentPng.height !== diffPng.height
  ) {
    throw new Error("approved visual delta inputs must have identical dimensions");
  }

  const entries = [];
  for (let offset = 0, pixel = 0; offset < diffPng.data.length; offset += 4, pixel += 1) {
    if (
      diffPng.data[offset] !== 255 ||
      diffPng.data[offset + 1] !== 0 ||
      diffPng.data[offset + 2] !== 0 ||
      diffPng.data[offset + 3] !== 255
    ) {
      continue;
    }
    const currentPixel = Array.from(currentPng.data.subarray(offset, offset + 4));
    const archivedPixel = Array.from(archivedPng.data.subarray(offset, offset + 4));
    entries.push({
      pixel,
      current: currentPixel,
      delta: currentPixel.map((channel, index) => channel - archivedPixel[index]),
    });
  }
  return changedPixelSignatureFromEntries(entries);
}
