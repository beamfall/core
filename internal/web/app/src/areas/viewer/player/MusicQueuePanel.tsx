import { ArrowDown, ArrowUp, ListMusic, Trash2, X } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { Dialog } from "@/components/ui/astryx-primitives";
import { Button } from "@/components/ui/button";
import { addCollectionMedia, createCollection } from "@/lib/graphql";
import { useT } from "@/lib/i18n";
import { useSession } from "@/lib/session";
import {
  currentAndUpcomingPlayable,
  enqueueQueueEdit,
  isProfileMismatchError,
  isUpcomingPlayableIndex,
  type MusicQueueRow,
  type MusicQueueView,
  movableNeighbours,
  moveMusicQueueTrack,
  playableMusic,
  removeMusicQueueTrack,
  upcomingPlayableIDs,
} from "../lib/musicQueue";
import { getActivePlayingMediaID } from "../lib/watchQueue";

interface FrozenPlaylistSave {
  key: string;
  name: string;
  libraryID: string;
  mediaIDs: string[];
  playlistID?: string;
}

type PanelNotice = { kind: "alert" | "status"; text: string } | null;

export function MusicQueuePanel({
  open,
  onRetry,
  onOpenChange,
  onPlay,
  currentID,
  queue,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onPlay: () => void;
  onRetry: () => void;
  currentID: string;
  queue: MusicQueueView | undefined;
}) {
  const t = useT();
  const sessionState = useSession() as {
    session?: { profileID?: string; deviceRole?: string } | null;
    manifest?: { libraries?: { id: string }[] } | null;
    isManager?: boolean;
  };
  const profileID = sessionState.session?.profileID;
  const deviceRole = sessionState.session?.deviceRole;
  const notManager =
    sessionState.isManager === false ||
    (deviceRole !== undefined && deviceRole !== "" && deviceRole !== "manager");
  const defaultLibraryID = sessionState.manifest?.libraries?.[0]?.id ?? "lib-1";

  const anchorRef = useRef<HTMLDivElement | null>(null);
  const dragIndexRef = useRef<number | null>(null);
  const [busy, setBusy] = useState(false);
  const [playlistName, setPlaylistName] = useState("");
  const [notice, setNotice] = useState<PanelNotice>(null);
  const frozenSaveRef = useRef<FrozenPlaylistSave | null>(null);

  const { rows, failed, invalid, pending } = queue ?? {};
  const currentIDRef = useRef(currentID);
  currentIDRef.current = currentID;
  const rowsRef = useRef(rows);
  rowsRef.current = rows;
  const first = rows?.find(playableMusic);
  const upcomingIDs = upcomingPlayableIDs(rows, currentID);

  useEffect(() => {
    if (!open) {
      setNotice(null);
      frozenSaveRef.current = null;
    }
  }, [open, profileID]);

  async function rereadQueue() {
    if (queue?.refresh) {
      await queue.refresh();
    } else {
      onRetry();
    }
  }

  function stillUpcomingEditable(
    liveRows: readonly MusicQueueRow[] | undefined,
    mediaID: string,
    liveCurrent: string,
  ): { valid: boolean; curIndex: number } {
    if (!liveRows || (liveCurrent && mediaID === liveCurrent)) {
      return { valid: false, curIndex: -1 };
    }
    const sourceIndex = liveRows.findIndex((row) => row.id === mediaID);
    const curIndex = liveCurrent ? liveRows.findIndex((row) => row.id === liveCurrent) : -1;
    return {
      valid: sourceIndex >= 0 && isUpcomingPlayableIndex(liveRows, sourceIndex, liveCurrent),
      curIndex,
    };
  }

  async function handleMove(mediaID: string, position: number) {
    if (busy) return;
    setBusy(true);
    setNotice(null);
    let writeFailed = false;
    let mismatched = false;
    try {
      await enqueueQueueEdit(async () => {
        const liveCurrent = getActivePlayingMediaID() ?? currentIDRef.current;
        const liveRows = rowsRef.current;
        const { valid, curIndex } = stillUpcomingEditable(liveRows, mediaID, liveCurrent);
        if (
          !valid ||
          !liveRows ||
          (curIndex >= 0 && position <= curIndex) ||
          position < 0 ||
          position >= liveRows.length
        ) {
          await rereadQueue();
          return;
        }
        try {
          await moveMusicQueueTrack(mediaID, position, profileID);
        } catch (err) {
          if (isProfileMismatchError(err)) {
            mismatched = true;
            return;
          }
          writeFailed = true;
        }
        await rereadQueue();
      });
    } finally {
      if (writeFailed && !mismatched) {
        setNotice({ kind: "alert", text: t("musicQueueWriteFailed") });
      }
      setBusy(false);
    }
  }

  async function handleRemove(mediaID: string) {
    if (busy) return;
    setBusy(true);
    setNotice(null);
    let writeFailed = false;
    let mismatched = false;
    try {
      await enqueueQueueEdit(async () => {
        const liveCurrent = getActivePlayingMediaID() ?? currentIDRef.current;
        if (!stillUpcomingEditable(rowsRef.current, mediaID, liveCurrent).valid) {
          await rereadQueue();
          return;
        }
        try {
          await removeMusicQueueTrack(mediaID, profileID);
        } catch (err) {
          if (isProfileMismatchError(err)) {
            mismatched = true;
            return;
          }
          writeFailed = true;
        }
        await rereadQueue();
      });
    } finally {
      if (writeFailed && !mismatched) {
        setNotice({ kind: "alert", text: t("musicQueueWriteFailed") });
      }
      setBusy(false);
    }
  }

  async function handleClearUpcoming() {
    if (busy) return;
    if (upcomingPlayableIDs(rows, currentID).length === 0) return;
    setBusy(true);
    setNotice(null);
    let writeFailed = false;
    let mismatched = false;
    try {
      await enqueueQueueEdit(async () => {
        // Recompute targets after any in-flight advance finishes so a newly playing track is never deleted.
        const liveCurrent = getActivePlayingMediaID() ?? currentIDRef.current;
        const upcoming = upcomingPlayableIDs(rowsRef.current, liveCurrent);
        if (upcoming.length === 0) {
          await rereadQueue();
          return;
        }
        for (const id of upcoming) {
          try {
            await removeMusicQueueTrack(id, profileID);
          } catch (err) {
            if (isProfileMismatchError(err)) {
              mismatched = true;
              return;
            }
            writeFailed = true;
            break;
          }
        }
        await rereadQueue();
      });
    } finally {
      if (writeFailed && !mismatched) {
        setNotice({ kind: "alert", text: t("musicQueueWriteFailed") });
      }
      setBusy(false);
    }
  }

  async function handleSavePlaylist() {
    if (busy) return;
    const name = playlistName.trim() || t("musicQueueTitle");

    // If retrying a failed member-add for the same playlist name, reuse the
    // frozen snapshot and already-created playlist ID (CORE-2847).
    const existing = frozenSaveRef.current;
    if (existing?.playlistID && existing.name === name) {
      setBusy(true);
      setNotice(null);
      try {
        await addCollectionMedia(existing.playlistID, existing.mediaIDs);
        frozenSaveRef.current = null;
        setNotice({ kind: "status", text: t("musicQueueSaved", { name: existing.name }) });
      } catch {
        setNotice({ kind: "alert", text: t("musicQueueSaveFailed") });
      } finally {
        setBusy(false);
      }
      return;
    }

    if (notManager) {
      setNotice({ kind: "alert", text: t("musicQueueSaveManagerOnly") });
      return;
    }
    if (failed) {
      setNotice({ kind: "alert", text: t("musicQueueReadFailed") });
      return;
    }
    if (pending || !rows) {
      setNotice({ kind: "alert", text: t("musicQueueSaveReadPending") });
      return;
    }

    const tracks = currentAndUpcomingPlayable(rows, currentID);
    if (tracks.length === 0) {
      setNotice({ kind: "alert", text: t("musicQueueEmpty") });
      return;
    }

    const seenIDs = new Set<string>();
    for (const track of tracks) {
      if (seenIDs.has(track.id)) {
        setNotice({ kind: "alert", text: t("musicQueueSaveDuplicateTrack") });
        return;
      }
      seenIDs.add(track.id);
    }

    const libraries = new Set(
      tracks.map((track) => track.libraryID).filter((id): id is string => Boolean(id)),
    );
    if (libraries.size > 1) {
      setNotice({ kind: "alert", text: t("musicQueueSaveMixedLibraries") });
      return;
    }

    const libraryID = [...libraries][0] ?? defaultLibraryID;
    const mediaIDs = tracks.map((track) => track.id);
    const snapshotKey = `${libraryID}:${name}:${mediaIDs.join(",")}`;
    const frozen: FrozenPlaylistSave =
      existing && existing.key === snapshotKey
        ? existing
        : { key: snapshotKey, name, libraryID, mediaIDs };
    frozenSaveRef.current = frozen;

    setBusy(true);
    setNotice(null);
    try {
      let playlistID = frozen.playlistID;
      if (!playlistID) {
        const created = await createCollection({ libraryID: frozen.libraryID, name: frozen.name });
        playlistID = created.id;
        frozen.playlistID = playlistID;
      }
      await addCollectionMedia(playlistID, frozen.mediaIDs);
      frozenSaveRef.current = null;
      setNotice({ kind: "status", text: t("musicQueueSaved", { name: frozen.name }) });
    } catch {
      setNotice({ kind: "alert", text: t("musicQueueSaveFailed") });
    } finally {
      setBusy(false);
    }
  }

  return (
    <Dialog.Root open={open} onOpenChange={onOpenChange} modal={false}>
      <div ref={anchorRef} className="relative">
        <Dialog.Trigger aria-label={t("musicQueueTitle")} className="bf-control is-icon">
          <ListMusic aria-hidden />
        </Dialog.Trigger>
        <Dialog.Portal container={anchorRef}>
          <Dialog.Popup className="bf-player-dialog bf-music-queue-dialog">
            <div className="bf-player-dialog-head bf-music-queue-head">
              <Dialog.Title render={<h3 />}>{t("musicQueueTitle")}</Dialog.Title>
              <Dialog.Close className="bf-player-dialog-back" aria-label={t("dialogClose")}>
                <X aria-hidden />
              </Dialog.Close>
            </div>
            {invalid ? (
              <p className="bf-music-queue-notice" role="alert">
                {t("playerTerminalUnavailable")}
              </p>
            ) : (
              <>
                {failed ? (
                  <>
                    <p className="bf-music-queue-notice" role="alert">
                      {t("musicQueueReadFailed")}
                    </p>
                    <Button size="sm" onClick={onRetry}>
                      {t("tryAgain")}
                    </Button>
                  </>
                ) : !rows ? (
                  <p className="bf-music-queue-status" role="status">
                    {t("loading")}
                  </p>
                ) : (
                  <>
                    {rows.length > 0 && (
                      <ol className="bf-music-queue-list">
                        {rows.map((row: MusicQueueRow, index: number) => {
                          const editable = isUpcomingPlayableIndex(rows, index, currentID);
                          const { upPosition, downPosition } = editable
                            ? movableNeighbours(rows, index, currentID)
                            : {};
                          const keyCount = rows.filter((entry) => entry.id === row.id).length;
                          const label = row.locked ? t("musicQueueLockedItem") : row.title;
                          return (
                            <li
                              key={keyCount > 1 ? `${row.id}-${index}` : row.id}
                              className="bf-music-queue-row"
                              data-current={row.id === currentID ? "true" : undefined}
                              data-queue-id={row.id}
                              data-queue-position={index}
                              draggable={editable && !busy ? true : undefined}
                              onDragStart={(event) => {
                                if (!editable || busy) return;
                                dragIndexRef.current = index;
                                event.dataTransfer?.setData("text/plain", String(index));
                              }}
                              onDragOver={(event) => {
                                if (editable && !busy) event.preventDefault();
                              }}
                              onDrop={(event) => {
                                if (!editable || busy) return;
                                event.preventDefault();
                                const raw = event.dataTransfer?.getData("text/plain");
                                const fromIndex =
                                  dragIndexRef.current ?? (raw ? Number(raw) : Number.NaN);
                                dragIndexRef.current = null;
                                if (!Number.isInteger(fromIndex) || fromIndex === index) return;
                                if (!isUpcomingPlayableIndex(rows, fromIndex, currentID)) {
                                  return;
                                }
                                const source = rows[fromIndex]!;
                                void handleMove(source.id, index);
                              }}
                              onKeyDown={(event) => {
                                if (!editable || busy) return;
                                if (event.key === "ArrowUp" && upPosition !== undefined) {
                                  event.preventDefault();
                                  void handleMove(row.id, upPosition);
                                } else if (
                                  event.key === "ArrowDown" &&
                                  downPosition !== undefined
                                ) {
                                  event.preventDefault();
                                  void handleMove(row.id, downPosition);
                                }
                              }}
                            >
                              <span
                                className="bf-music-queue-row-title"
                                aria-current={row.id === currentID ? "true" : undefined}
                                title={label}
                              >
                                {label}
                              </span>
                              {editable && (
                                <span className="bf-music-queue-actions">
                                  <button
                                    type="button"
                                    className="bf-music-queue-action"
                                    aria-label={t("musicQueueMoveUp", { title: row.title })}
                                    disabled={busy || upPosition === undefined}
                                    onClick={() =>
                                      upPosition !== undefined &&
                                      void handleMove(row.id, upPosition)
                                    }
                                  >
                                    <ArrowUp aria-hidden />
                                  </button>
                                  <button
                                    type="button"
                                    className="bf-music-queue-action"
                                    aria-label={t("musicQueueMoveDown", { title: row.title })}
                                    disabled={busy || downPosition === undefined}
                                    onClick={() =>
                                      downPosition !== undefined &&
                                      void handleMove(row.id, downPosition)
                                    }
                                  >
                                    <ArrowDown aria-hidden />
                                  </button>
                                  <button
                                    type="button"
                                    className="bf-music-queue-action is-danger"
                                    aria-label={t("musicQueueRemove", { title: row.title })}
                                    disabled={busy}
                                    onClick={() => void handleRemove(row.id)}
                                  >
                                    <Trash2 aria-hidden />
                                  </button>
                                </span>
                              )}
                            </li>
                          );
                        })}
                      </ol>
                    )}
                    {first ? (
                      <div className="bf-music-queue-controls">
                        <Button size="sm" onClick={onPlay}>
                          {t("musicQueuePlay")}
                        </Button>
                        {upcomingIDs.length > 0 && (
                          <Button
                            size="sm"
                            variant="outline"
                            disabled={busy}
                            onClick={() => void handleClearUpcoming()}
                          >
                            {t("musicQueueClearUpcoming")}
                          </Button>
                        )}
                      </div>
                    ) : (
                      <p className="bf-music-queue-status" role="status">
                        {t("musicQueueEmpty")}
                      </p>
                    )}
                  </>
                )}
                <div className="bf-music-queue-save">
                  <input
                    type="text"
                    className="bf-music-queue-input"
                    aria-label={t("musicQueuePlaylistName")}
                    value={playlistName}
                    placeholder={t("musicQueuePlaylistName")}
                    onChange={(event) => {
                      setPlaylistName(event.target.value);
                      frozenSaveRef.current = null;
                    }}
                  />
                  <Button
                    size="sm"
                    variant="outline"
                    disabled={busy}
                    onClick={() => void handleSavePlaylist()}
                  >
                    {t("musicQueueSaveAsPlaylist")}
                  </Button>
                </div>
                {notice && !(failed && notice.text === t("musicQueueReadFailed")) && (
                  <p className="bf-music-queue-notice" role={notice.kind}>
                    {notice.text}
                  </p>
                )}
              </>
            )}
          </Dialog.Popup>
        </Dialog.Portal>
      </div>
    </Dialog.Root>
  );
}
