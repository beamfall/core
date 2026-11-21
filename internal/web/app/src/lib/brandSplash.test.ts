import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { handOffSplash, SPLASH_FADE_MS, SPLASH_ID } from "./brandSplash";

function mountSplash(): HTMLElement {
  const splash = document.createElement("div");
  splash.id = SPLASH_ID;
  document.body.append(splash);
  return splash;
}

function deferred() {
  let resolve: () => void = () => {};
  let reject: (error: Error) => void = () => {};
  const promise = new Promise<void>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

describe("handOffSplash", () => {
  beforeEach(() => vi.useFakeTimers());
  afterEach(() => {
    vi.useRealTimers();
    document.body.innerHTML = "";
  });

  it("stays while the first screen loads, then fades and removes the splash", async () => {
    const app = deferred();
    const splash = mountSplash();
    const done = handOffSplash(app.promise);

    await vi.advanceTimersByTimeAsync(5_000);
    expect(splash.dataset.state).toBeUndefined();

    app.resolve();
    await vi.advanceTimersByTimeAsync(0);
    expect(splash.dataset.state).toBe("done");
    expect(splash.isConnected).toBe(true);

    await vi.advanceTimersByTimeAsync(SPLASH_FADE_MS);
    await done;
    expect(splash.isConnected).toBe(false);
  });

  it("does not wait for an intro that is still playing", async () => {
    const splash = mountSplash();
    const host = document.createElement("div");
    host.className = "bf-splash__layout";
    const root = host.attachShadow({ mode: "open" });
    Object.defineProperty(root, "getAnimations", {
      value: () => [{ finished: new Promise(() => {}) }],
    });
    splash.append(host);

    const done = handOffSplash(Promise.resolve());
    await vi.advanceTimersByTimeAsync(SPLASH_FADE_MS);
    await done;
    expect(splash.isConnected).toBe(false);
  });

  it("still hands off when the first screen fails to render", async () => {
    const app = deferred();
    const splash = mountSplash();
    const done = handOffSplash(app.promise);
    app.reject(new Error("boot failed"));
    await vi.advanceTimersByTimeAsync(SPLASH_FADE_MS);
    await done;
    expect(splash.isConnected).toBe(false);
  });

  it("does nothing on a page without the splash", async () => {
    await expect(handOffSplash(Promise.resolve())).resolves.toBeUndefined();
  });
});
