import type { Page } from "@playwright/test";
import { collectFatalErrors } from "./lib/spec";
import { expect, test } from "./lib/test";

// CORE-2468: the S/M/L poster size control on a library grid. Smaller sizes
// fit more columns, the choice survives a reload, and the card images ask for
// a smaller derivative at Small. The grid uses auto-fill tracks, so the column
// count holds even when the seeded library has only a few items.

async function openLibraryGrid(page: Page): Promise<void> {
  await page.goto("/web/");
  const link = page.locator('a[href*="/n/library-"]').first();
  await link.waitFor({ state: "attached" });
  const href = await link.getAttribute("href");
  expect(href, "a seeded library node link on the home page").toBeTruthy();
  await page.goto(`${href}/all`);
  await expect(page.getByRole("button", { name: /^Sort by / })).toBeVisible();
}

async function gridColumns(page: Page): Promise<number> {
  const grid = page.locator(".bf-grid").first();
  await expect(grid).toBeVisible();
  return grid.evaluate(
    (el) => getComputedStyle(el).gridTemplateColumns.split(" ").filter(Boolean).length,
  );
}

test.describe("poster size", () => {
  // Auto-fill counts step at fixed widths, so pick one where Large, Medium and
  // Small each land on a different count beside the default 200px sidebar
  // (CORE-2734); at 1280 Large and Medium both fit six.
  test.use({ viewport: { width: 1240, height: 900 } });

  test("smaller sizes fit more columns and the choice persists", async ({ page }) => {
    const errors = collectFatalErrors(page);
    await openLibraryGrid(page);

    const control = page.getByRole("group", { name: "Poster size" });
    const large = control.getByRole("button", { name: "Large posters" });
    const medium = control.getByRole("button", { name: "Medium posters" });
    const small = control.getByRole("button", { name: "Small posters" });
    await expect(large).toHaveAttribute("aria-pressed", "true");
    const largeColumns = await gridColumns(page);

    await medium.click();
    await expect(medium).toHaveAttribute("aria-pressed", "true");
    await expect.poll(() => gridColumns(page)).toBeGreaterThan(largeColumns);
    const mediumColumns = await gridColumns(page);

    await small.click();
    await expect(small).toHaveAttribute("aria-pressed", "true");
    await expect.poll(() => gridColumns(page)).toBeGreaterThan(mediumColumns);
    const smallColumns = await gridColumns(page);
    // Seeded items may have no artwork; when a card image renders, it asks for the small size.
    const images = page.locator(".bf-grid img[sizes]");
    if ((await images.count()) > 0) await expect(images.first()).toHaveAttribute("sizes", "160px");

    await page.reload();
    await expect(page.getByRole("button", { name: "Small posters" })).toHaveAttribute(
      "aria-pressed",
      "true",
    );
    await expect.poll(() => gridColumns(page)).toBe(smallColumns);

    // Back to the default so later specs on this browser see Large.
    await page.getByRole("button", { name: "Large posters" }).click();
    await expect.poll(() => gridColumns(page)).toBe(largeColumns);
    expect(errors, errors.join("\n")).toEqual([]);
  });

  // CORE-2893: the Recommended pivot carries the same control, and its shelf
  // cards narrow with the size. The choice is the one the Library pivot reads.
  test("the Recommended pivot's shelves follow the poster size", async ({ page }) => {
    const errors = collectFatalErrors(page);
    await page.goto("/web/");
    const link = page.locator('a[href*="/n/library-"]').first();
    await link.waitFor({ state: "attached" });
    const href = await link.getAttribute("href");
    expect(href, "a seeded library node link on the home page").toBeTruthy();
    await page.goto(`${href}`);
    await expect(page.getByRole("navigation", { name: "Library views" })).toBeVisible();

    const control = page.getByRole("group", { name: "Poster size" });
    await expect(control.getByRole("button", { name: "Large posters" })).toHaveAttribute(
      "aria-pressed",
      "true",
    );
    // A hydrated media card (a link), not the loading skeleton's placeholder.
    const card = page.locator(".bf-shelf a.bf-card").first();
    await expect(card).toBeVisible();
    const cardWidth = () => card.evaluate((el) => Math.round(el.getBoundingClientRect().width));

    // Desktop widths, then the phone widths below the 760px breakpoint. Large
    // keeps the shelf's earlier fixed width.
    const widths = [
      { viewport: 1240, Large: 164, Medium: 140, Small: 116 },
      { viewport: 390, Large: 128, Medium: 108, Small: 92 },
    ];
    for (const { viewport, ...bySize } of widths) {
      await page.setViewportSize({ width: viewport, height: 900 });
      for (const [size, width] of Object.entries(bySize)) {
        await control.getByRole("button", { name: `${size} posters` }).click();
        await expect.poll(cardWidth, `${size} at ${viewport}px`).toBe(width);
      }
    }
    await page.setViewportSize({ width: 1240, height: 900 });
    await control.getByRole("button", { name: "Small posters" }).click();

    await page.getByRole("link", { name: "Library", exact: true }).click();
    await expect(page.getByRole("button", { name: "Small posters" })).toHaveAttribute(
      "aria-pressed",
      "true",
    );

    // Back to the default so later specs on this browser see Large.
    await page.getByRole("button", { name: "Large posters" }).click();
    expect(errors, errors.join("\n")).toEqual([]);
  });
  // CORE-2893: one app-wide choice. Pick Small on the Categories pivot and
  // Home's shelves draw at Small too.
  test("a choice on any pivot reaches Home's shelves", async ({ page }) => {
    const errors = collectFatalErrors(page);
    await page.goto("/web/");
    const link = page.locator('a[href*="/n/library-"]').first();
    await link.waitFor({ state: "attached" });
    const href = await link.getAttribute("href");
    await page.goto(`${href}/categories`);
    const control = page.getByRole("group", { name: "Poster size" });
    await control.getByRole("button", { name: "Small posters" }).click();

    await page.getByRole("link", { name: "Home" }).first().click();
    const card = page.locator(".bf-shelf a.bf-card").first();
    await expect(card).toBeVisible();
    await expect
      .poll(() => card.evaluate((el) => Math.round(el.getBoundingClientRect().width)))
      .toBe(116);

    // Back to the default so later specs on this browser see Large.
    await page.goto(`${href}/categories`);
    await control.getByRole("button", { name: "Large posters" }).click();
    expect(errors, errors.join("\n")).toEqual([]);
  });

  // CORE-2914: the choice roams with the Profile. With this browser's own
  // value gone, as in a browser that never held a size, the page draws the
  // Profile's saved pick.
  test("a pick follows the Profile to a browser without one", async ({ page }) => {
    const errors = collectFatalErrors(page);
    await openLibraryGrid(page);
    // The save of exactly this size, read back from the mutation's result so a
    // GraphQL error under HTTP 200 fails the test.
    const savePick = async (size: string) => {
      const res = await page.waitForResponse((r) => {
        const body = r.request().postData() ?? "";
        return (
          r.url().includes("/graphql") &&
          body.includes("setClientPref") &&
          body.includes("web.poster-size") &&
          body.includes(`"${size}"`)
        );
      });
      const json = await res.json();
      expect(json.errors, JSON.stringify(json.errors)).toBeUndefined();
      expect(json.data?.setClientPref?.key).toBe("web.poster-size");
    };
    try {
      const saved = savePick("small");
      await page.getByRole("button", { name: "Small posters" }).click();
      await saved;

      await page.evaluate(() => localStorage.removeItem("beamfall.posterSize"));
      await page.reload();
      await expect(page.getByRole("button", { name: "Small posters" })).toHaveAttribute(
        "aria-pressed",
        "true",
      );
      await expect(page.locator("html")).toHaveAttribute("data-poster-size", "small");
    } finally {
      // Back to the default, which also saves Large to the Profile, so later
      // specs see Large.
      const restored = savePick("large");
      await page.getByRole("button", { name: "Large posters" }).click();
      await restored;
    }
    expect(errors, errors.join("\n")).toEqual([]);
  });

  // The app-wide control stays on the Library pivot in List view, where it
  // sizes the row thumbnails.
  for (const width of [1440, 390]) {
    test(`the Library pivot offers the control in List view and sizes its thumbnails at ${width}`, async ({
      page,
    }) => {
      const errors = collectFatalErrors(page);
      await page.setViewportSize({ width: 1440, height: 900 });
      await openLibraryGrid(page);
      await page.setViewportSize({ width, height: 900 });
      const control = page.getByRole("group", { name: "Poster size" });
      await page.getByRole("button", { name: "List", exact: true }).click();
      await expect(control).toBeVisible();
      const thumb = page.locator(".bf-stack .bf-poster").first();
      await expect(thumb).toBeVisible();
      await expect.poll(() => thumb.evaluate((el) => el.getBoundingClientRect().width)).toBe(48);
      await control.getByRole("button", { name: "Small posters" }).click();
      await expect.poll(() => thumb.evaluate((el) => el.getBoundingClientRect().width)).toBe(32);

      await control.getByRole("button", { name: "Large posters" }).click();
      await page.getByRole("button", { name: "Posters", exact: true }).click();
      expect(errors, errors.join("\n")).toEqual([]);
    });
  }
});

