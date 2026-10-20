import { readFileSync } from "node:fs";
import { join } from "node:path";
import { expect, type Page, test } from "@playwright/test";
import { expectNoAxeViolations } from "./lib/axe";

// A real single-segment fMP4 HLS VOD (H.264/AAC, 2s, 160x90) generated from
// internal/store/seedmedia/acceptance-review-demo.mp4. Two properties matter:
// a playlist with no segments makes both engines raise a fatal error and tear
// the transport down, and WebKit reports no support for `video/mp2t`, so an
// MPEG-TS variant never reaches the native-HLS path. fMP4 is also what the
// server's own transcode ladder emits.
const fixtures = join(import.meta.dirname, "fixtures");
const playlist = readFileSync(join(fixtures, "browser-compat.m3u8"), "utf8");
const initSegment = readFileSync(join(fixtures, "browser-compat-init.mp4"));
const mediaSegment = readFileSync(join(fixtures, "browser-compat0.m4s"));

// WebKit follows the macOS "full keyboard access" default, where Option+Tab
// traverses all interactive controls when plain Tab only visits text fields.
// Prove traversal reaches a named control instead of merely asserting that
// some element can be focused programmatically.
async function expectKeyboardFocusReachesAControl(page: Page, browserName: string): Promise<void> {
  const expected = page.getByRole("button", { name: "Playback details" });
  await expect(expected).toBeVisible();
  const key = browserName === "webkit" ? "Alt+Tab" : "Tab";
  await expected.focus();
  await page.keyboard.press(`Shift+${key}`);
  await expect(expected).not.toBeFocused();
  await page.keyboard.press(key);
  await expect(expected).toBeFocused();
}

async function installPlaybackRoutes(page: Page): Promise<void> {
  await page.route("**/api/playback-info", (route) =>
    route.fulfill({
      json: {
        decision: "transcode",
        streams: [{ kind: "hls", url: "/api/e2e/browser-compat.m3u8" }],
      },
    }),
  );
  await page.route("**/api/e2e/browser-compat.m3u8", (route) =>
    route.fulfill({ contentType: "application/vnd.apple.mpegurl", body: playlist }),
  );
  await page.route("**/api/e2e/browser-compat-init.mp4", (route) =>
    route.fulfill({ contentType: "video/mp4", body: initSegment }),
  );
  await page.route("**/api/e2e/browser-compat0.m4s", (route) =>
    route.fulfill({ contentType: "video/iso.segment", body: mediaSegment }),
  );
}

async function disableServiceWorkerRegistration(page: Page): Promise<void> {
  await page.addInitScript(() => {
    Object.defineProperty(navigator.serviceWorker, "register", {
      configurable: true,
      value: () => Promise.reject(new Error("service worker disabled for browser contracts")),
    });
  });
}

async function installTrackFixtures(page: Page): Promise<void> {
  await page.addInitScript(() => {
    type AudioTrack = { enabled: boolean; label: string; language: string };
    const audioTracks = new WeakMap<HTMLMediaElement, AudioTrack[]>();
    Object.defineProperty(HTMLMediaElement.prototype, "audioTracks", {
      configurable: true,
      get() {
        let tracks = audioTracks.get(this);
        if (!tracks) {
          tracks = [
            { enabled: true, label: "English", language: "en" },
            { enabled: false, label: "Spanish", language: "es" },
          ];
          audioTracks.set(this, tracks);
        }
        return tracks;
      },
    });

    const installSubtitleTracks = (video: HTMLVideoElement) => {
      if (video.dataset.compatTracksInstalled) return;
      video.dataset.compatTracksInstalled = "true";
      const english = video.addTextTrack("subtitles", "English", "en");
      english.mode = "showing";
      video.addTextTrack("subtitles", "Spanish", "es").mode = "disabled";
    };
    new MutationObserver((records) => {
      for (const record of records) {
        for (const node of record.addedNodes) {
          if (!(node instanceof Element)) continue;
          if (node instanceof HTMLVideoElement) installSubtitleTracks(node);
          for (const video of node.querySelectorAll("video")) installSubtitleTracks(video);
        }
      }
    }).observe(document, { childList: true, subtree: true });
  });
}

