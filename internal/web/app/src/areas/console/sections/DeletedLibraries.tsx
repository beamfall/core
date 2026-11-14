import { FolderPlus, HardDrive, History, Loader2, RotateCcw, Trash2 } from "lucide-react";
import { type ComponentType, useId, useRef, useState } from "react";
import {
  AlertDialog,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
  AlertDialogTrigger,
} from "@/components/ui/alert-dialog";
import { Button } from "@/components/ui/button";
import { purgeLibrary, restoreLibrary } from "@/lib/graphql";
import type { LibraryPurgeResult, TrashedLibraryInfo } from "@/lib/graphql/types";
import { useDisplayLocale, useT } from "@/lib/i18n";
import { requestManifestRefresh } from "@/lib/manifestRefresh";

// "Recently deleted" (CORE-1201): the libraries an admin deleted and can still
// get back. Restore is one click, because it loses nothing. "Delete
// permanently" is the only destructive step left, so it alone asks first.

/** A date the way the viewer's language writes it, or nothing when unparseable. */
function formatDay(value: string | null | undefined, locale: string): string {
  if (!value) return "";
  const at = new Date(value);
  if (Number.isNaN(at.getTime())) return "";
  try {
    return new Intl.DateTimeFormat(locale, { dateStyle: "medium" }).format(at);
  } catch {
    return at.toLocaleDateString();
  }
}

/** A failed mutation: what failed, then the server's reason when it gave one. */
export function MutationFailure({ title, reason }: { title: string; reason: string | null }) {
  return (
    <div role="alert" className="text-sm text-destructive empty:hidden">
      {reason !== null && (
        <>
          <p className="m-0 font-medium">{title}</p>
          {reason && <p className="m-0 mt-0.5 break-words text-xs">{reason}</p>}
        </>
      )}
    </div>
  );
}

