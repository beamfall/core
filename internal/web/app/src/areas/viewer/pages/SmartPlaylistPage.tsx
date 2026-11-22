import { Pencil, Play, Trash2 } from "lucide-react";
import { useEffect, useState } from "react";
import { useNavigate, useParams } from "react-router";
import { AppShell } from "@/components/chrome/AppShell";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Skeleton } from "@/components/ui/skeleton";
import type { SourceItem } from "@/lib/graphql/types";
import { useT } from "@/lib/i18n";
import { useSession, useViewerIdentity } from "@/lib/session";
import { useEngagementEpoch } from "../actions/engagementSync";
import { SmartPlaylistEditor } from "../components/SmartPlaylistEditor";
import { ViewerActions } from "../components/ViewerActions";
import { mediaPath } from "../lib/mediaAddress";
import {
  deleteSmartPlaylist,
  loadSmartPlaylistItems,
  loadSmartPlaylists,
  type SmartPlaylist,
} from "../lib/smartPlaylists";
import { formatDuration, playState } from "../lib/trackList";
import type { PlayQueueItem } from "../lib/watchQueue";
import "../components/listening.css";

// One smart playlist (CORE-2771): the items its rules match right now for the
// calling Profile, in its sort order, with Play all and per-row play. The
// server evaluates the rules on every read, so the list follows library and
// play-history changes on its own. A locked item (certification ceiling) is
// listed but cannot play and stays out of the queue. A manager device can edit
// the rules or delete the playlist.
export function SmartPlaylistPage() {
  const t = useT();
  const navigate = useNavigate();
  const { smartId = "" } = useParams();
  const { manifest, isManager } = useSession();
  const identity = useViewerIdentity();
  const libraryKey = (manifest?.libraries ?? []).map((library) => library.id).join("\n");
  const [playlist, setPlaylist] = useState<SmartPlaylist | null | undefined>(undefined);
  const [items, setItems] = useState<SourceItem[] | null>(null);
  const [editing, setEditing] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  // A play or rating anywhere can change which items match, so the list
  // rereads on each engagement change.
  const engagementEpoch = useEngagementEpoch();

  useEffect(() => {
    let alive = true;
    loadSmartPlaylists(libraryKey ? libraryKey.split("\n") : [])
      .then((list) => {
        if (alive) setPlaylist(list.find((entry) => entry.id === smartId) ?? null);
      })
      .catch(() => {
        if (alive) setPlaylist(null);
      });
    return () => {
      alive = false;
    };
  }, [libraryKey, smartId]);

  useEffect(() => {
    if (!playlist) return;
    void engagementEpoch;
    const controller = new AbortController();
    loadSmartPlaylistItems(playlist, controller.signal)
      .then(setItems)
      .catch(() => {
        if (!controller.signal.aborted) setItems([]);
      });
    return () => controller.abort();
  }, [playlist, engagementEpoch]);

  const listed: PlayQueueItem[] = (items ?? []).map((item) => {
    const entry: PlayQueueItem = { id: item.id, title: item.title };
    if (item.mediaClass) entry.mediaClass = item.mediaClass;
    // Plays-alone Media never queues on into a sibling (CORE-3147).
    if (item.playsAlone) entry.playsAlone = true;
    return entry;
  });
  const lockedIDs = new Set((items ?? []).filter((item) => item.locked).map((item) => item.id));
  const queue = listed.filter((entry) => !lockedIDs.has(entry.id));

  function play(entry: PlayQueueItem) {
    const inQueue = queue.some((item) => item.id === entry.id);
    navigate(mediaPath("watch", entry.id), {
      state: playState(identity, inQueue ? queue : [], entry),
    });
  }

  async function onDelete() {
    if (!playlist) return;
    setBusy(true);
    setError("");
    try {
      await deleteSmartPlaylist(playlist.id);
      navigate("/playlists");
    } catch {
      setError(t("playlistActionFailed"));
      setDeleting(false);
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
            <p className="flex flex-wrap gap-3 text-sm text-muted-foreground">
              {items !== null && (
                <span>{t("smartPlaylistItemCount", { count: items.length })}</span>
              )}
              <span>{t("smartPlaylistLive")}</span>
            </p>
            <div className="bf-list-toolbar">
              {queue.length > 0 && (
                <Button onClick={() => play(queue[0])}>
                  <Play aria-hidden size={16} />
                  {t("playlistPlayAll")}
                </Button>
              )}
              {isManager && (
                <>
                  <Button variant="outline" onClick={() => setEditing(true)}>
                    <Pencil aria-hidden size={16} />
                    {t("smartPlaylistEdit")}
                  </Button>
                  <Button variant="danger" onClick={() => setDeleting(true)}>
                    <Trash2 aria-hidden size={16} />
                    {t("smartPlaylistDelete")}
                  </Button>
                </>
              )}
            </div>
          </header>
          {error && (
            <p role="alert" className="text-sm text-[var(--state-danger)]">
              {error}
            </p>
          )}
          {items === null && <Skeleton aria-hidden className="bf-shimmer h-24 w-full" />}
          {items?.length === 0 && (
            <p className="text-sm text-muted-foreground">{t("smartPlaylistEmpty")}</p>
          )}
          {items && items.length > 0 && (
            <ol className="bf-track-list" aria-label={playlist.name}>
              {listed.map((entry, index) => (
                <li key={entry.id} className="bf-track">
                  <button
                    type="button"
                    className="bf-track-row"
                    aria-label={t("trackPlay", { title: entry.title })}
                    disabled={lockedIDs.has(entry.id)}
                    onClick={() => play(entry)}
                  >
                    <span className="bf-track-number">{index + 1}</span>
                    <span className="bf-track-copy">
                      <span className="bf-track-title">{entry.title}</span>
                    </span>
                    <span className="bf-track-duration">
                      {formatDuration(items[index]?.durationSeconds)}
                    </span>
                  </button>
                </li>
              ))}
            </ol>
          )}
        </div>
      )}
      {editing && playlist && (
        <SmartPlaylistEditor
          playlist={playlist}
          onClose={() => setEditing(false)}
          onSaved={(saved) => {
            setEditing(false);
            if (saved) setPlaylist(saved);
          }}
        />
      )}
      <Dialog
        open={deleting}
        onOpenChange={(open) => {
          if (!open) setDeleting(false);
        }}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t("smartPlaylistDelete")}</DialogTitle>
            <DialogDescription>
              {t("smartPlaylistDeleteConfirm", { name: playlist?.name ?? "" })}
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button variant="outline" onClick={() => setDeleting(false)}>
              {t("playlistCancel")}
            </Button>
            <Button variant="destructive" disabled={busy} onClick={() => void onDelete()}>
              {t("smartPlaylistDelete")}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </AppShell>
  );
}
