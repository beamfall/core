import { ArrowDown, ArrowUp, GripVertical, X } from "lucide-react";
import { useEffect, useId, useRef, useState } from "react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { getIdentityEpochSignal } from "@/lib/api";
import { addCollectionMedia } from "@/lib/graphql";
import { useT } from "@/lib/i18n";
import { useSession } from "@/lib/session";
import type { StringKey } from "@/strings";
import { updateMusicQueue } from "../lib/musicQueue";
import { createPlaylist, mediaLibraryID } from "../lib/playlists";
import type { PlayQueueItem } from "../lib/watchQueue";
import type { MusicPlayback } from "./useMusicPlayback";

export function MusicQueuePanel({
  id,
  profileID,
  music,
  onClose,
}: {
  id: string;
  profileID: string;
  music: MusicPlayback;
  onClose: () => void;
}) {
  const t = useT();
  const { isManager } = useSession();
  const headingID = useId();
  const close = useRef<HTMLButtonElement>(null);
  const dragged = useRef<string | null>(null);
  const [saving, setSaving] = useState(false);
  const [saved, setSaved] = useState(false);

  useEffect(() => close.current?.focus(), []);
  useEffect(() => {
    void updateMusicQueue(profileID, { kind: "refresh" });
  }, [profileID]);

  const { upcoming, busy } = music;
  const { current } = music;
  const move = (mediaID: string, from: number, to: number) => {
    const target = upcoming[to];
    if (busy || !target || from < 0 || from === to) return;
    void updateMusicQueue(profileID, {
      kind: "move",
      mediaID,
      anchor: target.id,
      after: to > from,
    });
  };
  return (
    <section
      id={id}
      aria-labelledby={headingID}
      className="bf-music-queue"
      onKeyDown={(event) => {
        if (event.key === "Escape") {
          event.stopPropagation();
          onClose();
        }
      }}
    >
      <header className="flex items-center justify-between gap-2">
        <h2 id={headingID} className="m-0 text-base font-semibold">
          {t("musicQueueTitle")}
        </h2>
        <button
          type="button"
          ref={close}
          className="bf-control is-icon"
          aria-label={t("dialogClose")}
          onClick={onClose}
        >
          <X aria-hidden />
        </button>
      </header>
      {current && (
        <p className="m-0 wrap-anywhere text-sm">
          <span className="font-semibold">{t("musicQueueNowPlaying")}</span> {current.title}
        </p>
      )}
      <h3 className="m-0 text-sm font-semibold">{t("musicQueueUpcoming")}</h3>
      {upcoming.length ? (
        <ol className="m-0 grid list-none gap-1 p-0">
          {upcoming.map((item, index) => (
            <li
              key={item.id}
              className="flex items-center gap-1 rounded-md py-1"
              onDragOver={(event) => {
                if (dragged.current && dragged.current !== item.id) event.preventDefault();
              }}
              onDrop={(event) => {
                const source = dragged.current;
                if (!source) return;
                event.preventDefault();
                move(
                  source,
                  upcoming.findIndex(({ id }) => id === source),
                  index,
                );
                dragged.current = null;
              }}
            >
              <span
                aria-hidden="true"
                draggable={!busy}
                className="cursor-grab px-1"
                onDragStart={(event) => {
                  dragged.current = item.id;
                  event.dataTransfer.effectAllowed = "move";
                  event.dataTransfer.setData("text/plain", item.id);
                }}
                onDragEnd={() => {
                  dragged.current = null;
                }}
              >
                <GripVertical size={16} />
              </span>
              <span className="min-w-0 flex-1 wrap-anywhere text-sm">{item.title}</span>
              <button
                type="button"
                className="bf-control is-icon"
                aria-label={t("musicQueueMoveUp", { title: item.title })}
                aria-disabled={busy || index === 0}
                onClick={() => move(item.id, index, index - 1)}
              >
                <ArrowUp aria-hidden />
              </button>
              <button
                type="button"
                className="bf-control is-icon"
                aria-label={t("musicQueueMoveDown", { title: item.title })}
                aria-disabled={busy || index === upcoming.length - 1}
                onClick={() => move(item.id, index, index + 1)}
              >
                <ArrowDown aria-hidden />
              </button>
              <button
                type="button"
                className="bf-control is-icon"
                aria-label={t("musicQueueRemove", { title: item.title })}
                aria-disabled={busy}
                onClick={() => {
                  if (busy) return;
                  close.current?.focus();
                  void updateMusicQueue(profileID, { kind: "remove", mediaID: item.id });
                }}
              >
                <X aria-hidden />
              </button>
            </li>
          ))}
        </ol>
      ) : (
        <p className="m-0 text-sm">{t("musicQueueEmpty")}</p>
      )}
      <div className="flex flex-wrap gap-2">
        <Button
          variant="outline"
          size="sm"
          disabled={busy || !upcoming.length}
          onClick={() => {
            close.current?.focus();
            void updateMusicQueue(profileID, { kind: "clear" });
          }}
        >
          {t("musicQueueClear")}
        </Button>
        <Button
          variant="outline"
          size="sm"
          disabled={busy || saving}
          onClick={() => {
            setSaved(false);
            setSaving(true);
          }}
        >
          {t("musicQueueSave")}
        </Button>
      </div>
      {saving && (
        <SaveQueueForm
          completeOrder={music.completeOrder}
          isManager={isManager}
          onClose={(success) => {
            close.current?.focus();
            setSaving(false);
            setSaved(success);
          }}
        />
      )}
      {saved && (
        <p role="status" className="m-0 text-sm">
          {t("musicQueueSaved")}
        </p>
      )}
    </section>
  );
}