function PurgeLibraryButton({
  library,
  disabled,
  onPurged,
}: {
  library: TrashedLibraryInfo;
  disabled: boolean;
  onPurged: (result: LibraryPurgeResult) => void | Promise<void>;
}) {
  const t = useT();
  const [open, setOpen] = useState(false);
  const [busy, setBusy] = useState(false);
  // null: no failure. Otherwise the server's reason, which may be empty.
  const [error, setError] = useState<string | null>(null);
  const cancelRef = useRef<HTMLButtonElement>(null);
  const inFlight = useRef(false);

  const consequences: { icon: ComponentType<{ className?: string }>; text: string }[] = [
    { icon: History, text: t("purgeLibraryRemoves", { count: library.mediaCount }) },
    { icon: HardDrive, text: t("deleteLibraryFilesKept") },
    { icon: FolderPlus, text: t("purgeLibraryRebuild") },
  ];

  function onOpenChange(next: boolean) {
    // A purge in flight cannot be cancelled, so the dialog waits for its answer.
    if (inFlight.current) return;
    setOpen(next);
    if (next) setError(null);
  }

  async function run() {
    if (inFlight.current) return;
    inFlight.current = true;
    setBusy(true);
    setError(null);
    let result: LibraryPurgeResult;
    try {
      result = await purgeLibrary(library.id, true);
    } catch (err) {
      setError(err instanceof Error ? err.message : "");
      return;
    } finally {
      inFlight.current = false;
      setBusy(false);
    }
    setOpen(false);
    await onPurged(result);
  }

  return (
    <AlertDialog open={open} onOpenChange={onOpenChange}>
      <AlertDialogTrigger asChild>
        <Button
          type="button"
          variant="ghost"
          size="sm"
          disabled={disabled}
          aria-label={t("purgeLibraryNamed", { library: library.title })}
          className="text-destructive hover:bg-destructive/10 hover:text-destructive"
        >
          <Trash2 aria-hidden />
          {t("purgeLibrary")}
        </Button>
      </AlertDialogTrigger>
      <AlertDialogContent initialFocus={cancelRef} aria-busy={busy || undefined}>
        <AlertDialogHeader>
          <AlertDialogTitle className="break-words">
            {t("purgeLibraryTitle", { library: library.title })}
          </AlertDialogTitle>
          <AlertDialogDescription render={<div />}>
            <ul className="m-0 flex list-none flex-col gap-2.5 p-0 text-left">
              {consequences.map(({ icon: Icon, text }) => (
                <li key={text} className="flex items-start gap-2.5">
                  <Icon aria-hidden className="mt-0.5 size-4 shrink-0 text-foreground/70" />
                  <span>{text}</span>
                </li>
              ))}
            </ul>
          </AlertDialogDescription>
        </AlertDialogHeader>
        <MutationFailure title={t("purgeLibraryFailed")} reason={error} />
        <AlertDialogFooter>
          <AlertDialogCancel ref={cancelRef} disabled={busy}>
            {t("cancel")}
          </AlertDialogCancel>
          {/* A plain button, not a dialog Close: the dialog stays open until the
              server answers, so a failure can say why. aria-disabled keeps focus
              on the button while the request runs. */}
          <Button
            type="button"
            variant="destructive"
            aria-disabled={busy || undefined}
            onClick={() => void run()}
          >
            {busy ? <Loader2 aria-hidden className="animate-spin" /> : <Trash2 aria-hidden />}
            {busy ? t("deleteLibraryDeleting") : t("purgeLibrary")}
          </Button>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}

function DeletedLibraryRow({
  library,
  onRestored,
  onPurged,
}: {
  library: TrashedLibraryInfo;
  onRestored: (library: TrashedLibraryInfo) => void | Promise<void>;
  onPurged: (result: LibraryPurgeResult) => void | Promise<void>;
}) {
  const t = useT();
  const locale = useDisplayLocale();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const inFlight = useRef(false);

  async function restore() {
    if (inFlight.current) return;
    inFlight.current = true;
    setBusy(true);
    setError(null);
    try {
      await restoreLibrary(library.id);
      requestManifestRefresh();
    } catch (err) {
      setError(err instanceof Error ? err.message : "");
      return;
    } finally {
      inFlight.current = false;
      setBusy(false);
    }
    await onRestored(library);
  }

  const deletedOn = formatDay(library.trashedAt, locale);
  const purgeOn = formatDay(library.purgeAfter, locale);
  const facts = [
    deletedOn && t("deletedLibraryWhen", { date: deletedOn }),
    t("deletedLibraryItems", { count: library.mediaCount }),
    library.purgeable
      ? purgeOn && t("deletedLibraryPurgeAfter", { date: purgeOn })
      : t("deletedLibraryKept"),
  ].filter(Boolean);

  return (
    <li className="rounded-xl border border-white/10 bg-white/[0.03] px-4 py-3 backdrop-blur-sm">
      <div className="flex flex-wrap items-center justify-between gap-x-4 gap-y-2">
        <div className="min-w-0">
          <p className="m-0 truncate text-sm font-medium" title={library.title}>
            {library.title}
          </p>
          <p className="m-0 mt-0.5 text-xs leading-snug text-muted-foreground">
            {facts.join(" · ")}
          </p>
        </div>
        <div className="flex shrink-0 items-center gap-1.5">
          {/* aria-disabled, not disabled: focus stays on the button while the
              restore runs, and inFlight drops a second press. */}
          <Button
            type="button"
            variant="outline"
            size="sm"
            aria-label={t("restoreLibraryNamed", { library: library.title })}
            aria-disabled={busy || undefined}
            onClick={() => void restore()}
          >
            {busy ? <Loader2 aria-hidden className="animate-spin" /> : <RotateCcw aria-hidden />}
            {busy ? t("restoreLibraryBusy") : t("restore")}
          </Button>
          {library.purgeable && (
            <PurgeLibraryButton library={library} disabled={busy} onPurged={onPurged} />
          )}
        </div>
      </div>
      <MutationFailure title={t("restoreLibraryFailed")} reason={error} />
    </li>
  );
}

export function DeletedLibraries({
  libraries,
  onRestored,
  onPurged,
}: {
  libraries: TrashedLibraryInfo[];
  /** Runs after the server restored one; reload both lists here. */
  onRestored: (library: TrashedLibraryInfo) => void | Promise<void>;
  /** Runs after the server purged one; reload the deleted list here. */
  onPurged: (result: LibraryPurgeResult) => void | Promise<void>;
}) {
  const t = useT();
  const headingID = useId();
  if (libraries.length === 0) return null;
  return (
    <section aria-labelledby={headingID} className="space-y-2.5 pt-3">
      <div className="space-y-0.5">
        <h3 id={headingID} className="m-0 text-sm font-semibold leading-snug">
          {t("recentlyDeleted")}
        </h3>
        <p className="m-0 max-w-2xl text-[13px] leading-snug text-muted-foreground">
          {t("recentlyDeletedLead")}
        </p>
      </div>
      <ul className="m-0 grid list-none gap-2 p-0">
        {libraries.map((library) => (
          <DeletedLibraryRow
            key={library.id}
            library={library}
            onRestored={onRestored}
            onPurged={onPurged}
          />
        ))}
      </ul>
    </section>
  );
}
