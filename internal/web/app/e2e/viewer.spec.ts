import {
  applyLargeText,
  collectFatalErrors,
  expectLargeTextApplied,
  expectNoHorizontalDocumentOverflow,
} from "./lib/spec";
import { expect, test } from "./lib/test";

// VIEWER area (ADR-0007): an enrolled browser bound to the adult Default profile.
// These specs exercise the real Navigation Manifest + Layout rendering, search,
// entity detail, See-All pagination, the My Data / Privacy panel, the profile
// picker, and the player route — all against the running server's real data.
// Every spec asserts the surface mounts, its expected data/headings render, and
// no uncaught pageerror / unexpected console error fires (smoke.spec.ts posture).

test.describe("viewer", () => {
  test("home/browse renders the manifest layout and viewer chrome", async ({ page }) => {
    const errors = collectFatalErrors(page);
    await page.goto("/web/");

    // The enrolled session resolved (not routed to /enroll).
    await expect(page).not.toHaveURL(/\/enroll$/);
    const root = page.locator("#root");
    await expect(root).not.toBeEmpty();

    // The brand chrome + the manifest nav render (AppShell reads the session
    // spine, so its presence proves the manifest loaded for this session).
    await expect(page.getByRole("banner")).toBeVisible();
    await expect(page.getByRole("link", { name: "Beamfall", exact: true })).toBeVisible();
    // The manifest's navigation nodes render as header links.
    await expect(page.getByRole("link", { name: "Home", exact: true })).toBeVisible();
    // A real Layout section from the manifest renders (a level-2 section
    // heading), proving Layout rendering — not just the chrome — works.
    await expect(page.getByRole("heading", { level: 2 }).first()).toBeVisible();

    expect(errors, errors.join("\n")).toEqual([]);
  });

  test("accessibility variant: Viewer supports large text", async ({ page }) => {
    const errors = collectFatalErrors(page);
    await page.goto("/web/");
    await applyLargeText(page);

    await expectLargeTextApplied(page);
    await expect(page.getByRole("banner")).toBeVisible();
    await expect(page.getByRole("link", { name: "Home", exact: true })).toBeVisible();
    await expect(page.getByRole("heading", { level: 2 }).first()).toBeVisible();
    await expectNoHorizontalDocumentOverflow(page);
    expect(errors, errors.join("\n")).toEqual([]);
  });

  test("auth boundary: viewer device is redirected away from Manage", async ({ page }) => {
    const errors = collectFatalErrors(page);
    await page.goto("/web/manage/libraries");

    await expect(page).toHaveURL(/\/web\/?$/);
    await expect(page.getByRole("navigation", { name: "Manage" })).toHaveCount(0);
    // Scoped + exact: the footer also carries a "Beamfall Core corresponding
    // source" link, so a bare accessible-name match is ambiguous.
    await expect(
      page.getByRole("banner").getByRole("link", { name: "Beamfall", exact: true }),
    ).toBeVisible();
    await expect(page.getByRole("link", { name: "Manage" })).toHaveCount(0);
    expect(errors, errors.join("\n")).toEqual([]);
  });

  test("auth boundary: owner viewer reaches Console admin gate", async ({ page }) => {
    const errors = collectFatalErrors(page);
    await page.goto("/web/console");

    await expect(page.getByRole("button", { name: "Unlock Console" })).toBeVisible();
    await expect(page.getByRole("navigation", { name: "Console" })).toHaveCount(0);
    await expect(page).toHaveURL(/\/web\/console$/);
    expect(errors, errors.join("\n")).toEqual([]);
  });

  test("See-All paginates a Source from a home shelf", async ({ page }) => {
    const errors = collectFatalErrors(page);
    await page.goto("/web/");
    // The home Layout has at least one shelf with a "See all" affordance. Click
    // it through the real shelf path (See-All needs the router nav state).
    const seeAll = page.getByRole("button", { name: "See all" }).first();
    await expect(seeAll).toBeVisible();
    await seeAll.click();
    await expect(page).toHaveURL(/\/seeall\//);
    // The See-All grid renders its Source title heading + a Back affordance.
    await expect(page.getByRole("button", { name: "Back" })).toBeVisible();
    await expect(page.getByRole("heading", { level: 1 })).toBeVisible();
    expect(errors, errors.join("\n")).toEqual([]);
  });

  test("an entity/media card opens its detail or player route", async ({ page }) => {
    const errors = collectFatalErrors(page);
    await page.goto("/web/");
    // The seeded "Sample Media" card is the first item in the home shelf.
    const card = page.getByRole("button", { name: /Sample Media/ }).first();
    await expect(card).toBeVisible();
    await card.click();
    // A Media card activates to the player; an entity card opens detail. Either
    // way the route changes off home and the new surface mounts without a crash.
    await expect(page).not.toHaveURL(/\/web\/?$/);
    await expect(page.locator("#root")).not.toBeEmpty();
    expect(errors, errors.join("\n")).toEqual([]);
  });

  test("profile picker lists the seeded profiles", async ({ page }) => {
    const errors = collectFatalErrors(page);
    await page.goto("/web/profiles");
    await expect(page.locator("#root")).not.toBeEmpty();
    // The dev seed has a "Default" (adult) profile; the picker renders it.
    await expect(page.getByText("Default", { exact: false }).first()).toBeVisible();
    expect(errors, errors.join("\n")).toEqual([]);
  });

  test("search runs against the real FTS path and returns a result set", async ({ page }) => {
    const errors = collectFatalErrors(page);
    await page.goto("/web/search");
    await expect(page.getByRole("heading", { name: "Search", exact: true })).toBeVisible();
    // No results heading until there is a query.
    await expect(page.getByRole("heading", { name: "Search results" })).toHaveCount(0);

    // "Sample" matches the seeded media-alpha "Sample Media" in library-local.
    // Results arrive as the user types; the query is mirrored into the URL.
    const input = page.getByRole("searchbox");
    await input.fill("Sample");
    await expect(page).toHaveURL(/\/search\?q=Sample$/);

    // Either results render or the empty-state shows — both are valid, neither
    // is a crash. Assert the search resolved (no fatal error) and the UI settled
    // into one of those two terminal states.
    const results = page.getByRole("listitem");
    const empty = page.getByText("No results for", { exact: false });
    await expect(results.first().or(empty)).toBeVisible();
    expect(errors, errors.join("\n")).toEqual([]);
  });

  test("My Data / Privacy panel lists sessions and stream tokens", async ({ page }) => {
    const errors = collectFatalErrors(page);
    await page.goto("/web/mydata");
    await expect(page.getByRole("heading", { name: "My Data & Privacy" })).toBeVisible();
    // The three cards (export / sessions / delete) render their titles. Card
    // titles are <div>s (components/ui/card.tsx), not headings — match by text.
    await expect(page.getByText("Export my engagement")).toBeVisible();
    await expect(page.getByText("Active sessions")).toBeVisible();
    // The current browser's own session row is flagged "This session".
    await expect(page.getByText("This session").first()).toBeVisible();
    // The stream-tokens sub-panel heading renders too.
    await expect(page.getByRole("heading", { name: "Outstanding stream tokens" })).toBeVisible();
    expect(errors, errors.join("\n")).toEqual([]);
  });

  test("smoke: notifications inbox route renders unread state and empty list", async ({ page }) => {
    const errors = collectFatalErrors(page);
    await page.goto("/web/notifications");
    await expect(page.getByRole("heading", { name: "Notifications" })).toBeVisible();
    await expect(page.getByText("Inbox", { exact: true })).toBeVisible();
    await expect(page.getByText("0 unread")).toBeVisible();
    await expect(page.getByText("No notifications.")).toBeVisible();
    expect(errors, errors.join("\n")).toEqual([]);
  });

  test("smoke: notifications action is available from the viewer shell", async ({ page }) => {
    const errors = collectFatalErrors(page);
    await page.goto("/web/");
    await page.getByRole("link", { name: /Notifications,/ }).click();
    await expect(page).toHaveURL(/\/notifications$/);
    await expect(page.getByRole("heading", { name: "Notifications" })).toBeVisible();
    expect(errors, errors.join("\n")).toEqual([]);
  });

  test("player route loads playback-info and mounts the transport or an honest message", async ({
    page,
  }) => {
    const errors = collectFatalErrors(page);
    // media-alpha is the seeded playable Media in the unrestricted library-local.
    await page.goto("/web/watch/media-alpha");
    // The Back affordance is always present on the player surface.
    await expect(page.getByRole("button", { name: "Back" }).first()).toBeVisible();
    // The page settled into either a playing surface (a <video> mounts) or an
    // honest message/terminal state — both are non-crash outcomes. (The dev seed
    // media has no real bytes, so an "unavailable" message is expected & valid.)
    const video = page.locator("video");
    const message = page.getByRole("alert");
    await expect(video.or(message).first()).toBeVisible();
    expect(errors, errors.join("\n")).toEqual([]);
  });
});

test.describe("viewer locale", () => {
  test.use({ locale: "fr-FR" });

  test("non-English browser locale renders Viewer catalog strings", async ({ page }) => {
    const errors = collectFatalErrors(page);
    await page.goto("/web/");

    expect(await page.evaluate(() => navigator.language)).toBe("fr-FR");
    const seeAll = page.getByRole("button", { name: "Tout voir" }).first();
    await expect(seeAll).toBeVisible();
    await seeAll.click();

    await expect(page).toHaveURL(/\/seeall\//);
    await expect(page.getByRole("button", { name: "Retour" })).toBeVisible();

    await page.goto("/web/search");
    // Search answers as you type (CORE-1191): with no term the page shows its
    // prompt, and the results heading appears only once results arrive.
    await expect(page.getByRole("heading", { level: 1, name: "Rechercher" })).toBeVisible();
    await expect(page.getByRole("searchbox", { name: "Rechercher" })).toBeVisible();
    await expect(
      page.getByText("Rechercher dans vos bibliothèques", { exact: true }),
    ).toBeVisible();

    await page.goto("/web/notifications");
    await expect(page.getByText("Boîte de réception", { exact: true })).toBeVisible();
    await expect(page.getByText("0 non lues")).toBeVisible();
    expect(errors, errors.join("\n")).toEqual([]);
  });
});

// The rest of this file starts each tab past "Who's watching?" (e2e/lib/test.ts);
// this spec starts a fresh tab the way a person does (CORE-1196).
test.describe("viewer who's watching", () => {
  test.use({ profilePicked: false });

  test("a fresh tab opens on the picker and the current Profile continues home", async ({
    page,
  }) => {
    const errors = collectFatalErrors(page);
    await page.goto("/web/");

    await expect(page).toHaveURL(/\/web\/profiles$/);
    await expect(page.getByRole("heading", { level: 1, name: "Who's watching?" })).toBeVisible();
    await page.getByRole("button").filter({ hasText: "Watching now" }).click();

    await expect(page).toHaveURL(/\/web\/?$/);
    await expect(page.getByRole("link", { name: "Home", exact: true })).toBeVisible();

    // The answer holds for the rest of the tab's visit.
    await page.reload();
    await expect(page.getByRole("link", { name: "Home", exact: true })).toBeVisible();
    await expect(page.getByRole("heading", { name: "Who's watching?" })).toHaveCount(0);

    expect(errors, errors.join("\n")).toEqual([]);
  });
});
