import { useCallback, useEffect, useState } from "react";
import { AppShell } from "@/components/chrome/AppShell";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import {
  deleteMyEngagement,
  fetchMyDataSummary,
  fetchMyEngagement,
  fetchMyEngagementLog,
  fetchMySessions,
} from "@/lib/graphql";
import type {
  MyDataSummary,
  MyEngagementEvent,
  MyEngagementLogVars,
  MySessionInfo,
  MySessionsInfo,
  MyStreamTokenInfo,
} from "@/lib/graphql/types";
import { useT } from "@/lib/i18n";
import { ViewerActions } from "../components/ViewerActions";

// The event-log page size (matches the server default; the server clamps).
const MY_DATA_LOG_PAGE_SIZE = 25;
// SelectItem cannot hold an empty value, so the "all libraries" option uses a
// sentinel that maps back to an absent libraryID filter.
const ALL_LIBRARIES = "__all__";

// My Data / Privacy panel (/mydata), ported from the vanilla openMyData /
// refreshMySessions / mydataExport / mydataDeleteAll (app.js). A Viewer-resident
// panel over the caller's OWN profile data (ADR-0036): export as download-only
// JSON (never forwarded), a read-only view of active Sessions + outstanding
// Stream Tokens (byte-blind — the server sends only target KIND + expiry, never
// a token value), and a hard-DELETE of the caller's engagement. Never manager-
// gated and never surfaced in the operator Console.
export function MyDataPage() {
  const t = useT();
  const [info, setInfo] = useState<MySessionsInfo | null>(null);
  const [sessionsError, setSessionsError] = useState<string | null>(null);
  const [exportMsg, setExportMsg] = useState<string | null>(null);
  const [exportError, setExportError] = useState<string | null>(null);
  const [deleteMsg, setDeleteMsg] = useState<string | null>(null);
  const [deleteError, setDeleteError] = useState<string | null>(null);

  // Summary (ADR-0036 §2.2 layer 1): computed-on-read aggregate over the
  // caller's own reveal-filtered data. Refreshed alongside the delete so the
  // counts drop to zero after a delete-all.
  const [summary, setSummary] = useState<MyDataSummary | null>(null);
  const [summaryError, setSummaryError] = useState<string | null>(null);

  // Event log (ADR-0036 §2.2 layer 2): paginated, Library + date-range
  // filterable. `applied` holds the filters the current page reflects; the
  // draft inputs become `applied` only on Apply.
  const [logPage, setLogPage] = useState<{
    events: MyEngagementEvent[];
    total: number;
  } | null>(null);
  const [logError, setLogError] = useState<string | null>(null);
  const [draftLibrary, setDraftLibrary] = useState<string>(ALL_LIBRARIES);
  const [draftFrom, setDraftFrom] = useState<string>("");
  const [draftTo, setDraftTo] = useState<string>("");
  const [applied, setApplied] = useState<MyEngagementLogVars>({});
  const [offset, setOffset] = useState(0);

  const refresh = useCallback(async () => {
    setSessionsError(null);
    try {
      setInfo(await fetchMySessions());
    } catch {
      setSessionsError(t("myDataSessionsError"));
    }
  }, [t]);

  const refreshSummary = useCallback(async () => {
    setSummaryError(null);
    try {
      setSummary(await fetchMyDataSummary());
    } catch {
      setSummaryError(t("myDataSummaryError"));
    }
  }, [t]);

  const refreshLog = useCallback(async () => {
    setLogError(null);
    try {
      setLogPage(
        await fetchMyEngagementLog({
          ...applied,
          limit: MY_DATA_LOG_PAGE_SIZE,
          offset,
        }),
      );
    } catch {
      setLogError(t("myDataLogError"));
    }
  }, [applied, offset, t]);

  useEffect(() => {
    void refresh();
    void refreshSummary();
  }, [refresh, refreshSummary]);

  useEffect(() => {
    void refreshLog();
  }, [refreshLog]);

  function onApplyFilters() {
    const vars: MyEngagementLogVars = {};
    if (draftLibrary !== ALL_LIBRARIES) vars.libraryID = draftLibrary;
    // <input type="date"> yields YYYY-MM-DD; widen to an RFC3339 instant that
    // covers the whole day (start-of-day for `from`, end-of-day for `to`) so a
    // single-day range is inclusive on both ends.
    if (draftFrom) vars.from = `${draftFrom}T00:00:00Z`;
    if (draftTo) vars.to = `${draftTo}T23:59:59Z`;
    setApplied(vars);
    setOffset(0);
  }

  function onClearFilters() {
    setDraftLibrary(ALL_LIBRARIES);
    setDraftFrom("");
    setDraftTo("");
    setApplied({});
    setOffset(0);
  }

  async function onExport() {
    setExportError(null);
    setExportMsg(null);
    try {
      const payload = await fetchMyEngagement();
      downloadJSON(payload, "beamfall-my-data.json");
      setExportMsg(t("myDataExportDone"));
    } catch {
      setExportError(t("myDataExportError"));
    }
  }

  async function onDeleteAll() {
    setDeleteError(null);
    setDeleteMsg(null);
    // Hard DELETE of the user's own data — confirm first. This is the user's own
    // engagement (not library content), so one confirmation suffices.
    if (typeof confirm === "function" && !confirm(t("myDataDeleteConfirm"))) {
      return;
    }
    try {
      await deleteMyEngagement("all");
      setDeleteMsg(t("myDataDeleteDone"));
      await Promise.all([refresh(), refreshSummary(), refreshLog()]);
    } catch (err) {
      const needsPin =
        err instanceof Error &&
        err.message.toLowerCase().includes("reveal pin required") &&
        typeof prompt === "function";
      const pin = needsPin ? prompt(t("myDataDeletePinPrompt")) : null;
      if (!pin) {
        setDeleteError(t("myDataDeleteError"));
        return;
      }
      try {
        await deleteMyEngagement("all", pin);
        setDeleteMsg(t("myDataDeleteDone"));
        await Promise.all([refresh(), refreshSummary(), refreshLog()]);
      } catch {
        setDeleteError(t("myDataDeleteError"));
      }
      return;
    }
  }

  const sessions = info?.sessions || [];
  const tokens = info?.streamTokens || [];

  return (
    <AppShell actions={<ViewerActions />}>
      <div className="mx-auto flex max-w-3xl flex-col gap-6">
        <header className="flex flex-col gap-2">
          <h1 className="bf-page-title">{t("myDataTitle")}</h1>
          <p className="bf-lead">{t("myDataLead")}</p>
        </header>

        <SummarySection summary={summary} error={summaryError} />

        <LogSection
          page={logPage}
          error={logError}
          libraries={summary?.libraryBreakdown.map((b) => b.libraryID) ?? []}
          draftLibrary={draftLibrary}
          draftFrom={draftFrom}
          draftTo={draftTo}
          offset={offset}
          pageSize={MY_DATA_LOG_PAGE_SIZE}
          onLibraryChange={setDraftLibrary}
          onFromChange={setDraftFrom}
          onToChange={setDraftTo}
          onApply={onApplyFilters}
          onClear={onClearFilters}
          onPrev={() => setOffset((o) => Math.max(0, o - MY_DATA_LOG_PAGE_SIZE))}
          onNext={() => setOffset((o) => o + MY_DATA_LOG_PAGE_SIZE)}
        />

        <Card>
          <CardHeader>
            <CardTitle>{t("myDataExportHeading")}</CardTitle>
          </CardHeader>
          <CardContent className="flex flex-col gap-3">
            <p className="text-sm text-[var(--text-muted)]">{t("myDataExportLead")}</p>
            <div>
              <Button onClick={onExport}>{t("myDataExport")}</Button>
            </div>
            {exportMsg && <p className="text-sm text-[var(--green-500)]">{exportMsg}</p>}
            {exportError && <p className="text-sm text-[var(--red-500)]">{exportError}</p>}
          </CardContent>
        </Card>

        <Card>
          <CardHeader className="flex flex-row items-center justify-between">
            <CardTitle>{t("myDataSessionsHeading")}</CardTitle>
            <Button variant="outline" size="sm" onClick={refresh}>
              {t("refresh")}
            </Button>
          </CardHeader>
          <CardContent className="flex flex-col gap-4">
            {sessionsError && <p className="text-sm text-[var(--red-500)]">{sessionsError}</p>}
            <section className="flex flex-col gap-2">
              {sessions.length === 0 ? (
                <p className="text-sm text-[var(--text-muted)]">{t("myDataSessionsEmpty")}</p>
              ) : (
                <ul className="flex flex-col gap-2">
                  {sessions.map((s) => (
                    <SessionRow key={s.id} session={s} />
                  ))}
                </ul>
              )}
            </section>
            <section className="flex flex-col gap-2">
              <h3 className="text-sm font-medium text-[var(--text-secondary)]">
                {t("myDataTokensHeading")}
              </h3>
              {tokens.length === 0 ? (
                <p className="text-sm text-[var(--text-muted)]">{t("myDataTokensEmpty")}</p>
              ) : (
                <ul className="flex flex-col gap-2">
                  {tokens.map((token, i) => (
                    <TokenRow key={i} token={token} />
                  ))}
                </ul>
              )}
            </section>
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle>{t("myDataDeleteHeading")}</CardTitle>
          </CardHeader>
          <CardContent className="flex flex-col gap-3">
            <p className="text-sm text-[var(--text-muted)]">{t("myDataDeleteLead")}</p>
            <div>
              <Button variant="destructive" onClick={onDeleteAll}>
                {t("myDataDeleteAll")}
              </Button>
            </div>
            {deleteMsg && <p className="text-sm text-[var(--green-500)]">{deleteMsg}</p>}
            {deleteError && <p className="text-sm text-[var(--red-500)]">{deleteError}</p>}
          </CardContent>
        </Card>
      </div>
    </AppShell>
  );
}

