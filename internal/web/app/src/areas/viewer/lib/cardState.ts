import type { SourceItem } from "@/lib/graphql/types";
import type { useT } from "@/lib/i18n";
import { formatScore } from "./viewerFields";

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
export function cardAccessibleName(
  item: SourceItem,
  state: CardState,
  rating?: CardRating,
): string {
  return [item.title, rating?.label, state.label].filter(Boolean).join(", ");
}

/** What a card says about its content rating and audience score (CORE-2400). */
export interface CardRating {
  /** The rating text, empty when unrated. */
  text: string;
  /** The score with one decimal, empty when there is none. */
  score: string;
  /** Both in words, for the accessible name: "Rated PG-13, audience score 7.8". */
  label: string;
}

/**
 * cardRating returns undefined when the item has neither, so the card draws
 * nothing. A zero score is no score (CORE-2524): providers report 0 for a title
 * nobody has rated yet.
 */
export function cardRating(item: SourceItem, locale: string, t: Translate): CardRating | undefined {
  const text = item.certification?.trim() ?? "";
  const value = item.audienceScore;
  // A 0 is a provider's placeholder, not a score, even with votes beside it
  // (CORE-2472, CORE-2524).
  const score =
    typeof value === "number" && Number.isFinite(value) && value > 0 && value <= 10
      ? formatScore(value, locale)
      : "";
  if (!text && !score) return undefined;
  let label = t("cardAudienceScore", { score });
  if (text) {
    label = score ? t("cardRatedScore", { rating: text, score }) : t("cardRated", { rating: text });
  }
  return { text, score, label };
}

// Rounds up to whole minutes, matching the server's own "min left" subtitle.
function timeLeftLabel(seconds: number, t: Translate): string {
  const minutes = Math.max(1, Math.ceil(seconds / 60));
  if (minutes >= 60) {
    return t("cardHoursLeft", {
      hours: Math.floor(minutes / 60),
      minutes: minutes % 60,
    });
  }
  return minutes === 1 ? t("cardMinuteLeft") : t("cardMinutesLeft", { count: minutes });
}
