import { readFileSync } from "node:fs";
import { join } from "node:path";
import { type CDPSession, expect, type Locator, type Page, test } from "@playwright/test";
import { expectVisualParity, settleFrames } from "./lib/visual-parity";

const mediaPath = join(
  import.meta.dirname,
  "..",
  "..",
  "..",
  "store",
  "seedmedia",
  "acceptance-review-demo.mp4",
);
const mediaURL = "/api/e2e/acceptance-review-demo.mp4";
const media = readFileSync(mediaPath);

async function openPlayer(page: Page): Promise<{ stage: Locator; video: Locator }> {
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
      if (video.dataset.visualTracksInstalled) return;
      video.dataset.visualTracksInstalled = "true";
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

  await page.route("**/api/playback-info", async (route) => {
    expect(route.request().method()).toBe("POST");
    await route.fulfill({
      json: {
        decision: "direct-play",
        streams: [{ kind: "direct-play", url: mediaURL }],
      },
    });
  });
  await page.route(`**${mediaURL}`, (route) => {
    const range = route.request().headers().range;
    if (!range) {
      return route.fulfill({ body: media, contentType: "video/mp4" });
    }
    const [startText, endText] = range.replace("bytes=", "").split("-");
    const start = Number(startText);
    const end = endText ? Math.min(Number(endText), media.length - 1) : media.length - 1;
    return route.fulfill({
      status: 206,
      body: media.subarray(start, end + 1),
      headers: {
        "Accept-Ranges": "bytes",
        "Content-Range": `bytes ${start}-${end}/${media.length}`,
        "Content-Type": "video/mp4",
      },
    });
  });

  await page.goto("/web/watch/media-alpha");
  const video = page.locator("video");
  const scrubber = page.getByRole("slider", { name: "Playback position" });
  await expect(video).toBeVisible();
  await expect(page.getByRole("heading", { name: "Playback details" })).toBeVisible();
  await expect(scrubber).toBeVisible();
  await expect.poll(() => video.evaluate((element) => element.duration)).toBe(2);
  await video.evaluate(
    (element) =>
      new Promise<void>((resolve) => {
        element.pause();
        element.addEventListener("seeked", () => resolve(), { once: true });
        element.currentTime = 1;
      }),
  );
  return { stage: video.locator(".."), video };
}

function transportTrigger(page: Page, name: string): Locator {
  return page
    .getByRole("combobox", { name, exact: true })
    .or(page.getByRole("button", { name, exact: true }))
    .first();
}

async function openTransportState(
  page: Page,
  menuName: string,
  selectedName: string,
): Promise<void> {
  const trigger = transportTrigger(page, menuName);
  await trigger.focus();
  await expect(trigger).toBeFocused();
  await page.keyboard.press("Enter");
  const listbox = page.getByRole("listbox", { name: menuName });
  await expect(listbox).toBeVisible();
  const option = listbox.getByRole("option", { name: selectedName, exact: true });
  await expect(option).toHaveAttribute("aria-selected", "true");
  await page.keyboard.press("ArrowDown");
  await expect(listbox.locator("[data-highlighted]")).toHaveCount(1);
  await page.keyboard.type(selectedName);
  await expect(option).toHaveAttribute("data-highlighted", "");
}

async function selectTransportState(
  page: Page,
  menuName: string,
  selectedName: string,
): Promise<void> {
  const trigger = transportTrigger(page, menuName);
  await trigger.focus();
  await page.keyboard.press("Enter");
  const listbox = page.getByRole("listbox", { name: menuName });
  await expect(listbox).toBeVisible();
  const option = listbox.getByRole("option", { name: selectedName, exact: true });
  await page.keyboard.type(selectedName);
  await expect(option).toHaveAttribute("data-highlighted", "");
  // Settle before each navigation key, and assert the highlight actually left
  // the option in between. This site targets the last option, so a dropped
  // ArrowUp is otherwise masked by ArrowDown clamping back onto it — the round
  // trip would pass while proving nothing.
  await settleFrames(page);
  await page.keyboard.press("ArrowUp");
  await expect(option).not.toHaveAttribute("data-highlighted", "");
  await settleFrames(page);
  await page.keyboard.press("ArrowDown");
  await expect(option).toHaveAttribute("data-highlighted", "");
  await page.keyboard.press("Enter");
  await expect(listbox).toBeHidden();
  await openTransportState(page, menuName, selectedName);
}