// SummarySection renders the ADR-0036 §2.2 layer-1 aggregate: headline counts,
// the activity date range, and the per-Library play breakdown.
function SummarySection({
  summary,
  error,
}: {
  summary: MyDataSummary | null;
  error: string | null;
}) {
  const t = useT();
  const stats: { label: string; value: number }[] = summary
    ? [
        { label: t("myDataSummaryPlays"), value: summary.plays },
        { label: t("myDataSummaryWatched"), value: summary.watched },
        { label: t("myDataSummaryFavorites"), value: summary.favorites },
        { label: t("myDataSummaryQueue"), value: summary.queueEntries },
        { label: t("myDataSummaryLibraries"), value: summary.libraryCount },
      ]
    : [];
  return (
    <Card>
      <CardHeader>
        <CardTitle>{t("myDataSummaryHeading")}</CardTitle>
      </CardHeader>
      <CardContent className="flex flex-col gap-4">
        <p className="text-sm text-[var(--text-muted)]">{t("myDataSummaryLead")}</p>
        {error && <p className="text-sm text-[var(--red-500)]">{error}</p>}
        {summary && (
          <>
            <div className="grid grid-cols-2 gap-3 sm:grid-cols-5">
              {stats.map((s) => (
                <div
                  key={s.label}
                  className="flex flex-col rounded-[var(--radius-control)] border border-[var(--border-subtle)] px-3 py-2"
                >
                  <span className="text-2xl font-semibold text-[var(--text-primary)]">
                    {s.value}
                  </span>
                  <span className="text-xs text-[var(--text-muted)]">{s.label}</span>
                </div>
              ))}
            </div>
            <p className="text-sm text-[var(--text-secondary)]">
              {summary.firstActivity && summary.lastActivity
                ? t("myDataSummaryRange", {
                    first: formatDate(summary.firstActivity),
                    last: formatDate(summary.lastActivity),
                  })
                : t("myDataSummaryRangeEmpty")}
            </p>
            {summary.libraryBreakdown.length > 0 && (
              <section className="flex flex-col gap-2">
                <h3 className="text-sm font-medium text-[var(--text-secondary)]">
                  {t("myDataSummaryByLibrary")}
                </h3>
                <ul className="flex flex-col gap-1">
                  {summary.libraryBreakdown.map((b) => (
                    <li key={b.libraryID} className="flex items-center justify-between text-sm">
                      <span className="text-[var(--text-primary)]">{b.libraryID}</span>
                      <Badge variant="outline">{b.plays}</Badge>
                    </li>
                  ))}
                </ul>
              </section>
            )}
          </>
        )}
      </CardContent>
    </Card>
  );
}

