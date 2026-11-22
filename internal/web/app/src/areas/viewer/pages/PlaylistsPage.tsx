import { type FormEvent, useCallback, useEffect, useState } from "react";
import { Link } from "react-router";
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
import { Input } from "@/components/ui/input";
import { Skeleton } from "@/components/ui/skeleton";
import type { CollectionInfo } from "@/lib/graphql/types";
import { useT } from "@/lib/i18n";
import { useSession } from "@/lib/session";
import { SmartPlaylistEditor } from "../components/SmartPlaylistEditor";
import { ViewerActions } from "../components/ViewerActions";
import { createPlaylist, deletePlaylist, loadPlaylists, renamePlaylist } from "../lib/playlists";
import { loadSmartPlaylists, type SmartPlaylist } from "../lib/smartPlaylists";

const SELECT_CLASS =
  "h-10 rounded-[var(--radius-control)] border border-input bg-transparent px-3 text-sm";

// The listener's playlists (CORE-1635): every Library's Collections, with
// create, rename and delete. Changes need a manager device (the server gates
// the Collection mutations), so other devices list them read-only and say why.
// Smart playlists (CORE-2771) are listed below them: rule-based Smart
// Collections the server evaluates on every read.
export function PlaylistsPage() {
  const t = useT();
  const { manifest, isManager } = useSession();
  const libraries = manifest?.libraries ?? [];
  const libraryKey = libraries.map((library) => library.id).join("\n");
  const [playlists, setPlaylists] = useState<CollectionInfo[] | null>(null);
  const [smartPlaylists, setSmartPlaylists] = useState<SmartPlaylist[] | null>(null);
  const [creatingSmart, setCreatingSmart] = useState(false);
  const [name, setName] = useState("");
  const [libraryID, setLibraryID] = useState("");
  const [renaming, setRenaming] = useState<{ id: string; name: string } | null>(null);
  const [deleting, setDeleting] = useState<CollectionInfo | null>(null);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  const reload = useCallback(async () => {
    setPlaylists(await loadPlaylists(libraryKey ? libraryKey.split("\n") : []));
  }, [libraryKey]);

  const reloadSmart = useCallback(async () => {
    setSmartPlaylists(await loadSmartPlaylists(libraryKey ? libraryKey.split("\n") : []));
  }, [libraryKey]);

  useEffect(() => {
    void reload().catch(() => setPlaylists([]));
  }, [reload]);

  useEffect(() => {
    void reloadSmart().catch(() => setSmartPlaylists([]));
  }, [reloadSmart]);

  async function run(action: () => Promise<unknown>) {
    setBusy(true);
    setError("");
    try {
      await action();
      await reload();
      return true;
    } catch {
      setError(t("playlistActionFailed"));
      return false;
    } finally {
      setBusy(false);
    }
  }

  async function onCreate(event: FormEvent) {
    event.preventDefault();
    const target = libraryID || libraries[0]?.id;
    if (!name.trim() || !target) return;
    if (await run(() => createPlaylist(target, name))) setName("");
  }

  async function onRename(event: FormEvent) {
    event.preventDefault();
    if (!renaming || !renaming.name.trim()) return;
    const { id, name: next } = renaming;
    if (await run(() => renamePlaylist(id, next))) setRenaming(null);
  }

  async function onDelete() {
    if (!deleting) return;
    const { id } = deleting;
    if (await run(() => deletePlaylist(id))) setDeleting(null);
  }

  return (
    <AppShell actions={<ViewerActions />}>
      <div className="flex flex-col gap-6">
        <h1 className="bf-page-title">{t("playlistsTitle")}</h1>
        {!isManager && <p className="text-sm text-muted-foreground">{t("playlistManagerOnly")}</p>}
        {isManager && libraries.length > 0 && (
          <form onSubmit={onCreate} className="bf-list-toolbar" aria-label={t("playlistNew")}>
            <Input
              aria-label={t("playlistNameLabel")}
              placeholder={t("playlistNew")}
              value={name}
              onChange={(event) => setName(event.target.value)}
              className="max-w-xs"
            />
            {libraries.length > 1 && (
              <select
                aria-label={t("playlistLibraryLabel")}
                className={SELECT_CLASS}
                value={libraryID || libraries[0].id}
                onChange={(event) => setLibraryID(event.target.value)}
              >
                {libraries.map((library) => (
                  <option key={library.id} value={library.id}>
                    {library.title}
                  </option>
                ))}
              </select>
            )}
            <Button type="submit" disabled={busy || !name.trim()}>
              {t("playlistCreate")}
            </Button>
          </form>
        )}
        {error && (
          <p role="alert" className="text-sm text-[var(--state-danger)]">
            {error}
          </p>
        )}
        {playlists === null && <Skeleton aria-hidden className="bf-shimmer h-24 w-full" />}
        {playlists?.length === 0 && (
          <p className="text-sm text-muted-foreground">{t("playlistsEmpty")}</p>
        )}
        {playlists && playlists.length > 0 && (
          <ul className="m-0 flex list-none flex-col gap-2 p-0">
            {playlists.map((playlist) => (
              <li key={playlist.id} className="bf-list-toolbar justify-between">
                {renaming?.id === playlist.id ? (
                  <form onSubmit={onRename} className="bf-list-toolbar">
                    <Input
                      aria-label={t("playlistNameLabel")}
                      value={renaming.name}
                      onChange={(event) =>
                        setRenaming({ id: playlist.id, name: event.target.value })
                      }
                      autoFocus
                    />
                    <Button type="submit" disabled={busy || !renaming.name.trim()}>
                      {t("playlistSave")}
                    </Button>
                    <Button type="button" variant="ghost" onClick={() => setRenaming(null)}>
                      {t("playlistCancel")}
                    </Button>
                  </form>
                ) : (
                  <Link
                    to={`/playlists/${encodeURIComponent(playlist.id)}`}
                    className="flex flex-col"
                  >
                    <span>{playlist.name}</span>
                    <span className="text-sm text-muted-foreground">
                      {t("playlistTrackCount", { count: playlist.memberCount })}
                    </span>
                  </Link>
                )}
                {isManager && renaming?.id !== playlist.id && (
                  <span className="bf-list-toolbar">
                    <Button
                      variant="outline"
                      size="sm"
                      onClick={() => setRenaming({ id: playlist.id, name: playlist.name })}
                    >
                      {t("playlistRename")}
                    </Button>
                    <Button variant="danger" size="sm" onClick={() => setDeleting(playlist)}>
                      {t("playlistDelete")}
                    </Button>
                  </span>
                )}
              </li>
            ))}
          </ul>
        )}
        <section className="flex flex-col gap-3" aria-labelledby="smart-playlists-title">
          <div className="bf-list-toolbar justify-between">
            <h2 id="smart-playlists-title" className="text-lg font-semibold">
              {t("smartPlaylistsTitle")}
            </h2>
            {isManager && libraries.length > 0 && (
              <Button variant="outline" onClick={() => setCreatingSmart(true)}>
                {t("smartPlaylistNew")}
              </Button>
            )}
          </div>
          {smartPlaylists === null && <Skeleton aria-hidden className="bf-shimmer h-16 w-full" />}
          {smartPlaylists?.length === 0 && (
            <p className="text-sm text-muted-foreground">{t("smartPlaylistsEmpty")}</p>
          )}
          {smartPlaylists && smartPlaylists.length > 0 && (
            <ul className="m-0 flex list-none flex-col gap-2 p-0">
              {smartPlaylists.map((playlist) => (
                <li key={playlist.id}>
                  <Link
                    to={`/playlists/smart/${encodeURIComponent(playlist.id)}`}
                    className="flex flex-col"
                  >
                    <span>{playlist.name}</span>
                    <span className="text-sm text-muted-foreground">
                      {t("smartPlaylistRuleCount", { count: playlist.rules.rules.length })}
                    </span>
                  </Link>
                </li>
              ))}
            </ul>
          )}
        </section>
      </div>
      {creatingSmart && (
        <SmartPlaylistEditor
          onClose={() => setCreatingSmart(false)}
          onSaved={() => {
            setCreatingSmart(false);
            void reloadSmart().catch(() => {});
          }}
        />
      )}
      <Dialog
        open={deleting !== null}
        onOpenChange={(open) => {
          if (!open) setDeleting(null);
        }}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t("playlistDelete")}</DialogTitle>
            <DialogDescription>
              {t("playlistDeleteConfirm", { name: deleting?.name ?? "" })}
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button variant="outline" onClick={() => setDeleting(null)}>
              {t("playlistCancel")}
            </Button>
            <Button variant="destructive" disabled={busy} onClick={() => void onDelete()}>
              {t("playlistDelete")}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </AppShell>
  );
}
