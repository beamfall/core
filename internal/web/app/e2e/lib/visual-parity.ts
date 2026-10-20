import { mkdirSync } from "node:fs";
import { isAbsolute, join, resolve } from "node:path";
import { expect, type Locator, type Page, type TestInfo } from "@playwright/test";
import { expectArchivedVisualParity } from "./archived-visual-parity";

const postDir = process.env.TECHDUR_VISUAL_POST_DIR;

export async function expectSelectedOption(option: Locator): Promise<void> {
  await expect
    .poll(() =>
      option.evaluate(
        (element) =>
          element.getAttribute("aria-selected") === "true" ||
          element.getAttribute("data-state") === "checked" ||
          element.hasAttribute("data-selected"),
      ),
    )
    .toBe(true);
}

/**
 * Let the open Select finish settling before the next keystroke.
 *
 * Base UI's `useListNavigation` moves focus across an animation frame, and a key
 * dispatched inside that window is silently dropped: under load, 7 of 24 runs
 * lost an ArrowUp this way. Two frames rather than a fixed delay, because frame
 * duration grows with host load — a wall-clock constant would have to be
 * re-validated at every load the fleet can produce, and this only misbehaves
 * when the host is busy. The in-page timer covers rAF being throttled to a stop
 * on a backgrounded page, so a stalled frame loop fails loudly rather than
 * hanging the gate.
 */
export async function settleFrames(page: Page): Promise<void> {
  await page.evaluate(
    () =>
      new Promise((resolve, reject) => {
        const stalled = setTimeout(
          () => reject(new Error("frame settle timed out after 2000ms (page backgrounded?)")),
          2000,
        );
        requestAnimationFrame(() =>
          requestAnimationFrame(() => {
            clearTimeout(stalled);
            resolve(null);
          }),
        );
      }),
  );
}

export async function openSelectState(
  page: Page,
  trigger: Locator,
  selectedName: string,
): Promise<void> {
  await trigger.focus();
  await expect(trigger).toBeFocused();
  await page.keyboard.press("Enter");
  const listbox = page.getByRole("listbox");
  await expect(listbox).toBeVisible();
  const option = page.getByRole("option", { name: selectedName, exact: true });
  await expectSelectedOption(option);
  await page.keyboard.press("ArrowDown");
  await expect(listbox.locator("[data-highlighted]")).toHaveCount(1);
  await page.keyboard.type(selectedName);
  await expect(option).toHaveAttribute("data-highlighted", "");
}

export async function expectVisualParity(
  page: Page,
  testInfo: TestInfo,
  name: string,
  options: { archivedSubject?: Locator; mask?: Locator[] } = {},
): Promise<void> {
  const fileName = `${name}.png`;
  if (postDir) {
    const root = isAbsolute(postDir) ? postDir : resolve(process.cwd(), postDir);
    const projectDir = join(root, testInfo.project.name);
    mkdirSync(projectDir, { recursive: true });
    await page.screenshot({
      path: join(projectDir, fileName),
      animations: "disabled",
      caret: "hide",
      mask: options.mask,
    });
  }
  await expect(page).toHaveScreenshot(fileName, {
    animations: "disabled",
    caret: "hide",
    maxDiffPixels: 0,
    threshold: 0.2,
    mask: options.mask,
  });
  await expectArchivedVisualParity(options.archivedSubject ?? page, testInfo, name);
  await testInfo.attach(`visual-parity-state:${testInfo.project.name}/${fileName}`, {
    body: "passed",
    contentType: "text/plain",
  });
}

export async function expectLocatorVisualParity(
  locator: Locator,
  testInfo: TestInfo,
  name: string,
): Promise<void> {
  const fileName = `${name}.png`;
  if (postDir) {
    const root = isAbsolute(postDir) ? postDir : resolve(process.cwd(), postDir);
    const projectDir = join(root, testInfo.project.name);
    mkdirSync(projectDir, { recursive: true });
    await locator.screenshot({
      path: join(projectDir, fileName),
      animations: "disabled",
      caret: "hide",
    });
  }
  await expect(locator).toHaveScreenshot(fileName, {
    animations: "disabled",
    caret: "hide",
    maxDiffPixels: 0,
    threshold: 0.2,
  });
  await expectArchivedVisualParity(locator, testInfo, name);
  await testInfo.attach(`visual-parity-state:${testInfo.project.name}/${fileName}`, {
    body: "passed",
    contentType: "text/plain",
  });
}
