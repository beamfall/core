import {
  ADMIN_PASSWORD,
  createSeedAdultProfile,
  disableSeedProfile,
  enrollRole,
  storageStateFor,
  switchEnrolledProfile,
} from "./lib/harness";
import {
  collectFatalErrors,
  expectReducedMotionActive,
  expectReducedMotionDurations,
  harnessState,
} from "./lib/spec";
import { expect, type Locator, type Page, test } from "./lib/test";
import { coverUiStates } from "./lib/ui-state-coverage";
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

// CORE-1197: a Manage page that needs an Admin Session asks for the admin
// password in place. This browser has none, so the page must show the prompt
// and neither the raw refusal nor an empty state for data it could not load;
// unlocking then loads the page without navigating. Skips after the locked
// assertions on a server whose admin password is not the harness one.
async function unlockManageInPlace(
  page: Page,
  copy: { title: string; submit: string } = { title: "Admin password needed", submit: "Unlock" },
): Promise<void> {
  const url = page.url();
  await expect(page.getByText(copy.title)).toBeVisible();
  await expect(page.getByText(/admin session required/i)).toHaveCount(0);
  test.skip(!harnessState().adminAvailable, "unlocking needs the harness admin password");
  await page.locator("#admin-password").fill(ADMIN_PASSWORD);
  await page.getByRole("button", { name: copy.submit, exact: true }).click();
  await expect(page.getByText(copy.title)).toHaveCount(0);
  expect(page.url()).toBe(url);
}

// The Admin Session lives server-side on this browser's session row, so a spec
// that unlocked would leave every later spec in the project unlocked. End it so
// each spec starts as a manager without an Admin Session.
async function endAdminSession(page: Page): Promise<void> {
  await page.request.post("/api/graphql", {
    headers: { "X-Beamfall-Csrf": "1" },
    data: { query: "mutation { endAdminSession { active } }" },
  });
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
      variables?: { query?: string; relation?: string };
    };
    const query = body.query ?? "";
    if (query.includes("query SearchResult")) {
      const items =
        body.variables?.query === "nothing"
          ? []
          : [
              { id: "visual-media", kind: "media", title: "Visual parity media", year: 2026 },
              {
                id: "visual-group",
                kind: "entity",
                title: "Visual group",
                entityKind: "group",
                entityKindDisplayName: "Group",
              },
            ];
      return route.fulfill({
        json: {
          data: {
            searchResult: {
              mode: "keyword",
              hasMore: false,
              restrictedResultsPresent: false,
              items,
            },
          },
        },
      });
    }
    if (query.includes("query Media(")) {
      return route.fulfill({
        json: { data: { media: { id: "visual-media", title: "Visual parity media" } } },
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
      // One extra and no editions, so the Editions empty state shows.
      const relation = body.variables?.relation ?? "extra-of";
      const mediaRelations =
        relation === "extra-of"
          ? [
              {
                relation,
                media: {
                  id: "visual-extra",
                  title: "Visual extra",
                  summary: "",
                  year: 2026,
                  favorited: false,
                },
              },
            ]
          : [];
      return route.fulfill({ json: { data: { mediaRelations } } });
    }
    if (query.includes("query Entity(")) {
      return route.fulfill({
        json: {
          data: {
            entity: { id: "visual-group", kind: "group", title: "Visual group", fields: [] },
          },
        },
      });
    }
    if (query.includes("query EntityRelations")) {
      const row = (relation: string, id: string, title: string, sortOrder: number) => ({
        relation,
        targetKind: "media",
        sortOrder,
        attributes: {},
        entity: null,
        media: { id, title, summary: "", year: 2026, favorited: false },
      });
      return route.fulfill({
        json: {
          data: {
            entityRelations: [
              row("member", "visual-one", "Visual one", 0),
              row("member", "visual-two", "Visual two", 1),
              row("featured", "visual-three", "Visual three", 0),
            ],
          },
        },
      });
    }
    return route.fallback();
  });
}

