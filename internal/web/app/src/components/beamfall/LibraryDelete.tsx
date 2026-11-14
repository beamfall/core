import { Trash2 } from "lucide-react";
import { useRef, useState } from "react";
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
import { deleteLibrary } from "@/lib/graphql";
import { useT } from "@/lib/i18n";
import { requestManifestRefresh } from "@/lib/manifestRefresh";

// Delete a Library from Console > Libraries or Manage > Libraries (CORE-1171).
// The server mutation is an audited soft delete: the Library leaves every store
// query while media rows, history and files on disk stay. The copy names where
// to restore it: Recently deleted in Console > Libraries (CORE-1201). A failure
// keeps the dialog open with the error so the owner can retry. After a delete
// the trigger leaves the page with its card, so focus moves to the heading of
// the surrounding section instead of falling back to the document body;
// Console > Libraries then moves it on to its deletion notice and Undo.
export function DeleteLibraryButton({
  library,
  onDeleted,
}: {
  library: { id: string; title: string };
  onDeleted: () => void;
}) {
  const t = useT();
  const [open, setOpen] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const triggerRef = useRef<HTMLButtonElement>(null);
  const focusAfterDelete = useRef<HTMLElement | null>(null);

  function changeOpen(next: boolean) {
    if (busy) return;
    setOpen(next);
    if (next) setError("");
  }

  async function run() {
    setBusy(true);
    setError("");
    try {
      await deleteLibrary(library.id);
      requestManifestRefresh();
    } catch (err) {
      setError(err instanceof Error && err.message ? err.message : t("deleteLibraryFailed"));
      setBusy(false);
      return;
    }
    focusAfterDelete.current = regionHeading(triggerRef.current);
    setBusy(false);
    setOpen(false);
    onDeleted();
  }

  return (
    <AlertDialog open={open} onOpenChange={changeOpen}>
      <AlertDialogTrigger asChild ref={triggerRef}>
        <Button variant="destructive" size="sm">
          <Trash2 aria-hidden="true" />
          {t("deleteLibrary")}
        </Button>
      </AlertDialogTrigger>
      <AlertDialogContent finalFocus={() => focusAfterDelete.current ?? true}>
        <AlertDialogHeader>
          <AlertDialogTitle>{t("deleteLibraryTitle", { library: library.title })}</AlertDialogTitle>
          <AlertDialogDescription>{t("deleteLibraryBody")}</AlertDialogDescription>
        </AlertDialogHeader>
        {error ? (
          <p role="alert" className="text-sm text-destructive">
            {error}
          </p>
        ) : null}
        <AlertDialogFooter>
          <AlertDialogCancel disabled={busy}>{t("cancel")}</AlertDialogCancel>
          {/* A plain button, not AlertDialogAction: that is a Close primitive and
              would dismiss the dialog before a failure could be shown. */}
          <Button variant="destructive" disabled={busy} onClick={() => void run()}>
            {busy ? t("deleteLibraryDeleting") : t("deleteLibrary")}
          </Button>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}

// The heading of the Console section or Manage card that lists the libraries.
function regionHeading(from: HTMLElement | null): HTMLElement | null {
  const heading = from
    ?.closest("section, [data-slot=card]")
    ?.querySelector<HTMLElement>("h1, h2, [role=heading]");
  if (!heading) return null;
  if (!heading.hasAttribute("tabindex")) heading.tabIndex = -1;
  return heading;
}
