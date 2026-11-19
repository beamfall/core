import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { handOffSplash, SPLASH_FADE_MS, SPLASH_ID, SPLASH_MAX_INTRO_MS } from "./brandSplash";

function mountSplash(animations: Array<{ finished: Promise<unknown> }> = []): HTMLElement {
  const splash = document.createElement("div");
  splash.id = SPLASH_ID;
  const host = document.createElement("div");
  host.className = "bf-splash__layout";
  const root = host.attachShadow({ mode: "open" });
  Object.defineProperty(root, "getAnimations", { value: () => animations });
  splash.append(host);
  document.body.append(splash);
  return splash;
}

function deferred() {
  let resolve: () => void = () => {};
  const promise = new Promise<void>((r) => {
    resolve = r;
  });
  return { promise, resolve };
}

describe("handOffSplash", () => {
  beforeEach(() => vi.useFakeTimers());
  afterEach(() => {
    vi.useRealTimers();
    document.body.innerHTML = "";
  });

  it("waits for both the intro and the first screen, then fades and removes the splash", async () => {
    const intro = deferred();
    const app = deferred();
    const splash = mountSplash([{ finished: intro.promise }]);
    const done = handOffSplash(app.promise);

    app.resolve();
    await vi.advanceTimersByTimeAsync(10);
    expect(splash.dataset.state).toBeUndefined();

    intro.resolve();
    await vi.advanceTimersByTimeAsync(0);
    expect(splash.dataset.state).toBe("done");
    expect(splash.isConnected).toBe(true);

    await vi.advanceTimersByTimeAsync(SPLASH_FADE_MS);
    await done;
    expect(splash.isConnected).toBe(false);
  });

  it("hands off as soon as the app is ready when there is no animation (reduced motion)", async () => {
    const splash = mountSplash();
    const done = handOffSplash(Promise.resolve());
    await vi.advanceTimersByTimeAsync(SPLASH_FADE_MS);
    await done;
    expect(splash.isConnected).toBe(false);
  });

  it("never lets a held-back intro keep the splash past the cap", async () => {
    const splash = mountSplash([{ finished: new Promise(() => {}) }]);
    void handOffSplash(Promise.resolve());
    await vi.advanceTimersByTimeAsync(SPLASH_MAX_INTRO_MS - 1);
    expect(splash.dataset.state).toBeUndefined();
    await vi.advanceTimersByTimeAsync(1 + SPLASH_FADE_MS);
    expect(splash.isConnected).toBe(false);
  });

  it("does nothing on a page without the splash", async () => {
    await expect(handOffSplash(Promise.resolve())).resolves.toBeUndefined();
  });
});