// The seeded library has no Collections, Categories or cast, so this samples
// each surface's own markup under the app's stylesheet: Large keeps the size
// each had before CORE-2893, and Small draws it smaller.
const SURFACES = `
  <ul class="bf-category-grid">${"<li><a class='bf-category-card' href='#'>C</a></li>".repeat(8)}</ul>
  <ul class="bf-tile-grid">${"<li><a class='bf-tile' href='#'>T</a></li>".repeat(8)}</ul>
  <ul class="bf-collection-grid">${"<li><div></div></li>".repeat(10)}</ul>
  <ul class="bf-shelf bf-cast"><li class="bf-cast-card"></li></ul>
  <div class="bf-row-link"><span class="bf-row-thumb"></span></div>`;

async function surfaceSizes(page: Page) {
  return page.evaluate((html) => {
    document.getElementById("zz-surfaces")?.remove();
    const host = document.createElement("div");
    host.id = "zz-surfaces";
    host.innerHTML = html;
    (document.querySelector("main") ?? document.body).prepend(host);
    const el = (sel: string) => host.querySelector(sel) as Element;
    const cols = (sel: string) => getComputedStyle(el(sel)).gridTemplateColumns.split(" ").length;
    return {
      category: cols(".bf-category-grid"),
      tile: cols(".bf-tile-grid"),
      collection: cols(".bf-collection-grid"),
      cast: Math.round(el(".bf-cast-card").getBoundingClientRect().width),
      row: Math.round(el(".bf-row-thumb").getBoundingClientRect().width),
    };
  }, SURFACES);
}

