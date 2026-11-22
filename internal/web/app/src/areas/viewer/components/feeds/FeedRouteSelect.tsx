import { useId, useState } from "react";
import { useT } from "@/lib/i18n";
import type { StringKey } from "@/strings";
import { NEW_ENTRY_ROUTES } from "../../lib/feeds";

const SELECT_CLASS =
  "h-8 rounded-[var(--radius-control)] border border-input bg-transparent px-2 text-sm";

const ROUTE_KEYS: Record<(typeof NEW_ENTRY_ROUTES)[number], StringKey> = {
  new: "feedsRouteNew",
  "queue-end": "feedsRouteQueueEnd",
  "queue-front": "feedsRouteQueueFront",
};

// Where a followed feed's new entries go for this Profile (CORE-2958): listed
// in New only, or also added to the end or front of the play queue.
export function FeedRouteSelect({
  route,
  onChange,
}: {
  route: string;
  onChange: (route: string) => Promise<void>;
}) {
  const t = useT();
  const id = useId();
  const [saving, setSaving] = useState(false);
  const [failed, setFailed] = useState(false);

  async function choose(next: string) {
    setSaving(true);
    setFailed(false);
    try {
      await onChange(next);
    } catch {
      setFailed(true);
    } finally {
      setSaving(false);
    }
  }

  return (
    <div className="flex flex-wrap items-center gap-2">
      <label htmlFor={id} className="text-sm">
        {t("feedsRouteLabel")}
      </label>
      <select
        id={id}
        className={SELECT_CLASS}
        value={route}
        disabled={saving}
        onChange={(event) => choose(event.target.value)}
      >
        {NEW_ENTRY_ROUTES.map((value) => (
          <option key={value} value={value}>
            {t(ROUTE_KEYS[value])}
          </option>
        ))}
      </select>
      {failed && (
        <span role="alert" className="text-sm text-[var(--text-secondary)]">
          {t("feedsActionFailed")}
        </span>
      )}
    </div>
  );
}
