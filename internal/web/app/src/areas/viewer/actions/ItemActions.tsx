import { Bookmark, BookmarkCheck, Circle, CircleCheck, Eye, EyeOff, Star } from "lucide-react";
import type { ItemTarget } from "@/lib/graphql/types";
import { useT } from "@/lib/i18n";
import { useItemHide } from "./hiddenItems";
import { useItemActions } from "./useItemActions";

const STARS = [1, 2, 3, 4, 5];

/**
 * The watched, watchlist and rating controls for one item, for the calling
 * Profile. Self-contained: give it a target (see itemTargetFor for a feed item)
 * and it loads the state, applies the actions and follows changes made on other
 * clients. Ratings are stored 1–10 and shown as five stars. Given a
 * `hideTitle`, it also offers Hide for the calling Profile (CORE-2836).
 */
export function ItemActions({
  target,
  isAudio = false,
  hideTitle,
  className = "",
}: {
  target: ItemTarget;
  isAudio?: boolean;
  /** The item's title, for the Hide notice; Hide is offered only when set. */
  hideTitle?: string;
  className?: string;
}) {
  const t = useT();
  const { state, pending, failed, retry, setWatched, setWatchlisted, setRating } =
    useItemActions(target);
  const disabled = pending || !state;
  const watched = state?.watched ?? false;
  const watchedLabel = isAudio
    ? t(watched ? "itemMarkUnplayed" : "itemMarkPlayed")
    : t(watched ? "itemMarkUnwatched" : "itemMarkWatched");
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
        {watchedLabel}
      </button>
      {!isAudio && (
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
      )}
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
      {hideTitle !== undefined && <HideButton target={target} title={hideTitle} />}
      {failed && (
        <span role="status" className="bf-item-actions-error">
          {t("itemActionFailed")}
        </span>
      )}
      {failed && !state && (
        <button type="button" className="bf-item-action" onClick={retry}>
          {t("tryAgain")}
        </button>
      )}
    </div>
  );
}

function HideButton({ target, title }: { target: ItemTarget; title: string }) {
  const t = useT();
  const { hidden, pending, failed, toggle } = useItemHide(target, title);
  return (
    <>
      <button
        type="button"
        className="bf-item-action"
        aria-pressed={hidden ?? false}
        disabled={pending || hidden === null}
        onClick={() => void toggle()}
      >
        {hidden ? <Eye aria-hidden="true" /> : <EyeOff aria-hidden="true" />}
        {t(hidden ? "itemUnhide" : "itemHide")}
      </button>
      {failed && (
        <span role="status" className="bf-item-actions-error">
          {t("itemActionFailed")}
        </span>
      )}
    </>
  );
}
