import { Check } from "lucide-react";
import { Link } from "react-router";
import type { SourceItem } from "@/lib/graphql/types";
import { useT } from "@/lib/i18n";
import { useViewerIdentity } from "@/lib/session";
import { artworkBucketURL } from "../lib/artwork";
import { childThumbURL, groupChildrenByKind } from "../lib/containerChildren";
import { mediaLinkState, resumeProgress } from "../lib/mediaDetail";
import { runtimeParts } from "../lib/viewerFields";

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
  return (
    <ol className="bf-children" aria-label={label}>
      {items.map((item, index) => (
        <ChildRow key={`${item.id}-${index}`} item={item} number={index + 1} />
      ))}
    </ol>
  );
}

function ChildRow({ item, number }: { item: SourceItem; number: number }) {
  const t = useT();
  const identity = useViewerIdentity();
  const thumb = childThumbURL(item);
  const duration = item.engagement?.durationSeconds;
  const runtime = duration && duration >= 60 ? runtimeParts(duration) : null;
  const progress = resumeProgress(item);
  const watched = item.engagement?.completed === true;

  return (
    <li className="bf-child">
      <Link
        className="bf-child-link"
        to={`/media/${encodeURIComponent(item.id)}`}
        state={mediaLinkState(item, identity)}
      >
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
    </li>
  );
}