test("shell, service worker, keyboard, and HLS selection stay browser-compatible", async ({
  page,
  browserName,
}) => {
  await installPlaybackRoutes(page);

  await page.goto("/web/watch/media-alpha");
  await expect(page.locator("#root")).not.toBeEmpty();

  await expect
    .poll(() =>
      page.evaluate(async () => {
        const registration = await Promise.race([
          navigator.serviceWorker.ready,
          new Promise<null>((resolve) => window.setTimeout(() => resolve(null), 5_000)),
        ]);
        return registration?.active?.state ?? "missing";
      }),
    )
    .toBe("activated");

  // Playwright only intercepts requests from a Service-Worker-controlled page in
  // Chromium; in WebKit the routes above are bypassed while the shell's worker is
  // in control, so the real server answers /api/playback-info. The worker has now
  // been proven to activate, so unregister it — and stop the shell re-registering
  // on the next load — then reload and exercise the HLS engine choice against the
  // mocked stream.
  await disableServiceWorkerRegistration(page);
  await page.evaluate(async () => {
    const registrations = await navigator.serviceWorker.getRegistrations();
    await Promise.all(registrations.map((registration) => registration.unregister()));
  });
  await page.reload();
  await expect(page.locator("#root")).not.toBeEmpty();
  await expectKeyboardFocusReachesAControl(page, browserName);

  await page.getByRole("button", { name: "Playback details" }).click();
  const dialog = page.getByRole("dialog", { name: "Playback details" });
  await expect(dialog).toBeVisible();
  await expect(dialog).toContainText(browserName === "webkit" ? "Native HLS" : "hls.js");
  await page.keyboard.press("Escape");
  await expect(dialog).toBeHidden();
});

test("migrated menu, dialog, listbox, and combobox keep axe and keyboard ARIA contracts", async ({
  page,
}) => {
  await installPlaybackRoutes(page);
  await installTrackFixtures(page);
  await disableServiceWorkerRegistration(page);
  await page.goto("/web/watch/media-alpha");
  await expect(page.locator("#root")).not.toBeEmpty();

  const trigger = page.getByRole("combobox", { name: "Subtitles", exact: true });
  await expect(trigger).toBeVisible();
  await expect(trigger).toHaveAttribute("aria-haspopup", "listbox");
  await expect(trigger).toHaveAttribute("aria-expanded", "false");
  expect(await trigger.getAttribute("aria-activedescendant")).toBeNull();
  await expectNoAxeViolations(page);

  await trigger.focus();
  await page.keyboard.press("ArrowDown");
  const listbox = page.getByRole("listbox", { name: "Subtitles" });
  const menu = page.locator(".bf-menu-interactive");
  await expect(menu).toBeVisible();
  await expect(listbox).toBeVisible();
  await expect(trigger).toHaveAttribute("aria-expanded", "true");
  const controlledID = await trigger.getAttribute("aria-controls");
  expect(controlledID).toBeTruthy();
  await expect(page.locator(`[id="${controlledID}"]`)).toBeVisible();
  expect(await trigger.getAttribute("aria-activedescendant")).toBeNull();
  expect(await listbox.getAttribute("aria-activedescendant")).toBeNull();

  const spanish = listbox.getByRole("option", { name: "Spanish", exact: true });
  await page.keyboard.press("ArrowDown");
  await expect(spanish).toHaveAttribute("data-highlighted", "");
  await expect(spanish).toBeFocused();
  await page.keyboard.press("Home");
  await expect(listbox.getByRole("option", { name: "Subtitles off", exact: true })).toHaveAttribute(
    "data-highlighted",
    "",
  );
  await page.keyboard.type("Spanish");
  await expect(spanish).toHaveAttribute("data-highlighted", "");
  await expect(spanish).toBeFocused();
  await expectNoAxeViolations(page);

  await page.keyboard.press("Enter");
  await expect(listbox).toBeHidden();
  await expect(trigger).toHaveAttribute("aria-expanded", "false");
  await expect(trigger).toBeFocused();
  expect(await trigger.getAttribute("aria-activedescendant")).toBeNull();

  await page.keyboard.press("ArrowUp");
  await expect(listbox).toBeVisible();
  await page.keyboard.type("English");
  const english = listbox.getByRole("option", { name: "English", exact: true });
  await expect(english).toHaveAttribute("data-highlighted", "");
  await expect(english).toBeFocused();
  await page.keyboard.press("Escape");
  await expect(listbox).toBeHidden();
  await expect(trigger).toBeFocused();

  await page.getByRole("button", { name: "Playback details" }).click();
  const dialog = page.getByRole("dialog", { name: "Playback details" });
  await expect(dialog).toBeVisible();
  await expectNoAxeViolations(page);
  await page.keyboard.press("Escape");
  await expect(dialog).toBeHidden();
});
