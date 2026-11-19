import { Check, Play } from "lucide-react";
import { Link, useNavigate } from "react-router";
import type { SourceItem } from "@/lib/graphql/types";
import { useT } from "@/lib/i18n";
import { useViewerIdentity } from "@/lib/session";
import { artworkBucketURL } from "../lib/artwork";
import { childThumbURL, groupChildrenByKind } from "../lib/containerChildren";
import { mediaLinkState, resumeProgress } from "../lib/mediaDetail";
import { runtimeParts } from "../lib/viewerFields";
import { type PlayQueueItem, playQueueOf, queueWindow } from "../lib/watchQueue";
import { preloadPlayer } from "../player/preload";
import { MediaItemCardMenu } from "./MediaItemCardMenu";

// A container Entity's children (CORE-1786), grouped by kind in the server's
// order. Each child is one link in document order, so keyboard focus follows
// the visual order; CSS lays them out as a grid on wide screens and as list
// rows (thumbnail, number, title, runtime, progress) on a phone. Motion is
// limited to the token hover and focus states.
export function ContainerChildren({
  items,
  label,
  groupLabel,
}: {
  items: readonly SourceItem[];
  /** Accessible name of the list (one group) or the region (several). */
  label: string;
  /** A group's heading; only shown when there is more than one group. */
  groupLabel?: (kind: string) => string;
}) {
  const groups = groupChildrenByKind(items);
  if (groups.length === 0) return null;
  if (groups.length === 1) return <ChildList items={groups[0].items} label={label} />;
  return (
    <section className="bf-children-groups" aria-label={label}>
      {groups.map((group) => {
        const heading = groupLabel?.(group.kind) || group.kind;
        return (
          <section key={group.kind} className="bf-section flex flex-col gap-3">
            <h2 className="bf-section-title">
              <span>{heading}</span>
            </h2>
            <ChildList items={group.items} label={heading} />
          </section>
        );
      })}
    </section>
  );
}

function ChildList({ items, label }: { items: readonly SourceItem[]; label: string }) {
  const queue = playQueueOf(items);
  return (
    <ol className="bf-children" aria-label={label}>
      {items.map((item, index) => (
        <ChildRow key={`${item.id}-${index}`} item={item} number={index + 1} queue={queue} />
      ))}
    </ol>
  );
}

function ChildRow({
  item,
  number,
  queue,
}: {
  item: SourceItem;
  number: number;
  queue: PlayQueueItem[];
}) {
  const t = useT();
  const identity = useViewerIdentity();
  const navigate = useNavigate();
  const path = `/media/${encodeURIComponent(item.id)}`;
  const queueState =
    identity && queue.length > 0
      ? { queue: queueWindow(queue, item.id), queueIdentity: identity }
      : {};
  const linkState = { ...mediaLinkState(item, identity), ...queueState };
  function play() {
    navigate(`/watch/${encodeURIComponent(item.id)}`, {
      state: { title: item.title, ...queueState },
    });
  }
  const thumb = childThumbURL(item);
  const duration = item.engagement?.durationSeconds;
  const runtime = duration && duration >= 60 ? runtimeParts(duration) : null;
  const progress = resumeProgress(item);
  const watched = item.engagement?.completed === true;

  return (
    <li className="bf-child">
      <Link className="bf-child-link" to={path} state={linkState}>
        <span className="bf-child-thumb" aria-hidden>
          {thumb && <img src={artworkBucketURL(thumb, "w320")} alt="" loading="lazy" />}
        </span>
        <span className="bf-child-text">
          <span className="bf-child-title">
            <span className="bf-child-number">{number}</span>
            <span>{item.title}</span>
          </span>
          {(runtime || watched) && (
            <span className="bf-child-meta">
              {runtime &&
                (runtime.hours > 0
                  ? t("mediaDetailRuntimeHours", runtime)
                  : t("mediaDetailRuntimeMinutes", { minutes: runtime.minutes }))}
              {watched && (
                <span className="bf-child-watched">
                  <Check aria-hidden />
                  {t("entityDetailWatched")}
                </span>
              )}
            </span>
          )}
          {progress && (
            <span
              className="bf-child-progress"
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
      </Link>
      {item.kind === "media" && !item.locked && (
        <div className="bf-child-actions">
          <button
            type="button"
            className="bf-row-play"
            aria-label={t("mediaDetailPlayLabel", { title: item.title })}
            onClick={play}
            onPointerEnter={preloadPlayer}
            onFocus={preloadPlayer}
          >
            <Play aria-hidden />
          </button>
          <MediaItemCardMenu
            title={item.title}
            primaryLabel={t("play")}
            isMedia
            isAudio={item.mediaClass === "audio"}
            href={
              new URL(`${import.meta.env.BASE_URL}${path.slice(1)}`, window.location.origin).href
            }
            target={{ mediaID: item.id }}
            onPrimary={play}
            onDetails={() => navigate(path, { state: linkState })}
          />
        </div>
      )}
    </li>
  );
}
