import { useId, useState } from "react";
import { Link } from "react-router";
import { AssignPanel } from "@/components/assign/AssignPanel";
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
import { Button } from "@/components/ui/button";
import { dismissUnmatched } from "@/lib/graphql";
import { useT } from "@/lib/i18n";
import type { FolderHint, UnmatchedGroup } from "./groups";
import { groupProblem } from "./reasons";

/** The scraper id the queue records for a source with no catalog to search. */
const MANUAL_SCRAPER_ID = "manual";

export function GroupFixPanel({
  group,
  hint,
  path,
  locked,
  onWriteStart,
  onFinished,
  onError,
}: {
  group: UnmatchedGroup;
  hint: FolderHint | undefined;
  path: string | undefined;
  /** True while any write on the page runs or its reload is pending. */
  locked: boolean;
  /**
   * Called before a write starts. The page holds the panel on this group until
   * onFinished, so a second panel for the same items cannot start a competing
   * batch while the first is still writing.
   */
  onWriteStart: () => void;
  /** Called after any write so the page reloads from the server. */
  onFinished: (message: string) => void;
  onError: (message: string) => void;
}) {
  const t = useT();
  const hideNoteID = useId();
  const [first, ...rest] = group.items;
  const manual = group.scraperID === MANUAL_SCRAPER_ID;
  const [busy, setBusy] = useState(false);
  const [panelBusy, setPanelBusy] = useState(false);
  const blocked = busy || locked || panelBusy;
  const [lastSearchYear, setLastSearchYear] = useState<number | undefined | null>(null);
  const problem = groupProblem(
    t,
    group,
    lastSearchYear === null || !hint ? hint : { ...hint, year: lastSearchYear },
  );
  const count = group.items.length;

  const hide = async (mediaIDs: string[]) => {
    onWriteStart();
    setBusy(true);
    const results = await Promise.allSettled(mediaIDs.map((id) => dismissUnmatched(id)));
    setBusy(false);
    onFinished(results.some((r) => r.status === "rejected") ? t("unmatchedUnavailable") : "");
  };

  return (
    <section className="min-w-0 rounded-md border border-border bg-card">
      <div className="space-y-1 border-b px-3 py-2">
        <p className="text-sm text-foreground">{problem.why}</p>
        {problem.hint && <p className="text-xs text-muted-foreground">{problem.hint}</p>}
      </div>

      <div className="space-y-3 p-3">
        {manual ? (
          <p className="text-xs text-muted-foreground">{t("unmatchedManualNoCatalog")}</p>
        ) : (
          first && (
            <AssignPanel
              items={[first, ...rest]}
              sources={[
                {
                  pluginID: group.pluginID,
                  scraperID: group.scraperID,
                  label: group.scraperID,
                  acceptsPastedID: true,
                },
              ]}
              initialQuery={hint?.title || group.candidateTitle || first.title}
              initialYear={hint?.year}
              unmatched={{ threshold: group.threshold, onSearchYear: setLastSearchYear, onError }}
              blocked={busy || locked}
              onActivityChange={setPanelBusy}
              onWriteChange={(writing) => {
                if (writing) onWriteStart();
              }}
              onFinished={(results, interrupted) => {
                if (interrupted !== undefined) {
                  onFinished(t("assignInterrupted", { done: interrupted, total: count }));
                  return;
                }
                const failed = results.filter((result) => result.error);
                const titles = failed.map(
                  ({ mediaID }) =>
                    group.items.find((item) => item.mediaID === mediaID)?.title || mediaID,
                );
                onFinished(
                  failed.length
                    ? t("unmatchedMatchedSome", {
                        done: count - failed.length,
                        total: count,
                        titles: titles.join(", "),
                      })
                    : "",
                );
              }}
            />
          )
        )}

        <div className="grid gap-2 border-t pt-3">
          <h4 className="text-sm font-semibold text-foreground">{t("unmatchedNamingHeading")}</h4>
          {!manual && <p className="text-xs text-muted-foreground">{t("unmatchedNamingLead")}</p>}
          <code className="break-all rounded bg-muted px-2 py-1 text-xs text-foreground">
            {path || t("unmatchedNoPath")}
          </code>
          <div className="flex flex-wrap items-center gap-2">
            <Button size="sm" variant="outline" asChild>
              <Link to="/manage/libraries">{t("unmatchedOpenLibraries")}</Link>
            </Button>
            <Button size="sm" variant="outline" asChild>
              <Link to="/manage/rename">{t("unmatchedOpenRename")}</Link>
            </Button>
          </div>
        </div>

        <div className="grid gap-2 border-t pt-3">
          <p id={hideNoteID} className="text-xs text-muted-foreground">
            {t("unmatchedHideBody")}
          </p>
          <ul className="divide-y rounded-md border">
            {group.items.map((item) => (
              <li
                key={item.mediaID}
                className="flex items-center justify-between gap-2 px-3 py-1.5"
              >
                <span className="min-w-0 break-words text-sm text-foreground">
                  {item.title || item.mediaID}
                </span>
                <Button
                  size="sm"
                  variant="ghost"
                  disabled={blocked}
                  aria-label={`${t("unmatchedHide")}: ${item.title || item.mediaID}`}
                  aria-describedby={hideNoteID}
                  onClick={() => void hide([item.mediaID])}
                >
                  {t("unmatchedHide")}
                </Button>
              </li>
            ))}
          </ul>
          {count > 1 && (
            <AlertDialog>
              <AlertDialogTrigger asChild>
                <Button size="sm" variant="ghost" className="justify-self-start" disabled={blocked}>
                  {t("unmatchedHideAll", { count })}
                </Button>
              </AlertDialogTrigger>
              <AlertDialogContent>
                <AlertDialogHeader>
                  <AlertDialogTitle>{t("unmatchedHideTitle")}</AlertDialogTitle>
                  <AlertDialogDescription>{t("unmatchedHideBody")}</AlertDialogDescription>
                </AlertDialogHeader>
                <AlertDialogFooter>
                  <AlertDialogCancel>{t("cancel")}</AlertDialogCancel>
                  <AlertDialogAction
                    onClick={() => void hide(group.items.map((item) => item.mediaID))}
                  >
                    {t("unmatchedHideAll", { count })}
                  </AlertDialogAction>
                </AlertDialogFooter>
              </AlertDialogContent>
            </AlertDialog>
          )}
        </div>
      </div>
    </section>
  );
}
