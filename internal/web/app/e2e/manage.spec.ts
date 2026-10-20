import { expect, type Locator, type Page, test } from "@playwright/test";
import {
  collectFatalErrors,
  expectReducedMotionActive,
  expectReducedMotionDurations,
} from "./lib/spec";
import {
  expectLocatorVisualParity,
  expectSelectedOption,
  expectVisualParity,
  openSelectState,
  settleFrames,
} from "./lib/visual-parity";

// MANAGE area (ADR-0007 §3): the promoted MANAGER browser's manager surface. The
// "manage" project ships the manager storageState, so every screen here resolves
// to a manager Scope server-side. Each spec asserts the screen mounts, its data
// or empty-state renders, and no uncaught pageerror / unexpected console error
// fires. Destructive actions (restore, bulk apply, delete) are asserted only as
// reachable affordances — never clicked — so the suite never mutates the world.

// Drive to a manage route and wait for its section card to settle. The manage
// area renders a shared left nav + the routed page inside a ManageSection card,
// whose title is the localized heading text (a <div>, not an ARIA heading).
async function gotoManage(page: Page, route: string): Promise<void> {
  await page.goto(`/web/manage/${route}`);
  // The shared manage nav is always present once the area mounts.
  await expect(page.getByRole("navigation", { name: "Manage" })).toBeVisible();
}

async function selectAndReopen(page: Page, trigger: Locator, selectedName: string): Promise<void> {
  await trigger.focus();
  await page.keyboard.press("Enter");
  const option = page.getByRole("option", { name: selectedName, exact: true });
  await page.keyboard.type(selectedName);
  await expect(option).toHaveAttribute("data-highlighted", "");
  // Settle before each navigation key, and assert the highlight actually left
  // the option in between. This site targets the last option, so a dropped
  // ArrowUp is otherwise masked by ArrowDown clamping back onto it — the round
  // trip would pass while proving nothing.
  await settleFrames(page);
  await page.keyboard.press("ArrowUp");
  await expect(option).not.toHaveAttribute("data-highlighted", "");
  await settleFrames(page);
  await page.keyboard.press("ArrowDown");
  await expect(option).toHaveAttribute("data-highlighted", "");
  await page.keyboard.press("Enter");
  await expect(page.getByRole("listbox")).toBeHidden();
  await openSelectState(page, trigger, selectedName);
}

async function mockCollections(page: Page): Promise<void> {
  await page.route("**/api/graphql", async (route) => {
    if (route.request().method() !== "POST") return route.fallback();
    const query = (route.request().postDataJSON() as { query?: string }).query ?? "";
    if (query.includes("query Collections")) {
      return route.fulfill({
        json: {
          data: {
            collections: [
              {
                id: "visual-collection",
                libraryID: "library-local",
                name: "Visual collection",
                parentID: null,
                sortOrder: 0,
                memberCount: 0,
                createdAt: "2026-07-30T00:00:00Z",
                updatedAt: "2026-07-30T00:00:00Z",
                trashedAt: null,
              },
            ],
          },
        },
      });
    }
    if (query.includes("query SmartCollections")) {
      return route.fulfill({ json: { data: { smartCollections: [] } } });
    }
    return route.fallback();
  });
}

async function mockCuration(page: Page): Promise<void> {
  await page.route("**/api/graphql", async (route) => {
    if (route.request().method() !== "POST") return route.fallback();
    const body = route.request().postDataJSON() as {
      query?: string;
      variables?: { relation?: string };
    };
    const query = body.query ?? "";
    if (query.includes("query MediaFields")) {
      return route.fulfill({
        json: {
          data: {
            mediaFields: [
              { name: "title", type: "string", value: "Visual parity media", label: "Title" },
            ],
          },
        },
      });
    }
    if (query.includes("query MediaFiles")) {
      return route.fulfill({
        json: {
          data: {
            mediaFiles: [
              {
                mediaID: "visual-media",
                path: "/media/visual-parity.mp4",
                contentFingerprint: "visual-fingerprint",
                versionLabel: "1080p",
                partOrdinal: 1,
                osHash: "",
                pHash: "",
              },
            ],
          },
        },
      });
    }
    if (query.includes("query MediaRelations")) {
      const relation = body.variables?.relation ?? "extra-of";
      return route.fulfill({
        json: {
          data: {
            mediaRelations: [
              {
                relation,
                media: {
                  id: `visual-${relation}`,
                  title: relation === "extra-of" ? "Visual extra" : "Visual edition",
                  summary: "",
                  year: 2026,
                  favorited: false,
                },
              },
            ],
          },
        },
      });
    }
    return route.fallback();
  });
}

