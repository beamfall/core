import { expect, test } from "@playwright/test";
import { seedViewerFocusSections } from "./fixtures/viewer-focus-sections";
import { expectNoAxeViolations, runAxe } from "./lib/axe";
import {
  applyLargeText,
  collectFatalErrors,
  expectLargeTextApplied,
  expectNoHorizontalDocumentOverflow,
} from "./lib/spec";
import { settleFrames } from "./lib/visual-parity";

test.describe("ADR-0100 a11y real-browser parity pass", () => {
  test("detects an unlabeled control", async ({ page }) => {
    await page.setContent(`<main id="root"><button></button></main>`);
    await expect
      .poll(() => runAxe(page))
      .toContainEqual(expect.objectContaining({ id: "button-name" }));
  });

  test("Viewer card text hierarchy maintains AA contrast on dark backgrounds", async ({ page }) => {
    await seedViewerFocusSections(page);
    await page.goto("/web/");
    await expect(page.locator('button[data-focus-section="recent"]').first()).toBeVisible();

    const result = await page.evaluate(() => {
      const root = document.documentElement;
      const parseRgb = (color: string) => {
        const channels = color
          .match(/[\d.]+/g)
          ?.slice(0, 3)
          .map(Number);
        if (!channels || channels.length !== 3) throw new Error(`Cannot parse color: ${color}`);
        return channels;
      };
      const luminance = (color: string) => {
        const channels = parseRgb(color).map((channel) => {
          const normalized = channel / 255;
          return normalized <= 0.04045 ? normalized / 12.92 : ((normalized + 0.055) / 1.055) ** 2.4;
        });
        return 0.2126 * channels[0] + 0.7152 * channels[1] + 0.0722 * channels[2];
      };
      const contrast = (foreground: string, background: string) => {
        const lighter = Math.max(luminance(foreground), luminance(background));
        const darker = Math.min(luminance(foreground), luminance(background));
        return (lighter + 0.05) / (darker + 0.05);
      };

      const measure = (selector: string, token: string) => {
        const foreground = getComputedStyle(document.querySelector<HTMLElement>(selector)!).color;
        const probe = document.createElement("span");
        probe.style.color = `var(${token})`;
        root.append(probe);
        const tokenColor = getComputedStyle(probe).color;
        const backgrounds = ["--bg-root", "--bg-surface", "--bg-raised"].map((backgroundToken) => {
          probe.style.backgroundColor = `var(${backgroundToken})`;
          const color = getComputedStyle(probe).backgroundColor;
          return { token: backgroundToken, color, ratio: contrast(foreground, color) };
        });
        probe.remove();
        return { selector, token, tokenColor, foreground, backgrounds };
      };

      return {
        title: measure(".bf-card-title", "--text"),
        subtitle: measure(".bf-card-sub", "--text-muted"),
      };
    });

    console.log(`Viewer card contrast ${JSON.stringify(result)}`);
    expect(result.title.foreground).not.toBe(result.subtitle.foreground);
    for (const subject of [result.title, result.subtitle]) {
      expect(subject.foreground).toBe(subject.tokenColor);
      for (const background of subject.backgrounds) {
        expect(
          background.ratio,
          `${subject.selector} ${subject.token} ${subject.foreground} on ${background.token} ${background.color}`,
        ).toBeGreaterThanOrEqual(4.5);
      }
    }
  });

  for (const surface of [
    { name: "Viewer", path: "/web/" },
    { name: "Manage", path: "/web/manage/libraries" },
    { name: "Console", path: "/web/console/config" },
  ]) {
    test(`${surface.name} passes axe and large-text reflow in Chromium`, async ({ page }) => {
      const errors = collectFatalErrors(page);
      if (surface.name === "Viewer") {
        await seedViewerFocusSections(page);
      }
      await page.goto(surface.path);
      await expect(page.locator("#root")).not.toBeEmpty();
      if (surface.name === "Viewer") {
        await expect(page.locator('button[data-focus-section="recent"]').first()).toBeVisible();
        await expect(
          page.locator('button[data-focus-section="your-most-played"]').first(),
        ).toBeVisible();
      }
      await settleFrames(page);
      await expectNoAxeViolations(page);
      await applyLargeText(page);
      await expectLargeTextApplied(page);
      await expectNoHorizontalDocumentOverflow(page);
      expect(errors, errors.join("\n")).toEqual([]);
    });
  }

  // CORE-0089: every Select trigger has an accessible name (axe button-name).
  const viewports = [
    { name: "1440x900", width: 1440, height: 900 },
    { name: "390x844", width: 390, height: 844 },
  ];
  for (const route of ["operations", "unmatched"]) {
    for (const viewport of viewports) {
      test(`manage/${route} has no unnamed buttons at ${viewport.name}`, async ({ page }) => {
        await page.setViewportSize({ width: viewport.width, height: viewport.height });
        await page.goto(`/web/manage/${route}`);
        await expect(page.locator("main h1")).toHaveCount(1);
        await settleFrames(page);
        const violations = await runAxe(page);
        expect(violations.filter((violation) => violation.id === "button-name")).toEqual([]);
      });
    }
  }

  // CORE-0089: every Manage and Viewer route renders exactly one h1.
  const manageRoutes = [
    "libraries",
    "operations",
    "convert",
    "rename",
    "trash",
    "backups",
    "subscriptions",
    "collections",
    "bulk",
    "tags",
    "curation",
    "entities",
    "artwork",
    "markers",
    "field-locks",
    "navigation",
    "unmatched",
    "restrictions",
    "profile",
  ];
  for (const route of manageRoutes) {
    test(`manage/${route} renders exactly one h1`, async ({ page }) => {
      await page.goto(`/web/manage/${route}`);
      await expect(page.locator("h1")).toHaveCount(1);
      await expect(page.locator("h1")).not.toBeEmpty();
    });
  }
  for (const route of ["", "search", "mydata", "notifications", "settings"]) {
    test(`viewer /${route} renders exactly one h1`, async ({ page }) => {
      await page.goto(`/web/${route}`);
      await expect(page.locator("#root")).not.toBeEmpty();
      await expect(page.locator("h1")).toHaveCount(1);
    });
  }

  // CORE-0089: every Console route has one h1 and no unnamed buttons (admin session via storageState).
  const consoleRoutes = [
    "",
    "operations",
    "sessions",
    "health",
    "activity",
    "libraries",
    "plugins",
    "identification",
    "layout",
    "access",
    "schedules",
    "remote",
    "api",
    "backups",
    "retention",
    "migrate",
    "updates",
    "config",
  ];
  for (const route of consoleRoutes) {
    test(`console/${route || "overview"} renders exactly one h1 and no unnamed buttons`, async ({
      page,
    }) => {
      await page.goto(`/web/console/${route}`);
      await expect(page.getByRole("navigation", { name: "Console" })).toBeVisible();
      await expect(page.locator("h1")).toHaveCount(1);
      await expect(page.locator("h1")).not.toBeEmpty();
      await settleFrames(page);
      const violations = await runAxe(page);
      expect(violations.filter((violation) => violation.id === "button-name")).toEqual([]);
    });
  }

  // Viewer routes that need a seeded id: reached through the page's own links.
  test("viewer /watch, /seeall and /profiles each render exactly one h1", async ({ page }) => {
    await page.goto("/web/watch/media-alpha");
    await expect(page.getByRole("button", { name: "Back" }).first()).toBeVisible();
    await expect(page.locator("h1")).toHaveCount(1);

    await page.goto("/web/profiles");
    await expect(page.locator("h1")).toHaveCount(1);

    await page.goto("/web/");
    await page.getByRole("button", { name: "See all" }).first().click();
    await expect(page).toHaveURL(/\/seeall\//);
    await expect(page.locator("h1")).toHaveCount(1);
    await expect(page.locator("h1")).not.toBeEmpty();
  });

  test("viewer /n and /entity render exactly one h1", async ({ page }) => {
    await page.goto("/web/");
    await page.getByRole("link").first().waitFor();
    const node = await page.locator('a[href*="/n/"]').first().getAttribute("href");
    expect(node, "a seeded /n/ link on the home page").toBeTruthy();
    // Entity pages are reached by card navigation, not links; a seeded id that resolves to nothing renders the leak-safe unavailable state, which must still carry the h1.
    for (const href of [node as string, "/web/entity/media-alpha"]) {
      await page.goto(href);
      await expect(page.locator("h1")).toHaveCount(1);
      await expect(page.locator("h1")).not.toBeEmpty();
    }
  });
});
