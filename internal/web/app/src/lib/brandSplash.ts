// Hands the cold-load splash (CORE-2556, brand-splash-plugin.ts) off to the first screen. The
// intro plays from the first paint without script; this waits until it has ended on the
// static lockup and the app has rendered underneath, then fades the splash out and removes it.
// Under reduced motion the intros carry no animations, so the static lockup stays only until
// the app is ready. The intro runs once per document load, so in-app navigation and a page
// restored from the back/forward cache never replay it.

export const SPLASH_ID = "bf-splash";
export const SPLASH_FADE_MS = 240;
// A hidden tab can hold animations back; never let the splash outlast this.
export const SPLASH_MAX_INTRO_MS = 4000;

function wait(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

/** Resolves once every animation in both intros has finished (at once when there are none). */
export function introFinished(splash: HTMLElement): Promise<void> {
  const animations = Array.from(splash.querySelectorAll<HTMLElement>(".bf-splash__layout")).flatMap(
    (host) => host.shadowRoot?.getAnimations?.() ?? [],
  );
  return Promise.all(animations.map((animation) => animation.finished.catch(() => undefined))).then(
    () => undefined,
  );
}

export function handOffSplash(appReady: Promise<unknown>, doc: Document = document): Promise<void> {
  const splash = doc.getElementById(SPLASH_ID);
  if (!splash) return Promise.resolve();
  const intro = Promise.race([introFinished(splash), wait(SPLASH_MAX_INTRO_MS)]);
  return Promise.all([intro, appReady.catch(() => undefined)]).then(async () => {
    splash.dataset.state = "done";
    await wait(SPLASH_FADE_MS);
    splash.remove();
  });
}
