import { join } from "node:path";
import { type Browser, expect, type Page, test } from "@playwright/test";
import { collectFatalErrors, harnessAuthDir, harnessState } from "./lib/spec";

const authDir = harnessAuthDir();
const MANAGED_VIEWER_STATE = join(authDir, "managed-viewer.json");
const MANAGED_MANAGER_STATE = join(authDir, "managed-manager.json");
const NON_OWNER_VIEWER_STATE = join(authDir, "non-owner-viewer.json");

async function rolePage(
  browser: Browser,
  storageState: string,
): Promise<{ page: Page; close: () => Promise<void> }> {
  const context = await browser.newContext({ storageState });
  const page = await context.newPage();
  return { page, close: () => context.close() };
}

test.describe("auth matrix", () => {
  const { baseURL } = harnessState();

  test("managed viewer is denied access to Manage and Console", async ({ browser }) => {
    const { page, close } = await rolePage(browser, MANAGED_VIEWER_STATE);
    try {
      const errors = collectFatalErrors(page);

      await page.goto(`${baseURL}/web/manage/libraries`);
      await expect(page).toHaveURL(/\/web\/?$/);
      await expect(page.getByRole("navigation", { name: "Manage" })).toHaveCount(0);

      // ADR-0085 §30: a managed (non-adult) Profile is NOT silently bounced off
      // /web/console — it falls through to the AdminGate, which states the
      // locked reason. Authority is still denied: no unlock form, no nav.
      await page.goto(`${baseURL}/web/console`);
      await expect(page).toHaveURL(/\/web\/console$/);
      await expect(page.getByText("Console unavailable", { exact: true })).toBeVisible();
      await expect(page.getByRole("button", { name: "Unlock Console" })).toHaveCount(0);
      await expect(page.getByRole("navigation", { name: "Console" })).toHaveCount(0);

      expect(errors, errors.join("\n")).toEqual([]);
    } finally {
      await close();
    }
  });

  test("managed manager is denied access to Manage and Console", async ({ browser }) => {
    const { page, close } = await rolePage(browser, MANAGED_MANAGER_STATE);
    try {
      const errors = collectFatalErrors(page);

      await page.goto(`${baseURL}/web/manage/libraries`);
      await expect(page).toHaveURL(/\/web\/?$/);
      await expect(page.getByRole("navigation", { name: "Manage" })).toHaveCount(0);

      // ADR-0085 §30: a managed (non-adult) Profile is NOT silently bounced off
      // /web/console — it falls through to the AdminGate, which states the
      // locked reason. Authority is still denied: no unlock form, no nav.
      await page.goto(`${baseURL}/web/console`);
      await expect(page).toHaveURL(/\/web\/console$/);
      await expect(page.getByText("Console unavailable", { exact: true })).toBeVisible();
      await expect(page.getByRole("button", { name: "Unlock Console" })).toHaveCount(0);
      await expect(page.getByRole("navigation", { name: "Console" })).toHaveCount(0);

      expect(errors, errors.join("\n")).toEqual([]);
    } finally {
      await close();
    }
  });

  test("adult non-owner reaches Console gate but cannot unlock Admin Session", async ({
    browser,
  }) => {
    const { page, close } = await rolePage(browser, NON_OWNER_VIEWER_STATE);
    try {
      const errors = collectFatalErrors(page);

      await page.goto(`${baseURL}/web/console`);
      await expect(page).toHaveURL(/\/web\/console$/);
      await expect(page.getByRole("button", { name: "Unlock Console" })).toBeVisible();

      await page.getByLabel("Admin password").fill("e2e-admin-pw");
      await page.getByRole("button", { name: "Unlock Console" }).click();
      await expect(page.getByRole("alert")).toContainText("The admin password was not accepted.");
      await expect(page.getByRole("navigation", { name: "Console" })).toHaveCount(0);

      expect(errors, errors.join("\n")).toEqual([]);
    } finally {
      await close();
    }
  });
});
