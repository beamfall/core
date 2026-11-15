import { useNavigate } from "react-router";
import { useT } from "@/lib/i18n";
import { useViewerIdentity } from "@/lib/session";
import { itemTargetFor } from "../actions";
import { formatDuration, playState, type TrackRow } from "../lib/trackList";
import type { PlayQueueItem } from "../lib/watchQueue";
import { MediaItemCardMenu } from "./MediaItemCardMenu";
import "./listening.css";

// A numbered list of audio Media rows (CORE-1635): number, title, the credited
// people when they differ from the container's own, and runtime. Activating a
// row plays it with the whole list as the player's queue; the row's More menu
// is the card menu in its audio wording.
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

  function play(row: TrackRow) {
    navigate(`/watch/${encodeURIComponent(row.item.id)}`, {
      state: playState(identity, queue, row.item),
    });
  }

  return (
    <ol className="bf-track-list" aria-label={label}>
      {rows.map((row) => {
        const duration = formatDuration(row.durationSeconds);
        const path = `watch/${encodeURIComponent(row.item.id)}`;
        return (
          <li key={row.item.id} className="bf-card-shell bf-track">
            <button
              type="button"
              className="bf-track-row"
              aria-label={t("trackPlay", { title: row.item.title })}
              disabled={row.item.locked}
              onClick={() => play(row)}
            >
              <span className="bf-track-number">{row.number}</span>
              <span className="bf-track-copy">
                <span className="bf-track-title">{row.item.title}</span>
                {row.artist && <span className="bf-track-artist">{row.artist}</span>}
              </span>
              <span className="bf-track-duration">{duration}</span>
            </button>
            <MediaItemCardMenu
              title={row.item.title}
              primaryLabel={t("play")}
              isMedia
              isAudio
              href={new URL(`${import.meta.env.BASE_URL}${path}`, window.location.origin).href}
              target={itemTargetFor(row.item)}
              onPrimary={() => play(row)}
            />
          </li>
        );
      })}
    </ol>
  );
}