test.describe("manage", () => {
  test.afterEach(async ({ page }) => endAdminSession(page));

  test("the area mounts with the manager role and section nav", async ({ page }) => {
    const errors = collectFatalErrors(page);
    await page.goto("/web/manage");
    // /manage redirects to /manage/libraries.
    await expect(page).toHaveURL(/\/manage\/libraries$/);
    await expect(page.getByRole("navigation", { name: "Manage" })).toBeVisible();
    // Non-managers are redirected to "/", so staying here with the route's
    // heading proves this browser resolved to the manager role.
    await expect(page.getByRole("heading", { level: 1, name: "Libraries" })).toBeVisible();
    expect(errors, errors.join("\n")).toEqual([]);
  });

  test("accessibility variant: Manage supports reduced motion", async ({ page }) => {
    const errors = collectFatalErrors(page);
    await page.emulateMedia({ reducedMotion: "reduce" });
    await page.goto("/web/manage");

    await expectReducedMotionActive(page);
    await expect(page).toHaveURL(/\/manage\/libraries$/);
    await expect(page.getByRole("navigation", { name: "Manage" })).toBeVisible();
    await expect(page.getByRole("heading", { level: 1, name: "Libraries" })).toBeVisible();
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

  // CORE-2913 redesigned Curation as "Versions & links": search for an item, then edit its
  // files, extras and editions, or a group's ordered items. The four golden names predate it and
  // stay because the archived manifest pins them (archived-visual-parity.ts); each now records the
  // nearest state of the new page: the search picker, the Files list, the Extras list and the
  // group's link-type Select.
  test("visual parity covers Curation search, linked items and the link-type Select", async ({
    page,
  }, testInfo) => {
    await mockCuration(page);
    await gotoManage(page, "curation");

    const search = page.getByLabel("Search the catalog");
    await search.fill("nothing");
    await page.getByRole("button", { name: "Search", exact: true }).click();
    await expect(page.getByText("Nothing in the catalog matches that search.")).toBeVisible();
    coverUiStates(testInfo, "/manage/curation#empty:search");

    await search.fill("visual");
    await page.getByRole("button", { name: "Search", exact: true }).click();
    const openMedia = page.getByRole("button", { name: "Open: Visual parity media" });
    await openMedia.focus();
    await expect(openMedia).toBeFocused();
    await expectVisualParity(
      page,
      testInfo,
      "manage-curation-subject-select-open-focused-selected",
    );

    await openMedia.click();
    await expect(page).toHaveURL(/open=visual-media&as=media/);
    await expect(page.getByText("/media/visual-parity.mp4")).toBeVisible();
    await expect(page.getByText("No alternate editions are linked to this item.")).toBeVisible();
    coverUiStates(testInfo, "/manage/curation#empty:editions");
    const editDetails = page.getByRole("link", { name: "Edit details" });
    await editDetails.focus();
    await expect(editDetails).toBeFocused();
    await expectVisualParity(page, testInfo, "manage-curation-versions-tab-focused-selected");

    const addExtra = page.getByRole("button", { name: "Add an extra" });
    await addExtra.click();
    const extraSearch = page.getByLabel("Add an extra");
    await expect(extraSearch).toBeVisible();
    await extraSearch.focus();
    await expect(extraSearch).toBeFocused();
    await expectVisualParity(page, testInfo, "manage-curation-extras-tab-focused-selected");

    await page.goto("/web/manage/curation?open=visual-group&as=entity");
    await expect(page.getByText("Visual three")).toBeVisible();
    await page.getByRole("button", { name: "Add an item" }).click();
    const relation = page.getByRole("combobox", { name: "Link type" });
    await selectAndReopen(page, relation, "featured");
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
      "Entity lookup",
      "Artwork candidates",
      "Show restricted content",
      "Switch profile",
    ]) {
      await expect(nav.getByRole("link", { name: label })).toBeVisible();
    }
    // Expert tools sit behind the one "Show advanced settings" switch (CORE-1117).
    const expert = ["Bulk edit & file operations", "Field locks"];
    for (const label of expert) {
      await expect(nav.getByRole("link", { name: label })).toHaveCount(0);
    }
    await page.getByRole("switch", { name: "Show advanced settings" }).click();
    for (const label of expert) {
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
    // The section menu's phone toggle repeats the current section's name and is
    // hidden at this width, so these probes look for a visible match.
    await expect(page.getByText("Libraries").filter({ visible: true }).first()).toBeVisible();
    await expect(page.getByText("Nothing to show in this library yet.")).toHaveCount(0);
    await unlockManageInPlace(page);
    // CORE-1131 shows each library as a card headed by its title.
    const localLibrary = page.getByRole("heading", { level: 3, name: "Local Library" });
    const emptyState = page.getByText("Nothing to show in this library yet.");
    await expect(localLibrary.or(emptyState)).toBeVisible();
    // The Create Library affordance is reachable.
    await expect(page.getByRole("button", { name: "Create Library" })).toBeVisible();
    expect(errors, errors.join("\n")).toEqual([]);
  });

  test("trash renders its list or empty-state with restore gated per row", async ({ page }) => {
    const errors = collectFatalErrors(page);
    await gotoManage(page, "trash");
    await expect(page.getByText("Trash").filter({ visible: true }).first()).toBeVisible();
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
    await expect(
      page.getByText("Bulk edit & file operations").filter({ visible: true }).first(),
    ).toBeVisible();
    expect(errors, errors.join("\n")).toEqual([]);
  });

  test("artwork candidates screen renders", async ({ page }) => {
    const errors = collectFatalErrors(page);
    await gotoManage(page, "artwork");
    await expect(
      page.getByText("Artwork candidates").filter({ visible: true }).first(),
    ).toBeVisible();
    expect(errors, errors.join("\n")).toEqual([]);
  });

  test("subscriptions screen renders", async ({ page }) => {
    const errors = collectFatalErrors(page);
    await gotoManage(page, "subscriptions");
    await expect(page.getByText("Subscriptions").filter({ visible: true }).first()).toBeVisible();
    expect(errors, errors.join("\n")).toEqual([]);
  });

  test("operations (jobs) screen renders", async ({ page }) => {
    const errors = collectFatalErrors(page);
    await gotoManage(page, "operations");
    await expect(page.locator("#root")).not.toBeEmpty();
    await unlockManageInPlace(page);
    // WEB-5 evidence: the Manage operations route exposes the launch Scheduler
    // and Watcher surfaces, not just a generic page shell.
    for (const label of ["Jobs", "Schedules", "Folder watcher"]) {
      await expect(page.getByText(label).filter({ visible: true }).first()).toBeVisible();
    }
    expect(errors, errors.join("\n")).toEqual([]);
  });

  test("unmatched queue screen renders", async ({ page }) => {
    const errors = collectFatalErrors(page);
    await gotoManage(page, "unmatched");
    await expect(
      page.getByText("Unmatched records").filter({ visible: true }).first(),
    ).toBeVisible();
    await expect(page.getByText("No unmatched records.")).toHaveCount(0);
    await unlockManageInPlace(page);
    await expect(
      page
        .getByText("No unmatched records.")
        .or(page.getByRole("button", { name: /^Hide/ }).first()),
    ).toBeVisible();
    expect(errors, errors.join("\n")).toEqual([]);
  });

  test("field-locks screen renders", async ({ page }) => {
    const errors = collectFatalErrors(page);
    await gotoManage(page, "field-locks");
    await expect(page.getByText("Field locks").filter({ visible: true }).first()).toBeVisible();
    await expect(page.getByLabel("Media ID")).toBeVisible();
    await expect(page.getByText("Enter a media ID to view its field locks.")).toBeVisible();
    expect(errors, errors.join("\n")).toEqual([]);
  });

  test("restrictions screen renders", async ({ page }) => {
    const errors = collectFatalErrors(page);
    await gotoManage(page, "restrictions");
    await expect(
      page.getByText("Show restricted content").filter({ visible: true }).first(),
    ).toBeVisible();
    expect(errors, errors.join("\n")).toEqual([]);
  });

  // CORE-2463: with no Restrictions PIN the page offers Set PIN instead of an
  // unlock field; the PIN then unlocks the session, and Change PIN replaces it.
  // The development seed gives its adult Profiles a PIN, so this runs on a new
  // adult Profile of its own (no Restrictions PIN) in a fresh manager browser,
  // leaving the shared harness Profiles untouched.
  test("restrictions sets a PIN, unlocks with it and changes it", async ({ browser, baseURL }) => {
    if (!baseURL) throw new Error("baseURL is not set");
    const entryPIN = "2463";
    const profileID = await createSeedAdultProfile(
      baseURL,
      `Restrictions Setup ${Date.now()}`,
      entryPIN,
    );
    const manager = await enrollRole(baseURL, "manager");
    await switchEnrolledProfile(baseURL, manager, profileID, entryPIN);
    const context = await browser.newContext({ storageState: storageStateFor(baseURL, manager) });
    const page = await context.newPage();
    try {
      const errors = collectFatalErrors(page);
      await gotoManage(page, "restrictions");
      const setPin = page.getByRole("button", { name: "Set PIN", exact: true });
      const unlock = page.locator("#reveal-pin");
      await expect(page.getByText(/No Restrictions PIN is set yet/)).toBeVisible();
      await expect(unlock).toHaveCount(0);

      await page.getByLabel("New PIN").fill("2463");
      await page.getByLabel("Confirm PIN").fill("2463");
      await setPin.click();
      await expect(page.getByText("PIN saved.", { exact: true })).toBeVisible();

      await unlock.fill("2463");
      await page.getByRole("button", { name: "Reveal", exact: true }).click();
      await expect(page.locator('[data-slot="status-badge"][data-state="revealed"]')).toBeVisible();

      await page.getByRole("button", { name: "Change PIN", exact: true }).click();
      await page.getByLabel("New PIN").fill("8642");
      await page.getByLabel("Confirm PIN").fill("8642");
      await page.getByRole("button", { name: "Change PIN", exact: true }).click();
      await expect(page.getByText("PIN saved.", { exact: true })).toBeVisible();
      await expect(
        page.getByRole("link", { name: "Choose restricted Libraries in Console › Libraries" }),
      ).toBeVisible();

      await page.getByRole("button", { name: "Relock", exact: true }).click();
      await unlock.fill("8642");
      await page.getByRole("button", { name: "Reveal", exact: true }).click();
      await expect(page.locator('[data-slot="status-badge"][data-state="revealed"]')).toBeVisible();
      expect(errors, errors.join("\n")).toEqual([]);
    } finally {
      await context.close();
      await disableSeedProfile(baseURL, profileID);
    }
  });

  test("profile screen renders", async ({ page }) => {
    const errors = collectFatalErrors(page);
    await gotoManage(page, "profile");
    await expect(page.getByText("Switch profile").filter({ visible: true }).first()).toBeVisible();
    expect(errors, errors.join("\n")).toEqual([]);
  });
});

test.describe("manage locale", () => {
  test.use({ locale: "fr-FR" });
  test.afterEach(async ({ page }) => endAdminSession(page));

  test("non-English browser locale renders Manage catalog strings", async ({ page }, testInfo) => {
    const errors = collectFatalErrors(page);
    await page.goto("/web/manage/libraries");
    const nav = page.getByRole("navigation", { name: "Gérer" });
    await expect(nav).toBeVisible();
    await expect(nav.getByRole("link", { name: "Bibliothèques" })).toBeVisible();
    await unlockManageInPlace(page, {
      title: "Mot de passe administrateur requis",
      submit: "Déverrouiller",
    });
    await expect(page.getByRole("button", { name: "Créer une bibliothèque" })).toBeVisible();
    expect(errors, errors.join("\n")).toEqual([]);
    coverUiStates(testInfo, "area:manage@locale:fr-FR");
  });
});
