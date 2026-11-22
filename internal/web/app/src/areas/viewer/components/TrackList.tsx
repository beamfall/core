import { Play } from "lucide-react";
import type { ReactNode } from "react";
import { Link, useNavigate } from "react-router";
import { useT } from "@/lib/i18n";
import { useViewerIdentity } from "@/lib/session";
import { itemTargetFor } from "../actions";
import { activateLink } from "../lib/activateLink";
import { mediaPath } from "../lib/mediaAddress";
import { mediaLinkState } from "../lib/mediaDetail";
import {
  audioQueue,
  formatDuration,
  playState,
  type TrackRow,
  trackGroups,
} from "../lib/trackList";
import type { PlayQueueItem } from "../lib/watchQueue";
import { preloadPlayer } from "../player/preload";
import { MediaItemCardMenu } from "./MediaItemCardMenu";
import "./listening.css";

// An audio container's numbered list (CORE-1635), one TrackList per stored
// sequence group under its heading, with one queue in display order so
// playback runs on across groups. The Entity page lists it under "Tracks"; a
// Media detail page lists its container's under the container's title and
// marks its own row (CORE-3065).
export function ContainerTracks({
  tracks,
  title,
  currentID,
}: {
  tracks: readonly TrackRow[];
  /** The section heading; defaults to the list label. */
  title?: ReactNode;
  /** The Media whose page this is, marked as the current row. */
  currentID?: string;
}) {
  const t = useT();
  const queue = audioQueue(tracks.map((track) => track.item));
  return (
    <section className="bf-section flex flex-col gap-3">
      <h2 className="bf-section-title">
        <span>{title ?? t("trackListLabel")}</span>
      </h2>
      {trackGroups(tracks).map(({ group, rows: groupRows }) => {
        if (group === undefined)
          return (
            <TrackList
              key="all"
              rows={groupRows}
              queue={queue}
              label={t("trackListLabel")}
              currentID={currentID}
            />
          );
        const heading = t("trackGroupLabel", { number: group });
        return (
          <div key={group} className="bf-track-group">
            <h3 className="bf-track-group-title">{heading}</h3>
            <TrackList rows={groupRows} queue={queue} label={heading} currentID={currentID} />
          </div>
        );
      })}
    </section>
  );
}

// A numbered list of audio Media rows (CORE-1635): number, title, the credited
// people when they differ from the container's own, and runtime. The row links
// to Media detail, and its Play button starts playback with the full queue.
// The row for `currentID` is marked as the current page (CORE-3065).
export function TrackList({
  rows,
  queue,
  label,
  currentID,
}: {
  rows: readonly TrackRow[];
  /** The list's playable rows, in order, handed to the player. */
  queue: readonly PlayQueueItem[];
  /** Accessible name of the list. */
  label: string;
  /** The Media whose page this is, marked as the current row. */
  currentID?: string;
}) {
  const t = useT();
  const navigate = useNavigate();
  const identity = useViewerIdentity();

  return (
    <ol className="bf-track-list" aria-label={label}>
      {rows.map((row) => {
        const { item } = row;
        const duration = formatDuration(row.durationSeconds);
        const path = mediaPath("media", item.id);
        const openDetails = () =>
          navigate(path, {
            state: { ...mediaLinkState(item, identity), ...playState(identity, queue, item) },
          });
        const play = () =>
          navigate(mediaPath("watch", item.id), { state: playState(identity, queue, item) });
        return (
          <li
            key={item.id}
            className="bf-card-shell bf-track"
            data-current={item.id === currentID ? "" : undefined}
          >
            <Link
              className="bf-track-row"
              to={path}
              aria-current={item.id === currentID ? "page" : undefined}
              onClick={(event) => activateLink(event, openDetails)}
            >
              <span className="bf-track-number">{row.number}</span>
              <span className="bf-track-copy">
                <span className="bf-track-title">{item.title}</span>
                {row.artist && <span className="bf-track-artist">{row.artist}</span>}
              </span>
              <span className="bf-track-duration">{duration}</span>
            </Link>
            {!item.locked && (
              <div className="bf-child-actions">
                <button
                  type="button"
                  className="bf-row-play"
                  aria-label={t("trackPlay", { title: item.title })}
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
                  isAudio
                  credits={row.credits}
                  containerID={item.container?.id}
                  href={
                    new URL(`${import.meta.env.BASE_URL}${path.slice(1)}`, window.location.origin)
                      .href
                  }
                  target={itemTargetFor(item)}
                  onPrimary={play}
                  onDetails={openDetails}
                />
              </div>
            )}
          </li>
        );
      })}
    </ol>
  );
}
