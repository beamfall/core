import { useEffect, useState } from "react";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import type { CollectionInfo } from "@/lib/graphql/types";
import { useT } from "@/lib/i18n";
import { useSession } from "@/lib/session";
import { addMediaToPlaylist, loadPlaylists } from "../lib/playlists";

// "Add to playlist" from any Media card menu (CORE-1635). Playlists are the
// server's Collections, one list per Library; it mounts only when chosen, so a
// grid of cards loads nothing until then. Changing a playlist needs a manager
// device (the server gates the Collection mutations), so other devices see why.
export function AddToPlaylistDialog({
  mediaID,
  title,
  onClose,
}: {
  mediaID: string;
  title: string;
  onClose: () => void;
}) {
  const t = useT();
  const { manifest, isManager } = useSession();
  const [playlists, setPlaylists] = useState<CollectionInfo[] | null>(null);
  const [status, setStatus] = useState("");
  const [busy, setBusy] = useState(false);
  const libraryIDs = (manifest?.libraries ?? []).map((library) => library.id).join("\n");

  useEffect(() => {
    let alive = true;
    loadPlaylists(libraryIDs ? libraryIDs.split("\n") : [])
      .then((list) => {
        if (alive) setPlaylists(list);
      })
      .catch(() => {
        if (alive) setPlaylists([]);
      });
    return () => {
      alive = false;
    };
  }, [libraryIDs]);

  async function add(playlist: CollectionInfo) {
    setBusy(true);
    setStatus("");
    try {
      await addMediaToPlaylist(playlist.id, mediaID);
      setStatus(t("addToPlaylistAdded", { name: playlist.name }));
    } catch {
      setStatus(t("addToPlaylistFailed"));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Dialog
      open
      onOpenChange={(next) => {
        if (!next) onClose();
      }}
    >
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t("addToPlaylistTitle", { title })}</DialogTitle>
          {!isManager && <DialogDescription>{t("playlistManagerOnly")}</DialogDescription>}
        </DialogHeader>
        {playlists && playlists.length === 0 && (
          <p className="m-0 text-sm text-muted-foreground">{t("addToPlaylistEmpty")}</p>
        )}
        {playlists && playlists.length > 0 && (
          <ul className="m-0 flex list-none flex-col gap-1 p-0">
            {playlists.map((playlist) => (
              <li key={playlist.id}>
                <Button
                  variant="ghost"
                  className="w-full justify-between"
                  disabled={busy || !isManager}
                  onClick={() => void add(playlist)}
                >
                  <span>{playlist.name}</span>
                  <span className="text-muted-foreground">
                    {t("playlistTrackCount", { count: playlist.memberCount })}
                  </span>
                </Button>
              </li>
            ))}
          </ul>
        )}
        <p role="status" className="m-0 min-h-5 text-sm">
          {status}
        </p>
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>
            {t("playlistCancel")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
