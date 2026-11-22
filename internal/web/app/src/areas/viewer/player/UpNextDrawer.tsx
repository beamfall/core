import { X } from "lucide-react";
import { useEffect, useRef } from "react";
import { useT } from "@/lib/i18n";
import type { PlayQueueItem } from "../lib/watchQueue";

/** Upcoming rows the drawer lists after the playing one. */
const UPCOMING_ROWS = 20;

// The audio Up next drawer (CORE-3216): the lab's Played before / Playing now /
// Playing next ribbon, turned vertical along the stage's right edge. A row plays
// that item; the queue itself is edited elsewhere (the server-queue popover).
export function UpNextDrawer({
  items,
  currentID,
  onPick,
  onClose,
}: {
  items: readonly PlayQueueItem[];
  currentID: string;
  onPick: (item: PlayQueueItem) => void;
  onClose: () => void;
}) {
  const t = useT();
  const at = items.findIndex((item) => item.id === currentID);
  const before = at > 0 ? items[at - 1] : undefined;
  const now = at >= 0 ? items[at] : undefined;
  const upcoming = items.slice(at + 1, at + 1 + UPCOMING_ROWS);
  const closeRef = useRef<HTMLButtonElement>(null);

  useEffect(() => {
    closeRef.current?.focus({ preventScroll: true });
  }, []);

  return (
    <aside
      className="bf-queue-drawer"
      aria-label={t("upNextList")}
      onKeyDown={(event) => {
        if (event.key === "Escape") {
          event.stopPropagation();
          onClose();
        }
      }}
    >
      <header className="bf-queue-drawer-head">
        <h2 className="bf-queue-drawer-heading">{t("upNext")}</h2>
        <button
          ref={closeRef}
          type="button"
          className="bf-control is-icon"
          aria-label={t("dismiss")}
          onClick={onClose}
        >
          <X aria-hidden="true" />
        </button>
      </header>
      {before && (
        <Section label={t("playedBefore")}>
          <Row item={before} state="before" onPick={onPick} />
        </Section>
      )}
      {now && (
        <Section label={t("playingNow")}>
          <Row item={now} state="now" onPick={onPick} />
        </Section>
      )}
      {upcoming.length > 0 && (
        <Section label={t("upNext")}>
          {upcoming.map((item) => (
            <Row key={item.id} item={item} state="next" onPick={onPick} />
          ))}
        </Section>
      )}
    </aside>
  );
}

function Section({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <section className="bf-queue-drawer-section">
      <h3 className="bf-queue-drawer-eyebrow">{label}</h3>
      <ol className="bf-queue-drawer-list">{children}</ol>
    </section>
  );
}

function Row({
  item,
  state,
  onPick,
}: {
  item: PlayQueueItem;
  state: "before" | "now" | "next";
  onPick: (item: PlayQueueItem) => void;
}) {
  return (
    <li>
      <button
        type="button"
        className="bf-queue-drawer-row"
        data-state={state}
        aria-current={state === "now" ? "true" : undefined}
        disabled={state === "now"}
        onClick={() => onPick(item)}
      >
        {item.title}
      </button>
    </li>
  );
}
