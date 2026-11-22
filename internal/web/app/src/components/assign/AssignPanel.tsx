import { useEffect, useId, useRef, useState } from "react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { getIdentityEpochSignal } from "@/lib/api";
import {
  fixMatch,
  fixMatchCandidates,
  fixMatchLockedFields,
  fixMatchPreview,
  identifyLookup,
} from "@/lib/graphql";
import type {
  FixMatchCurrentInfo,
  FixMatchLockedFieldInfo,
  FixMatchSourceInfo,
} from "@/lib/graphql/manage";
import type { FixMatchCandidateInfo, FixMatchFieldDiff } from "@/lib/graphql/types";
import { useT } from "@/lib/i18n";
import type { StringKey } from "@/strings";
import { useAssignInterruptionNotice } from "./AssignInterruptionNotice";
import { percent, reasonLabel } from "./matchLabels";

export interface AssignItem {
  mediaID: string;
  title: string;
  year?: number;
  sequence?: number;
  index?: number;
  current?: FixMatchCurrentInfo | null;
}

type Source = Pick<FixMatchSourceInfo, "pluginID" | "scraperID" | "label" | "acceptsPastedID">;
type Pairing = {
  mediaID: string;
  diff: FixMatchFieldDiff[];
  locked: FixMatchLockedFieldInfo[];
  clearLocks: string[];
};
type Result = { mediaID: string; error?: string };
type Preview = { rows: Pairing[]; epoch: AbortSignal | undefined };

interface UnmatchedFlow {
  threshold: number;
  onSearchYear: (year: number | undefined) => void;
  onError: (message: string) => void;
}

const fieldLabels: Record<string, StringKey> = {
  title: "itemMetadataTitle",
  summary: "itemMetadataOverview",
  year: "itemMetadataYear",
  sortTitle: "itemMetadataSortTitle",
  artworkPoster: "artworkSlotPoster",
  artworkBackdrop: "artworkSlotBackdrop",
  artworkThumb: "artworkSlotThumb",
};

function sourceKey(source: Pick<Source, "pluginID" | "scraperID">) {
  return JSON.stringify([source.pluginID, source.scraperID]);
}

