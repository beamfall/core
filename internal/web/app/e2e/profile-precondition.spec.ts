import { enrollRole, storageStateFor, switchEnrolledProfile } from "./lib/harness";
import { collectFatalErrors } from "./lib/spec";
import { expect, markProfilePicked, test } from "./lib/test";

// CORE-2923: tabs share one session. When another tab switches it to a new
// Profile, a tab still showing the old one must not act as that Profile: its
// next request names the Profile it shows, the server refuses the mismatch,
// and the tab reloads to the session's current Profile.
test.describe("stale tab after a cross-tab Profile switch", () => {
  test("refuses the stale tab's request and reloads it to the new Profile", async ({
    browser,
    baseURL,
  }) => {
    if (!baseURL) throw new Error("baseURL is not set");
    // A browser of its own, so the switch leaves the shared harness session alone.
    const viewer = await enrollRole(baseURL, "viewer");
    const context = await browser.newContext({ storageState: storageStateFor(baseURL, viewer) });
    await markProfilePicked(context);
    const stale = await context.newPage();
    try {
      const errors = collectFatalErrors(stale);
      await stale.goto("/web/");
      await expect(stale.getByRole("button", { name: "Account menu for Default" })).toBeVisible();

      // Wait from before the switch: any request the tab makes next is refused.
      const refused = stale.waitForResponse(
        (response) => response.url().includes("/api/") && response.status() === 409,
      );
      // Another tab switches the shared session to the Managed Profile.
      await switchEnrolledProfile(baseURL, viewer, "profile-managed");

      // Opening a title reads it in-app, naming the Profile the tab still shows.
      await stale
        .getByRole("link", { name: /Sample Media/ })
        .first()
        .click();
      const refusal = await refused;
      expect(refusal.request().headers()["x-beamfall-profile"]).toBe("profile-default");
      // The reload aborts the refused request, so its body is the Go tests' to check.
      await expect(stale.getByRole("button", { name: "Account menu for Managed" })).toBeVisible();
      // Every request the tab had in flight for the old Profile is refused once.
      const unexpected = errors.filter((text) => !/Failed to load resource.*\b409\b/.test(text));
      expect(unexpected, unexpected.join("\n")).toEqual([]);
    } finally {
      await context.close();
    }
  });
});
