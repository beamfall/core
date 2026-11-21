import { useEffect, useRef } from "react";

/**
 * Runs `tick` about every `intervalMS` while the page is visible and `enabled`,
 * one at a time: the next tick waits for the previous one to settle. A hidden
 * page stops polling and polls at once when it is shown again.
 */
export function useVisiblePoll(
  tick: (signal: AbortSignal) => Promise<unknown>,
  intervalMS: number,
  enabled = true,
): void {
  const tickRef = useRef(tick);
  tickRef.current = tick;
  useEffect(() => {
    if (!enabled) return;
    const controller = new AbortController();
    let timer: number | undefined;
    let running = false;
    const schedule = () => {
      window.clearTimeout(timer);
      if (controller.signal.aborted || document.hidden) return;
      timer = window.setTimeout(run, intervalMS);
    };
    const run = () => {
      if (running || controller.signal.aborted) return;
      running = true;
      tickRef
        .current(controller.signal)
        .catch(() => {})
        .finally(() => {
          running = false;
          schedule();
        });
    };
    const onVisibility = () => {
      if (!document.hidden) run();
      else window.clearTimeout(timer);
    };
    document.addEventListener("visibilitychange", onVisibility);
    run();
    return () => {
      controller.abort();
      window.clearTimeout(timer);
      document.removeEventListener("visibilitychange", onVisibility);
    };
  }, [enabled, intervalMS]);
}