function SaveQueueForm({
  completeOrder,
  isManager,
  onClose,
}: {
  completeOrder: () => Promise<readonly PlayQueueItem[]>;
  isManager: boolean;
  onClose: (success: boolean) => void;
}) {
  const t = useT();
  const [queue, setQueue] = useState<readonly PlayQueueItem[]>();
  const [name, setName] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<StringKey>();
  const [playlistID, setPlaylistID] = useState<string>();
  const input = useRef<HTMLInputElement>(null);

  useEffect(() => input.current?.focus(), []);

  async function save() {
    if (!name.trim() || busy) return;
    const epoch = getIdentityEpochSignal();
    setBusy(true);
    setError(undefined);
    try {
      const tracks = queue ?? (await completeOrder());
      if (epoch?.aborted) return;
      if (!queue) setQueue(tracks);
      let id = playlistID;
      if (!id) {
        const libraries = new Set(
          await Promise.all(tracks.map((item) => item.libraryID ?? mediaLibraryID(item.id))),
        );
        if (epoch?.aborted) return;
        if (libraries.size !== 1) {
          setError("musicQueueMixedLibraries");
          return;
        }
        const playlist = await createPlaylist([...libraries][0], name);
        id = playlist.id;
        setPlaylistID(id);
      }
      if (epoch?.aborted) return;
      await addCollectionMedia(
        id,
        tracks.map((item) => item.id),
      );
      if (!epoch?.aborted) onClose(true);
    } catch {
      if (!epoch?.aborted) setError("musicQueueSaveFailed");
    } finally {
      if (!epoch?.aborted) setBusy(false);
    }
  }

  return (
    <form
      className="grid gap-2"
      onSubmit={(event) => {
        event.preventDefault();
        if (isManager) void save();
      }}
    >
      {isManager ? (
        <label className="grid gap-1 text-sm">
          {t("playlistNameLabel")}
          <Input
            ref={input}
            value={name}
            disabled={busy || !!playlistID}
            onChange={(event) => setName(event.target.value)}
          />
        </label>
      ) : (
        <p className="m-0 text-sm" role="status">
          {t("playlistManagerOnly")}
        </p>
      )}
      {error && (
        <p className="m-0 text-sm" role="alert">
          {t(error)}
        </p>
      )}
      <div className="flex flex-wrap gap-2">
        {isManager && (
          <Button type="submit" size="sm" disabled={busy || !name.trim()}>
            {t("playlistSave")}
          </Button>
        )}
        <Button
          type="button"
          variant="outline"
          size="sm"
          disabled={busy}
          onClick={() => onClose(false)}
        >
          {t("playlistCancel")}
        </Button>
      </div>
    </form>
  );
}
