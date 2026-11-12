import type { SourceItem } from "@/lib/graphql/types";
import type { useT } from "@/lib/i18n";

type Translate = ReturnType<typeof useT>;

/**
 * What a card shows about the acting Profile's playback state, derived once so
 * the artwork overlay and the accessible name can never disagree.
 */
export interface CardState {
  /** 0–100 while the item is resumable, otherwise undefined. */
  progressPercent?: number;
  finished: boolean;
  /** Unfinished ordered children of a container, when it has any. */
  remainingCount?: number;
  /** The state in words, for the accessible name; empty when there is none. */
  label: string;
}

export function cardState(item: SourceItem, t: Translate): CardState {
  const state = item.engagement;
  if (!state) {
    return { finished: false, label: "" };
  }
  if (state.completed) {
    return { finished: true, label: t("cardFinished") };
  }
  if (state.remainingCount && state.remainingCount > 0) {
    return {
      finished: false,
      remainingCount: state.remainingCount,
      label: t("cardUnplayed", { count: state.remainingCount }),
    };
  }
  const position = state.positionSeconds ?? 0;
  const duration = state.durationSeconds ?? 0;
  if (position > 0 && duration > position) {
    return {
      finished: false,
      progressPercent: Math.min(100, Math.max(1, Math.round((position / duration) * 100))),
      label: timeLeftLabel(duration - position, t),
    };
  }
  return { finished: false, label: "" };
}

/**
 * The card's accessible name: its title, then its state where known. The wire
 * carries no display type for a SourceItem, so none is announced.
 */
export function cardAccessibleName(item: SourceItem, state: CardState): string {
  return state.label ? `${item.title}, ${state.label}` : item.title;
}

// Rounds up to whole minutes, matching the server's own "min left" subtitle.
function timeLeftLabel(seconds: number, t: Translate): string {
  const minutes = Math.max(1, Math.ceil(seconds / 60));
  if (minutes >= 60) {
    return t("cardHoursLeft", { hours: Math.floor(minutes / 60), minutes: minutes % 60 });
  }
  return minutes === 1 ? t("cardMinuteLeft") : t("cardMinutesLeft", { count: minutes });
}
