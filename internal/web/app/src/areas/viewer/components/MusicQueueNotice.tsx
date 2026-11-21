import { useEffect, useSyncExternalStore } from "react";
import { getIdentityEpochSignal } from "@/lib/api";
import { useT } from "@/lib/i18n";
import type { MusicQueueEdit, QueueEditResult } from "../lib/musicQueue";
import "../remote/remote.css";

type Notice = {
  result: Exclude<QueueEditResult, "dropped">;
  edit: MusicQueueEdit;
  epoch: AbortSignal | undefined;
};

let notice: Notice | undefined;
const listeners = new Set<() => void>();

function setNotice(next: Notice | undefined) {
  notice = next;
  for (const listener of listeners) listener();
}

function subscribe(listener: () => void) {
  listeners.add(listener);
  return () => {
    listeners.delete(listener);
  };
}

export function showMusicQueueNotice(result: Notice["result"], edit: MusicQueueEdit) {
  setNotice({ result, edit, epoch: getIdentityEpochSignal() });
}

export function MusicQueueNotice() {
  const t = useT();
  const current = useSyncExternalStore(subscribe, () => notice);
  useEffect(() => {
    if (!current) return;
    const clear = () => {
      if (notice === current) setNotice(undefined);
    };
    const timer = setTimeout(clear, 4000);
    current.epoch?.addEventListener("abort", clear, { once: true });
    return () => {
      clearTimeout(timer);
      current.epoch?.removeEventListener("abort", clear);
    };
  }, [current]);

  if (!current || current.epoch?.aborted || current.epoch !== getIdentityEpochSignal()) return null;
  return (
    <div className="bf-remote-message" data-register="viewer" role="status">
      <p className="bf-remote-message-text">
        {t(
          current.result === "failed"
            ? "musicQueueEditFailed"
            : current.edit === "next"
              ? "musicQueueNextConfirmed"
              : "musicQueueAddConfirmed",
        )}
      </p>
    </div>
  );
}
