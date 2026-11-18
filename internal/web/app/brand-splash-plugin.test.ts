import { describe, expect, it } from "vitest";
import { BRAND_SPLASH_MARKER, brandSplash, brandSplashMarkup } from "./brand-splash-plugin";

const appDir = import.meta.dirname;

type IndexHtmlHook = (html: string) => string;

function transform(html: string): string {
  const hook = brandSplash({ appDir }).transformIndexHtml as IndexHtmlHook;
  return hook(html);
}

describe("brand splash plugin", () => {
  it("puts each vendored intro in its own declarative shadow root", () => {
    const markup = brandSplashMarkup(appDir);
    const doc = new DOMParser().parseFromString(`<body>${markup}</body>`, "text/html");
    const layouts = Array.from(doc.querySelectorAll("#bf-splash > .bf-splash__layout"));
    expect(layouts.map((el) => el.getAttribute("data-layout"))).toEqual(["horizontal", "stacked"]);
    for (const layout of layouts) {
      const template = layout.querySelector(":scope > template");
      expect(template?.getAttribute("shadowrootmode")).toBe("open");
      expect(template?.innerHTML).toContain('class="bf"');
    }
    expect(markup).toContain('viewBox="0 0 686 256"');
    expect(markup).toContain('viewBox="0 0 448 350"');
    const splash = doc.getElementById("bf-splash");
    expect(splash?.getAttribute("aria-hidden")).toBe("true");
    // The ready sentinel comes after both intros: its arrival starts both clocks together.
    expect(splash?.lastElementChild?.className).toBe("bf-splash__ready");
    expect(markup).toContain("animation-play-state:var(--bf-splash-play,running)!important");
  });

  it("replaces the index.html marker and refuses a page without one", () => {
    const html = transform(`<body>${BRAND_SPLASH_MARKER}<div id="root"></div></body>`);
    expect(html).not.toContain(BRAND_SPLASH_MARKER);
    expect(html.indexOf('id="bf-splash"')).toBeLessThan(html.indexOf('id="root"'));
    expect(() => transform("<body></body>")).toThrow(/marker/);
  });
});
