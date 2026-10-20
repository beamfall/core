import { mkdtempSync, rmSync, unlinkSync, utimesSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { expect, type Page, test } from "@playwright/test";
import { graphqlWithSeedManager } from "./lib/harness";
import {
  collectFatalErrors,
  expectBodyTextContrastsBackground,
  expectForcedColorsActive,
  harnessState,
} from "./lib/spec";
import {
  expectLocatorVisualParity,
  expectVisualParity,
  openSelectState,
} from "./lib/visual-parity";

// CONSOLE area (ADR-0007 §4): the operator surface, gated by both the MANAGER
// role AND an Admin Session. The "console" project ships the manager browser's
// storageState with an Admin Session already opened server-side (global-setup
// verifyAdminSession), so the AdminGate renders unlocked and each section's data
// loads. Every section asserts its <h2> heading renders and no fatal error fires.
//
// The whole describe skips when the server has no admin password (an external
// BEAMFALL_E2E_URL started without BEAMFALL_ADMIN_PASSWORD — e.g. the
// orchestrator's 8810). The self-booted gate server always has it.
const { adminAvailable } = harnessState();

async function seedLibraryHealthDrilldownData(): Promise<() => void> {
  const { baseURL } = harnessState();
  // The scan job may be claimed by the server's own background worker before
  // this helper gets to drain it, in which case `runJob` reports "no runnable
  // job". Either path is fine as long as the job reaches a terminal state, so
  // poll the queue instead of assuming the manual drain always wins the race.
  const jobIsSettled = async (expectedID: string): Promise<boolean> => {
    const data = (await graphqlWithSeedManager(
      baseURL,
      "query Jobs { jobs { id status } }",
      {},
    )) as { jobs?: { id?: string; status?: string }[] };
    const job = data.jobs?.find((row) => row.id === expectedID);
    return job !== undefined && !["queued", "running", "yielded"].includes(job.status ?? "");
  };
  const runQueuedJob = async (expectedID: string) => {
    for (let attempt = 0; attempt < 40; attempt += 1) {
      try {
        const data = (await graphqlWithSeedManager(
          baseURL,
          "mutation RunJob { runJob { id status } }",
          {},
        )) as { runJob?: { id?: string; status?: string } };
        if (data.runJob?.id === expectedID) return;
      } catch (err) {
        if (!/no runnable job/.test(String(err))) throw err;
      }
      if (await jobIsSettled(expectedID)) return;
      await new Promise((resolve) => setTimeout(resolve, 250));
    }
    const final = (await graphqlWithSeedManager(
      baseURL,
      "query Job($id: String!) { job(id: $id) { id status error } }",
      { id: expectedID },
    )) as { job?: { status?: string; error?: string } };
    throw new Error(
      `scan job ${expectedID} did not complete: ${final.job?.status ?? "missing"} ${final.job?.error ?? ""}`,
    );
  };
  const root = mkdtempSync(join(tmpdir(), "beamfall-health-e2e-"));
  const old = new Date(Date.now() - 10_000);
  const writeMedia = (name: string, content: string | Buffer) => {
    const path = join(root, name);
    writeFileSync(path, content);
    utimesSync(path, old, old);
    return path;
  };
  const duplicateA = Buffer.alloc(16);
  duplicateA.writeBigUInt64LE(1n, 0);
  duplicateA.writeBigUInt64LE(2n, 8);
  const duplicateB = Buffer.alloc(16);
  duplicateB.writeBigUInt64LE(0n, 0);
  duplicateB.writeBigUInt64LE(3n, 8);
  const broken = writeMedia("broken-health.mp4", "broken fixture");
  writeMedia("alpha-health.mp4", duplicateA);
  writeMedia("omega-health.mp4", duplicateB);
  writeMedia("unmatched-health.mp4", "unmatched fixture");

  await graphqlWithSeedManager(
    baseURL,
    "mutation SetRoots($libraryID: String!, $roots: [String!]!) { setLibraryRoots(libraryID: $libraryID, roots: $roots) { id } }",
    { libraryID: "library-local", roots: [root] },
  );
  const firstScan = (await graphqlWithSeedManager(
    baseURL,
    "mutation Scan($libraryID: String!, $root: String!) { scanLibrary(libraryID: $libraryID, root: $root) { id status } }",
    { libraryID: "library-local", root },
  )) as { scanLibrary?: { id?: string } };
  await runQueuedJob(firstScan.scanLibrary?.id ?? "");

  unlinkSync(broken);
  const secondScan = (await graphqlWithSeedManager(
    baseURL,
    "mutation Rescan($libraryID: String!, $root: String!) { scanLibrary(libraryID: $libraryID, root: $root) { id status } }",
    { libraryID: "library-local", root },
  )) as { scanLibrary?: { id?: string } };
  await runQueuedJob(secondScan.scanLibrary?.id ?? "");

  // Keep the watched root alive until the assertions finish. Removing it here
  // makes the watcher mark every seeded file missing, hiding duplicate groups.
  return () => rmSync(root, { recursive: true, force: true });
}

test.describe("console", () => {
  test.skip(
    !adminAvailable,
    "no admin password on this server — Admin Session cannot be opened; " +
      "boot the gate server (no BEAMFALL_E2E_URL) for Console coverage.",
  );

  async function gotoConsole(page: Page, route: string): Promise<void> {
    await page.goto(`/web/console${route ? `/${route}` : ""}`);
    // The Console section nav is only present once the AdminGate is unlocked;
    // its presence proves the Admin Session resolved.
    await expect(page.getByRole("navigation", { name: "Console" })).toBeVisible();
  }

  test("admin gate is unlocked and the section nav renders", async ({ page }) => {
    const errors = collectFatalErrors(page);
    await gotoConsole(page, "");
    // The unlock form must NOT be present (we are past the gate).
    await expect(page.getByRole("button", { name: "Unlock Console" })).toHaveCount(0);
    // The index section renders the current Console overview.
    await expect(page.getByRole("heading", { name: "Overview" })).toBeVisible();
    expect(errors, errors.join("\n")).toEqual([]);
  });

  test("accessibility variant: Console supports high contrast", async ({ page }) => {
    const errors = collectFatalErrors(page);
    await page.emulateMedia({ forcedColors: "active", contrast: "more" });
    await gotoConsole(page, "");

    await expectForcedColorsActive(page);
    await expect(page.getByRole("heading", { name: "Overview" })).toBeVisible();
    await expectBodyTextContrastsBackground(page);
    expect(errors, errors.join("\n")).toEqual([]);
  });

  test("Beamfall Console exposes local Core management sections", async ({ page }) => {
    const errors = collectFatalErrors(page);
    await gotoConsole(page, "");
    const nav = page.getByRole("navigation", { name: "Console" });
    // The expanded rail (the collapsed "Maintain" group is asserted below).
    for (const label of [
      "Overview",
      "Operations",
      "Live sessions",
      "Library health",
      "Activity",
      "Libraries",
      "Plugins",
      "Capabilities",
      "Layout & Navigation",
      "Profiles & access",
      "Schedules",
      "Remote access",
      "API tokens",
    ]) {
      await expect(nav.getByRole("link", { name: label, exact: true })).toBeVisible();
    }
    // "Maintain" ships collapsed; expanding it must reveal the operator-only
    // destinations that no longer sit on the primary rail.
    await nav.getByText("Maintain", { exact: true }).click();
    for (const label of ["Backups", "Retention rules", "Updates", "Server configuration"]) {
      await expect(nav.getByRole("link", { name: label })).toBeVisible();
    }
    await expect(page.getByText("beamfall-web")).toHaveCount(0);
    await expect(page.getByText(/checkout|pricing|entitlement/i)).toHaveCount(0);
    expect(errors, errors.join("\n")).toEqual([]);
  });

  test("visual parity covers shared Console Select and alert dialog states", async ({
    page,
  }, testInfo) => {
    await gotoConsole(page, "access");
    await expect(page.getByRole("heading", { name: "Profiles" })).toBeVisible();

    const kind = page.getByRole("combobox", { name: "Kind" }).first();
    await openSelectState(page, kind, "Adult");
    await expectLocatorVisualParity(
      page.locator('[data-slot="select-content"]'),
      testInfo,
      "console-kind-select-open-focused-selected",
    );
    await page.keyboard.press("Escape");

    await page.getByRole("button", { name: "Disable" }).first().click();
    const alertDialog = page.getByRole("alertdialog", { name: "Disable this profile?" });
    await expect(alertDialog).toBeVisible();
    const cancel = alertDialog.getByRole("button", { name: "Cancel" });
    await cancel.focus();
    await expect(cancel).toBeFocused();
    // The Devices table's "current device" flag oscillates across refreshes
    // (the first E2E manager row's "This device" chip and Demote/Revoke
    // actions swap — see CONSOLE-DEVICE-CURRENT-FLAP-1). Masking only the
    // name/chip and action cells is not enough: the optional chip changes that
    // cell's intrinsic width, and a table's column widths are shared, so the
    // Role/State cells shift horizontally and their pixels differ even though
    // their text is identical. Mask the whole row; every cell's text stays
    // asserted below, so the mask drops pixels, not coverage.
    // (Role queries can't find it here: the open alert dialog marks the app
    // root aria-hidden, so match by element + text instead.)
    const devicesSection = page
      .locator("section.console-section")
      .filter({ has: page.locator("h2", { hasText: "Devices" }) });
    await expect(devicesSection).toHaveCount(1);
    const deviceRows = devicesSection.locator('[data-slot="table-body"] > [data-slot="table-row"]');
    await expect.poll(() => deviceRows.count()).toBeGreaterThanOrEqual(3);
    const deviceRow = (name: string) =>
      devicesSection
        .locator('[data-slot="table-cell"]:first-child')
        .filter({ hasText: new RegExp(`^${name}`) })
        .locator("..");
    const currentDeviceRow = deviceRows.first();
    const tvRow = deviceRow("TV");
    const revokedRow = deviceRow("Revoked");
    await expect(tvRow).toHaveCount(1);
    await expect(revokedRow).toHaveCount(1);

    const currentDeviceCells = currentDeviceRow.locator(':scope > [data-slot="table-cell"]');
    const tvCells = tvRow.locator(':scope > [data-slot="table-cell"]');
    const revokedCells = revokedRow.locator(':scope > [data-slot="table-cell"]');
    await expect(currentDeviceCells).toHaveCount(4);
    await expect(tvCells).toHaveCount(4);
    await expect(revokedCells).toHaveCount(4);
    await expect(currentDeviceCells.nth(0)).toContainText("E2E manager");
    await expect(currentDeviceCells.nth(1)).toHaveText("Manager");
    await expect(currentDeviceCells.nth(2)).toHaveText("Active");
    await expect(tvCells.nth(0)).toHaveText("TV");
    await expect(tvCells.nth(1)).toHaveText("Viewer");
    await expect(tvCells.nth(2)).toHaveText("Active");
    await expect(tvCells.nth(3).locator("button")).toHaveText(["Promote", "Revoke"]);
    await expect(revokedCells.nth(0)).toHaveText("Revoked");
    await expect(revokedCells.nth(1)).toHaveText("Viewer");
    await expect(revokedCells.nth(2)).toHaveText("Revoked");
    await expect(revokedCells.nth(3).locator("button")).toHaveCount(0);

    const currentDeviceChip = currentDeviceCells.nth(0).locator(".status-chip", {
      hasText: "This device",
    });
    const managerActions = currentDeviceCells.nth(3).locator("button");
    if ((await currentDeviceChip.count()) === 1) {
      await expect(currentDeviceChip).toHaveText("This device");
      await expect(managerActions).toHaveCount(0);
    } else {
      await expect(managerActions).toHaveText(["Demote", "Revoke"]);
    }
    await expect(alertDialog).toContainText("This can’t be undone from the web Console yet.");
    await expect(page.locator("h2", { hasText: "Profiles" })).toBeVisible();
    await expect(devicesSection).toContainText("Devices");
    await expectVisualParity(page, testInfo, "console-alert-dialog-open-focused", {
      archivedSubject: alertDialog,
      mask: [currentDeviceRow],
    });
  });

  // Each Console section: route → expected <h2> heading text.
  const SECTIONS: { route: string; heading: string }[] = [
    { route: "libraries", heading: "Libraries" },
    { route: "plugins", heading: "Plugins" },
    { route: "config", heading: "Server configuration" },
    { route: "access", heading: "Enforced filters" },
    { route: "operations", heading: "Resource governor" },
    { route: "sessions", heading: "Update awareness" },
    { route: "health", heading: "Library health" },
    { route: "remote", heading: "Remote access" },
    { route: "api", heading: "API tokens" },
    { route: "activity", heading: "Activity" },
  ];

  for (const { route, heading } of SECTIONS) {
    test(`console/${route} renders its data without error`, async ({ page }) => {
      const errors = collectFatalErrors(page);
      await gotoConsole(page, route);
      await expect(page.getByRole("heading", { name: heading }).first()).toBeVisible();
      expect(errors, errors.join("\n")).toEqual([]);
    });
  }

  test("console/config exposes hardware transcoding controls", async ({ page }) => {
    const errors = collectFatalErrors(page);
    await gotoConsole(page, "config");

    await expect(page.getByRole("heading", { name: "Server configuration" })).toBeVisible();
    // Hardware transcoding lives behind the section's "Advanced" disclosure.
    await page.locator("details.console-disclosure > summary").click();
    await expect(page.getByRole("heading", { name: "Hardware transcoding" })).toBeVisible();
    for (const label of ["Hardware acceleration", "Codec", "Quality", "Device"]) {
      await expect(page.getByText(label).first()).toBeVisible();
    }
    await expect(page.getByRole("button", { name: "Save" })).toHaveCount(2);
    expect(errors, errors.join("\n")).toEqual([]);
  });

  // Update awareness / Jobs / Live sessions now share the "Live sessions"
  // destination; /console/operations owns the resource governor + job queue.
  test("console/sessions exposes update awareness, jobs and live sessions rows", async ({
    page,
  }) => {
    const errors = collectFatalErrors(page);
    await gotoConsole(page, "sessions");

    await expect(page.getByRole("heading", { name: "Update awareness" })).toBeVisible();
    await expect(page.getByText("plain GET").first()).toBeVisible();
    await expect(page.getByText(/backup and review migration notes/i)).toHaveCount(0);
    await expect(page.getByRole("switch", { name: "Check for updates" })).toBeVisible();
    await expect(page.getByRole("button", { name: "Save update settings" })).toBeVisible();
    await expect(page.getByText("Current").first()).toBeVisible();
    await expect(page.getByText("Latest").first()).toBeVisible();
    await expect(page.getByRole("heading", { name: "Jobs" })).toBeVisible();
    await expect(page.getByRole("heading", { name: "Live sessions" })).toBeVisible();
    await expect(page.getByText(/No active sessions|Terminate|Restricted/i).first()).toBeVisible();
    expect(errors, errors.join("\n")).toEqual([]);
  });

  test("console/health exposes launch health dimensions and bulk remediation surface", async ({
    page,
  }) => {
    const errors = collectFatalErrors(page);
    await gotoConsole(page, "health");

    await expect(page.getByRole("heading", { name: "Library health" })).toBeVisible();
    for (const label of [
      "Unmatched",
      "Broken",
      "Scrape failures",
      "Missing artwork",
      "Duplicates",
    ]) {
      await expect(page.getByText(label).first()).toBeVisible();
    }
    await expect(page.getByText("All libraries").first()).toBeVisible();
    expect(errors, errors.join("\n")).toEqual([]);
  });

  test("console/health opens drilldowns and runs selected remediation", async ({ page }) => {
    const errors = collectFatalErrors(page);

    async function exerciseSelectedDimension({
      buttonLabel,
      title,
      action,
      confirm,
      expectedText,
    }: {
      buttonLabel: string;
      title: RegExp;
      action: string;
      confirm?: string | RegExp;
      expectedText?: string | RegExp;
    }) {
      await gotoConsole(page, "health");
      await page
        .getByRole("row", { name: /Local Library/ })
        .getByRole("button", { name: new RegExp(`^${buttonLabel}: \\d+$`) })
        .click();
      await expect(page.getByText(title)).toBeVisible();
      if (expectedText) {
        await expect(page.getByText(expectedText)).toBeVisible();
      }
      await expect(page.getByRole("checkbox").first()).toBeVisible();
      await page.getByRole("button", { name: "Select all" }).click();
      await expect(page.getByRole("button", { name: action })).toBeEnabled();
      await page.getByRole("button", { name: "Clear selection" }).click();
      await expect(page.getByRole("button", { name: action })).toBeDisabled();
      await page.getByRole("checkbox").first().check();
      await page.getByRole("button", { name: action }).click();
      if (confirm) {
        // Scope to the alert dialog: the confirm copy is carried by both the
        // visible title and the sr-only description, so a bare text match is
        // ambiguous.
        await expect(page.getByRole("alertdialog")).toContainText(confirm);
        await page.getByRole("button", { name: action }).last().click();
      }
      await expect(page.getByText(title)).toHaveCount(0);
    }

    const cleanup = await seedLibraryHealthDrilldownData();
    try {
      await exerciseSelectedDimension({
        buttonLabel: "Duplicates",
        title: /Local Library - Duplicates/,
        action: "Merge duplicates",
        confirm: /Merged-away items move to Trash/,
        expectedText: "Survivor",
      });
      await exerciseSelectedDimension({
        buttonLabel: "Unmatched",
        title: /Local Library - Unmatched/,
        action: "Rescan unmatched",
      });
      await exerciseSelectedDimension({
        buttonLabel: "Scrape failures",
        title: /Local Library - Scrape failures/,
        action: "Rescrape failures",
      });
      await exerciseSelectedDimension({
        buttonLabel: "Missing artwork",
        title: /Local Library - Missing artwork/,
        action: "Fetch missing artwork",
      });
      await exerciseSelectedDimension({
        buttonLabel: "Broken",
        title: /Local Library - Broken/,
        action: "Trash broken",
        confirm: "Move every broken file in this library to Trash?",
      });
      expect(errors, errors.join("\n")).toEqual([]);
    } finally {
      cleanup();
    }
  });

  test("console/remote exposes local migration wizard", async ({ page }) => {
    const errors = collectFatalErrors(page);
    await gotoConsole(page, "remote");

    await expect(page.getByRole("heading", { name: "Remote access" })).toBeVisible();
    await expect(page.getByRole("heading", { name: "Migration wizard" })).toBeVisible();
    await expect(page.getByText(/No importers available|Configure/i).first()).toBeVisible();
    expect(errors, errors.join("\n")).toEqual([]);
  });
});

test.describe("console locale", () => {
  test.skip(
    !adminAvailable,
    "no admin password on this server — Admin Session cannot be opened; " +
      "boot the gate server (no BEAMFALL_E2E_URL) for Console coverage.",
  );
  test.use({ locale: "fr-FR" });

  test("non-English browser locale renders Console catalog strings", async ({ page }) => {
    const errors = collectFatalErrors(page);
    await page.goto("/web/console");
    const nav = page.getByRole("navigation", { name: "Console" });
    await expect(nav).toBeVisible();
    await expect(nav.getByRole("link", { name: "Bibliothèques", exact: true })).toBeVisible();
    await nav.getByRole("link", { name: "Bibliothèques", exact: true }).click();
    await expect(page.getByRole("heading", { name: "Bibliothèques", exact: true })).toBeVisible();

    await page.goto("/web/console/api");
    await expect(page.getByText("Portées", { exact: true })).toBeVisible();
    await expect(page.getByText("Lire le catalogue", { exact: true })).toBeVisible();
    await expect(page.getByText("Contrôler la lecture", { exact: true })).toBeVisible();
    expect(errors, errors.join("\n")).toEqual([]);
  });
});
