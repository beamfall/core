import { expect, type Page, test } from "@playwright/test";
import { collectFatalErrors, expectNoHorizontalDocumentOverflow } from "./lib/spec";

const CLIP_UTILITY_SELECTOR = [
  '[class*="truncate"]',
  '[class*="line-clamp"]',
  '[class*="whitespace-nowrap"]',
  '[class*="overflow-hidden"]',
].join(",");

interface ClipViolation {
  text: string;
  className: string;
  scrollWidth: number;
  clientWidth: number;
  scrollHeight: number;
  clientHeight: number;
}

async function findUnrecoverableClips(page: Page): Promise<ClipViolation[]> {
  return page.evaluate((selector) => {
    function visible(el: Element): boolean {
      const rect = el.getBoundingClientRect();
      const style = getComputedStyle(el);
      return (
        rect.width > 0 &&
        rect.height > 0 &&
        style.visibility !== "hidden" &&
        style.display !== "none"
      );
    }

    function textOf(el: Element): string {
      return (el.textContent || "").replace(/\s+/g, " ").trim();
    }

    function recoverable(el: Element, text: string): boolean {
      const title = el.getAttribute("title") || "";
      const aria = el.getAttribute("aria-label") || "";
      return title.includes(text) || aria.includes(text);
    }

    return Array.from(document.querySelectorAll(selector))
      .filter(visible)
      .map((el) => ({ el, text: textOf(el) }))
      .filter(({ text }) => text.length > 0)
      .filter(({ el, text }) => {
        const clipped =
          el.scrollWidth - el.clientWidth > 1 || el.scrollHeight - el.clientHeight > 1;
        return clipped && !recoverable(el, text);
      })
      .map(({ el, text }) => ({
        text,
        className: el.getAttribute("class") || "",
        scrollWidth: el.scrollWidth,
        clientWidth: el.clientWidth,
        scrollHeight: el.scrollHeight,
        clientHeight: el.clientHeight,
      }));
  }, CLIP_UTILITY_SELECTOR);
}

async function expectNoUnrecoverableClip(page: Page): Promise<void> {
  const violations = await findUnrecoverableClips(page);
  expect(violations, violations.map((v) => `${v.text} :: ${v.className}`).join("\n")).toEqual([]);
}

test.describe("ADR-0034 text-expansion browser overflow arm", () => {
  test("detects hard visual clipping without an accessible full-value fallback", async ({
    page,
  }) => {
    await page.setContent(`
      <main>
        <div class="truncate" style="width: 8ch; overflow: hidden; white-space: nowrap">
          ⟦Expanded localized label that is clipped without recovery⟧
        </div>
      </main>
    `);
    await expect.poll(() => findUnrecoverableClips(page)).toHaveLength(1);
  });

  for (const surface of [
    { name: "Viewer", path: "/web/" },
    { name: "Manage", path: "/web/manage/libraries" },
    { name: "Console", path: "/web/console/config" },
  ]) {
    test(`${surface.name} clip-risk utilities stay recoverable under real layout`, async ({
      page,
    }) => {
      const errors = collectFatalErrors(page);
      await page.goto(surface.path);
      await expect(page.locator("#root")).not.toBeEmpty();
      await expectNoHorizontalDocumentOverflow(page);
      await expectNoUnrecoverableClip(page);
      expect(errors, errors.join("\n")).toEqual([]);
    });
  }
});
