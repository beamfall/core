import { useLayoutEffect, useState } from "react";

/** What the transport draws over the stage's lower edge: its bottom block and a segment-skip button. */
const LOWER = ".bf-transport-bottom, .bf-control.is-skip-segment";
/** The floating top bar (Back, key, tempo) is the stage's sibling. */
const BAR = ":scope > .bf-player-bar";

export interface ChromeReserve {
  /** Pixels the top bar's controls take from the stage's top edge. */
  top: number;
  /** Pixels the transport takes from the stage's bottom edge. */
  bottom: number;
}

/**
 * The room the player chrome takes at the stage's top and bottom edges while
 * `active` (CORE-3373). A karaoke picture is laid out between them so the
 * controls never cover a lyric line. The last measurement is kept while the
 * chrome hides or the transport unmounts, so the picture never jumps; null
 * until the transport has been measured once, and again once `active` turns
 * off. It measures before paint, so the first frame is already laid out.
 */
export function useChromeReserve(
  stageRef: React.RefObject<HTMLElement | null>,
  active: boolean,
): ChromeReserve | null {
  const [reserve, setReserve] = useState<ChromeReserve | null>(null);

  useLayoutEffect(() => {
    const stage = stageRef.current;
    if (!active || !stage || typeof ResizeObserver === "undefined") {
      setReserve(null);
      return;
    }
    const host = stage.parentElement;
    const observed = new Set<Element>();
    const measure = () => {
      const box = stage.getBoundingClientRect();
      let lowest = Number.POSITIVE_INFINITY;
      for (const element of stage.querySelectorAll(LOWER)) {
        lowest = Math.min(lowest, element.getBoundingClientRect().top);
      }
      // Nothing to measure while the transport is away: keep the last reserve.
      if (lowest === Number.POSITIVE_INFINITY) return;
      let top = 0;
      // A fullscreen stage hides its sibling bar, so the bar takes no room.
      const bar = document.fullscreenElement === stage ? null : host?.querySelector(BAR);
      for (const control of bar?.children ?? []) {
        top = Math.max(top, control.getBoundingClientRect().bottom - box.top);
      }
      const next = { top: Math.ceil(top), bottom: Math.ceil(box.bottom - lowest) };
      if (next.bottom <= 0) return;
      setReserve((prev) =>
        prev && prev.top === next.top && prev.bottom === next.bottom ? prev : next,
      );
    };
    const resize = new ResizeObserver(measure);
    const track = () => {
      const wanted = new Set<Element>([stage, ...stage.querySelectorAll(LOWER)]);
      const bar = host?.querySelector(BAR);
      if (bar) wanted.add(bar);
      for (const element of observed) {
        if (!wanted.has(element)) {
          resize.unobserve(element);
          observed.delete(element);
        }
      }
      for (const element of wanted) {
        if (!observed.has(element)) {
          resize.observe(element);
          observed.add(element);
        }
      }
      measure();
    };
    // The transport and the bar's karaoke controls mount and unmount with playback; follow them.
    const mutations = new MutationObserver(track);
    mutations.observe(host ?? stage, { childList: true, subtree: true });
    document.addEventListener("fullscreenchange", measure);
    track();
    return () => {
      document.removeEventListener("fullscreenchange", measure);
      mutations.disconnect();
      resize.disconnect();
    };
  }, [stageRef, active]);

  return reserve;
}
