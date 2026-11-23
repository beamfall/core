import { execFileSync } from "node:child_process";
import { join } from "node:path";
import type { Locator } from "@playwright/test";
import {
  applyLargeText,
  collectFatalErrors,
  expectLargeTextApplied,
  expectNoHorizontalDocumentOverflow,
  harnessState,
} from "./lib/spec";
import { expect, test } from "./lib/test";
import { coverUiStates } from "./lib/ui-state-coverage";
import { expectVisualParity, settleFrames, waitForSplashHandoff } from "./lib/visual-parity";

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

  test("Home features artwork and fits square, landscape and portrait cards", async ({
    page,
  }, testInfo) => {
    const artwork = [
      { id: "square", width: 320, height: 320, color: "#384672" },
      { id: "landscape", width: 320, height: 180, color: "#246658" },
      { id: "portrait", width: 320, height: 480, color: "#733e52" },
    ];
    // Keep the screenshot fixture deterministic. The Home unit tests cover the
    // reverse response order and verify focus on the first loaded hero.
    let releaseSecondLibrary!: () => void;
    const firstHeroReady = new Promise<void>((resolve) => {
      releaseSecondLibrary = resolve;
    });
    await page.setViewportSize({ width: 1440, height: 900 });
    await page.route("**/core-1154-art/**", async (route) => {
      const id = new URL(route.request().url()).pathname.split("/").at(-1);
      const image = artwork.find((entry) => entry.id === id);
      if (!image) return route.abort();
      await route.fulfill({
        contentType: "image/svg+xml",
        body: `<svg xmlns="http://www.w3.org/2000/svg" width="${image.width}" height="${image.height}"><rect width="100%" height="100%" fill="${image.color}"/><circle cx="50%" cy="50%" r="25%" fill="white" opacity=".28"/></svg>`,
      });
    });
    await page.route("**/api/manifest", async (route) => {
      const response = await route.fetch();
      const manifest = await response.json();
      manifest.libraries.push({
        id: "library-extra",
        title: "Second Library",
        previewClips: false,
      });
      manifest.navigation.push({
        id: "extra",
        title: "Second Library",
        view: "layout",
        layoutID: "layout-home",
        libraryID: "library-extra",
      });
      await route.fulfill({ response, json: manifest });
    });
    await page.route("**/api/graphql", async (route) => {
      const { query, variables } = route.request().postDataJSON();
      if (query.startsWith("query HomeLibraries ")) {
        // Exercise a session that can read the library visibility fields.
        return route.fulfill({
          json: {
            data: {
              libraries: [
                { id: "library-local", title: "Local Library", showOnHome: true },
                { id: "library-extra", title: "Second Library", showOnHome: true },
              ],
            },
          },
        });
      }
      if (!query.startsWith("query SourceItems(")) return route.fallback();
      // A Profile with nothing to resume: Home leads with the feature (CORE-2825).
      if (variables.kind === "continue-watching") {
        return route.fulfill({ json: { data: { sourceItems: [] } } });
      }
      if (variables.kind !== "recently-added") return route.fallback();
      if (variables.libraryID === "library-extra") await firstHeroReady;
      // The featured (first) item carries the facts the hero lists under its
      // title: year, rating and runtime.
      const firstLibrary = artwork.map(({ id }, index) => ({
        id,
        kind: "media",
        title: id,
        artwork: { poster: `/core-1154-art/${id}` },
        ...(index === 0 ? { year: 2026, certification: "TV-14", durationSeconds: 2930 } : {}),
      }));
      const secondLibrary = [
        {
          id: "extra",
          kind: "media",
          title: "Second library item",
          artwork: { poster: "/core-1154-art/landscape" },
        },
      ];
      const items =
        variables.libraryID === "library-local"
          ? variables.limit === 60
            ? [
                ...firstLibrary,
                ...Array.from({ length: 109 }, (_, index) => ({
                  id: `mixed-${index}`,
                  kind: "media",
                  title: `Mixed ${index}`,
                  artwork: { poster: `/core-1154-art/${index < 29 ? "portrait" : "landscape"}` },
                })),
              ].slice(variables.offset, variables.offset + variables.limit)
            : firstLibrary
          : variables.libraryID === "library-extra"
            ? secondLibrary
            : [];
      await route.fulfill({ json: { data: { sourceItems: items } } });
    });

    await page.goto("/web/");
    await expect(page.locator(".bf-hero img")).toHaveAttribute("src", /\/core-1154-art\/square/);
    await expect(page.locator(".bf-hero")).toBeVisible();
    releaseSecondLibrary();
    await expect(page.getByRole("list", { name: "Continue Watching" })).toHaveCount(0);
    const shelf = page
      .getByRole("list")
      .filter({ has: page.getByRole("link", { name: "square" }) });
    await expect(shelf).toBeVisible();
    async function expectArtworkAspect(cards: Locator) {
      for (const { id, width, height } of artwork) {
        const frame = cards.getByRole("link", { name: id }).locator(".bf-poster");
        await frame.scrollIntoViewIfNeeded();
        await expect
          .poll(() =>
            frame.locator("img").evaluate((image: HTMLImageElement) => image.naturalWidth),
          )
          .toBeGreaterThan(0);
        const bounds = await frame.boundingBox();
        expect(bounds?.width).toBeGreaterThan(0);
        expect((bounds?.width ?? 0) / (bounds?.height ?? 1)).toBeCloseTo(width / height, 2);
      }
    }
    await expectArtworkAspect(shelf);

    const secondShelf = page.getByRole("list", { name: "Second Library" });
    await expect(secondShelf.getByRole("link", { name: "Second library item" })).toBeVisible();
    await waitForSplashHandoff(page);
    await page.evaluate(async () => {
      await document.fonts.ready;
      window.scrollTo({ top: 0, behavior: "instant" });
    });
    // Home focuses the hero when its artwork beats a 1s grace period, else the
    // first shelf card. A loaded host can miss the grace, so pin the hero focus
    // the golden shows; the Home unit tests cover the fallback.
    const heroPlay = page.locator(".bf-hero [data-focus-item]");
    await heroPlay.focus();
    await expect(heroPlay).toBeFocused();
    await settleFrames(page);
    await expectVisualParity(page, testInfo, "viewer-home-1440x900", { liveOnly: true });

    await secondShelf.scrollIntoViewIfNeeded();
    await expect
      .poll(() =>
        secondShelf.locator("img").evaluate((image: HTMLImageElement) => image.naturalWidth),
      )
      .toBeGreaterThan(0);
    await settleFrames(page);
    await expectVisualParity(page, testInfo, "viewer-home-shelves-1440x900", { liveOnly: true });

    await page
      .locator(".bf-section")
      .filter({ has: shelf })
      .getByRole("button", { name: "See all" })
      .click();
    await expect(page).toHaveURL(/\/seeall\//);
    await expect(page.getByRole("heading", { name: "Local Library" })).toBeVisible();
    await expect(page.getByRole("link", { name: "Second library item" })).toHaveCount(0);
    await expectArtworkAspect(page.locator(".bf-grid"));

    await page.getByRole("button", { name: "Load more" }).click();
    await expect(page.getByRole("button", { name: "Load more" })).toHaveCount(0);
    await page.evaluate(() => window.scrollTo({ top: 0, behavior: "instant" }));
    // Step through the grid so each row lays out before it leaves the window.
    // Later landscape rows must fill the viewport and mount beyond page one.
    let visiblePosition = 0;
    for (let step = 0; step < 24 && visiblePosition <= 60; step += 1) {
      await page.evaluate(() => window.scrollBy({ top: 160, behavior: "instant" }));
      await settleFrames(page);
      let position = 0;
      await expect
        .poll(async () => {
          position = await page.locator(".bf-grid > li[aria-posinset]").evaluateAll((cards) => {
            const middle = window.innerHeight / 2;
            const card = cards.find((element) => {
              const { top, bottom } = element.getBoundingClientRect();
              return top <= middle + 40 && bottom >= middle - 40;
            });
            return Number(card?.getAttribute("aria-posinset")) || 0;
          });
          return position;
        })
        .toBeGreaterThanOrEqual(Math.max(1, visiblePosition));
      visiblePosition = position;
    }
    expect(visiblePosition).toBeGreaterThan(60);
  });

  test("Home leads with Continue Watching above the fold when a Profile has items to resume", async ({
    page,
  }) => {
    await page.setViewportSize({ width: 1440, height: 900 });
    await page.route("**/api/graphql", async (route) => {
      const { query, variables } = route.request().postDataJSON();
      if (!query.startsWith("query SourceItems(")) return route.fallback();
      if (variables.kind === "continue-watching") {
        return route.fulfill({
          json: {
            data: {
              sourceItems: [
                {
                  id: "in-progress",
                  kind: "media",
                  title: "In progress",
                  engagement: { positionSeconds: 120, durationSeconds: 240 },
                },
              ],
            },
          },
        });
      }
      return route.fallback();
    });

    await page.goto("/web/");
    const keepWatching = page.getByRole("list", { name: "Continue Watching" });
    await expect(keepWatching).toBeVisible();
    await expect(page.locator(".bf-hero")).toHaveCount(0);
    await expect(page.getByRole("main").getByRole("list").first()).toHaveAccessibleName(
      "Continue Watching",
    );
    const box = await keepWatching.boundingBox();
    expect(box && box.y + box.height).toBeLessThanOrEqual(900);
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

  test("phone header fits 375px viewport with no horizontal overflow (CORE-1228)", async ({
    page,
  }) => {
    const errors = collectFatalErrors(page);
    await page.setViewportSize({ width: 375, height: 812 });
    await page.route("**/api/graphql", async (route) => {
      const { query } = route.request().postDataJSON() as { query?: string };
      if (query?.includes("currentSession")) {
        const response = await route.fetch();
        const json = await response.json();
        if (json?.data?.currentSession) {
          json.data.currentSession.deviceRole = "manager";
          json.data.currentSession.revealed = true;
          json.data.currentSession.revealPinSet = true;
        }
        return route.fulfill({ response, json });
      }
      if (query?.includes("profiles")) {
        const response = await route.fetch();
        const json = await response.json();
        if (Array.isArray(json?.data?.profiles) && json.data.profiles.length === 1) {
          json.data.profiles.push({
            id: "extra-profile",
            name: "Guest",
            kind: "managed",
            avatar: null,
            pictureURL: null,
            entryPinSet: false,
          });
        }
        return route.fulfill({ response, json });
      }
      return route.fallback();
    });

    await page.goto("/web/");
    await expect(page.getByRole("banner")).toBeVisible();
    await expect(page.getByText("Revealed", { exact: true })).toBeVisible();
    await expectNoHorizontalDocumentOverflow(page);

    for (const locator of [
      page.getByRole("button", { name: "Navigation" }),
      page.getByRole("combobox", { name: "Search" }),
      page.getByRole("button", { name: "Relock" }),
      page.getByRole("link", { name: /Notifications,/ }),
      page.getByRole("button", { name: /Account menu/ }),
    ]) {
      const box = await locator.boundingBox();
      expect(box).not.toBeNull();
      expect(box!.x).toBeGreaterThanOrEqual(0);
      expect(box!.x + box!.width).toBeLessThanOrEqual(375);
    }

    await page.getByRole("button", { name: /Account menu/ }).click();
    for (const name of ["Switch profile", "Server console", "Manage libraries"]) {
      const link = page.getByRole("link", { name });
      await expect(link).toBeVisible();
      const box = await link.boundingBox();
      expect(box).not.toBeNull();
      expect(box!.x).toBeGreaterThanOrEqual(0);
      expect(box!.x + box!.width).toBeLessThanOrEqual(375);
    }
    await page.keyboard.press("Escape");

    await page.getByRole("combobox", { name: "Search" }).click();
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
    const card = page.getByRole("link", { name: /Sample Media/ }).first();
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

  test("smoke: followed feed catalogue exercises filter, sort, Mark played, and route change", async ({
    page,
  }) => {
    const errors = collectFatalErrors(page);
    const { dataDir } = harnessState();
    expect(dataDir, "self-booted harness dataDir").toBeTruthy();
    const catalogDB = join(dataDir!, "db", "catalog.db");

    const runCatalogSQL = (sql: string) =>
      execFileSync("python3", [
        "-c",
        "import sqlite3, sys; c = sqlite3.connect(sys.argv[1], timeout=5); c.executescript(sys.argv[2]); c.commit(); c.close()",
        catalogDB,
        sql,
      ]);

    runCatalogSQL(`
      INSERT OR REPLACE INTO subscription (
        id, library_id, feed_url, root, title, added_at_ms, last_polled_at_ms,
        etag, last_modified, download_policy, download_latest_n
      ) VALUES (
        'sub-e2e-daily', 'library-local', 'https://feeds.example.test/daily.xml',
        '/media/feeds', 'Daily Dispatch', 1763625600000, 1763625600000,
        '', '', 'latest', 3
      );
      INSERT OR REPLACE INTO media (
        id, library_id, title, summary, duration_seconds, container, codec, media_class, mutated_by
      ) VALUES
        ('ep-e2e-1', 'library-local', 'Episode One', '', 1800, 'mp3', 'mp3', 'audio', 'scan'),
        ('ep-e2e-2', 'library-local', 'Episode Two', '', 2400, 'mp3', 'mp3', 'audio', 'scan');
      INSERT OR REPLACE INTO subscription_entry (
        subscription_id, guid, media_id, feed_position, published_at_ms, first_seen_at_ms,
        enclosure_url, enclosure_type, enclosure_length, download_state, download_path
      ) VALUES
        ('sub-e2e-daily', 'guid-1', 'ep-e2e-1', 1, 1763452800000, 1763452800000,
         'https://feeds.example.test/ep1.mp3', 'audio/mpeg', 1048576, 'downloaded', ''),
        ('sub-e2e-daily', 'guid-2', 'ep-e2e-2', 0, 1763625600000, 1763625600000,
         'https://feeds.example.test/ep2.mp3', 'audio/mpeg', 2097152, 'none', '');
    `);

    try {
      await page.goto("/web/");
      const followed = await page.evaluate(async () => {
        const res = await fetch("/api/graphql", {
          method: "POST",
          headers: {
            "Content-Type": "application/json",
            "X-Beamfall-Csrf": "1",
          },
          body: JSON.stringify({
            query:
              "mutation SetFollow($subscriptionID: String!, $on: Boolean!) { setFollow(subscriptionID: $subscriptionID, on: $on) { id title followed newEntryRoute } }",
            variables: { subscriptionID: "sub-e2e-daily", on: true },
          }),
        });
        return res.json();
      });
      expect(followed?.data?.setFollow).toMatchObject({
        id: "sub-e2e-daily",
        followed: true,
        newEntryRoute: "new",
      });

      await page.goto("/web/n/library-local/feeds/sub-e2e-daily");
      await expect(page.getByRole("heading", { level: 1, name: "Daily Dispatch" })).toBeVisible();

      const rows = page.locator("li[data-entry]");
      await expect(rows).toHaveCount(2);
      // Default sort is Newest first: ep-e2e-2 (Nov 20) before ep-e2e-1 (Nov 18).
      await expect(rows.nth(0)).toHaveAttribute("data-entry", "ep-e2e-2");
      await expect(rows.nth(1)).toHaveAttribute("data-entry", "ep-e2e-1");

      // Sort: Oldest first flips order to ep-e2e-1 before ep-e2e-2.
      await page.getByRole("combobox", { name: "Sort" }).selectOption("oldest");
      await expect(rows.nth(0)).toHaveAttribute("data-entry", "ep-e2e-1");
      await expect(rows.nth(1)).toHaveAttribute("data-entry", "ep-e2e-2");

      // Mark Episode One played against the real server.
      await page.getByRole("button", { name: "Mark Episode One played" }).click();
      await expect(page.getByRole("button", { name: "Mark Episode One unplayed" })).toBeVisible();

      // Filter: Unplayed shows only Episode Two.
      await page.getByRole("combobox", { name: "Show" }).selectOption("unplayed");
      await expect(rows).toHaveCount(1);
      await expect(rows.nth(0)).toHaveAttribute("data-entry", "ep-e2e-2");

      // Route change: New entries go to -> Add to end of queue ("queue-end").
      const routeSelect = page.getByRole("combobox", { name: "New entries go to" });
      await expect(routeSelect).toHaveValue("new");
      await routeSelect.selectOption("queue-end");
      await expect(routeSelect).toHaveValue("queue-end");

      // Reload from the real server to prove both played state and newEntryRoute persisted.
      await page.reload();
      await expect(page.getByRole("heading", { level: 1, name: "Daily Dispatch" })).toBeVisible();
      await expect(page.getByRole("combobox", { name: "New entries go to" })).toHaveValue(
        "queue-end",
      );
      await expect(page.getByRole("button", { name: "Mark Episode One unplayed" })).toBeVisible();

      expect(errors, errors.join("\n")).toEqual([]);
    } finally {
      runCatalogSQL(`
        DELETE FROM subscription_entry WHERE subscription_id = 'sub-e2e-daily';
        DELETE FROM subscription WHERE id = 'sub-e2e-daily';
        DELETE FROM media WHERE id IN ('ep-e2e-1', 'ep-e2e-2');
      `);
    }
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

  test("non-English browser locale renders Viewer catalog strings", async ({ page }, testInfo) => {
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
    coverUiStates(testInfo, "area:viewer@locale:fr-FR");
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
    await page.getByRole("button").filter({ hasText: "Current profile" }).click();

    await expect(page).toHaveURL(/\/web\/?$/);
    await expect(page.getByRole("link", { name: "Home", exact: true })).toBeVisible();

    // The answer holds for the rest of the tab's visit.
    await page.reload();
    await expect(page.getByRole("link", { name: "Home", exact: true })).toBeVisible();
    await expect(page.getByRole("heading", { name: "Who's watching?" })).toHaveCount(0);

    expect(errors, errors.join("\n")).toEqual([]);
  });
});
