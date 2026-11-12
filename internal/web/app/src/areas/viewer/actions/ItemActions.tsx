import { Bookmark, BookmarkCheck, Circle, CircleCheck, Star } from "lucide-react";
import type { ItemTarget } from "@/lib/graphql/types";
import { useT } from "@/lib/i18n";
import { useItemActions } from "./useItemActions";

const STARS = [1, 2, 3, 4, 5];

/**
 * The watched, watchlist and rating controls for one item, for the calling
 * Profile. Self-contained: give it a target (see itemTargetFor for a feed item)
 * and it loads the state, applies the actions and follows changes made on other
 * clients. Ratings are stored 1–10 and shown as five stars.
 */
export function ItemActions({
  target,
  className = "",
}: {
  target: ItemTarget;
  className?: string;
}) {
  const t = useT();
  const { state, pending, failed, setWatched, setWatchlisted, setRating } = useItemActions(target);
  const disabled = pending || !state;
  const watched = state?.watched ?? false;
  const watchlisted = state?.watchlisted ?? false;
  const stars = Math.ceil((state?.rating ?? 0) / 2);

  return (
    <div className={`bf-item-actions ${className}`.trim()} aria-busy={pending}>
      <button
        type="button"
        className="bf-item-action"
        aria-pressed={watched}
        disabled={disabled}
        onClick={() => void setWatched(!watched)}
      >
        {watched ? <CircleCheck aria-hidden="true" /> : <Circle aria-hidden="true" />}
        {watched ? t("itemMarkUnwatched") : t("itemMarkWatched")}
      </button>
      <button
        type="button"
        className="bf-item-action"
        aria-pressed={watchlisted}
        disabled={disabled}
        onClick={() => void setWatchlisted(!watchlisted)}
      >
        {watchlisted ? <BookmarkCheck aria-hidden="true" /> : <Bookmark aria-hidden="true" />}
        {watchlisted ? t("itemWatchlistRemove") : t("itemWatchlistAdd")}
      </button>
      <fieldset className="bf-item-rating" disabled={disabled}>
        <legend className="sr-only">{t("itemYourRating")}</legend>
        {STARS.map((star) => (
          <button
            key={star}
            type="button"
            className="bf-item-star"
            aria-pressed={star <= stars}
            aria-label={
              star === stars ? t("itemRatingClear") : t("itemRate", { stars: String(star) })
            }
            // Choosing the current star again clears the rating.
            onClick={() => void setRating(star === stars ? 0 : star * 2)}
          >
            <Star aria-hidden="true" fill={star <= stars ? "currentColor" : "none"} />
          </button>
        ))}
      </fieldset>
      {failed && (
        <span role="status" className="bf-item-actions-error">
          {t("itemActionFailed")}
        </span>
      )}
    </div>
  );
}
