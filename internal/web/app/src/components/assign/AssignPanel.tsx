import { useEffect, useId, useRef, useState } from "react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { getIdentityEpochSignal } from "@/lib/api";
import {
  fixMatch,
  fixMatchCandidates,
  fixMatchChildren,
  fixMatchLockedFields,
  fixMatchPreview,
  identifyLookup,
} from "@/lib/graphql";
import type {
  FixMatchChildInfo,
  FixMatchCurrentInfo,
  FixMatchLockedFieldInfo,
  FixMatchSourceInfo,
} from "@/lib/graphql/manage";
import type { FixMatchCandidateInfo, FixMatchFieldDiff } from "@/lib/graphql/types";
import { useT } from "@/lib/i18n";
import { clearViewerCache } from "@/lib/viewerCache";
import type { StringKey } from "@/strings";
import { AssignChildColumn, type ChildColumn } from "./AssignChildColumn";
import { useAssignInterruptionNotice } from "./AssignInterruptionNotice";
import { AssignOrdinalForm } from "./AssignOrdinalForm";
import { AssignPairingSelect, type ChildChoice } from "./AssignPairingSelect";
import {
  formatOrdinal,
  itemIndex,
  levelLabel,
  lockedOrdinal,
  type Ordinal,
  ordinalIndex,
  ordinalLabels,
} from "./assignOrdinals";
import { percent, reasonLabel } from "./matchLabels";

export interface AssignItem {
  mediaID: string;
  title: string;
  year?: number;
  sequence?: number;
  index?: number;
  current?: FixMatchCurrentInfo | null;
}

type Source = Pick<FixMatchSourceInfo, "pluginID" | "scraperID" | "label" | "acceptsPastedID"> &
  Partial<Pick<FixMatchSourceInfo, "supportsChildren" | "childLevels">>;
type MatchTarget = { pluginID: string; scraperID: string; candidateID: string };
type PlannedMatch = {
  mediaID: string;
  ordinal?: Ordinal;
};
type PreviewDiff = FixMatchFieldDiff[] | "loading" | null;
type Pairing = PlannedMatch & {
  sameCandidate: boolean;
  replaceDiff?: PreviewDiff;
  keepDiff?: PreviewDiff;
  locked: FixMatchLockedFieldInfo[];
  clearLocks: string[];
};
type Result = { mediaID: string; error?: string };
type Preview = { candidate: MatchTarget; rows: Pairing[]; epoch: AbortSignal | undefined };
type ChildPairings = {
  rows: FixMatchChildInfo[];
  choices: Record<string, ChildChoice>;
};

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
  ordinal: "assignOrdinal",
};

function sourceKey(source: Pick<Source, "pluginID" | "scraperID">) {
  return JSON.stringify([source.pluginID, source.scraperID]);
}

function keepsOrdinal(row: Pairing) {
  return row.locked.some(({ field }) => field === "ordinal") && !row.clearLocks.includes("ordinal");
}

function diffKey(row: Pairing) {
  return keepsOrdinal(row) ? "keepDiff" : "replaceDiff";
}

