// CORE-1305: covers rows of the UI state inventory (lib/ui-states.mjs) that no other spec does.
// Two projects run this file: ui-states at desktop width and ui-states-mobile on a phone. Titles
// carry "UI state inventory" so the smoke runs them once, in its unfiltered run, where the
// coverage reporter reads them.

import { join } from "node:path";
import {
  applyLargeText,
  collectFatalErrors,
  expectForcedColorsActive,
  expectLargeTextApplied,
  expectNoHorizontalDocumentOverflow,
  harnessAuthDir,
} from "./lib/spec";
import { type Browser, expect, markProfilePicked, type Page, test } from "./lib/test";
import { coverUiStates } from "./lib/ui-state-coverage";
import { CONSOLE_ROUTES, MANAGE_ROUTES, SCREENS, UI_STATE_GAPS } from "./lib/ui-states.mjs";

const isMobile = (projectName: string) => projectName === "ui-states-mobile";

// Answer one GraphQL operation and pass every other request through.
async function routeOperation(
  page: Page,
  operation: string,
  reply: { data?: unknown; errors?: unknown },
): Promise<void> {
  await page.route("**/api/graphql", async (route) => {
    const query = (route.request().postDataJSON() as { query?: string } | null)?.query ?? "";
    if (new RegExp(`\\b(query|mutation) ${operation}\\b`).test(query)) {
      await route.fulfill({ contentType: "application/json", body: JSON.stringify(reply) });
      return;
    }
    await route.fallback();
  });
}

// A fresh server: the wizard opens on its first step.
async function routeFreshSetup(page: Page): Promise<void> {
  await page.route("**/api/setup", (route) =>
    route.fulfill({ contentType: "application/json", body: JSON.stringify({ required: true }) }),
  );
}

// The seeded library's node. Home and Ambient are nodes too, but /all needs a library behind it.
async function libraryNodeHref(page: Page): Promise<string> {
  await page.goto("/web/");
  // On a phone the navigation is a dialog that mounts its links only while open.
  if ((page.viewportSize()?.width ?? Infinity) <= 760) {
    await page.getByRole("button", { name: "Navigation" }).click();
  }
  const link = page.locator('a[href*="/n/library-"]').first();
  await link.waitFor({ state: "attached" });
  const href = await link.getAttribute("href");
  expect(href, "a seeded library node link on the home page").toBeTruthy();
  return href as string;
}

const FIXTURE_ENTITY = {
  id: "entity-fixture",
  kind: "category",
  title: "Fixture Category",
  fields: [],
};

