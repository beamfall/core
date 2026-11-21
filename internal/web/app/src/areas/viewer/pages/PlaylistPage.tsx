import { ArrowDown, ArrowUp, Play, X } from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { useNavigate, useParams } from "react-router";
import { AppShell } from "@/components/chrome/AppShell";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { fetchMediaRows } from "@/lib/graphql/browse";
import type { CollectionInfo, GenericMediaInfo } from "@/lib/graphql/types";
import { useT } from "@/lib/i18n";
import { useSession, useViewerIdentity } from "@/lib/session";
import { MediaItemCardMenu } from "../components/MediaItemCardMenu";
import { ViewerActions } from "../components/ViewerActions";
import { fetchEntryLocks } from "../lib/collections";
import { mediaPath } from "../lib/mediaAddress";
import {
  loadPlaylists,
  memberOrder,
  moveInPlaylist,
  removeMediaFromPlaylist,
} from "../lib/playlists";
import { formatDuration, playState } from "../lib/trackList";
import type { PlayQueueItem } from "../lib/watchQueue";
import "../components/listening.css";

// One playlist (CORE-1635): its members in stored order, Play all, and on a
// manager device move up/down and remove. Rows play with the playlist as the
// player's queue. Member titles come from each Media's own read, so a member
// the acting Profile cannot see is simply not listed. A member locked by the
// Profile's certification ceiling is listed but cannot play, and Play all
// leaves it out of the queue (CORE-1876). A muted member has no lock state (the
// feed drops it), so it stays out of the queue too and plays only on its own,
// where the server's ceiling check still applies.
export function PlaylistPage() {
  const t = useT();
  const navigate = useNavigate();
  const { playlistId = "" } = useParams();
  const { manifest, isManager } = useSession();
  const identity = useViewerIdentity();
  const libraryKey = (manifest?.libraries ?? []).map((library) => library.id).join("\n");
  const [playlist, setPlaylist] = useState<CollectionInfo | null | undefined>(undefined);
  const [rows, setRows] = useState<Record<string, GenericMediaInfo>>({});
  // Each entry's lock state, "unknown" when the read failed, null while loading.
  const [locks, setLocks] = useState<Map<string, boolean> | "unknown" | null>(null);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    let alive = true;
    loadPlaylists(libraryKey ? libraryKey.split("\n") : [])
      .then((list) => {
        if (alive) setPlaylist(list.find((entry) => entry.id === playlistId) ?? null);
      })
      .catch(() => {
        if (alive) setPlaylist(null);
      });
    return () => {
      alive = false;
    };
  }, [libraryKey, playlistId]);

  const order = useMemo(() => (playlist ? memberOrder(playlist) : []), [playlist]);
  const orderKey = order.join("\n");

  useEffect(() => {
    if (!orderKey) return;
    const controller = new AbortController();
    fetchMediaRows(orderKey.split("\n"), controller.signal)
      .then((next) => setRows((prev) => ({ ...prev, ...next })))
      .catch(() => {});
    return () => controller.abort();
  }, [orderKey]);

  // Lock state is per Profile, so it reloads when the playlist or its members
  // change. Nothing plays until it is known; if the read fails, playback still
  // meets the server's own ceiling check.
  useEffect(() => {
    setLocks(null);
    if (!orderKey) return;
    const controller = new AbortController();
    fetchEntryLocks(playlistId, orderKey.split("\n").length, controller.signal)
      .then(setLocks)
      .catch(() => {
        if (!controller.signal.aborted) setLocks("unknown");
      });
    return () => controller.abort();
  }, [playlistId, orderKey]);

  const listed: PlayQueueItem[] = order
    .filter((id) => rows[id])
    .map((id) => {
      const row = rows[id];
      return row.mediaClass
        ? { id, title: row.title, mediaClass: row.mediaClass }
        : { id, title: row.title };
    });
  const lockOf = (id: string): boolean | undefined =>
    locks === "unknown" ? false : locks?.get(id);
  const queue = locks ? listed.filter((entry) => lockOf(entry.id) === false) : [];

  function play(entry: PlayQueueItem) {
    const inQueue = queue.some((item) => item.id === entry.id);
    navigate(mediaPath("watch", entry.id), {
      state: playState(identity, inQueue ? queue : [], entry),
    });
  }

  async function change(action: () => Promise<CollectionInfo>) {
    setBusy(true);
    setError("");
    try {
      setPlaylist(await action());
    } catch {
      setError(t("playlistActionFailed"));
    } finally {
      setBusy(false);
    }
  }

  return (
    <AppShell actions={<ViewerActions />}>
      {playlist === undefined && <Skeleton aria-hidden className="bf-shimmer h-24 w-full" />}
      {playlist === null && <p className="text-sm">{t("playlistNotFound")}</p>}
      {playlist && (
        <div className="flex flex-col gap-6">
          <header className="flex flex-col gap-3">
            <h1 className="bf-page-title">{playlist.name}</h1>
            <p className="text-sm text-muted-foreground">
              {t("playlistTrackCount", { count: playlist.memberCount })}
            </p>
            {queue.length > 0 && (
              <Button className="self-start" onClick={() => play(queue[0])}>
                <Play aria-hidden size={16} />
                {t("playlistPlayAll")}
              </Button>
            )}
          </header>
          {error && (
            <p role="alert" className="text-sm text-[var(--state-danger)]">
              {error}
            </p>
          )}
          {order.length === 0 && (
            <p className="text-sm text-muted-foreground">{t("playlistEmpty")}</p>
          )}
          {listed.length > 0 && (
            <ol className="bf-track-list" aria-label={playlist.name}>
              {listed.map((entry, index) => {
                const position = order.indexOf(entry.id);
                const path = mediaPath("media", entry.id);
                const musicMenu =
                  entry.mediaClass === "audio" &&
                  locks !== "unknown" &&
                  locks?.get(entry.id) === false;
                return (
                  <li key={entry.id} className="bf-track">
                    <button
                      type="button"
                      className="bf-track-row"
                      aria-label={t("trackPlay", { title: entry.title })}
                      disabled={locks === null || lockOf(entry.id) === true}
                      onClick={() => play(entry)}
                    >
                      <span className="bf-track-number">{index + 1}</span>
                      <span className="bf-track-copy">
                        <span className="bf-track-title">{entry.title}</span>
                      </span>
                      <span className="bf-track-duration">
                        {formatDuration(rows[entry.id]?.durationSeconds)}
                      </span>
                    </button>
                    {(isManager || musicMenu) && (
                      <div className="bf-track-tools">
                        {musicMenu && (
                          <MediaItemCardMenu
                            title={entry.title}
                            primaryLabel={t("play")}
                            isMedia
                            isAudio
                            href={
                              new URL(
                                `${import.meta.env.BASE_URL}${path.slice(1)}`,
                                window.location.origin,
                              ).href
                            }
                            target={{ mediaID: entry.id }}
                            onPrimary={() => play(entry)}
                            onDetails={() => navigate(path)}
                          />
                        )}
                        {isManager && (
                          <>
                            <Button
                              variant="ghost"
                              size="icon-sm"
                              aria-label={t("playlistMoveUp", { title: entry.title })}
                              disabled={busy || position === 0}
                              onClick={() =>
                                void change(() => moveInPlaylist(playlist, position, -1))
                              }
                            >
                              <ArrowUp aria-hidden size={16} />
                            </Button>
                            <Button
                              variant="ghost"
                              size="icon-sm"
                              aria-label={t("playlistMoveDown", { title: entry.title })}
                              disabled={busy || position === order.length - 1}
                              onClick={() =>
                                void change(() => moveInPlaylist(playlist, position, 1))
                              }
                            >
                              <ArrowDown aria-hidden size={16} />
                            </Button>
                            <Button
                              variant="ghost"
                              size="icon-sm"
                              aria-label={t("playlistRemoveTrack", { title: entry.title })}
                              disabled={busy}
                              onClick={() =>
                                void change(() => removeMediaFromPlaylist(playlist.id, entry.id))
                              }
                            >
                              <X aria-hidden size={16} />
                            </Button>
                          </>
                        )}
                      </div>
                    )}
                  </li>
                );
              })}
            </ol>
          )}
        </div>
      )}
    </AppShell>
  );
}
