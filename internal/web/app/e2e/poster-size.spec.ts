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
});
