import { Check, Play } from "lucide-react";
import { Link, useNavigate } from "react-router";
import type { SourceItem } from "@/lib/graphql/types";
import { useT } from "@/lib/i18n";
import { useViewerIdentity } from "@/lib/session";
import { activateLink } from "../lib/activateLink";
import { artworkBucketURL } from "../lib/artwork";
import { childThumbURL, groupChildrenByKind } from "../lib/containerChildren";
import { mediaPath } from "../lib/mediaAddress";
import { mediaLinkState, resumeProgress } from "../lib/mediaDetail";
import {
  firstGuardedContainer,
  guardContainerOf,
  isSpoilerCandidate,
  revealItem,
  type SpoilerState,
  useSpoilerGuard,
} from "../lib/spoilerGuard";
import { runtimeParts } from "../lib/viewerFields";
import { type PlayQueueItem, playQueueOf, queueWindow } from "../lib/watchQueue";
import { preloadPlayer } from "../player/preload";
import { MediaItemCardMenu } from "./MediaItemCardMenu";
import { SpoilerContainerToggle, SpoilerRevealButton, SpoilerStill } from "./SpoilerGuard";

// A container Entity's children (CORE-1786), grouped by kind in the server's
// order. Each child's detail link comes before its actions, so keyboard focus
// follows the visual order. CSS lays them out as a grid on wide screens and as list
// rows (thumbnail, number, title, runtime, progress) on a phone. Motion is
// limited to the token hover and focus states. With a containerID, unplayed
// children keep their still behind the spoiler guard (CORE-3057).
export function ContainerChildren({
  items,
  label,
  groupLabel,
  containerID,
  containerArt,
}: {
  items: readonly SourceItem[];
  /** Accessible name of the list (one group) or the region (several). */
  label: string;
  /** A group's heading; only shown when there is more than one group. */
  groupLabel?: (kind: string) => string;
  /** The outermost container holding these children; the spoiler guard needs it. */
  containerID?: string;
  /** The container's art, drawn dimmed in place of a guarded still. */
  containerArt?: string;
}) {
  const groups = groupChildrenByKind(items);
  const guard = { containerID, containerArt };
  // One Show spoilers toggle above every group, keyed like the rows' guard.
  const guardKey = firstGuardedContainer(items, containerID);
  const spoiler = useSpoilerGuard(guardKey !== undefined);
  const toggle = guardKey && spoiler.guardOn && (
    <SpoilerContainerToggle containerID={guardKey} revealed={spoiler.containerRevealed(guardKey)} />
  );
  if (groups.length === 0) return null;
  if (groups.length === 1) {
    return (
      <>
        {toggle}
        <ChildList items={groups[0].items} label={label} {...guard} />
      </>
    );
  }
  return (
    <section className="bf-children-groups" aria-label={label}>
      {toggle}
      {groups.map((group) => {
        const heading = groupLabel?.(group.kind) || group.kind;
        return (
          <section key={group.kind} className="bf-section flex flex-col gap-3">
            <h2 className="bf-section-title">
              <span>{heading}</span>
            </h2>
            <ChildList items={group.items} label={heading} {...guard} />
          </section>
        );
      })}
    </section>
  );
}

function ChildList({
  items,
  label,
  containerID,
  containerArt,
}: {
  items: readonly SourceItem[];
  label: string;
  containerID?: string;
  containerArt?: string;
}) {
  const queue = playQueueOf(items);
  const spoiler = useSpoilerGuard(
    items.some((item) => isSpoilerCandidate(item, guardContainerOf(item, containerID))),
  );
  return (
    <ol className="bf-children" aria-label={label}>
      {items.map((item, index) => (
        <ChildRow
          key={`${item.id}-${index}`}
          item={item}
          number={index + 1}
          queue={queue}
          guard={spoiler.state(item, guardContainerOf(item, containerID))}
          containerArt={containerArt}
        />
      ))}
    </ol>
  );
}

function ChildRow({
  item,
  number,
  queue,
  guard,
  containerArt,
}: {
  item: SourceItem;
  number: number;
  queue: PlayQueueItem[];
  guard: SpoilerState;
  containerArt?: string;
}) {
  const t = useT();
  const identity = useViewerIdentity();
  const navigate = useNavigate();
  const path = mediaPath("media", item.id);
  function queueState() {
    return identity && queue.length > 0
      ? { queue: queueWindow(queue, item.id), queueIdentity: identity }
      : {};
  }
  function openDetails() {
    navigate(path, { state: { ...mediaLinkState(item, identity), ...queueState() } });
  }
  function play() {
    navigate(mediaPath("watch", item.id), {
      state: { title: item.title, ...queueState() },
    });
  }
  const thumb = childThumbURL(item);
  const duration = item.engagement?.durationSeconds;
  const runtime = duration && duration >= 60 ? runtimeParts(duration) : null;
  const progress = resumeProgress(item);
  const watched = item.engagement?.completed === true;

  return (
    <li className="bf-child">
      <Link
        className="bf-child-link"
        to={path}
        onClick={(event) => activateLink(event, openDetails)}
      >
        <span className="bf-child-thumb" aria-hidden>
          {guard === "hidden" ? (
            <SpoilerStill itemID={item.id} art={containerArt} />
          ) : (
            thumb && (
              <img
                className={guard === "none" ? undefined : "bf-spoiler-shown"}
                src={artworkBucketURL(thumb, "w320")}
                alt=""
                loading="lazy"
              />
            )
          )}
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
      {item.kind === "media" && item.locked && guard !== "none" && guard !== "container" && (
        // A locked child cannot play, but its reveal stays reachable.
        <div className="bf-child-actions">
          <SpoilerRevealButton itemID={item.id} title={item.title} state={guard} />
        </div>
      )}
      {item.kind === "media" && !item.locked && (
        <div className="bf-child-actions">
          <SpoilerRevealButton itemID={item.id} title={item.title} state={guard} />
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
            containerID={item.container?.id}
            href={
              new URL(`${import.meta.env.BASE_URL}${path.slice(1)}`, window.location.origin).href
            }
            target={{ mediaID: item.id }}
            onPrimary={play}
            onDetails={openDetails}
            onReveal={guard === "hidden" ? () => revealItem(item.id) : undefined}
          />
        </div>
      )}
    </li>
  );
}
