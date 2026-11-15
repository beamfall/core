import { afterEach, describe, expect, it } from "vitest";
import {
  CAST_SDK_URL,
  type CastEnvironment,
  castSupported,
  loadCastSdk,
  resetCastSdkForTests,
} from "./castSdk";

const CHROME_UA =
  "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36";
const FIREFOX_UA = "Mozilla/5.0 (X11; Linux x86_64; rv:128.0) Gecko/20100101 Firefox/128.0";
const IOS_CHROME_UA =
  "Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) CriOS/130.0 Mobile/15E148 Safari/604.1 Chrome/130.0";

function scripts(doc: Document): HTMLScriptElement[] {
  return Array.from(doc.head.querySelectorAll("script"));
}

function environment(overrides: Partial<CastEnvironment> = {}): CastEnvironment {
  const doc = document.implementation.createHTMLDocument("cast");
  return {
    isSecureContext: true,
    chrome: {},
    navigator: { userAgent: CHROME_UA },
    document: doc,
    ...overrides,
  };
}

afterEach(() => resetCastSdkForTests());

describe("castSupported", () => {
  it("accepts a secure Chromium browser", () => {
    expect(castSupported(environment())).toBe(true);
  });

  it("refuses an insecure context, a non-Chromium browser and iOS", () => {
    expect(castSupported(environment({ isSecureContext: false }))).toBe(false);
    expect(
      castSupported(environment({ chrome: undefined, navigator: { userAgent: FIREFOX_UA } })),
    ).toBe(false);
    expect(castSupported(environment({ navigator: { userAgent: IOS_CHROME_UA } }))).toBe(false);
    expect(castSupported(null)).toBe(false);
  });
});

describe("loadCastSdk", () => {
  it("adds no script and makes no request where Cast cannot work", async () => {
    const env = environment({ chrome: undefined, navigator: { userAgent: FIREFOX_UA } });
    await expect(loadCastSdk(env)).resolves.toBeNull();
    expect(scripts(env.document as Document)).toHaveLength(0);
    expect(env.__onGCastApiAvailable).toBeUndefined();
  });

  it("loads the sender script once and resolves the framework when it reports in", async () => {
    const env = environment();
    const first = loadCastSdk(env);
    const second = loadCastSdk(env);
    const added = scripts(env.document as Document);
    expect(added).toHaveLength(1);
    expect(added[0]?.src).toBe(CAST_SDK_URL);
    expect(added[0]?.async).toBe(true);

    const framework = { CastContext: {} } as unknown as NonNullable<
      CastEnvironment["cast"]
    >["framework"];
    const chromeCast = { media: {} } as unknown as NonNullable<CastEnvironment["chrome"]>["cast"];
    env.cast = { framework };
    env.chrome = { cast: chromeCast };
    env.__onGCastApiAvailable?.(true);
    await expect(first).resolves.toEqual({ framework, chromeCast });
    expect(second).toBe(first);
  });

  it("resolves null when the SDK reports no Cast support", async () => {
    const env = environment();
    const load = loadCastSdk(env);
    env.__onGCastApiAvailable?.(false);
    await expect(load).resolves.toBeNull();
  });

  it("resolves null when the script fails to load", async () => {
    const env = environment();
    const load = loadCastSdk(env);
    scripts(env.document as Document)[0]?.onerror?.(new Event("error"));
    await expect(load).resolves.toBeNull();
  });
});
