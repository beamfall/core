import { useEffect, useId, useRef, useState } from "react";
import { Link } from "react-router";
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
import { Input } from "@/components/ui/input";
import {
  dismissUnmatched,
  fixMatch,
  fixMatchCandidates,
  fixMatchPreview,
  identifyLookup,
} from "@/lib/graphql";
import type { FixMatchCandidateInfo, FixMatchFieldDiff } from "@/lib/graphql/types";
import { useT } from "@/lib/i18n";
import type { FolderHint, UnmatchedGroup } from "./groups";
import { groupProblem, percent, reasonLabel } from "./reasons";

/**
 * GroupFixPanel fixes every item of one UnmatchedGroup in one step
 * (CORE-2910). It opens with the folder's title and year already searched, so
 * the common fix is one click on "Match all"; a pasted catalog ID, a preview
 * that writes nothing, naming advice and Hide stay one step away.
 */

/** The scraper id the queue records for a source with no catalog to search. */
const MANUAL_SCRAPER_ID = "manual";

export function GroupFixPanel({
  group,
  hint,
  path,
  onWriteStart,
  onFinished,
  onError,
}: {
  group: UnmatchedGroup;
  hint: FolderHint | undefined;
  path: string | undefined;
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
  const first = group.items[0];
  const source = { pluginID: group.pluginID, scraperID: group.scraperID };
  const manual = group.scraperID === MANUAL_SCRAPER_ID;
  const [query, setQuery] = useState(group.candidateTitle || hint?.title || first?.title || "");
  const [year, setYear] = useState(hint?.year ? String(hint.year) : "");
  const [candidates, setCandidates] = useState<FixMatchCandidateInfo[]>([]);
  const [selectedID, setSelectedID] = useState("");
  const [pastedID, setPastedID] = useState("");
  const [diff, setDiff] = useState<FixMatchFieldDiff[]>([]);
  const [busy, setBusy] = useState(false);
  const [progress, setProgress] = useState("");
  // The year the shown candidates were searched with: it picks the default
  // selection and the Suggested badge, so a corrected year wins over the folder.
  const [suggestedYear, setSuggestedYear] = useState<number | undefined>(undefined);
  const searched = useRef(false);
  // Bumped on every selection; a preview answered for an older one is dropped.
  const selection = useRef(0);

  const problem = groupProblem(t, group, hint);
  const count = group.items.length;

  // A new selection invalidates the preview: a diff for one entry next to
  // another entry's Match button would misreport what is about to be written.
  const select = (candidateID: string) => {
    selection.current++;
    setSelectedID(candidateID);
    setDiff([]);
  };

  const runSearch = async (text: string, yearText: string) => {
    if (manual || !text.trim() || !first) return;
    const parsedYear = Number.parseInt(yearText, 10);
    const searchYear = parsedYear > 0 ? parsedYear : undefined;
    setBusy(true);
    try {
      const scored = await fixMatchCandidates({
        mediaID: first.mediaID,
        ...source,
        query: text.trim(),
        ...(searchYear ? { year: searchYear } : {}),
      });
      setCandidates(scored);
      setSuggestedYear(searchYear);
      const preferred = searchYear
        ? scored.find((candidate) => candidate.year === searchYear)
        : undefined;
      select((preferred ?? scored[0])?.id ?? "");
    } catch {
      onError(t("unmatchedUnavailable"));
    } finally {
      setBusy(false);
    }
  };

  // The panel is keyed by group, so this runs once per group: the operator
  // lands on candidates already searched with the folder's name and year.
  useEffect(() => {
    if (searched.current) return;
    searched.current = true;
    void runSearch(query, year);
  }, []);

  // Paste-an-id: the fetched entry becomes the single candidate and flows
  // through the same preview-then-match path. It carries no score — nothing
  // was compared, so a 0% confidence would be a lie.
  const onFetchByID = async () => {
    const candidateID = pastedID.trim();
    if (!candidateID || !first) return;
    setBusy(true);
    try {
      const fetched = await identifyLookup({ mediaID: first.mediaID, ...source, candidateID });
      setSuggestedYear(undefined);
      setCandidates([
        {
          ...fetched,
          ...source,
          confidence: 0,
          threshold: group.threshold,
          scored: false,
          reason: "unscored",
        },
      ]);
      select(fetched.id || candidateID);
    } catch {
      onError(t("unmatchedPasteIDFailed"));
    } finally {
      setBusy(false);
    }
  };

  const onPreview = async () => {
    if (!selectedID || !first) return;
    const asked = selection.current;
    setBusy(true);
    try {
      const rows = await fixMatchPreview({
        mediaID: first.mediaID,
        ...source,
        candidateID: selectedID,
      });
      if (asked === selection.current) setDiff(rows);
    } catch {
      onError(t("unmatchedUnavailable"));
    } finally {
      setBusy(false);
    }
  };

  // Match all writes one item at a time so progress is honest and a refusal
  // names exactly which items are still unmatched.
  const onMatch = async () => {
    if (!selectedID) return;
    onWriteStart();
    setBusy(true);
    const failed: string[] = [];
    let done = 0;
    for (const item of group.items) {
      setProgress(t("unmatchedMatching", { done: done + failed.length + 1, total: count }));
      try {
        await fixMatch({ mediaID: item.mediaID, ...source, candidateID: selectedID });
        done++;
      } catch {
        failed.push(item.title || item.mediaID);
      }
    }
    setProgress("");
    setBusy(false);
    onFinished(
      failed.length > 0
        ? t("unmatchedMatchedSome", { done, total: count, titles: failed.join(", ") })
        : "",
    );
  };

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
          <>
            <form
              className="flex flex-wrap gap-2"
              onSubmit={(e) => {
                e.preventDefault();
                void runSearch(query, year);
              }}
            >
              <Input
                className="min-w-0 flex-1 basis-48"
                aria-label={t("unmatchedSearchLabel")}
                value={query}
                onChange={(e) => setQuery(e.target.value)}
              />
              <Input
                className="w-24"
                aria-label={t("unmatchedYearLabel")}
                inputMode="numeric"
                placeholder="YYYY"
                value={year}
                onChange={(e) => setYear(e.target.value.replace(/\D/g, "").slice(0, 4))}
              />
              <Button type="submit" size="sm" variant="outline" disabled={busy || !query.trim()}>
                {t("unmatchedSearch")}
              </Button>
            </form>

            {candidates.length === 0 ? (
              <p className="text-xs text-muted-foreground">
                {busy ? t("loading") : t("unmatchedNoCandidates")}
              </p>
            ) : (
              <>
                <p className="text-xs text-muted-foreground">{t("unmatchedPickHint")}</p>
                <ul className="space-y-2">
                  {candidates.map((candidate) => (
                    <li key={candidate.id}>
                      <button
                        type="button"
                        className="grid w-full gap-1 rounded-md border px-3 py-2 text-left hover:bg-muted/50 data-[active=true]:border-primary data-[active=true]:bg-muted/40"
                        data-active={selectedID === candidate.id}
                        aria-pressed={selectedID === candidate.id}
                        onClick={() => select(candidate.id)}
                      >
                        <div className="flex items-start justify-between gap-2">
                          <span className="min-w-0 break-words text-sm font-medium text-foreground">
                            {candidate.title}
                            {candidate.year ? ` (${candidate.year})` : ""}
                          </span>
                          <span className="flex shrink-0 gap-1">
                            {suggestedYear !== undefined && candidate.year === suggestedYear && (
                              <Badge>{t("unmatchedSuggested")}</Badge>
                            )}
                            <Badge variant={candidate.scored ? "secondary" : "outline"}>
                              {candidate.scored
                                ? percent(candidate.confidence)
                                : t("unmatchedCandidateUnscored")}
                            </Badge>
                          </span>
                        </div>
                        {candidate.reason && (
                          <span className="text-xs text-muted-foreground">
                            {reasonLabel(t, candidate.reason)}
                          </span>
                        )}
                      </button>
                    </li>
                  ))}
                </ul>
              </>
            )}

            <div className="flex flex-wrap items-center gap-2">
              <Button size="sm" disabled={busy || !selectedID} onClick={() => void onMatch()}>
                {count === 1 ? t("unmatchedApplyPin") : t("unmatchedMatchAll", { count })}
              </Button>
              <Button
                size="sm"
                variant="outline"
                disabled={busy || !selectedID}
                onClick={() => void onPreview()}
              >
                {t("unmatchedPreviewLoad")}
              </Button>
              {progress && (
                <span className="text-xs text-muted-foreground" role="status">
                  {progress}
                </span>
              )}
            </div>

            {diff.length > 0 && (
              <dl className="grid gap-2 rounded-md border p-2">
                {diff.map((row) => (
                  <div key={row.field} className="grid gap-0.5">
                    <dt className="text-xs text-muted-foreground">{row.field}</dt>
                    <dd className="break-words text-sm text-foreground">
                      {row.changed ? (
                        <>
                          <span className="text-muted-foreground line-through">
                            {row.current || "—"}
                          </span>{" "}
                          → <span className="font-medium">{row.proposed}</span>
                        </>
                      ) : (
                        <span className="text-muted-foreground">
                          {t("unmatchedPreviewUnchanged")}
                        </span>
                      )}
                    </dd>
                  </div>
                ))}
              </dl>
            )}

            <details className="rounded-md border px-3 py-2">
              <summary className="cursor-pointer text-sm text-foreground">
                {t("unmatchedKnowID")}
              </summary>
              <div className="mt-2 grid gap-1">
                <p className="text-xs text-muted-foreground">{t("unmatchedPasteIDHint")}</p>
                <div className="flex gap-2">
                  <Input
                    aria-label={t("unmatchedPasteIDLabel")}
                    value={pastedID}
                    onChange={(e) => setPastedID(e.target.value)}
                  />
                  <Button
                    size="sm"
                    variant="outline"
                    disabled={busy || !pastedID.trim()}
                    onClick={() => void onFetchByID()}
                  >
                    {t("unmatchedPasteIDFetch")}
                  </Button>
                </div>
              </div>
            </details>
          </>
        )}

        <div className="grid gap-2 border-t pt-3">
          <h4 className="text-sm font-semibold text-foreground">{t("unmatchedNamingHeading")}</h4>
          <p className="text-xs text-muted-foreground">{t("unmatchedNamingLead")}</p>
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
                  disabled={busy}
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
                <Button size="sm" variant="ghost" className="justify-self-start" disabled={busy}>
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