export function AssignPanel({
  items,
  sources,
  defaultSource,
  matchSources = sources,
  dialog = false,
  initialQuery = items[0].title,
  initialYear = items[0].year,
  unmatched,
  blocked = false,
  onWriteChange,
  onActivityChange,
  onFinished,
}: {
  items: readonly [AssignItem, ...AssignItem[]];
  sources: readonly Source[];
  defaultSource?: Source;
  matchSources?: readonly Source[];
  dialog?: boolean;
  initialQuery?: string;
  initialYear?: number;
  unmatched?: UnmatchedFlow;
  blocked?: boolean;
  onWriteChange?: (busy: boolean) => void;
  onActivityChange?: (busy: boolean) => void;
  onFinished: (results: readonly Result[], interrupted?: number) => void;
}) {
  const t = useT();
  const showInterruptionNotice = useAssignInterruptionNotice();
  const heading = useId();
  const first = items[0];
  const [source, setSource] = useState(defaultSource ?? sources[0]);
  const [query, setQuery] = useState(initialQuery);
  const [year, setYear] = useState(initialYear ? String(initialYear) : "");
  const [candidates, setCandidates] = useState<FixMatchCandidateInfo[]>([]);
  const [selectedID, setSelectedID] = useState("");
  const [pastedID, setPastedID] = useState("");
  const [approval, setApproval] = useState<Preview | null>(null);
  const [confirmed, setConfirmed] = useState(false);
  const [results, setResults] = useState<Result[]>([]);
  const [interrupted, setInterrupted] = useState<number>();
  const [progress, setProgress] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [suggestedYear, setSuggestedYear] = useState<number>();
  const searched = useRef(false);
  const selection = useRef(0);
  const live = useRef(true);
  const preview = approval?.rows;

  const multi = items.length > 1 && !unmatched;
  // A single candidate cannot describe multiple child positions in this flow.
  // Only an unnumbered selection can share a candidate after confirmation.
  const ordinalConflict =
    multi &&
    items.some((item) => typeof item.sequence === "number" || typeof item.index === "number");
  const disabled = busy || blocked;
  const fieldLabel = (field: string) =>
    Object.hasOwn(fieldLabels, field) ? t(fieldLabels[field]) : field;
  const report = (message: string) => {
    if (!live.current) return;
    if (unmatched) unmatched.onError(message);
    else setError(message);
  };

  const setActivity = (active: boolean) => {
    setBusy(active);
    if (live.current) onActivityChange?.(active);
  };

  const select = (id: string) => {
    selection.current++;
    setSelectedID(id);
    setApproval(null);
    setConfirmed(false);
    setResults([]);
    setError("");
  };

  const runSearch = async () => {
    if (!source || !query.trim()) return;
    const searchYear = Number(year) || undefined;
    select("");
    setCandidates([]);
    setActivity(true);
    try {
      const scored = await fixMatchCandidates({
        mediaID: first.mediaID,
        pluginID: source.pluginID,
        scraperID: source.scraperID,
        query: query.trim(),
        ...(searchYear ? { year: searchYear } : {}),
      });
      setCandidates(scored);
      setSuggestedYear(searchYear);
      if (live.current) unmatched?.onSearchYear(searchYear);
      const preferred = searchYear
        ? scored.find((candidate) => candidate.year === searchYear)
        : undefined;
      select((preferred ?? scored[0])?.id ?? "");
    } catch {
      report(t(unmatched ? "unmatchedUnavailable" : "assignSearchFailed"));
    } finally {
      setActivity(false);
    }
  };

  useEffect(() => {
    live.current = true;
    return () => {
      selection.current++;
      live.current = false;
    };
  }, []);

  // Unmatched still opens on its folder's search, with the suggested candidate selected.
  useEffect(() => {
    if (!unmatched || searched.current) return;
    searched.current = true;
    void runSearch();
  }, []);

  const selected = candidates.find((candidate) => candidate.id === selectedID);
  // Older unmatched rows are tied to their recorded source. In Assign, use
  // the source carried by the chosen search result when the provider supplies it.
  const target = (candidateID: string) => ({
    pluginID: (!unmatched && selected?.pluginID) || source.pluginID,
    scraperID: (!unmatched && selected?.scraperID) || source.scraperID,
    candidateID,
  });

  const loadPreview = async (candidateID: string, fromPaste = false) => {
    if (!candidateID) return;
    if (fromPaste) {
      setCandidates([]);
      select(candidateID);
    } else {
      select(selectedID);
    }
    const asked = selection.current;
    const epoch = getIdentityEpochSignal();
    const candidate = fromPaste
      ? { pluginID: source.pluginID, scraperID: source.scraperID, candidateID }
      : target(candidateID);
    setActivity(true);
    try {
      const rows = await Promise.all(
        (unmatched ? [first] : items).map(async ({ mediaID }) => {
          const [diff, locked] = await Promise.all([
            fixMatchPreview({ mediaID, ...candidate }),
            unmatched ? [] : fixMatchLockedFields(mediaID),
          ]);
          return { mediaID, diff, locked, clearLocks: [] };
        }),
      );
      if (asked === selection.current && !epoch?.aborted && epoch === getIdentityEpochSignal()) {
        setApproval({ rows, epoch });
      }
    } catch {
      if (asked === selection.current && !epoch?.aborted && epoch === getIdentityEpochSignal())
        report(t(unmatched ? "unmatchedUnavailable" : "assignPreviewFailed"));
    } finally {
      setActivity(false);
    }
  };

  const fetchUnmatchedID = async () => {
    const candidateID = pastedID.trim();
    if (!candidateID || !unmatched) return;
    setActivity(true);
    try {
      const fetched = await identifyLookup({ mediaID: first.mediaID, ...target(candidateID) });
      setSuggestedYear(undefined);
      setCandidates([
        {
          ...fetched,
          id: fetched.id || candidateID,
          pluginID: source.pluginID,
          scraperID: source.scraperID,
          confidence: 0,
          threshold: unmatched.threshold,
          scored: false,
          reason: "unscored",
        },
      ]);
      select(fetched.id || candidateID);
    } catch {
      report(t("unmatchedPasteIDFailed"));
    } finally {
      setActivity(false);
    }
  };

  const canApply =
    !!selectedID &&
    !disabled &&
    (!!unmatched ||
      (!!preview && results.length === 0 && !ordinalConflict && (!multi || confirmed)));

  const apply = async () => {
    if (!canApply) return;
    const candidate = target(selectedID);
    const epoch = approval ? approval.epoch : getIdentityEpochSignal();
    const identityEnded = () => epoch?.aborted || epoch !== getIdentityEpochSignal();
    onWriteChange?.(true);
    setActivity(true);
    const next: Result[] = [];
    for (const [index, item] of items.entries()) {
      if (identityEnded()) break;
      const { mediaID } = item;
      setProgress(t("unmatchedMatching", { done: index + 1, total: items.length }));
      const clearLocks = preview?.find((row) => row.mediaID === mediaID)?.clearLocks;
      try {
        await fixMatch(
          { mediaID, ...candidate, ...(clearLocks?.length ? { clearLocks } : {}) },
          epoch,
        );
        next.push({ mediaID });
      } catch (err) {
        if (identityEnded()) break;
        next.push({
          mediaID,
          error: (err instanceof Error && err.message) || t("assignApplyFailed"),
        });
      }
      setResults([...next]);
    }
    const confirmedChanges =
      next.length < items.length ? next.filter((result) => !result.error).length : undefined;
    if (confirmedChanges !== undefined) {
      setInterrupted(confirmedChanges);
      showInterruptionNotice?.({ done: confirmedChanges, total: items.length });
    }
    setProgress("");
    setActivity(false);
    if (live.current) {
      onWriteChange?.(false);
      onFinished(next, confirmedChanges);
    }
  };

  const toggleLock = (mediaID: string, field: string, checked: boolean) => {
    setApproval((current) => {
      if (!current) return null;
      const rows = current.rows.map((row) =>
        row.mediaID === mediaID
          ? {
              ...row,
              clearLocks: checked
                ? [...row.clearLocks, field]
                : row.clearLocks.filter((name) => name !== field),
            }
          : row,
      );
      return { ...current, rows };
    });
  };

  const resultText = (result: Result) =>
    result.error ? t("assignResultFailed", { error: result.error }) : t("assignApplied");

  if (interrupted !== undefined) {
    return (
      <p role="status" className="text-sm">
        {t("assignInterrupted", { done: interrupted, total: items.length })}
      </p>
    );
  }

  return (
    <section aria-labelledby={dialog ? undefined : heading} className="space-y-3">
      {!dialog && (
        <h3 id={heading} className="text-sm font-semibold">
          {t("assignHeading")}
        </h3>
      )}
      {!unmatched && (
        <ul className="text-sm text-muted-foreground">
          {items.map(({ mediaID, title, current }) => (
            <li key={mediaID}>
              {multi && <strong>{title || mediaID} · </strong>}
              {current
                ? t("assignCurrent", {
                    title: current.year ? `${current.title} (${current.year})` : current.title,
                    source:
                      matchSources.find((entry) => sourceKey(entry) === sourceKey(current))
                        ?.label ?? current.scraperID,
                  })
                : t("assignNotMatched")}
            </li>
          ))}
        </ul>
      )}
      {!source ? (
        <p className="text-sm text-muted-foreground">{t("assignNoSources")}</p>
      ) : (
        <>
          {sources.length > 1 && (
            <label className="grid gap-1 text-sm">
              {t("assignSource")}
              <select
                className="rounded-md border bg-card p-2"
                value={sourceKey(source)}
                disabled={disabled}
                onChange={(event) => {
                  const next = sources.find((entry) => sourceKey(entry) === event.target.value);
                  if (!next) return;
                  setSource(next);
                  setCandidates([]);
                  setPastedID("");
                  select("");
                }}
              >
                {sources.map((entry) => (
                  <option key={sourceKey(entry)} value={sourceKey(entry)}>
                    {entry.label}
                  </option>
                ))}
              </select>
            </label>
          )}

          <form
            className="flex flex-wrap gap-2"
            onSubmit={(event) => {
              event.preventDefault();
              if (!disabled) void runSearch();
            }}
          >
            <Input
              className="min-w-0 flex-1 basis-48"
              aria-label={t("unmatchedSearchLabel")}
              value={query}
              disabled={disabled}
              onChange={(event) => setQuery(event.target.value)}
            />
            <Input
              className="w-24"
              aria-label={t("unmatchedYearLabel")}
              inputMode="numeric"
              placeholder="YYYY"
              value={year}
              disabled={disabled}
              onChange={(event) => setYear(event.target.value.replace(/\D/g, "").slice(0, 4))}
            />
            <Button type="submit" size="sm" variant="outline" disabled={disabled || !query.trim()}>
              {t("unmatchedSearch")}
            </Button>
          </form>

          {candidates.length === 0 ? (
            <p className="text-xs text-muted-foreground">
              {busy ? t("loading") : t("unmatchedNoCandidates")}
            </p>
          ) : (
            <>
              <p className="text-xs text-muted-foreground">
                {t(unmatched ? "unmatchedPickHint" : "assignPickHint")}
              </p>
              <ul className="space-y-2">
                {candidates.map((candidate) => (
                  <li key={candidate.id}>
                    <button
                      type="button"
                      className="grid w-full gap-1 rounded-md border px-3 py-2 text-left hover:bg-muted/50 data-[active=true]:border-primary data-[active=true]:bg-muted/40"
                      data-active={selectedID === candidate.id}
                      aria-pressed={selectedID === candidate.id}
                      disabled={blocked || (!unmatched && busy)}
                      onClick={() => select(candidate.id)}
                    >
                      <div className="flex items-start justify-between gap-2">
                        <span className="min-w-0 break-words text-sm font-medium">
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
            {unmatched && (
              <Button size="sm" disabled={!canApply} onClick={() => void apply()}>
                {items.length === 1
                  ? t("unmatchedApplyPin")
                  : t("unmatchedMatchAll", { count: items.length })}
              </Button>
            )}
            <Button
              size="sm"
              variant="outline"
              disabled={disabled || !selectedID || (!unmatched && results.length > 0)}
              onClick={() => void loadPreview(selectedID)}
            >
              {t("unmatchedPreviewLoad")}
            </Button>
          </div>

          {source.acceptsPastedID && (
            <details className="rounded-md border px-3 py-2">
              <summary className="cursor-pointer text-sm">{t("unmatchedKnowID")}</summary>
              <form
                className="mt-2 flex gap-2"
                onSubmit={(event) => {
                  event.preventDefault();
                  if (disabled) return;
                  if (unmatched) void fetchUnmatchedID();
                  else void loadPreview(pastedID.trim(), true);
                }}
              >
                <Input
                  aria-label={t("unmatchedPasteIDLabel")}
                  value={pastedID}
                  disabled={disabled}
                  onChange={(event) => {
                    setPastedID(event.target.value);
                    if (!unmatched && selectedID && !selected && results.length === 0) select("");
                  }}
                />
                <Button
                  type="submit"
                  size="sm"
                  variant="outline"
                  disabled={disabled || !pastedID.trim()}
                >
                  {t(unmatched ? "unmatchedPasteIDFetch" : "assignPreviewID")}
                </Button>
              </form>
            </details>
          )}

          {multi && preview && (
            <div className="overflow-x-auto">
              <table className="w-full text-left text-sm">
                <thead>
                  <tr className="border-b">
                    <th scope="col" className="p-2">
                      {t("assignItem")}
                    </th>
                    <th scope="col" className="p-2">
                      {t("unmatchedSelectedMatch")}
                    </th>
                    <th scope="col" className="p-2">
                      {t("assignResult")}
                    </th>
                  </tr>
                </thead>
                <tbody>
                  {items.map((item) => {
                    const result = results.find((entry) => entry.mediaID === item.mediaID);
                    return (
                      <tr key={item.mediaID} className="border-b">
                        <th scope="row" className="p-2 font-medium">
                          {item.title || item.mediaID}
                        </th>
                        <td className="p-2">
                          {selected
                            ? selected.year
                              ? `${selected.title} (${selected.year})`
                              : selected.title
                            : selectedID}
                        </td>
                        <td className="p-2" aria-live="polite">
                          {result ? resultText(result) : "—"}
                        </td>
                      </tr>
                    );
                  })}
                </tbody>
              </table>
            </div>
          )}

          {preview?.map((row, index) => (
            <div key={row.mediaID} className="space-y-2 rounded-md border p-2">
              {!unmatched && (
                <h4 className="text-sm font-semibold">
                  {multi ? items[index].title || row.mediaID : t("assignPreviewHeading")}
                </h4>
              )}
              <dl className="grid gap-2">
                {row.diff.map((change) => (
                  <div key={change.field} className="grid gap-0.5">
                    <dt className="text-xs text-muted-foreground">{fieldLabel(change.field)}</dt>
                    <dd className="break-words text-sm">
                      {change.changed ? (
                        <>
                          <span className="text-muted-foreground line-through">
                            {change.current || "—"}
                          </span>
                          {" → "}
                          <span className="font-medium">{change.proposed}</span>
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
              {!unmatched && (
                <fieldset
                  disabled={disabled || results.length > 0}
                  className="space-y-2 border-t pt-2 text-sm"
                >
                  <legend className="font-semibold">{t("assignLockedFields")}</legend>
                  {row.locked.length === 0 && <p>{t("assignNoLocks")}</p>}
                  {row.locked.map(({ field, value }) => (
                    <label key={field} className="flex items-start gap-2">
                      <input
                        type="checkbox"
                        className="mt-1"
                        checked={row.clearLocks.includes(field)}
                        onChange={(event) => toggleLock(row.mediaID, field, event.target.checked)}
                      />
                      <span className="min-w-0 break-words">
                        {t("assignReplaceLock", { field: fieldLabel(field) })}
                        {value && <span className="block text-muted-foreground">{value}</span>}
                      </span>
                    </label>
                  ))}
                </fieldset>
              )}
            </div>
          ))}
          {preview &&
            multi &&
            (ordinalConflict ? (
              <p role="alert" className="text-sm">
                {t("assignOrdinalConflict")}
              </p>
            ) : (
              <label className="flex items-start gap-2 text-sm">
                <input
                  type="checkbox"
                  className="mt-1"
                  checked={confirmed}
                  disabled={disabled || results.length > 0}
                  onChange={(event) => setConfirmed(event.target.checked)}
                />
                {t("assignConfirmShared")}
              </label>
            ))}
          {!unmatched && (
            <Button size="sm" disabled={!canApply} onClick={() => void apply()}>
              {t("unmatchedApply")}
            </Button>
          )}
          {progress && (
            <p role="status" className="text-sm">
              {progress}
            </p>
          )}
          {!unmatched && !multi && results[0] && (
            <p role="status" className="text-sm">
              {resultText(results[0])}
            </p>
          )}
          {error && (
            <p role="alert" className="text-sm">
              {error}
            </p>
          )}
        </>
      )}
    </section>
  );
}
