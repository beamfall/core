import { expect, type Page, test } from "@playwright/test";
import { collectFatalErrors, harnessState } from "./lib/spec";

// AUTH / enrollment (ADR-0012 §5), all from an UNENROLLED browser (the "unauth"
// project ships no storageState). Covers the enroll surface, a real invalid-code
// rejection against the live server, and a full faithful enroll round trip: mint
// a code through the manager lane, redeem it in the browser form, and land in an
// authenticated session.

test.describe("enrollment", () => {
  test("unenrolled browser lands on the enroll surface with a pairing form", async ({ page }) => {
    const errors = collectFatalErrors(page);
    await page.goto("/web/");
    // The session spine 401s without cookies and routes to /enroll.
    await expect(page).toHaveURL(/\/enroll$/);
    await expect(page.getByRole("heading", { name: "Connect this browser" })).toBeVisible();
    await expect(page.getByLabel("Pairing code")).toBeVisible();
    await expect(page.getByRole("button", { name: "Connect" })).toBeVisible();
    expect(errors, errors.join("\n")).toEqual([]);
  });

  test("an invalid pairing code is rejected with the generic message", async ({ page }) => {
    const errors = collectFatalErrors(page);
    await page.goto("/web/enroll");
    await page.getByLabel("Pairing code").fill("NOTACODE");
    await page.getByRole("button", { name: "Connect" }).click();
    // The server returns 401 for an invalid code; the app surfaces the single
    // generic credential-safe message (never echoing why).
    await expect(page.getByRole("alert")).toHaveText("That pairing code is not valid.");
    expect(errors, errors.join("\n")).toEqual([]);
  });

  test("a freshly minted code enrolls the browser into an authenticated session", async ({
    page,
  }) => {
    const errors = collectFatalErrors(page);
    const { baseURL } = harnessState();
    // Mint a real one-time viewer code through the manager lane, then redeem it
    // in the BROWSER enroll form to exercise the whole UI enroll path.
    const code = await mintViewerCode(baseURL);

    await page.goto("/web/enroll");
    await page.getByLabel("Pairing code").fill(code);
    await page.getByRole("button", { name: "Connect" }).click();

    // On success the app reloads to BASE_URL and the session spine re-bootstraps
    // into the authenticated viewer (no longer routed to /enroll).
    await expect(page).toHaveURL(/\/web\/?$/, { timeout: 10_000 });
    await expect(page).not.toHaveURL(/\/enroll$/);
    await expect(page.getByRole("link", { name: "Beamfall", exact: true })).toBeVisible();
    expect(errors, errors.join("\n")).toEqual([]);
  });
});

test.describe("first-run setup folder picker", () => {
  test("uses a browsed folder as the first Library root", async ({ page }) => {
    const errors = collectFatalErrors(page);
    await mockSetupAtLibraryStep(page, {
      "/": { name: "/", path: "/", children: [{ name: "media", path: "/media" }] },
      "/media": {
        name: "media",
        path: "/media",
        children: [{ name: "films", path: "/media/films" }],
      },
      "/media/films": { name: "films", path: "/media/films", children: [] },
    });

    await page.goto("/web/setup");
    await expect(page.getByRole("heading", { name: "Create your first Library" })).toBeVisible();
    await page.getByRole("button", { name: /Browse/ }).click();

    await page.getByRole("button", { name: /media/ }).click();
    await page.getByRole("button", { name: /films/ }).click();
    await page.getByRole("button", { name: "Use this folder" }).click();

    await expect(page.getByLabel("Root folder")).toHaveValue("/media/films");
    await expect(page.getByRole("button", { name: "Close folder picker" })).toHaveCount(0);
    expect(errors, errors.join("\n")).toEqual([]);
  });

  test("keeps the picker open and shows an error when folder read fails", async ({ page }) => {
    const errors = collectFatalErrors(page);
    await mockSetupAtLibraryStep(page, {
      "/": {
        name: "/",
        path: "/",
        children: [{ name: "denied", path: "/denied" }],
      },
    });

    await page.goto("/web/setup");
    await page.getByRole("button", { name: /Browse/ }).click();
    await page.getByRole("button", { name: /denied/ }).click();

    await expect(page.getByText("Couldn't read that folder.")).toBeVisible();
    await expect(page.getByRole("button", { name: "Use this folder" })).toBeVisible();
    await expect(page.getByLabel("Root folder")).toHaveValue("");
    expect(errors, errors.join("\n")).toEqual([]);
  });
});

type MockFolder = {
  name: string;
  path: string;
  children: { name: string; path: string }[];
};

async function mockSetupAtLibraryStep(
  page: Page,
  folders: Record<string, MockFolder>,
): Promise<void> {
  await page.route("**/api/setup", async (route) => {
    await route.fulfill({
      contentType: "application/json",
      body: JSON.stringify({ required: false }),
    });
  });
  await page.route("**/api/graphql", async (route) => {
    const body = route.request().postDataJSON() as {
      query?: string;
      variables?: { path?: string | null };
    };
    const query = body.query ?? "";

    if (query.includes("SetupState")) {
      await route.fulfill({
        contentType: "application/json",
        body: JSON.stringify({
          data: {
            setupState: {
              adminPasswordSet: true,
              hasLibrary: false,
              hasOwnerProfile: true,
              pluginsActivated: true,
              complete: false,
            },
          },
        }),
      });
      return;
    }

    if (query.includes("Plugins")) {
      await route.fulfill({
        contentType: "application/json",
        body: JSON.stringify({
          data: { plugins: [{ id: "filmtv", displayName: { en: "Film & TV" } }] },
        }),
      });
      return;
    }

    if (query.includes("BrowseServerFolders")) {
      const path = body.variables?.path || "/";
      const node = folders[path];
      if (!node) {
        await route.fulfill({
          contentType: "application/json",
          body: JSON.stringify({ errors: [{ message: "not found" }] }),
        });
        return;
      }
      await route.fulfill({
        contentType: "application/json",
        body: JSON.stringify({
          data: {
            browseServerFolders: {
              name: node.name,
              path: node.path,
              kind: "folder",
              children: node.children.map((child) => ({
                name: child.name,
                path: child.path,
                kind: "folder",
              })),
            },
          },
        }),
      });
      return;
    }

    await route.continue();
  });
}

// Mint a one-time viewer pairing code through the seeded manager header lane —
// the same path the harness uses, exposed here so the UI enroll test can redeem
// a real code in the browser form.
async function mintViewerCode(baseURL: string): Promise<string> {
  const res = await fetch(`${baseURL}/api/pairing-codes`, {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      "X-Beamfall-Csrf": "1",
      "X-Beamfall-Session": "session-manager",
      "X-Beamfall-Device": "device-mac",
      "X-Beamfall-Device-Secret": "dev-manager-secret",
    },
    body: JSON.stringify({ deviceRole: "viewer" }),
  });
  if (res.status !== 201) {
    throw new Error(`mint viewer code failed: ${res.status} ${await res.text()}`);
  }
  return (await res.json()).code as string;
}