async function openSharedSelectState(
  page: Page,
  trigger: Locator,
  selectedName: string,
): Promise<void> {
  const native = (await trigger.evaluate((element) => element.tagName)) === "SELECT";
  if (native) {
    // The open popup is browser/OS chrome and is excluded from Playwright page
    // screenshots. Prove that state through :open, selection, and DOM focus.
    await trigger.selectOption({ label: selectedName });
    await trigger.focus();
    await trigger.click();
    await expect.poll(() => trigger.evaluate((element) => element.matches(":open"))).toBe(true);
    const option = trigger.getByRole("option", { name: selectedName, exact: true });
    await expect
      .poll(() => option.evaluate((element) => (element as HTMLOptionElement).selected))
      .toBe(true);
    await expect(trigger).toBeFocused();
    return;
  }

  await trigger.focus();
  await expect(trigger).toBeFocused();
  await page.keyboard.press("Enter");
  const listbox = page.getByRole("listbox");
  await expect(listbox).toBeVisible();
  const option = page.getByRole("option", { name: selectedName, exact: true });
  await page.keyboard.press("ArrowDown");
  await expect(listbox.locator("[data-highlighted]")).toHaveCount(1);
  await page.keyboard.type(selectedName);
  await expect(option).toHaveAttribute("data-highlighted", "");
}

async function closeSharedSelectPopup(page: Page): Promise<void> {
  const listbox = page.getByRole("listbox");
  if ((await listbox.count()) > 0 && (await listbox.first().isVisible())) {
    await page.keyboard.press("Escape");
  }
}

async function point(locator: Locator, x: number, y: number) {
  const box = await locator.boundingBox();
  expect(box).not.toBeNull();
  return { x: box!.x + box!.width * x, y: box!.y + box!.height * y };
}

async function swipe(cdp: CDPSession, start: { x: number; y: number }, dy: number) {
  const touch = (y: number) => [{ x: start.x, y, id: 0, radiusX: 1, radiusY: 1, force: 1 }];
  await cdp.send("Input.dispatchTouchEvent", {
    type: "touchStart",
    touchPoints: touch(start.y),
  });
  await cdp.send("Input.dispatchTouchEvent", {
    type: "touchMove",
    touchPoints: touch(start.y + dy),
  });
  await cdp.send("Input.dispatchTouchEvent", { type: "touchEnd", touchPoints: [] });
}

test("visual parity covers every migrated player widget state", async ({
  page,
  isMobile,
}, testInfo) => {
  await openPlayer(page);

  const skipInterval = page.getByRole("combobox", { name: "This show" });
  await openSharedSelectState(page, skipInterval, "30s");
  await expectVisualParity(page, testInfo, "player-skip-interval-select-open-focused-selected");
  await page.keyboard.press("Escape");
  await skipInterval.selectOption({ label: "Skip buttons" });

  const scrubber = page.getByRole("slider", { name: "Playback position" });
  await scrubber.focus();
  await page.keyboard.press("ArrowRight");
  await expect(scrubber).toHaveAttribute("aria-valuenow", "2");
  await expectVisualParity(page, testInfo, "player-playback-slider-focused-selected");

  const volume = page.getByRole("slider", { name: "Volume" });
  await volume.focus();
  await page.keyboard.press("Home");
  for (let step = 0; step < 8; step++) await page.keyboard.press("ArrowRight");
  await expect
    .poll(() =>
      volume.evaluate(
        (element) => element.getAttribute("aria-valuenow") ?? (element as HTMLInputElement).value,
      ),
    )
    .toBe("0.4");
  await expect(volume).toHaveAttribute("aria-valuetext", /40%/);
  await expectVisualParity(page, testInfo, "player-volume-slider-focused-selected");

  await selectTransportState(page, "Audio", "Spanish");
  const audioListbox = page.getByRole("listbox", { name: "Audio" });
  const visualAudioRows = page.locator(".bf-menu-visual .bf-menu-option");
  await expect(visualAudioRows.filter({ hasText: "Spanish" })).toHaveClass(/is-focused/);
  await expectVisualParity(page, testInfo, "player-audio-listbox-open-focused-selected");
  const englishAudio = audioListbox.getByRole("option", { name: "English", exact: true });
  await englishAudio.hover();
  await expect(englishAudio).toHaveAttribute("data-highlighted", "");
  await expect(visualAudioRows.filter({ hasText: "English" })).toHaveClass(/is-focused/);
  await page.keyboard.press("Escape");

  await selectTransportState(page, "Subtitles", "Spanish");
  await expectVisualParity(page, testInfo, "player-subtitles-listbox-open-focused-selected");
  await page.keyboard.press("Escape");

  await openTransportState(page, "Quality", "Direct play");
  await expectVisualParity(page, testInfo, "player-quality-listbox-open-focused-selected");
  await page.keyboard.press("Escape");

  const speed = page.getByRole("combobox", { name: "Playback speed" });
  await openSharedSelectState(page, speed, "1.25×");
  await expectVisualParity(page, testInfo, "player-speed-select-open-focused-selected");
  await page.keyboard.press("Escape");

  const subtitleStyling = page.getByRole("button", { name: "Subtitle styling" });
  await expect(subtitleStyling).toBeVisible();
  await subtitleStyling.click();
  const subtitleDialog = page.getByRole("dialog", { name: "Subtitle styling" });
  await expect(subtitleDialog).toBeVisible();
  const reset = subtitleDialog.getByRole("button", { name: "Reset" });
  await reset.focus();
  await expect(reset).toBeFocused();
  await expectVisualParity(page, testInfo, "player-subtitle-dialog-open-focused");

  const size = subtitleDialog.getByRole("combobox", { name: "Size" });
  await openSharedSelectState(page, size, "Large");
  await expectVisualParity(page, testInfo, "player-subtitle-size-open-focused-selected");
  await closeSharedSelectPopup(page);

  const background = subtitleDialog.getByRole("combobox", { name: "Background" });
  await openSharedSelectState(page, background, "Solid");
  await expectVisualParity(page, testInfo, "player-subtitle-background-open-focused-selected");
  await closeSharedSelectPopup(page);
  await page.keyboard.press("Escape");

  await page.getByRole("button", { name: "Playback details" }).click();
  const detailsDialog = page.getByRole("dialog", { name: "Playback details" });
  await expect(detailsDialog).toBeVisible();
  await detailsDialog.focus();
  await expect(detailsDialog).toBeFocused();
  await expectVisualParity(page, testInfo, "player-details-dialog-open-focused");
  await page.keyboard.press("Escape");

  if (!isMobile) {
    await page.goto("/web/");
    const restrictions = page.getByRole("button", { name: "Restrictions", exact: true });
    await expect(restrictions).toBeVisible();
    await restrictions.click();
    const sharedDialog = page.getByRole("dialog", { name: "Restrictions" });
    await expect(sharedDialog).toBeVisible();
    const pin = sharedDialog.getByLabel("PIN");
    await expect(pin).toBeFocused();
    await expectVisualParity(page, testInfo, "shared-dialog-open-focused");
  }
});