async function previewPairing(candidate: MatchTarget, row: Pairing) {
  const lock = row.locked.find(({ field }) => field === "ordinal");
  const vars = { mediaID: row.mediaID, ...candidate };
  if (lock && keepsOrdinal(row)) {
    if (row.sameCandidate) return fixMatchPreview(vars);
    const previous = lockedOrdinal(lock.value);
    return previous ? fixMatchPreview({ ...vars, ...previous }) : null;
  }
  // A new candidate drops the saved ordinal by default. For the same candidate,
  // pass the cleared lock so the preview uses the parser's ordinal, as Apply will.
  const clearLocks = lock && row.sameCandidate ? { clearLocks: ["ordinal"] } : {};
  return fixMatchPreview({ ...vars, ...row.ordinal, ...clearLocks });
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
  const [columns, setColumns] = useState<ChildColumn[]>([]);
  const [activeColumn, setActiveColumn] = useState(0);
  const [deepestKey, setDeepestKey] = useState<string>();
  const [ordinalEntry, setOrdinalEntry] = useState<{ fixedSequence?: string }>();
  const [childPairings, setChildPairings] = useState<ChildPairings>();
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
  const childRequest = useRef(0);
  const live = useRef(true);
  const preview = approval?.rows;

  const multi = items.length > 1 && !unmatched;
  const levels = (!unmatched && source?.supportsChildren && source.childLevels) || [];
  const hierarchy = levels.length > 0;
  const labels = ordinalLabels(levels, t);
  const ordinalConflict =
    multi &&
    !childPairings &&
    items.some(
      (item) =>
        typeof item.sequence === "number" ||
        (hierarchy ? itemIndex(item) !== undefined : typeof item.index === "number"),
    );
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

  const resetPreview = () => {
    selection.current++;
    setApproval(null);
    setConfirmed(false);
    setResults([]);
    setError("");
  };

  const select = (id: string) => {
    childRequest.current++;
    setSelectedID(id);
    setColumns([]);
    setActiveColumn(0);
    setDeepestKey(undefined);
    setOrdinalEntry(undefined);
    setChildPairings(undefined);
    resetPreview();
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
      if (!hierarchy) select((preferred ?? scored[0])?.id ?? "");
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
      childRequest.current++;
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
  const target = (candidateID: string, result = selected): MatchTarget => ({
    pluginID: (!unmatched && result?.pluginID) || source.pluginID,
    scraperID: (!unmatched && result?.scraperID) || source.scraperID,
    candidateID,
  });

  const loadChildren = async (
    candidate: MatchTarget,
    level: number,
    parent?: FixMatchChildInfo,
  ) => {
    const asked = ++childRequest.current;
    const epoch = getIdentityEpochSignal();
    const setColumn = (column: ChildColumn) =>
      setColumns((current) => [...current.slice(0, level - 1), column]);
    resetPreview();
    setDeepestKey(undefined);
    setOrdinalEntry(undefined);
    setChildPairings(undefined);
    setActiveColumn(level);
    setColumn({ parent });
    const currentRequest = () =>
      asked === childRequest.current && !epoch?.aborted && epoch === getIdentityEpochSignal();
    try {
      const rows = await fixMatchChildren({
        mediaID: first.mediaID,
        ...candidate,
        level,
        ...(parent ? { parentKey: parent.key } : {}),
      });
      if (!currentRequest()) return;
      setColumn({ parent, rows });
      if (multi && level === levels.length && rows.length) {
        setChildPairings({
          rows,
          choices: Object.fromEntries(
            items.map((item) => {
              const index = itemIndex(item);
              const matches = rows.filter((row) => row.index === index && index !== undefined);
              return [item.mediaID, { key: matches.length === 1 ? matches[0].key : "", index: "" }];
            }),
          ),
        });
      }
    } catch {
      if (currentRequest()) setColumn({ parent, failed: true });
    }
  };

  const loadPreview = async (
    candidateID: string,
    fromPaste = false,
    plan: PlannedMatch[] = (unmatched ? [first] : items).map(({ mediaID }) => ({ mediaID })),
  ) => {
    if (!candidateID) return;
    if (fromPaste) {
      setCandidates([]);
      select(candidateID);
    } else {
      resetPreview();
    }
    const asked = selection.current;
    const epoch = getIdentityEpochSignal();
    const candidate = fromPaste
      ? { pluginID: source.pluginID, scraperID: source.scraperID, candidateID }
      : target(candidateID);
    const currentRequest = () =>
      asked === selection.current && !epoch?.aborted && epoch === getIdentityEpochSignal();
    setActivity(true);
    try {
      const rows = await Promise.all(
        plan.map(async ({ mediaID, ordinal }) => {
          const locked = unmatched ? [] : await fixMatchLockedFields(mediaID);
          const currentID = items.find((item) => item.mediaID === mediaID)?.current?.candidateID;
          const hasOrdinalLock = locked.some(({ field }) => field === "ordinal");
          const sameCandidate = currentID === candidateID;
          const row: Pairing = {
            mediaID,
            ordinal,
            sameCandidate,
            locked,
            clearLocks: hasOrdinalLock && !sameCandidate ? ["ordinal"] : [],
          };
          if (currentRequest()) {
            const request = previewPairing(candidate, row);
            row[diffKey(row)] = await (hasOrdinalLock ? request.catch(() => null) : request);
          }
          return row;
        }),
      );
      if (currentRequest()) setApproval({ candidate, rows, epoch });
    } catch {
      if (currentRequest()) report(t(unmatched ? "unmatchedUnavailable" : "assignPreviewFailed"));
    } finally {
      setActivity(false);
    }
  };

  const pickChild = (level: number, row: FixMatchChildInfo) => {
    if (level < levels.length) {
      void loadChildren(target(selectedID), level + 1, row);
      return;
    }
    if (multi) return;
    resetPreview();
    setDeepestKey(row.key);
    const sequence = row.sequence ?? "";
    if (row.index == null) {
      setOrdinalEntry({ fixedSequence: sequence });
    } else {
      setOrdinalEntry(undefined);
      void loadPreview(selectedID, false, [
        { mediaID: first.mediaID, ordinal: { sequence, index: row.index } },
      ]);
    }
  };

  const pairingKeys = childPairings
    ? Object.values(childPairings.choices)
        .map(({ key }) => key)
        .filter(Boolean)
    : [];
  const duplicatePairing = new Set(pairingKeys).size < pairingKeys.length;
  const pairedPlan = childPairings
    ? items.flatMap(({ mediaID }) => {
        const choice = childPairings.choices[mediaID];
        const row = childPairings.rows.find(({ key }) => key === choice.key);
        if (!row) return [];
        const index = row.index ?? ordinalIndex(choice.index);
        if (index === undefined) return [];
        return [{ mediaID, ordinal: { sequence: row.sequence ?? "", index } }];
      })
    : [];
  const canPreviewPairs =
    pairedPlan.length > 0 && pairedPlan.length === pairingKeys.length && !duplicatePairing;

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
      (!!preview &&
        results.length === 0 &&
        preview.every((row) => Array.isArray(row[diffKey(row)])) &&
        !ordinalConflict &&
        !duplicatePairing &&
        (!multi || (childPairings ? preview.every(({ ordinal }) => ordinal) : confirmed))));

  const apply = async () => {
    if (!canApply) return;
    const candidate = approval?.candidate ?? target(selectedID);
    const epoch = approval ? approval.epoch : getIdentityEpochSignal();
    const identityEnded = () => epoch?.aborted || epoch !== getIdentityEpochSignal();
    onWriteChange?.(true);
    setActivity(true);
    const next: Result[] = [];
    const writes = unmatched ? items.map(({ mediaID }) => ({ mediaID })) : (preview ?? []);
    for (const [index, { mediaID }] of writes.entries()) {
      if (identityEnded()) break;
      setProgress(t("unmatchedMatching", { done: index + 1, total: writes.length }));
      const row = unmatched ? undefined : preview?.[index];
      const keep = row && keepsOrdinal(row);
      try {
        await fixMatch(
          {
            mediaID,
            ...candidate,
            ...(row && (keep || row.clearLocks.length) ? { clearLocks: row.clearLocks } : {}),
            ...(keep ? undefined : row?.ordinal),
          },
          epoch,
        );
        next.push({ mediaID });
        // Invalidate cached viewer documents as each write is confirmed, whether
        // or not the panel is still mounted (Back during a write unmounts it).
        if (!identityEnded()) clearViewerCache();
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
      next.length < writes.length ? next.filter((result) => !result.error).length : undefined;
    if (confirmedChanges !== undefined) {
      setInterrupted(confirmedChanges);
      showInterruptionNotice?.({ done: confirmedChanges, total: writes.length });
    }
    setProgress("");
    setActivity(false);
    if (live.current) {
      onWriteChange?.(false);
      onFinished(next, confirmedChanges);
    }
  };

  const toggleLock = async (row: Pairing, field: string, checked: boolean) => {
    if (!approval) return;
    const { candidate, epoch } = approval;
    if (epoch?.aborted || epoch !== getIdentityEpochSignal()) return;
    const asked = selection.current;
    const clearLocks = checked
      ? [...row.clearLocks, field]
      : row.clearLocks.filter((name) => name !== field);
    const next = { ...row, clearLocks };
    const key = diffKey(next);
    const needsDiff = field === "ordinal" && next[key] === undefined;
    const updateRow = (changes: Partial<Pairing>) => {
      setApproval(
        (current) =>
          current && {
            ...current,
            rows: current.rows.map((entry) =>
              entry.mediaID === row.mediaID ? { ...entry, ...changes } : entry,
            ),
          },
      );
    };
    updateRow(needsDiff ? { clearLocks, [key]: "loading" } : { clearLocks });
    if (!needsDiff) return;
    const diff = await previewPairing(candidate, next).catch(() => null);
    if (asked === selection.current && !epoch?.aborted && epoch === getIdentityEpochSignal()) {
      updateRow({ [key]: diff });
    }
  };

  const resultText = (result: Result) =>
    result.error ? t("assignResultFailed", { error: result.error }) : t("assignApplied");

  if (interrupted !== undefined) {
    return (
      <p role="status" className="text-sm">
        {t("assignInterrupted", {
          done: interrupted,
          total: unmatched ? items.length : (preview?.length ?? items.length),
        })}
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

          {hierarchy && activeColumn > 0 && (
            <Button
              className="lg:hidden"
              size="sm"
              variant="outline"
              onClick={() => setActiveColumn(activeColumn - 1)}
            >
              {t("back")}
            </Button>
          )}
          <div className={hierarchy ? "lg:flex lg:items-start lg:gap-3" : undefined}>
            <div
              className={
                hierarchy
                  ? `${activeColumn === 0 ? "" : "hidden lg:block"} min-w-0 flex-1 space-y-2`
                  : undefined
              }
            >
              {hierarchy && <h4 className="text-sm font-semibold">{t("assignCandidates")}</h4>}
              {candidates.length === 0 ? (
                <p className="text-xs text-muted-foreground">
                  {busy ? t("loading") : t("unmatchedNoCandidates")}
                </p>
              ) : (
                <>
                  {!hierarchy && (
                    <p className="text-xs text-muted-foreground">
                      {t(unmatched ? "unmatchedPickHint" : "assignPickHint")}
                    </p>
                  )}
                  <ul className="space-y-2">
                    {candidates.map((candidate) => (
                      <li key={candidate.id}>
                        <button
                          type="button"
                          className="grid w-full gap-1 rounded-md border px-3 py-2 text-left hover:bg-muted/50 data-[active=true]:border-primary data-[active=true]:bg-muted/40"
                          data-active={selectedID === candidate.id}
                          aria-pressed={selectedID === candidate.id}
                          disabled={blocked || (!unmatched && busy)}
                          onClick={() => {
                            select(candidate.id);
                            if (hierarchy) void loadChildren(target(candidate.id, candidate), 1);
                          }}
                        >
                          <div className="flex items-start justify-between gap-2">
                            {hierarchy && candidate.artwork?.poster && (
                              <img
                                className="h-10 w-8 shrink-0 rounded object-cover"
                                src={candidate.artwork.poster}
                                alt={candidate.title}
                                loading="lazy"
                                referrerPolicy="no-referrer"
                              />
                            )}
                            <span className="min-w-0 break-words text-sm font-medium">
                              {hierarchy && candidate.artwork?.poster ? (
                                <span aria-hidden="true">{candidate.title}</span>
                              ) : (
                                candidate.title
                              )}
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
            </div>
            {levels.map((name, index) => (
              <div
                key={`${index}-${name}`}
                className={`${activeColumn === index + 1 ? "" : "hidden lg:block"} min-w-0 flex-1 space-y-2`}
              >
                <AssignChildColumn
                  label={levelLabel(name, t)}
                  column={columns[index]}
                  selectedKey={
                    columns[index + 1]?.parent?.key ??
                    (index + 1 === levels.length ? deepestKey : undefined)
                  }
                  disabled={disabled}
                  onPick={
                    multi && index + 1 === levels.length
                      ? undefined
                      : (row) => pickChild(index + 1, row)
                  }
                  onRetry={() =>
                    void loadChildren(target(selectedID), index + 1, columns[index]?.parent)
                  }
                />
              </div>
            ))}
          </div>

          <div className="flex flex-wrap items-center gap-2">
            {unmatched && (
              <Button size="sm" disabled={!canApply} onClick={() => void apply()}>
                {items.length === 1
                  ? t("unmatchedApplyPin")
                  : t("unmatchedMatchAll", { count: items.length })}
              </Button>
            )}
            {!childPairings && !deepestKey && !ordinalEntry && (
              <Button
                size="sm"
                variant="outline"
                disabled={disabled || !selectedID || (!unmatched && results.length > 0)}
                onClick={() => void loadPreview(selectedID)}
              >
                {t(hierarchy ? "assignPreviewCandidate" : "unmatchedPreviewLoad")}
              </Button>
            )}
            {hierarchy && selected && !multi && !ordinalEntry && (
              <Button
                size="sm"
                variant="outline"
                disabled={disabled || results.length > 0}
                onClick={() => {
                  resetPreview();
                  setDeepestKey(undefined);
                  setOrdinalEntry({});
                }}
              >
                {t("assignEnterNumbers")}
              </Button>
            )}
          </div>
          {ordinalEntry && (
            <AssignOrdinalForm
              key={deepestKey === undefined ? "manual" : `row-${deepestKey}`}
              fixedSequence={ordinalEntry.fixedSequence}
              labels={labels}
              disabled={disabled || results.length > 0}
              onChange={resetPreview}
              onCancel={() => {
                resetPreview();
                setDeepestKey(undefined);
                setOrdinalEntry(undefined);
              }}
              onPreview={(ordinal) =>
                void loadPreview(selectedID, false, [{ mediaID: first.mediaID, ordinal }])
              }
            />
          )}

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

          {multi && (preview || childPairings) && (
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
                          {childPairings ? (
                            <AssignPairingSelect
                              rows={childPairings.rows}
                              choice={childPairings.choices[item.mediaID]}
                              title={item.title || item.mediaID}
                              indexLabel={labels.index}
                              disabled={disabled || results.length > 0}
                              onChange={(choice) => {
                                resetPreview();
                                setChildPairings({
                                  ...childPairings,
                                  choices: { ...childPairings.choices, [item.mediaID]: choice },
                                });
                              }}
                            />
                          ) : selected?.year ? (
                            `${selected.title} (${selected.year})`
                          ) : (
                            (selected?.title ?? selectedID)
                          )}
                        </td>
                        <td className="p-2" aria-live="polite">
                          {result
                            ? resultText(result)
                            : childPairings && !childPairings.choices[item.mediaID].key
                              ? t("assignSkipped")
                              : "—"}
                        </td>
                      </tr>
                    );
                  })}
                </tbody>
              </table>
            </div>
          )}

          {childPairings && (
            <>
              {duplicatePairing && (
                <p role="alert" className="text-sm">
                  {t("assignDuplicatePairing")}
                </p>
              )}
              <Button
                size="sm"
                variant="outline"
                disabled={disabled || results.length > 0 || !canPreviewPairs}
                onClick={() => void loadPreview(selectedID, false, pairedPlan)}
              >
                {t("unmatchedPreviewLoad")}
              </Button>
            </>
          )}

          {preview?.map((row) => {
            const item = items.find(({ mediaID }) => mediaID === row.mediaID);
            const lock = row.locked.find(({ field }) => field === "ordinal");
            const index = item ? itemIndex(item) : undefined;
            const before = lock
              ? lockedOrdinal(lock.value)
              : index !== undefined
                ? { sequence: item?.sequence?.toString() ?? "", index }
                : undefined;
            const keep = keepsOrdinal(row);
            const after = keep ? before : row.ordinal;
            const diff = row[diffKey(row)];
            const changes = Array.isArray(diff) ? diff : undefined;
            return (
              <div key={row.mediaID} className="space-y-2 rounded-md border p-2">
                {!unmatched && (
                  <h4 className="text-sm font-semibold">
                    {multi ? item?.title || row.mediaID : t("assignPreviewHeading")}
                  </h4>
                )}
                <dl className="grid gap-2">
                  {(row.ordinal || lock) && (
                    <div className="grid gap-0.5">
                      <dt className="text-xs text-muted-foreground">{t("assignOrdinal")}</dt>
                      <dd className="break-words text-sm">
                        {before ? formatOrdinal(before, labels) : lock?.value || "—"}
                        {" → "}
                        {after ? formatOrdinal(after, labels) : t("assignUseParsedOrdinal")}
                      </dd>
                    </div>
                  )}
                  {changes?.map((change) => (
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
                          onChange={(event) => void toggleLock(row, field, event.target.checked)}
                        />
                        <span className="min-w-0 break-words">
                          {field === "ordinal" && !row.ordinal
                            ? t("assignClearOrdinal")
                            : t("assignReplaceLock", { field: fieldLabel(field) })}
                          {value && (
                            <span className="block text-muted-foreground">
                              {field === "ordinal" && before
                                ? formatOrdinal(before, labels)
                                : value}
                            </span>
                          )}
                        </span>
                      </label>
                    ))}
                  </fieldset>
                )}
                {diff === "loading" && <p role="status">{t("loading")}</p>}
                {diff === null && <p role="alert">{t("assignPreviewFailed")}</p>}
              </div>
            );
          })}
          {preview &&
            multi &&
            !childPairings &&
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
