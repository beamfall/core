import { Play } from "lucide-react";
import { Link, useNavigate } from "react-router";
import { useT } from "@/lib/i18n";
import { useViewerIdentity } from "@/lib/session";
import { itemTargetFor } from "../actions";
import { activateLink } from "../lib/activateLink";
import { mediaPath } from "../lib/mediaAddress";
import { mediaLinkState } from "../lib/mediaDetail";
import { formatDuration, playState, type TrackRow } from "../lib/trackList";
import type { PlayQueueItem } from "../lib/watchQueue";
import { preloadPlayer } from "../player/preload";
import { MediaItemCardMenu } from "./MediaItemCardMenu";
import "./listening.css";

// A numbered list of audio Media rows (CORE-1635): number, title, the credited
// people when they differ from the container's own, and runtime. The row links
// to Media detail, and its Play button starts playback with the full queue.
export function TrackList({
  rows,
  queue,
  label,
}: {
  rows: readonly TrackRow[];
  /** The list's playable rows, in order, handed to the player. */
  queue: readonly PlayQueueItem[];
  /** Accessible name of the list. */
  label: string;
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
          <li key={item.id} className="bf-card-shell bf-track">
            <Link
              className="bf-track-row"
              to={path}
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
                  queueUnlocked={!item.locked}
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