test("mobile touch gestures seek, clamp volume, and exclude transport controls", async ({
  page,
  isMobile,
}) => {
  test.skip(!isMobile, "touch gestures run in the mobile Chromium project");
  const { stage, video } = await openPlayer(page);
  // The per-show skip interval is a server-side pref that other tests change;
  // pin it to the profile default (10s) so the seek amounts below hold.
  await page.getByRole("combobox", { name: "This show" }).selectOption({ label: "Skip buttons" });
  const feedback = page.getByRole("status");
  const right = await point(stage, 0.75, 0.2);
  const left = await point(stage, 0.25, 0.2);

  await video.evaluate((element) => {
    element.currentTime = 0;
  });
  for (let tap = 0; tap < 3; tap++) await page.touchscreen.tap(right.x, right.y);
  await expect(feedback).toHaveText("Forward 20 seconds");
  await expect.poll(() => video.evaluate((element) => element.currentTime)).toBeCloseTo(2, 1);

  for (let tap = 0; tap < 3; tap++) await page.touchscreen.tap(left.x, left.y);
  await expect(feedback).toHaveText("Back 20 seconds");
  await expect.poll(() => video.evaluate((element) => element.currentTime)).toBeCloseTo(0, 1);

  const cdp = await page.context().newCDPSession(page);
  await video.evaluate((element) => {
    element.volume = 0.95;
  });
  await swipe(cdp, right, -50);
  await expect(feedback).toHaveText("Volume 100%");
  await expect.poll(() => video.evaluate((element) => element.volume)).toBe(1);

  await video.evaluate((element) => {
    element.volume = 0.05;
  });
  await swipe(cdp, right, 50);
  await expect(feedback).toHaveText("Volume 0%");
  await expect.poll(() => video.evaluate((element) => element.volume)).toBe(0);
  await cdp.detach();

  await expect(feedback).toHaveCount(0, { timeout: 1_500 });
  await video.evaluate((element) => {
    element.pause();
    element.currentTime = 1;
  });
  const mute = page.getByRole("button", { name: /^(Mute|Unmute)$/ });
  await mute.tap();
  await mute.tap();
  // The role=slider element is the 16px thumb anchored at the current value;
  // a finger lands on the full-width control surface, so tap that instead.
  await expect(page.getByRole("slider", { name: "Playback position" })).toBeVisible();
  const scrubber = page.locator(".bf-scrubber .bf-slider-control");
  await scrubber.tap({ position: { x: 1, y: 22 } });
  await scrubber.tap({ position: { x: 1, y: 22 } });
  await expect(feedback).toHaveCount(0);
  await expect.poll(() => video.evaluate((element) => element.currentTime)).toBeLessThan(0.25);
});