// LogSection renders the ADR-0036 §2.2 layer-2 event log: the Library + date
// filters, the page of events (newest first), and pagination controls.
function LogSection({
  page,
  error,
  libraries,
  draftLibrary,
  draftFrom,
  draftTo,
  offset,
  pageSize,
  onLibraryChange,
  onFromChange,
  onToChange,
  onApply,
  onClear,
  onPrev,
  onNext,
}: {
  page: { events: MyEngagementEvent[]; total: number } | null;
  error: string | null;
  libraries: string[];
  draftLibrary: string;
  draftFrom: string;
  draftTo: string;
  offset: number;
  pageSize: number;
  onLibraryChange: (value: string) => void;
  onFromChange: (value: string) => void;
  onToChange: (value: string) => void;
  onApply: () => void;
  onClear: () => void;
  onPrev: () => void;
  onNext: () => void;
}) {
  const t = useT();
  const events = page?.events ?? [];
  const total = page?.total ?? 0;
  const rangeStart = total === 0 ? 0 : offset + 1;
  const rangeEnd = offset + events.length;
  const hasPrev = offset > 0;
  const hasNext = offset + pageSize < total;
  return (
    <Card>
      <CardHeader>
        <CardTitle>{t("myDataLogHeading")}</CardTitle>
      </CardHeader>
      <CardContent className="flex flex-col gap-4">
        <p className="text-sm text-[var(--text-muted)]">{t("myDataLogLead")}</p>

        <div className="flex flex-wrap items-end gap-3">
          <div className="flex flex-col gap-1">
            <Label htmlFor="mydata-log-library">{t("myDataLogFilterLibrary")}</Label>
            <Select value={draftLibrary} onValueChange={onLibraryChange}>
              <SelectTrigger id="mydata-log-library" className="w-48">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value={ALL_LIBRARIES}>{t("myDataLogFilterAllLibraries")}</SelectItem>
                {libraries.map((id) => (
                  <SelectItem key={id} value={id}>
                    {id}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
          <div className="flex flex-col gap-1">
            <Label htmlFor="mydata-log-from">{t("myDataLogFilterFrom")}</Label>
            <Input
              id="mydata-log-from"
              type="date"
              value={draftFrom}
              onChange={(e) => onFromChange(e.target.value)}
              className="w-40"
            />
          </div>
          <div className="flex flex-col gap-1">
            <Label htmlFor="mydata-log-to">{t("myDataLogFilterTo")}</Label>
            <Input
              id="mydata-log-to"
              type="date"
              value={draftTo}
              onChange={(e) => onToChange(e.target.value)}
              className="w-40"
            />
          </div>
          <div className="flex gap-2">
            <Button size="sm" onClick={onApply}>
              {t("myDataLogApply")}
            </Button>
            <Button variant="outline" size="sm" onClick={onClear}>
              {t("myDataLogClear")}
            </Button>
          </div>
        </div>

        {error && <p className="text-sm text-[var(--red-500)]">{error}</p>}

        {events.length === 0 ? (
          <p className="text-sm text-[var(--text-muted)]">{t("myDataLogEmpty")}</p>
        ) : (
          <ul className="flex flex-col gap-2">
            {events.map((ev, i) => (
              <EventRow key={`${ev.kind}-${ev.mediaID}-${i}`} event={ev} />
            ))}
          </ul>
        )}

        <div className="flex items-center justify-between">
          <span className="text-xs text-[var(--text-muted)]">
            {t("myDataLogPage", {
              from: rangeStart,
              to: rangeEnd,
              total,
            })}
          </span>
          <div className="flex gap-2">
            <Button variant="outline" size="sm" onClick={onPrev} disabled={!hasPrev}>
              {t("myDataLogPrev")}
            </Button>
            <Button variant="outline" size="sm" onClick={onNext} disabled={!hasNext}>
              {t("myDataLogNext")}
            </Button>
          </div>
        </div>
      </CardContent>
    </Card>
  );
}

function EventRow({ event }: { event: MyEngagementEvent }) {
  const t = useT();
  const kindLabel =
    event.kind === "play"
      ? t("myDataLogKindPlay")
      : event.kind === "favorite"
        ? t("myDataLogKindFavorite")
        : t("myDataLogKindQueue");
  return (
    <li className="flex items-center justify-between rounded-[var(--radius-control)] border border-[var(--border-subtle)] px-3 py-2">
      <div className="flex flex-col">
        <span className="text-sm text-[var(--text-primary)]">
          {kindLabel} · {event.mediaID}
        </span>
        <span className="text-xs text-[var(--text-muted)]">{event.libraryID}</span>
      </div>
      <span className="text-xs text-[var(--text-muted)]">{formatDateTime(event.occurredAt)}</span>
    </li>
  );
}

function SessionRow({ session }: { session: MySessionInfo }) {
  const t = useT();
  const parts: string[] = [];
  if (session.deviceRole) parts.push(session.deviceRole);
  const expires = formatDateTime(session.expiresAt);
  if (expires) parts.push(`${t("expires")} ${expires}`);
  return (
    <li
      className={
        "flex items-center justify-between rounded-[var(--radius-control)] border px-3 py-2 " +
        (session.current ? "border-[var(--accent)]" : "border-[var(--border-subtle)]")
      }
    >
      <div className="flex flex-col">
        <span className="text-sm text-[var(--text-primary)]">
          {session.deviceName || session.deviceID || session.id}
        </span>
        {parts.length > 0 && (
          <span className="text-xs text-[var(--text-muted)]">{parts.join(" · ")}</span>
        )}
      </div>
      {session.current && <Badge variant="outline">{t("myDataCurrent")}</Badge>}
    </li>
  );
}

function TokenRow({ token }: { token: MyStreamTokenInfo }) {
  const t = useT();
  const expires = formatDateTime(token.expiresAt);
  return (
    <li className="flex items-center justify-between rounded-[var(--radius-control)] border border-[var(--border-subtle)] px-3 py-2">
      <span className="text-sm text-[var(--text-primary)]">{token.targetKind || ""}</span>
      {expires && (
        <span className="text-xs text-[var(--text-muted)]">
          {t("expires")} {expires}
        </span>
      )}
    </li>
  );
}

// Triggers a local download of the export JSON without a server-side file or a
// forward (ADR-0036): the blob is created, clicked, and revoked locally.
function downloadJSON(payload: unknown, filename: string) {
  const json = JSON.stringify(payload, null, 2);
  const blob = new Blob([json], { type: "application/json" });
  const url = URL.createObjectURL(blob);
  const link = document.createElement("a");
  link.href = url;
  link.download = filename;
  link.click();
  URL.revokeObjectURL(url);
}

// Formats an ISO timestamp via the browser locale, ported from the vanilla
// formatDateTime (app.js); an unparseable value renders as "".
function formatDateTime(value: string | undefined): string {
  if (!value) return "";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return "";
  return date.toLocaleString();
}

// Date-only formatting for the Summary's activity range (no time component).
function formatDate(value: string | undefined): string {
  if (!value) return "";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return "";
  return date.toLocaleDateString();
}
