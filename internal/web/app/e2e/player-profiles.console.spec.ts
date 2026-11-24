import { collectFatalErrors, harnessState } from "./lib/spec";
import { expect, test } from "./lib/test";

// CORE-3314 (ADR-0240): the Console's Player Profiles section and the QR route a
// player display shows, in a real browser against the real server. Read-only:
// it creates no Player Profile, so the shared server stays as the other console
// specs expect it. Vitest covers the editors and the picker against mocks.

test.describe("player profiles", () => {
  test("the console offers Player Profiles and the player kind", async ({ page }) => {
    test.skip(!harnessState().adminAvailable, "the Console needs an Admin Session");
    const errors = collectFatalErrors(page);
    await page.goto("/web/console/access");
    await expect(page.getByRole("navigation", { name: "Console" })).toBeVisible();

    await expect(page.getByRole("heading", { name: "Player Profiles" })).toBeVisible();

    const kind = page.getByRole("combobox", { name: "Profile type" }).first();
    await kind.click();
    await expect(page.getByRole("option", { name: "Player", exact: true })).toBeVisible();
    await page.keyboard.press("Escape");
    expect(errors, errors.join("\n")).toEqual([]);
  });

  test("an unknown display handle shows only that the display isn't available", async ({
    page,
  }) => {
    const errors = collectFatalErrors(page);
    await page.goto("/web/play-on?display=not-a-handle");
    await expect(page.getByRole("alert")).toHaveText("This display isn’t available.");
    await expect(page.getByRole("button", { name: "Pause" })).toHaveCount(0);
    expect(errors, errors.join("\n")).toEqual([]);
  });
});
