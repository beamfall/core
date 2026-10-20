import { join } from "node:path";
import { expect, test } from "@playwright/test";
import { collectFatalErrors, harnessState } from "./lib/spec";
import { queueUnmatchedRecord, resolvedMediaID } from "./lib/unmatched-record";

// METAPARITY-ARTWORK-E2E-1: the create-Media-with-artwork chain, unmocked, in a
// real browser against the real server.
//
// RecordInbox.test.tsx exercises the same operator flow with BOTH
// createMediaFromUnmatched and fetch replaced by test doubles, so it can only
// prove which calls the component makes — never that the server accepts them or
// that the resulting Media carries the artwork. This spec closes that gap: the
// operator fills the real form, the browser issues the real
// createMediaFromUnmatched mutation and the real multipart upload to
// /api/media/artwork/upload, and the assertions read the artwork back off the
// server — as raw bytes and as a decoded <img> the browser actually painted.
//
// Only the PRECONDITION is injected (one pending Unmatched Record; see
// lib/unmatched-record.ts for why the server exposes no API that mints one).
//
// FILE NAME: this flow needs the CONSOLE storageState — the manager browser with
// an Admin Session. createMediaFromUnmatched and the artwork route are
// manager-gated, but the Unmatched screen also reads `libraries` (admin-gated) to
// populate the target-Library picker, and a plain manager browser fails that read
// and never resolves a Library to create into. The console Playwright project
// matches /console\.spec\.ts/, so the `.console.spec.ts` suffix is how this file
// joins it; playwright.config.ts is outside this ticket's scope. It stays out of
// console.spec.ts itself because that suite is deliberately read-only and this
// one writes.

const RECORD_ID = "urec-e2e-create-artwork";
const RECORD_TITLE = "E2E Artwork Reel";
const MEDIA_TITLE = "E2E Artwork Reel 2026";
// A second pending record. RecordInbox renders nothing at all once its list is
// empty, and the creation notice lives inside it — so with a single record the
// section unmounts on success and the operator's confirmation goes with it. The
// spare record keeps the inbox mounted, which is the realistic queue state.
const SPARE_RECORD_ID = "urec-e2e-create-artwork-spare";
const SPARE_RECORD_TITLE = "E2E Artwork Reel Spare";
const LIBRARY_ID = "library-local";
const LIBRARY_TITLE = "Local Library";

// A real PNG the repo already ships, used as the operator's chosen artwork.
// Reusing a checked-in image keeps the payload a genuinely decodable one — the
// upload route rejects anything image.DecodeConfig cannot read.
const ARTWORK_PATH = join(import.meta.dirname, "..", "public", "favicon.png");

test.describe("manage unmatched: create Media with artwork", () => {
  test("the real create + upload chain lands artwork the Media renders", async ({ page }) => {
    test.skip(!harnessState().adminAvailable, "the Library picker needs an Admin Session");
    const errors = collectFatalErrors(page);
    queueUnmatchedRecord({
      id: RECORD_ID,
      libraryID: LIBRARY_ID,
      sourceHandle: "/media/e2e-artwork-reel.mkv",
      candidateTitle: RECORD_TITLE,
    });
    queueUnmatchedRecord({
      id: SPARE_RECORD_ID,
      libraryID: LIBRARY_ID,
      sourceHandle: "/media/e2e-artwork-reel-spare.mkv",
      candidateTitle: SPARE_RECORD_TITLE,
    });

    await page.goto("/web/manage/unmatched");
    await expect(page.getByText("Unmatched records").first()).toBeVisible();

    // Pin the create target to the unrestricted seed Library so the Media the
    // chain produces is visible to this Profile without a reveal.
    await page.getByRole("combobox").first().click();
    await page.getByRole("option", { name: LIBRARY_TITLE }).click();

    // The record reached the browser through the real unmatchedRecords read. Key
    // the row on its source handle — the spare record's title shares this prefix.
    const recordRow = page.getByRole("button", { name: /e2e-artwork-reel\.mkv/ });
    await recordRow.click();
    await expect(page.getByText("No external ID")).toBeVisible();

    await page.getByLabel("Title · required").fill(MEDIA_TITLE);
    await page.getByLabel("Year · optional").fill("2026");
    // The file input carries an aria-label ("Artwork upload") that overrides its
    // visible <label>, and the artwork-error state renders a second control with
    // that same name — address the create-form input by its own id.
    await page.locator("#unmatched-create-artwork").setInputFiles(ARTWORK_PATH);

    // Both legs of the chain are asserted on the wire, unmocked: the GraphQL
    // create and the multipart artwork upload must each be accepted by the
    // running server.
    const createResponse = page.waitForResponse(
      (res) =>
        res.url().endsWith("/api/graphql") &&
        res.request().postData()?.includes("createMediaFromUnmatched") === true,
    );
    const uploadResponse = page.waitForResponse((res) =>
      res.url().endsWith("/api/media/artwork/upload"),
    );
    await page.getByRole("button", { name: "Create Media" }).click();

    expect((await createResponse).status()).toBe(200);
    expect((await uploadResponse).status()).toBe(200);
    await expect(page.getByText(`${MEDIA_TITLE} created`, { exact: false })).toBeVisible();

    // The server committed the resolution, so the record leaves the inbox and
    // names the Media the chain created.
    const mediaID = resolvedMediaID(RECORD_ID);
    expect(mediaID, "the server did not resolve the record to a Media").toBeTruthy();
    await expect(recordRow).toHaveCount(0);

    // (1) The Media's own artwork route serves the uploaded bytes back. Read it
    // from inside the page: the session rides HttpOnly cookies the browser
    // holds, and the route is scope-gated (401 without them).
    // The endpoint serves a scaled derivative of the upload, not the upload's own
    // bytes, so assert an image body rather than byte-equality with the file.
    const poster = await page.evaluate(async (id) => {
      const res = await fetch(`/api/media/artwork/${id}/poster`, { credentials: "same-origin" });
      return {
        status: res.status,
        contentType: res.headers.get("content-type") ?? "",
        byteLength: (await res.arrayBuffer()).byteLength,
      };
    }, mediaID as string);
    expect(poster.status).toBe(200);
    expect(poster.contentType).toMatch(/^image\//);
    expect(poster.byteLength).toBeGreaterThan(0);

    // (2) The Media RENDERS that artwork. Find it through the viewer's real
    // search path and assert its card paints a poster <img> pointed at THIS
    // Media's artwork route, decoded by the browser (naturalWidth > 0 — a
    // broken or missing image reads 0).
    await page.goto("/web/search");
    await page.getByRole("searchbox").fill(MEDIA_TITLE);
    await page.getByRole("button", { name: "Search", exact: true }).click();

    const posterImage = page.locator(`.bf-poster img[src*="/api/media/artwork/${mediaID}/poster"]`);
    await expect(posterImage).toBeVisible();
    await expect
      .poll(() => posterImage.evaluate((img: HTMLImageElement) => img.naturalWidth))
      .toBeGreaterThan(0);

    expect(errors, errors.join("\n")).toEqual([]);
  });
});
