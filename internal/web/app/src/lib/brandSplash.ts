// Hands the cold-load splash (CORE-2556, brand-splash-plugin.ts) off to the first screen. The
// intro plays from the first paint without script and covers the app only while it loads: once
// the first screen has rendered the splash fades out and is removed, cutting the intro short if
// it is still playing (CORE-2928). Under reduced motion the intros carry no animations, so the
// static lockup shows until the app is ready. The intro runs once per document load, so in-app
// navigation and a page restored from the back/forward cache never replay it.

export const SPLASH_ID = "bf-splash";
export const SPLASH_FADE_MS = 240;

function wait(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

export function handOffSplash(appReady: Promise<unknown>, doc: Document = document): Promise<void> {
  const splash = doc.getElementById(SPLASH_ID);
  if (!splash) return Promise.resolve();
  return appReady
    .catch(() => undefined)
    .then(async () => {
      splash.dataset.state = "done";
      await wait(SPLASH_FADE_MS);
      splash.remove();
    });
}