for (const { width, large, small } of [
  {
    width: 1440,
    large: { category: 4, tile: 6, collection: 7, cast: 120, row: 240 },
    small: { category: 6, tile: 9, collection: 9, cast: 88, row: 160 },
  },
  {
    width: 390,
    large: { category: 2, tile: 1, collection: 2, cast: 96, row: 120 },
    small: { category: 3, tile: 3, collection: 4, cast: 70, row: 80 },
  },
]) {
  test(`every thumbnail surface follows the poster size at ${width}px`, async ({ page }) => {
    const errors = collectFatalErrors(page);
    await page.goto("/web/");
    const link = page.locator('a[href*="/n/library-"]').first();
    await link.waitFor({ state: "attached" });
    const href = await link.getAttribute("href");
    await page.setViewportSize({ width, height: 900 });
    await page.goto(`${href}/collections`);
    const control = page.getByRole("group", { name: "Poster size" });
    await expect(control.getByRole("button", { name: "Large posters" })).toHaveAttribute(
      "aria-pressed",
      "true",
    );
    expect(await surfaceSizes(page)).toEqual(large);

    await control.getByRole("button", { name: "Small posters" }).click();
    await expect.poll(() => surfaceSizes(page)).toEqual(small);

    await control.getByRole("button", { name: "Large posters" }).click();
    expect(errors, errors.join("\n")).toEqual([]);
  });
}
