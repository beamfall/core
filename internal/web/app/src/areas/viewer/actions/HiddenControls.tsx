import { EyeOff, X } from "lucide-react";
import { type ReactNode, useEffect, useRef, useState } from "react";
import { Link } from "react-router";
import { buttonVariants } from "@/components/ui/button";
import { useT } from "@/lib/i18n";
import "../remote/remote.css";
import "./hidden.css";
import {
  clearHiddenNotice,
  dismissHiddenNotice,
  type HiddenNotice,
  unhideItem,
  useHiddenItems,
  useHiddenNotice,
  useHideWatched,
} from "./hiddenItems";

const NOTICE_MS = 8000;

/**
 * The Undo notice shown after a hide. Mount once per viewer session. It waits
 * while the pointer or focus is on it, so nobody loses the Undo mid-reach.
 * Pass the bound Profile: a switch drops the notice, so one Profile never sees,
 * or undoes, another's hide.
 */
export function HiddenUndoNotice({ profileID }: { profileID?: string }) {
  const notice = useHiddenNotice();
  // The cleanup runs when the bound Profile changes and when the viewer
  // remounts for another Profile, so a notice never outlives its Profile.
  useEffect(() => clearHiddenNotice, [profileID]);
  return notice ? <UndoNotice notice={notice} /> : null;
}

function UndoNotice({ notice }: { notice: HiddenNotice }) {
  const t = useT();
  const [held, setHeld] = useState(false);
  const [failed, setFailed] = useState(false);
  const hovered = useRef(false);
  const focused = useRef(false);
  const id = notice.id;

  useEffect(() => {
    setFailed(false);
  }, [id]);

  useEffect(() => {
    if (held) return;
    const timer = setTimeout(() => dismissHiddenNotice(id), NOTICE_MS);
    return () => clearTimeout(timer);
  }, [id, held]);

  const sync = () => setHeld(hovered.current || focused.current);

  return (
    <div
      className="bf-remote-message bf-hidden-notice"
      role="status"
      onPointerEnter={() => {
        hovered.current = true;
        sync();
      }}
      onPointerLeave={() => {
        hovered.current = false;
        sync();
      }}
      onFocus={() => {
        focused.current = true;
        sync();
      }}
      onBlur={() => {
        focused.current = false;
        sync();
      }}
    >
      <p className="bf-remote-message-text m-0 min-w-0 flex-1">
        {failed ? t("itemActionFailed") : t("itemHiddenNotice", { title: notice.title })}
      </p>
      <button
        type="button"
        className="bf-hidden-notice-undo"
        onClick={() => {
          unhideItem(notice.target).then(
            () => dismissHiddenNotice(notice.id),
            () => setFailed(true),
          );
        }}
      >
        {t("itemHideUndo")}
      </button>
      <button
        type="button"
        className="bf-remote-message-dismiss"
        aria-label={t("itemHideDismiss")}
        onClick={() => dismissHiddenNotice(notice.id)}
      >
        <X aria-hidden />
      </button>
    </div>
  );
}

/**
 * An empty browse that this Profile's own hides may explain: with any item
 * hidden or Hide watched on, it says so and points to Profile settings instead
 * of the library empty state. Until both are known it renders nothing, so the
 * first-scan prompt never flashes for a Profile that has only hidden things.
 */
export function HiddenAwareEmpty({ fallback }: { fallback: ReactNode }) {
  const t = useT();
  const { mutes, failed } = useHiddenItems();
  const watched = useHideWatched();
  if (failed && watched.failed) return fallback;
  if ((mutes === null && !failed) || (watched.on === null && !watched.failed)) return null;
  if (!(mutes?.length || watched.on)) return fallback;
  return (
    <div className="bf-empty flex flex-col gap-3" role="status">
      <p className="text-lg font-semibold text-[var(--text-primary)]">{t("hiddenEmptyTitle")}</p>
      <div className="mt-2 flex flex-wrap items-start justify-center gap-2">
        <Link to="/settings" className={buttonVariants({ variant: "outline" })}>
          {t("hiddenEmptyShow")}
        </Link>
      </div>
    </div>
  );
}

/** The library toolbar's Hide watched toggle; the same setting as in Profile settings. */
export function HideWatchedToggle() {
  const t = useT();
  const { on, set } = useHideWatched();
  return (
    <button
      type="button"
      className="bf-chip bf-chip-square"
      aria-label={t("hideWatched")}
      title={t("hideWatched")}
      aria-pressed={on ?? false}
      disabled={on === null}
      onClick={() => void set(!on)}
    >
      <EyeOff aria-hidden className="size-4" />
    </button>
  );
}
