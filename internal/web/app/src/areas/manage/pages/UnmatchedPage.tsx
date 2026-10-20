import { useCallback, useEffect, useMemo, useState } from "react";
import { EmptyLine, ErrorLine, ManageSection } from "@/components/beamfall/workbench";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
  AlertDialogTrigger,
} from "@/components/ui/alert-dialog";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import {
  dismissUnmatched,
  fetchLibraries,
  fetchUnmatchedQueue,
  runHealthBulkOp,
} from "@/lib/graphql";
import type { LibraryInfo, UnmatchedItemInfo } from "@/lib/graphql/types";
import { useT } from "@/lib/i18n";
import { FixMatchPanel } from "./unmatched/FixMatchPanel";
import { RecordInbox } from "./unmatched/RecordInbox";
import { percent, reasonLabel } from "./unmatched/reasons";

// Unmatched records (ADR-0003 §4, ADR-0037 §2.3): scan and import records the
// pipeline could not auto-match. Migration Wizard unmatched items land here via
// the same store queue. The operator can search candidates, fix-match a row,
// dismiss individual records, or bulk-rescan a Library to retry matching.
export function UnmatchedPage() {
  const t = useT();
  const [items, setItems] = useState<UnmatchedItemInfo[]>([]);
  const [libraries, setLibraries] = useState<LibraryInfo[]>([]);
  const [libraryID, setLibraryID] = useState("");
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [rescanMsg, setRescanMsg] = useState("");
  const [activeID, setActiveID] = useState("");

  const activeItem = useMemo(
    () => items.find((item) => item.mediaID === activeID) ?? items[0],
    [activeID, items],
  );

  const load = useCallback(async () => {
    setError("");
    setLoading(true);
    try {
      const [queue, libs] = await Promise.all([fetchUnmatchedQueue(), fetchLibraries()]);
      setItems(queue);
      setLibraries(libs);
      if (!activeID && queue[0]) setActiveID(queue[0].mediaID);
      if (libs.length > 0 && !libraryID) setLibraryID(libs[0].id);
    } catch {
      setError(t("unmatchedUnavailable"));
    } finally {
      setLoading(false);
    }
  }, [t, libraryID, activeID]);

  useEffect(() => {
    void load();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const onDismiss = async (item: UnmatchedItemInfo) => {
    setError("");
    try {
      await dismissUnmatched(item.mediaID);
      setItems((prev) => prev.filter((i) => i.mediaID !== item.mediaID));
      if (activeID === item.mediaID) setActiveID("");
    } catch {
      setError(t("unmatchedUnavailable"));
    }
  };

  const onApplied = (item: UnmatchedItemInfo) => {
    setItems((prev) => prev.filter((i) => i.mediaID !== item.mediaID));
    setActiveID("");
  };

  // Clear queue is a destructive batch: it is confirm-gated, and because the
  // server may have dismissed some rows before one call failed, the list is
  // reloaded from the server afterwards instead of trusting local state.
  const onClearQueue = async () => {
    setError("");
    const results = await Promise.allSettled(items.map((item) => dismissUnmatched(item.mediaID)));
    setActiveID("");
    await load();
    if (results.some((r) => r.status === "rejected")) setError(t("unmatchedUnavailable"));
  };

  const onRescan = async () => {
    if (!libraryID) return;
    setError("");
    setRescanMsg("");
    try {
      const result = await runHealthBulkOp("rescanUnmatched", libraryID);
      setRescanMsg(`${result.affected} ${t("bulkOpsAffected")}`);
    } catch {
      setError(t("unmatchedUnavailable"));
    }
  };

  return (
    <div className="space-y-4">
      <ManageSection
        title={t("unmatchedHeading")}
        lead={t("unmatchedLead")}
        actions={
          <div className="flex items-center gap-2">
            {libraries.length > 0 && (
              <Select value={libraryID} onValueChange={setLibraryID}>
                <SelectTrigger className="w-44">
                  <SelectValue placeholder={t("library")} />
                </SelectTrigger>
                <SelectContent>
                  {libraries.map((lib) => (
                    <SelectItem key={lib.id} value={lib.id}>
                      {lib.title}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            )}
            <Button
              variant="outline"
              size="sm"
              disabled={!libraryID}
              onClick={() => void onRescan()}
            >
              {t("unmatchedRescan")}
            </Button>
            <AlertDialog>
              <AlertDialogTrigger asChild>
                <Button variant="ghost" size="sm" disabled={items.length === 0}>
                  {t("unmatchedClearQueue")}
                </Button>
              </AlertDialogTrigger>
              <AlertDialogContent>
                <AlertDialogHeader>
                  <AlertDialogTitle>{t("unmatchedClearQueue")}</AlertDialogTitle>
                  <AlertDialogDescription>
                    {t("unmatchedOpenCount", { count: String(items.length) })}
                  </AlertDialogDescription>
                </AlertDialogHeader>
                <AlertDialogFooter>
                  <AlertDialogCancel>{t("cancel")}</AlertDialogCancel>
                  <AlertDialogAction
                    onClick={() => void onClearQueue()}
                    className="bg-destructive text-destructive-foreground hover:bg-destructive/90"
                  >
                    {t("unmatchedClearQueue")}
                  </AlertDialogAction>
                </AlertDialogFooter>
              </AlertDialogContent>
            </AlertDialog>
          </div>
        }
      >
        <ErrorLine message={error} />
        {rescanMsg && <p className="mb-2 text-sm text-muted-foreground">{rescanMsg}</p>}
        {loading ? (
          <EmptyLine message={t("loading")} />
        ) : items.length === 0 ? (
          <EmptyLine message={t("unmatchedEmpty")} />
        ) : (
          <div className="grid gap-3 xl:grid-cols-[minmax(220px,0.9fr)_minmax(320px,1.15fr)_minmax(260px,0.9fr)]">
            <section className="rounded-md border border-border bg-card">
              <div className="flex items-center justify-between border-b px-3 py-2">
                <h3 className="text-sm font-semibold text-foreground">{t("unmatchedQueue")}</h3>
                <span className="text-xs text-muted-foreground">
                  {t("unmatchedOpenCount", { count: String(items.length) })}
                </span>
              </div>
              <div className="divide-y">
                {items.map((item) => (
                  <button
                    type="button"
                    key={item.mediaID}
                    className="grid w-full gap-1 px-3 py-2 text-left hover:bg-muted/50 data-[active=true]:bg-muted"
                    data-active={activeItem?.mediaID === item.mediaID}
                    onClick={() => setActiveID(item.mediaID)}
                  >
                    <div className="flex items-start justify-between gap-2">
                      <span className="min-w-0 break-words font-medium text-foreground">
                        {item.title || item.mediaID}
                      </span>
                      <Badge variant={item.confidence < item.threshold ? "outline" : "secondary"}>
                        {percent(item.confidence)}
                      </Badge>
                    </div>
                    <span className="text-xs text-muted-foreground">
                      {item.candidateTitle
                        ? t("unmatchedParsedGuess", {
                            title: item.candidateTitle,
                          })
                        : t("unmatchedNoParsedGuess")}
                    </span>
                    <span className="text-xs text-muted-foreground">
                      {t("unmatchedThreshold", {
                        threshold: percent(item.threshold),
                      })}{" "}
                      · {item.pluginID}/{item.scraperID}
                    </span>
                    {item.failureReason && (
                      <span className="text-xs text-muted-foreground">
                        {reasonLabel(t, item.failureReason)}
                      </span>
                    )}
                  </button>
                ))}
              </div>
            </section>

            {activeItem && (
              <FixMatchPanel
                key={activeItem.mediaID}
                item={activeItem}
                onApplied={() => onApplied(activeItem)}
                onError={setError}
              />
            )}

            {activeItem && (
              <aside className="rounded-md border border-border bg-card">
                <div className="border-b px-3 py-2">
                  <h3 className="text-sm font-semibold text-foreground">
                    {t("unmatchedResolution")}
                  </h3>
                  <p className="text-xs text-muted-foreground">
                    {activeItem.mediaID ? t("unmatchedAssociateMode") : t("unmatchedCreateMode")}
                  </p>
                </div>
                <div className="space-y-3 p-3">
                  <div className="grid gap-2 text-sm">
                    <div>
                      <div className="text-xs text-muted-foreground">{t("title")}</div>
                      <div className="font-medium text-foreground">{activeItem.title}</div>
                    </div>
                    <div>
                      <div className="text-xs text-muted-foreground">
                        {t("unmatchedFailureReason")}
                      </div>
                      <div className="font-medium text-foreground">
                        {reasonLabel(t, activeItem.failureReason) || t("none")}
                      </div>
                    </div>
                    <div>
                      <div className="text-xs text-muted-foreground">{t("unmatchedSource")}</div>
                      <div className="font-medium text-foreground">
                        {activeItem.pluginID}/{activeItem.scraperID}
                      </div>
                    </div>
                  </div>
                  <Button size="sm" variant="ghost" onClick={() => void onDismiss(activeItem)}>
                    {t("unmatchedDismiss")}
                  </Button>
                </div>
              </aside>
            )}
          </div>
        )}
      </ManageSection>
      <RecordInbox libraryID={libraryID} />
    </div>
  );
}