// Navigate to a screen, filling route parameters from the seeded harness.
async function gotoScreen(page: Page, screen: string): Promise<void> {
  switch (screen) {
    case "/setup":
      await routeFreshSetup(page);
      await page.goto("/web/setup");
      return;
    case "/n/:nodeId":
      await page.goto(await libraryNodeHref(page));
      return;
    case "/n/:nodeId/all":
      await page.goto(`${await libraryNodeHref(page)}/all`);
      // The page redirects away when the node has no library, so wait for its own filter bar.
      await expect(page.getByRole("combobox", { name: "Sort by" })).toBeVisible();
      await expect(page).toHaveURL(/\/all$/);
      return;
    case "/entity/:entityId":
      // The development seed has no Entities, so the detail page reads a stubbed one.
      await routeOperation(page, "Entity", { data: { entity: FIXTURE_ENTITY } });
      await routeOperation(page, "MediaForEntity", { data: { mediaForEntity: [] } });
      await page.goto(`/web/entity/${FIXTURE_ENTITY.id}`);
      await expect(
        page.getByRole("heading", { level: 1, name: FIXTURE_ENTITY.title }),
      ).toBeVisible();
      return;
    case "/seeall/:sourceId":
      await page.goto("/web/");
      await page.getByRole("button", { name: "See all" }).first().click();
      await expect(page).toHaveURL(/\/seeall\//);
      return;
    case "/watch/:mediaId":
      await page.goto("/web/watch/media-alpha");
      await expect(page.getByRole("button", { name: "Back" }).first()).toBeVisible();
      return;
    default:
      await page.goto(`/web${screen}`);
      if (screen.startsWith("/console")) {
        await expect(page.getByRole("navigation", { name: "Console" })).toBeVisible();
      }
  }
}

async function expectOneHeading(page: Page): Promise<void> {
  await expect(page.locator("h1")).toHaveCount(1);
  await expect(page.locator("h1")).not.toBeEmpty();
}

test.describe("UI state inventory sweep", () => {
  for (const screen of SCREENS) {
    test(`${screen} renders at this width, in large text and in forced colors`, async ({
      page,
    }, testInfo) => {
      const errors = collectFatalErrors(page);
      await gotoScreen(page, screen);
      await expectOneHeading(page);

      if (isMobile(testInfo.project.name)) {
        expect(errors, errors.join("\n")).toEqual([]);
        // A screen known to overflow a phone is listed in UI_STATE_GAPS; drop the gap to enforce it.
        if (Object.hasOwn(UI_STATE_GAPS, `${screen}@mobile`)) return;
        await expectNoHorizontalDocumentOverflow(page);
        coverUiStates(testInfo, `${screen}@mobile`);
        return;
      }
      coverUiStates(testInfo, screen);

      await applyLargeText(page);
      await expectLargeTextApplied(page);
      await expectOneHeading(page);
      await expectNoHorizontalDocumentOverflow(page);
      coverUiStates(testInfo, `${screen}@large-text`);

      await page.emulateMedia({ forcedColors: "active" });
      await expectForcedColorsActive(page);
      await expect(page.locator("h1")).toBeVisible();
      await expectNoHorizontalDocumentOverflow(page);
      expect(errors, errors.join("\n")).toEqual([]);
      coverUiStates(testInfo, `${screen}@forced-colors`);
    });
  }
});

test.describe("UI state inventory in-page states", () => {
  test.beforeEach(({ page: _page }, testInfo) => {
    test.skip(isMobile(testInfo.project.name), "in-page states run at desktop width");
  });

  test("play history filters, empty and error states", async ({ page }, testInfo) => {
    await gotoScreen(page, "/console/history");
    await expect(page.getByRole("heading", { level: 1, name: "Play history" })).toBeVisible();

    await page.getByRole("combobox", { name: "Profile" }).click();
    await expect(page.getByRole("option", { name: "All profiles" })).toBeVisible();
    await page.keyboard.press("Escape");
    await page.getByRole("combobox", { name: "Library" }).click();
    await expect(page.getByRole("option", { name: "All libraries" })).toBeVisible();
    await page.keyboard.press("Escape");
    coverUiStates(testInfo, "/console/history#select:profile", "/console/history#select:library");

    await routeOperation(page, "PlayHistory", { data: { playHistory: [] } });
    await routeOperation(page, "PlayStats", {
      data: { playStats: { windowDays: 30, profiles: [], items: [] } },
    });
    await page.reload();
    await expect(page.getByText("No plays match these filters.")).toBeVisible();
    await expect(page.getByText("Nothing was played in this period.")).toBeVisible();
    coverUiStates(testInfo, "/console/history#empty:table", "/console/history#empty:rankings");

    await page.unrouteAll({ behavior: "wait" });
    await routeOperation(page, "PlayHistory", { errors: [{ message: "unavailable" }] });
    await routeOperation(page, "PlayStats", { errors: [{ message: "unavailable" }] });
    await page.reload();
    await expect(page.getByText("Could not load play history.")).toBeVisible();
    await expect(page.getByText("Could not load rankings.")).toBeVisible();
    coverUiStates(testInfo, "/console/history#error:table", "/console/history#error:rankings");
  });

  test("an entity deep link that resolves to nothing shows not available", async ({
    page,
  }, testInfo) => {
    // media-alpha is a Media id, so the Entity query answers null: the leak-safe terminal page.
    await page.goto("/web/entity/media-alpha");
    await expect(page.getByRole("heading", { level: 1, name: "Not available" })).toBeVisible();
    coverUiStates(testInfo, "/entity/:entityId#error:not-found");
  });

  test("library browse sort and load failure", async ({ page }, testInfo) => {
    await gotoScreen(page, "/n/:nodeId/all");
    await page.getByRole("combobox", { name: "Sort by" }).click();
    await expect(page.getByRole("option").first()).toBeVisible();
    await page.keyboard.press("Escape");
    coverUiStates(testInfo, "/n/:nodeId/all#select:sort");

    await routeOperation(page, "LibraryItems", { errors: [{ message: "unavailable" }] });
    await page.reload();
    await expect(page.getByText("Could not load this library.")).toBeVisible();
    coverUiStates(testInfo, "/n/:nodeId/all#error:failed");
  });

  test("promote rejects each failure with its own message", async ({ page }, testInfo) => {
    const statuses = [401, 403, 500];
    // The last request is held until the test has seen the busy state, and the test releases it
    // only after the handler has it, so neither side can wait on the other forever.
    let arrived: () => void = () => {};
    const held = new Promise<void>((resolve) => (arrived = resolve));
    let release: () => void = () => {};
    const released = new Promise<void>((resolve) => (release = resolve));
    await page.route("**/api/web/promote", async (route) => {
      const status = statuses.shift() ?? 500;
      if (status === 500) {
        arrived();
        await released;
      }
      await route.fulfill({ status, contentType: "application/json", body: "{}" });
    });
    await page.goto("/web/promote");
    await expectOneHeading(page);
    const code = page.getByRole("textbox", { name: "Pairing code" });
    const submit = page.getByRole("button", { name: "Promote" });

    await code.fill("AAAA-BBBB");
    await submit.click();
    await expect(page.getByText("That pairing code is not valid.")).toBeVisible();
    await submit.click();
    await expect(page.getByText("That's a viewer code", { exact: false })).toBeVisible();
    await submit.click();
    await expect(submit).toBeDisabled();
    await held;
    release();
    await expect(
      page.getByText("The server couldn't promote this browser.", { exact: false }),
    ).toBeVisible();
    coverUiStates(
      testInfo,
      "/promote#error:invalid-401",
      "/promote#error:viewer-code-403",
      "/promote#loading:busy",
      "/promote#error:generic",
    );
  });
});

test.describe("UI state inventory unenrolled states", () => {
  test.use({ storageState: { cookies: [], origins: [] } });
  test.beforeEach(({ page: _page }, testInfo) => {
    test.skip(isMobile(testInfo.project.name), "in-page states run at desktop width");
  });

  test("enroll reports a server failure and an unreachable server", async ({ page }, testInfo) => {
    let reply: "fail" | "abort" = "fail";
    await page.route("**/api/web/enroll", (route) =>
      reply === "abort"
        ? route.abort("connectionrefused")
        : route.fulfill({ status: 500, contentType: "application/json", body: "{}" }),
    );
    await page.goto("/web/enroll");
    await page.getByRole("textbox", { name: "Pairing code" }).fill("AAAA-BBBB");
    await page.getByRole("button", { name: "Connect" }).click();
    await expect(
      page.getByText("The server couldn't connect this browser.", { exact: false }),
    ).toBeVisible();
    reply = "abort";
    await page.getByRole("button", { name: "Connect" }).click();
    await expect(page.getByText("Can't reach the server.", { exact: false })).toBeVisible();
    coverUiStates(testInfo, "/enroll#error:generic", "/enroll#error:offline");
  });
});

test.describe("UI state inventory enroll locale", () => {
  test.use({ storageState: { cookies: [], origins: [] }, locale: "fr-FR" });
  test.beforeEach(({ page: _page }, testInfo) => {
    test.skip(isMobile(testInfo.project.name), "in-page states run at desktop width");
  });

  test("enroll renders its French catalog strings", async ({ page }, testInfo) => {
    await page.goto("/web/enroll");
    await expect(
      page.getByRole("heading", { level: 1, name: "Connecter ce navigateur" }),
    ).toBeVisible();
    await expect(page.getByRole("textbox", { name: "Code de jumelage" })).toBeVisible();
    await expect(page.getByRole("button", { name: "Connecter", exact: true })).toBeVisible();
    coverUiStates(testInfo, "area:enroll@locale:fr-FR");
  });
});

// Live goldens under e2e/goldens/ for areas the archived set never had (CORE-1305). They capture
// surfaces no other project mutates: the enroll page, the first setup step on a mocked fresh
// server, and the search prompt. Exact comparison: the config's defaults, no tolerance.
const shot = { animations: "disabled", caret: "hide", scale: "css" } as const;

test.describe("UI state inventory goldens", () => {
  test.beforeEach(({ page: _page }, testInfo) => {
    test.skip(isMobile(testInfo.project.name), "goldens are recorded at desktop width");
  });

  test("viewer search prompt golden", async ({ page }, testInfo) => {
    await page.goto("/web/search");
    await expect(page.getByRole("heading", { level: 1, name: "Search" })).toBeVisible();
    await expect(page.getByPlaceholder("Search your libraries")).toBeVisible();
    await expect(page.locator("main")).toHaveScreenshot("viewer-search-prompt.png", shot);
    coverUiStates(testInfo, "/search");
  });

  test("setup first step golden", async ({ page }, testInfo) => {
    await gotoScreen(page, "/setup");
    await expectOneHeading(page);
    await expect(page).toHaveScreenshot("setup-first-step.png", shot);
    coverUiStates(testInfo, "/setup");
  });
});

test.describe("UI state inventory enroll golden", () => {
  test.use({ storageState: { cookies: [], origins: [] } });
  test.beforeEach(({ page: _page }, testInfo) => {
    test.skip(isMobile(testInfo.project.name), "goldens are recorded at desktop width");
  });

  test("enroll page golden", async ({ page }, testInfo) => {
    await page.goto("/web/enroll");
    await expect(
      page.getByRole("heading", { level: 1, name: "Connect this browser" }),
    ).toBeVisible();
    await expect(page).toHaveScreenshot("enroll-page.png", shot);
    coverUiStates(testInfo, "/enroll");
  });
});

// Each role is a storage state global-setup writes; the default seeded Profile is the owner.
async function asRole(
  browser: Browser,
  state: string | { cookies: []; origins: [] },
): Promise<{ page: Page; close: () => Promise<void> }> {
  const storageState = typeof state === "string" ? join(harnessAuthDir(), state) : state;
  const context = await browser.newContext({ storageState });
  if (typeof state === "string") await markProfilePicked(context);
  const page = await context.newPage();
  return { page, close: () => context.close() };
}

test.describe("UI state inventory roles", () => {
  test.beforeEach(({ page: _page }, testInfo) => {
    test.skip(isMobile(testInfo.project.name), "roles run at desktop width");
  });

  test("the owner reaches every area", async ({ page }, testInfo) => {
    await page.goto("/web/");
    await expectOneHeading(page);
    await gotoScreen(page, "/manage/libraries");
    await expect(page.getByRole("navigation", { name: "Manage" })).toBeVisible();
    await gotoScreen(page, "/console");
    await page.goto("/web/promote");
    await expectOneHeading(page);
    coverUiStates(
      testInfo,
      "area:viewer@role:owner",
      "area:manage@role:owner",
      "area:console@role:owner",
      "area:promote@role:owner",
    );
  });

  test("other Profiles see the viewer home", async ({ browser }, testInfo) => {
    for (const [state, id] of [
      ["non-owner-viewer.json", "area:viewer@role:adult-non-owner"],
      ["managed-viewer.json", "area:viewer@role:managed"],
    ] as const) {
      const { page, close } = await asRole(browser, state);
      try {
        await page.goto("/web/");
        await expect(page).toHaveURL(/\/web\/?$/);
        await expectOneHeading(page);
        coverUiStates(testInfo, id);
      } finally {
        await close();
      }
    }
  });

  test("a viewer device is turned away from Manage", async ({ browser }, testInfo) => {
    const { page, close } = await asRole(browser, "viewer.json");
    try {
      await page.goto("/web/manage/libraries");
      await expect(page).toHaveURL(/\/web\/?$/);
      await expect(page.getByRole("navigation", { name: "Manage" })).toHaveCount(0);
      coverUiStates(testInfo, "area:manage@role:viewer-device");
    } finally {
      await close();
    }
  });

  test("an unenrolled browser lands on enroll, or on setup for a fresh server", async ({
    browser,
  }, testInfo) => {
    const { page, close } = await asRole(browser, { cookies: [], origins: [] });
    try {
      await page.goto("/web/");
      await expect(page).toHaveURL(/\/web\/enroll$/);
      await expect(page.getByRole("textbox", { name: "Pairing code" })).toBeVisible();
      coverUiStates(testInfo, "area:viewer@role:unenrolled", "area:enroll@role:unenrolled");

      await routeFreshSetup(page);
      await page.goto("/web/setup");
      await expectOneHeading(page);
      coverUiStates(testInfo, "area:setup@role:unenrolled");
    } finally {
      await close();
    }
  });
});

// Manage and Console route lists stay referenced so a renamed export breaks this file's import.
void MANAGE_ROUTES;
void CONSOLE_ROUTES;