test.describe("manage", () => {
  test("the area mounts with the manager role and section nav", async ({ page }) => {
    const errors = collectFatalErrors(page);
    await page.goto("/web/manage");
    // /manage redirects to /manage/libraries.
    await expect(page).toHaveURL(/\/manage\/libraries$/);
    await expect(page.getByRole("navigation", { name: "Manage" })).toBeVisible();
    // The MANAGER badge proves this browser resolved to the manager role.
    await expect(page.getByText("MANAGER").first()).toBeVisible();
    expect(errors, errors.join("\n")).toEqual([]);
  });

  test("accessibility variant: Manage supports reduced motion", async ({ page }) => {
    const errors = collectFatalErrors(page);
    await page.emulateMedia({ reducedMotion: "reduce" });
    await page.goto("/web/manage");

    await expectReducedMotionActive(page);
    await expect(page).toHaveURL(/\/manage\/libraries$/);
    await expect(page.getByRole("navigation", { name: "Manage" })).toBeVisible();
    await expect(page.getByText("MANAGER").first()).toBeVisible();
    await expectReducedMotionDurations(page);
    expect(errors, errors.join("\n")).toEqual([]);
  });

  test("visual parity covers Collections tab states", async ({ page }, testInfo) => {
    await gotoManage(page, "collections");
    const manual = page.getByRole("tab", { name: "Collections", exact: true });
    const smart = page.getByRole("tab", { name: "Smart", exact: true });
    await expect(manual).toHaveAttribute("aria-selected", "true");
    await manual.focus();
    await expect(manual).toBeFocused();
    await expectVisualParity(page, testInfo, "manage-collections-tab-focused-selected");

    await smart.click();
    await expect(smart).toHaveAttribute("aria-selected", "true");
    await smart.focus();
    await expect(smart).toBeFocused();
    await expectVisualParity(page, testInfo, "manage-smart-tab-focused-selected");
  });

  test("visual parity covers Curation tabs and shared Select states", async ({
    page,
  }, testInfo) => {
    await mockCuration(page);
    await gotoManage(page, "curation");

    const subjectType = page.getByRole("combobox", { name: "Subject type" });
    await openSelectState(page, subjectType, "Media");
    await expectVisualParity(
      page,
      testInfo,
      "manage-curation-subject-select-open-focused-selected",
    );
    await page.keyboard.press("Escape");

    await page.getByLabel("Subject ID").fill("visual-media");
    await page.getByRole("button", { name: "Load" }).click();
    await expect(page.getByText("/media/visual-parity.mp4")).toBeVisible();

    const versions = page.getByRole("tab", { name: "Versions", exact: true });
    const extras = page.getByRole("tab", { name: "Extras", exact: true });
    await expect(versions).toHaveAttribute("aria-selected", "true");
    await versions.focus();
    await expect(versions).toBeFocused();
    await expectVisualParity(page, testInfo, "manage-curation-versions-tab-focused-selected");

    await extras.click();
    await expect(extras).toHaveAttribute("aria-selected", "true");
    await extras.focus();
    await expect(extras).toBeFocused();
    await expectVisualParity(page, testInfo, "manage-curation-extras-tab-focused-selected");

    const relation = page.getByRole("combobox", { name: "Relation" });
    await selectAndReopen(page, relation, "edition-of");
    await expectVisualParity(
      page,
      testInfo,
      "manage-curation-relation-select-open-focused-selected",
    );
  });

  test("visual parity covers shared Manage Select and alert dialog states", async ({
    page,
  }, testInfo) => {
    await mockCollections(page);
    await gotoManage(page, "collections");
    await expect(page.getByText("Visual collection", { exact: true })).toBeVisible();

    const library = page.getByRole("combobox", { name: "Library" });
    await openSelectState(page, library, "Local Library");
    await expectLocatorVisualParity(
      page.locator('[data-slot="select-content"]'),
      testInfo,
      "manage-library-select-open-focused-selected",
    );
    await page.keyboard.press("Escape");

    await page
      .getByRole("row", { name: /Visual collection/ })
      .getByRole("button", { name: "Delete", exact: true })
      .click();
    const alertDialog = page.getByRole("alertdialog", { name: "Delete", exact: true });
    await expect(alertDialog).toBeVisible();
    const cancel = alertDialog.getByRole("button", { name: "Cancel" });
    await cancel.focus();
    await expect(cancel).toBeFocused();
    await expect(alertDialog).toContainText("Visual collection");
    await expect(page.locator('nav[aria-label="Manage"]')).toBeVisible();
    await expectVisualParity(page, testInfo, "manage-alert-dialog-open-focused", {
      archivedSubject: alertDialog,
    });
  });

  test("Core content-management sections are discoverable locally", async ({ page }) => {
    const errors = collectFatalErrors(page);
    await gotoManage(page, "libraries");
    const nav = page.getByRole("navigation", { name: "Manage" });
    for (const label of [
      "Libraries",
      "Jobs",
      "Trash",
      "Subscriptions",
      "Unmatched records",
      "Bulk edit & file operations",
      "Manage",
      "Artwork candidates",
      "Field locks",
      "Restrictions",
      "Profiles",
    ]) {
      await expect(nav.getByRole("link", { name: label })).toBeVisible();
    }
    await expect(page.getByText("beamfall-web")).toHaveCount(0);
    await expect(page.getByText(/checkout|pricing|entitlement/i)).toHaveCount(0);
    expect(errors, errors.join("\n")).toEqual([]);
  });

  test("auth boundary: manager without admin session reaches Console gate", async ({ page }) => {
    const errors = collectFatalErrors(page);
    await page.goto("/web/console");

    await expect(page.getByRole("button", { name: "Unlock Console" })).toBeVisible();
    await expect(page.getByRole("navigation", { name: "Console" })).toHaveCount(0);
    await expect(page).toHaveURL(/\/web\/console$/);
    expect(errors, errors.join("\n")).toEqual([]);
  });

  test("libraries renders the library list or empty state", async ({ page }) => {
    const errors = collectFatalErrors(page);
    await gotoManage(page, "libraries");
    await expect(page.getByText("Libraries").first()).toBeVisible();
    const localLibrary = page.getByRole("cell", { name: "Local Library" });
    const emptyState = page.getByText("Nothing to show in this library yet.");
    await expect(localLibrary.or(emptyState)).toBeVisible();
    // The Create Library affordance is reachable.
    await expect(page.getByRole("button", { name: "Create Library" })).toBeVisible();
    expect(errors, errors.join("\n")).toEqual([]);
  });

  test("trash renders its list or empty-state with restore gated per row", async ({ page }) => {
    const errors = collectFatalErrors(page);
    await gotoManage(page, "trash");
    await expect(page.getByText("Trash").first()).toBeVisible();
    // Either the trash table renders (with a Restore button per row) or the
    // empty-state line shows — both are non-crash outcomes. The seed trash is
    // empty, so the empty-state is expected; assert one of the two settled.
    const restore = page.getByRole("button", { name: "Restore" });
    const empty = page.getByText("Trash is empty", { exact: false });
    await expect(restore.first().or(empty)).toBeVisible();
    expect(errors, errors.join("\n")).toEqual([]);
  });

  test("bulk edit renders its preview surface", async ({ page }) => {
    const errors = collectFatalErrors(page);
    await gotoManage(page, "bulk");
    await expect(page.getByText("Bulk edit & file operations").first()).toBeVisible();
    expect(errors, errors.join("\n")).toEqual([]);
  });

  test("artwork candidates screen renders", async ({ page }) => {
    const errors = collectFatalErrors(page);
    await gotoManage(page, "artwork");
    await expect(page.getByText("Artwork candidates").first()).toBeVisible();
    expect(errors, errors.join("\n")).toEqual([]);
  });

  test("subscriptions screen renders", async ({ page }) => {
    const errors = collectFatalErrors(page);
    await gotoManage(page, "subscriptions");
    await expect(page.getByText("Subscriptions").first()).toBeVisible();
    expect(errors, errors.join("\n")).toEqual([]);
  });

  test("operations (jobs) screen renders", async ({ page }) => {
    const errors = collectFatalErrors(page);
    await gotoManage(page, "operations");
    await expect(page.locator("#root")).not.toBeEmpty();
    // WEB-5 evidence: the Manage operations route exposes the launch Scheduler
    // and Watcher surfaces, not just a generic page shell.
    for (const label of ["Jobs", "Schedules", "Folder watcher"]) {
      await expect(page.getByText(label).first()).toBeVisible();
    }
    expect(errors, errors.join("\n")).toEqual([]);
  });

  test("unmatched queue screen renders", async ({ page }) => {
    const errors = collectFatalErrors(page);
    await gotoManage(page, "unmatched");
    await expect(page.getByText("Unmatched records").first()).toBeVisible();
    await expect(
      page
        .getByText("No unmatched records.")
        .or(page.getByRole("button", { name: "Dismiss" }).first()),
    ).toBeVisible();
    expect(errors, errors.join("\n")).toEqual([]);
  });

  test("field-locks screen renders", async ({ page }) => {
    const errors = collectFatalErrors(page);
    await gotoManage(page, "field-locks");
    await expect(page.getByText("Field locks").first()).toBeVisible();
    await expect(page.getByLabel("Media ID")).toBeVisible();
    await expect(page.getByRole("button", { name: "Load" })).toBeDisabled();
    await expect(page.getByText("Enter a media ID to view its field locks.")).toBeVisible();
    expect(errors, errors.join("\n")).toEqual([]);
  });

  test("restrictions screen renders", async ({ page }) => {
    const errors = collectFatalErrors(page);
    await gotoManage(page, "restrictions");
    await expect(page.getByText("Restrictions").first()).toBeVisible();
    expect(errors, errors.join("\n")).toEqual([]);
  });

  test("profile screen renders", async ({ page }) => {
    const errors = collectFatalErrors(page);
    await gotoManage(page, "profile");
    await expect(page.getByText("Profiles").first()).toBeVisible();
    expect(errors, errors.join("\n")).toEqual([]);
  });
});

test.describe("manage locale", () => {
  test.use({ locale: "fr-FR" });

  test("non-English browser locale renders Manage catalog strings", async ({ page }) => {
    const errors = collectFatalErrors(page);
    await page.goto("/web/manage/libraries");
    const nav = page.getByRole("navigation", { name: "Gérer" });
    await expect(nav).toBeVisible();
    await expect(nav.getByRole("link", { name: "Bibliothèques" })).toBeVisible();
    await expect(page.getByRole("button", { name: "Créer une bibliothèque" })).toBeVisible();
    expect(errors, errors.join("\n")).toEqual([]);
  });
});
