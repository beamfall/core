import { LibraryBig, type LucideIcon } from "lucide-react";
import { useEffect, useId, useSyncExternalStore } from "react";
import { ErrorLine } from "@/components/beamfall/workbench";
import { setLibraryIcon } from "@/lib/graphql";
import type { LibraryInfo } from "@/lib/graphql/types";
import { useT } from "@/lib/i18n";
import { LIBRARY_ICON_NAMES, type LibraryIconName, libraryIcon } from "@/lib/libraryIcons";
import { requestManifestRefresh } from "@/lib/manifestRefresh";
import { cn } from "@/lib/utils";
import type { StringKey } from "@/strings";

// Choosing a Library's icon (CORE-1200). The choice shows at once and is saved
// behind it. Picks made in quick succession (arrow keys walk a radio group one
// option at a time) settle into a single write of the last one, so the
// activity log records the choice and not the route to it. The tracker is
// module-level so the card header and the picker agree, and so a write still
// goes out if the card unmounts before the pause ends.

type IconLibrary = Pick<LibraryInfo, "id" | "title" | "icon" | "iconChoice" | "defaultIcon">;

interface Pending {
  /** The choice on screen; "" is the default. */
  wanted: string;
  /** Set once the server holds `wanted`; the entry then waits for the reload. */
  saved: boolean;
  onSaved: () => void;
}

const SETTLE_MS = 250;
const pending = new Map<string, Pending>();
const failed = new Set<string>();
const timers = new Map<string, number>();
const saving = new Set<string>();
const listeners = new Set<() => void>();
let version = 0;

function emit() {
  version++;
  for (const listener of listeners) listener();
}

function subscribe(listener: () => void) {
  listeners.add(listener);
  return () => listeners.delete(listener);
}

function pick(libraryID: string, icon: string, onSaved: () => void) {
  pending.set(libraryID, { wanted: icon, saved: false, onSaved });
  failed.delete(libraryID);
  emit();
  window.clearTimeout(timers.get(libraryID));
  timers.set(
    libraryID,
    window.setTimeout(() => void save(libraryID), SETTLE_MS),
  );
}

// One write at a time per library. A pick made while a write is in flight is
// sent when it returns, so the server always ends on the last choice.
async function save(libraryID: string) {
  if (saving.has(libraryID)) return;
  saving.add(libraryID);
  try {
    for (;;) {
      const entry = pending.get(libraryID);
      if (!entry || entry.saved) return;
      let stored = true;
      try {
        await setLibraryIcon(libraryID, entry.wanted);
      } catch {
        stored = false;
      }
      // A newer pick replaces this one, whatever became of it.
      if (pending.get(libraryID) !== entry) continue;
      if (!stored) {
        // Back to what the server last reported.
        pending.delete(libraryID);
        failed.add(libraryID);
        emit();
        return;
      }
      entry.saved = true;
      requestManifestRefresh();
      entry.onSaved();
      return;
    }
  } finally {
    saving.delete(libraryID);
  }
}

/** Forgets every in-progress choice. For tests. */
export function resetLibraryIconChoices() {
  for (const timer of timers.values()) window.clearTimeout(timer);
  timers.clear();
  pending.clear();
  failed.clear();
  saving.clear();
  emit();
}

function useChoice(library: IconLibrary): { choice: string; failed: boolean } {
  useSyncExternalStore(
    subscribe,
    () => version,
    () => version,
  );
  const entry = pending.get(library.id);
  const server = library.iconChoice ?? "";
  // The reloaded library carries the saved choice, so the entry has done its job.
  useEffect(() => {
    if (entry?.saved && entry.wanted === server) pending.delete(library.id);
  }, [entry, server, library.id]);
  return { choice: entry ? entry.wanted : server, failed: failed.has(library.id) };
}

/** The symbol a Library card draws: the choice on screen, else what the server publishes. */
export function useLibraryIconSymbol(library: IconLibrary): LucideIcon {
  const { choice } = useChoice(library);
  const name = pending.has(library.id) ? choice || library.defaultIcon : library.icon;
  return libraryIcon(name) ?? LibraryBig;
}

const LABELS: Record<LibraryIconName, StringKey> = {
  film: "libraryIconFilm",
  tv: "libraryIconTv",
  video: "libraryIconVideo",
  note: "libraryIconNote",
  mic: "libraryIconMic",
  broadcast: "libraryIconBroadcast",
  headphones: "libraryIconHeadphones",
  book: "libraryIconBook",
  image: "libraryIconImage",
  camera: "libraryIconCamera",
  people: "libraryIconPeople",
  star: "libraryIconStar",
  heart: "libraryIconHeart",
  sparkles: "libraryIconSparkles",
  layers: "libraryIconLayers",
  folder: "libraryIconFolder",
};

const TILE =
  "flex h-9 min-w-9 cursor-pointer items-center justify-center gap-1.5 rounded-lg border border-white/10 bg-white/[0.03] px-2 text-muted-foreground transition-colors hover:border-white/25 hover:text-foreground peer-checked:border-primary/60 peer-checked:bg-primary/15 peer-checked:text-primary peer-focus-visible:ring-2 peer-focus-visible:ring-ring motion-reduce:transition-none";

// The icon picker on a Library card: one radio group, "Default" first, then
// every icon in the vocabulary. Native radios carry the keyboard behaviour
// (Tab reaches the group, arrow keys move the choice) and each one is named.
export function LibraryIconPicker({
  library,
  onChanged,
}: {
  library: IconLibrary;
  /** Runs after the server holds the choice; reload the library list here. */
  onChanged: () => void;
}) {
  const t = useT();
  const id = useId();
  const { choice, failed: saveFailed } = useChoice(library);
  // A server without Library icons publishes no default, and would refuse the write.
  if (!library.defaultIcon) return null;

  const options: { value: string; label: string; Icon: LucideIcon; text?: string }[] = [
    {
      value: "",
      label: t("libraryIconDefault"),
      Icon: libraryIcon(library.defaultIcon) ?? LibraryBig,
      text: t("libraryIconDefault"),
    },
    ...LIBRARY_ICON_NAMES.map((name) => ({
      value: name as string,
      label: t(LABELS[name]),
      Icon: libraryIcon(name) ?? LibraryBig,
    })),
  ];
  return (
    <div className="space-y-2">
      <div>
        <p
          id={`${id}-label`}
          className="m-0 text-xs font-semibold uppercase tracking-wide text-muted-foreground"
        >
          <span aria-hidden>{t("libraryIcon")}</span>
          <span className="sr-only">
            {t("libraryControlName", { control: t("libraryIcon"), library: library.title })}
          </span>
        </p>
        <p id={`${id}-help`} className="m-0 text-xs text-muted-foreground">
          {t("libraryIconHelp")}
        </p>
      </div>
      <div
        role="radiogroup"
        aria-labelledby={`${id}-label`}
        aria-describedby={`${id}-help`}
        className="flex flex-wrap gap-1.5"
      >
        {options.map(({ value, label, Icon, text }) => (
          <label key={value} title={label} className="relative">
            <input
              type="radio"
              name={`${id}-icon`}
              value={value}
              checked={choice === value}
              onChange={() => pick(library.id, value, onChanged)}
              aria-label={label}
              className="peer absolute inset-0 m-0 size-full cursor-pointer opacity-0"
            />
            <span aria-hidden className={cn(TILE, text && "px-2.5")}>
              <Icon className="size-4" />
              {text && <span className="text-xs font-medium">{text}</span>}
            </span>
          </label>
        ))}
      </div>
      <ErrorLine message={saveFailed ? t("libraryIconSaveFailed") : ""} />
    </div>
  );
}
