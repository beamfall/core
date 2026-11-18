import { Check, Play } from "lucide-react";
import { useMemo } from "react";
import { Link, useNavigate } from "react-router";
import type { SourceItem } from "@/lib/graphql/types";
import { useDisplayLocale, useT } from "@/lib/i18n";
import { useViewerIdentity } from "@/lib/session";
import { artworkBucketURL } from "../lib/artwork";
import { childThumbURL } from "../lib/containerChildren";
import { itemRuntimeSeconds, rowOrdinals } from "../lib/containerPage";
import { mediaLinkState, resumeProgress } from "../lib/mediaDetail";
import { runtimeParts } from "../lib/viewerFields";
import { type PlayerNavState, playQueueOf, queueWindow } from "../lib/watchQueue";
import { preloadPlayer } from "../player/preload";

// A container's own video Media as detail rows (CORE-2510): the still, the
// ordinal its siblings do not share with the runtime, the title once and a
// two-line summary. The row is a link to the item's detail page; a separate
// button beside it plays the item with the container's Media as the queue.
// Values the item lacks are left out.
export function ContainerMediaRows({
  items,
  label,
}: {
  items: readonly SourceItem[];
  /** Accessible name of the list. */
  label: string;
}) {
  const t = useT();
  const locale = useDisplayLocale();
  const identity = useViewerIdentity();
  const navigate = useNavigate();
  const media = useMemo(() => items.filter((item) => item.kind === "media"), [items]);
  const ordinals = useMemo(() => rowOrdinals(media, locale), [media, locale]);
  const queue = useMemo(() => playQueueOf(media), [media]);
  if (media.length === 0) return null;

  function play(item: SourceItem) {
    const state: PlayerNavState = { title: item.title };
    if (identity && queue.length > 0) {
      state.queue = queueWindow(queue, item.id);
      state.queueIdentity = identity;
    }
    navigate(`/watch/${encodeURIComponent(item.id)}`, { state });
  }

  // The detail page forwards the container's queue to its own Play.
  function linkState(item: SourceItem) {
    const link = mediaLinkState(item, identity);
    if (!link || queue.length === 0) return link;
    return { ...link, queue: queueWindow(queue, item.id), queueIdentity: identity };
  }

  return (
    <ol className="bf-rows" aria-label={label}>
      {media.map((item, index) => {
        const thumb = childThumbURL(item);
        const seconds = itemRuntimeSeconds(item);
        const runtime = seconds ? runtimeParts(seconds) : null;
        const ordinal = ordinals.get(item.id) ?? "";
        const progress = resumeProgress(item);
        const watched = item.engagement?.completed === true;
        const summary = item.summary?.trim() ?? "";
        return (
          <li key={`${item.id}-${index}`} className="bf-row">
            <Link
              className="bf-row-link"
              to={`/media/${encodeURIComponent(item.id)}`}
              state={linkState(item)}
            >
              <span className="bf-row-thumb" aria-hidden>
                {thumb && <img src={artworkBucketURL(thumb, "w320")} alt="" loading="lazy" />}
                {progress && (
                  <span
                    className="bf-child-progress bf-row-progress"
                    role="progressbar"
                    aria-valuemin={0}
                    aria-valuemax={100}
                    aria-valuenow={progress.percent}
                    aria-valuetext={t("mediaDetailProgress", { percent: progress.percent })}
                  >
                    <span style={{ width: `${progress.percent}%` }} />
                  </span>
                )}
              </span>
              <span className="bf-row-text">
                <span className="bf-row-title">{item.title}</span>
                {(ordinal || runtime || watched) && (
                  <span className="bf-child-meta">
                    {[
                      ordinal,
                      runtime &&
                        (runtime.hours > 0
                          ? t("mediaDetailRuntimeHours", runtime)
                          : t("mediaDetailRuntimeMinutes", { minutes: runtime.minutes })),
                    ]
                      .filter(Boolean)
                      .join(" · ")}
                    {watched && (
                      <span className="bf-child-watched">
                        <Check aria-hidden />
                        {t("entityDetailWatched")}
                      </span>
                    )}
                  </span>
                )}
                {progress && (
                  <span className="sr-only">
                    {t("mediaDetailProgress", { percent: progress.percent })}
                  </span>
                )}
                {summary && <span className="bf-row-summary">{summary}</span>}
              </span>
            </Link>
            {!item.locked && (
              <button
                type="button"
                className="bf-row-play"
                aria-label={t("mediaDetailPlayLabel", { title: item.title })}
                onClick={() => play(item)}
                onPointerEnter={preloadPlayer}
                onFocus={preloadPlayer}
              >
                <Play aria-hidden />
              </button>
            )}
          </li>
        );
      })}
    </ol>
  );
}
