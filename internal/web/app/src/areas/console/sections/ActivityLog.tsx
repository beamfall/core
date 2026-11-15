import { ChevronDown, ChevronRight, RefreshCw } from "lucide-react";
import { Fragment, useEffect, useMemo, useState } from "react";
import {
  DataTable,
  EmptyLine,
  EmptyState,
  ErrorState,
  StatusBadge,
} from "@/components/beamfall/workbench";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Skeleton } from "@/components/ui/skeleton";
import { TableCell, TableRow } from "@/components/ui/table";
import {
  ACTIVITY_CATEGORIES,
  ACTIVITY_CATEGORY_LABELS,
  type ActivityCategory,
  type ActivityNames,
  type ActivityRow,
  activityMatches,
  describeActivity,
  formatLocalTime,
  formatRelativeTime,
  formatUtcTime,
  parseActivityTime,
  type Translate,
} from "@/lib/activityDisplay";
import { fetchDevices, fetchLibraries, fetchProfilesAdmin } from "@/lib/graphql";
import {
  ACTIVITY_PAGE_SIZE,
  type ActivityRecord,
  fetchActivityRecords,
} from "@/lib/graphql/activity";
import { fetchMediaRows } from "@/lib/graphql/browse";
import { useDisplayLocale, useT } from "@/lib/i18n";
import { Section } from "../Section";
import { useAsync } from "../useAsync";

interface ActivityPage {
  entries: ActivityRecord[];
  names: ActivityNames;
  /** False once a page came back short, so no older events remain. */
  more: boolean;
}

// Names are a courtesy: an entry still reads as a sentence without them, so a
// failed lookup yields no names instead of failing the page.
async function namesBy<T>(
  load: Promise<T[]>,
  id: (item: T) => string,
  name: (item: T) => string,
): Promise<Map<string, string>> {
  try {
    return new Map((await load).map((item) => [id(item), name(item)]));
  } catch {
    return new Map();
  }
}

// Media titles come from the reveal-filtered media(id:) read, so an item the
// viewer may not see keeps its generic noun. Only ids not yet named are read.
async function mediaTitles(
  entries: readonly ActivityRecord[],
  known: ReadonlyMap<string, string>,
  signal: AbortSignal,
): Promise<Map<string, string>> {
  const ids = [
    ...new Set(
      entries.flatMap((e) =>
        e.targetType === "media" && e.targetID && !known.has(e.targetID) ? [e.targetID] : [],
      ),
    ),
  ];
  const titles = new Map(known);
  if (ids.length === 0) return titles;
  try {
    for (const [id, row] of Object.entries(await fetchMediaRows(ids, signal))) {
      if (row.title) titles.set(id, row.title);
    }
  } catch {
    if (signal.aborted) throw signal.reason;
  }
  return titles;
}

async function fetchActivityPage(signal: AbortSignal): Promise<ActivityPage> {
  const [entries, profiles, devices, libraries] = await Promise.all([
    fetchActivityRecords(signal),
    namesBy(
      fetchProfilesAdmin(signal),
      (p) => p.id,
      (p) => p.name,
    ),
    namesBy(
      fetchDevices(signal),
      (d) => d.deviceID,
      (d) => d.name,
    ),
    namesBy(
      fetchLibraries(signal),
      (l) => l.id,
      (l) => l.title,
    ),
  ]);
  const media = await mediaTitles(entries, new Map(), signal);
  return {
    entries,
    names: { profiles, devices, libraries, media },
    more: entries.length >= ACTIVITY_PAGE_SIZE,
  };
}

// The next page continues at the number of events already loaded; an event
// that arrived since the first page shifts the window, so ids seen are skipped.
async function fetchOlderPage(page: ActivityPage, signal: AbortSignal): Promise<ActivityPage> {
  const older = await fetchActivityRecords(signal, page.entries.length);
  const seen = new Set(page.entries.map((e) => e.id));
  const fresh = older.filter((e) => !seen.has(e.id));
  const media = await mediaTitles(fresh, page.names.media, signal);
  return {
    entries: [...page.entries, ...fresh],
    names: { ...page.names, media },
    more: older.length >= ACTIVITY_PAGE_SIZE,
  };
}

/** The current time, refreshed each minute so relative times stay honest. */
function useMinuteClock(): Date {
  const [now, setNow] = useState(() => new Date());
  useEffect(() => {
    const timer = setInterval(() => setNow(new Date()), 60_000);
    return () => clearInterval(timer);
  }, []);
  return now;
}

// Activity log (ADR-0012 §11 / ADR-0040): the append-only, reveal-filtered
// operator view of server actions, newest first in pages of ACTIVITY_PAGE_SIZE,
// as localized sentences in the viewer's own time. Read-only.
export function ActivityLog() {
  const t = useT();
  const locale = useDisplayLocale();
  const now = useMinuteClock();
  const { data, loading, failed, refresh, replace } = useAsync(fetchActivityPage);
  const [older, setOlder] = useState<"idle" | "loading" | "failed">("idle");
  const [query, setQuery] = useState("");
  const [category, setCategory] = useState<ActivityCategory | null>(null);
  const [open, setOpen] = useState<ReadonlySet<string>>(new Set());

  const rows = useMemo(
    () =>
      (data?.entries ?? []).map((entry) => ({
        entry,
        row: describeActivity(entry, t, data?.names),
      })),
    [data, t],
  );
  const counts = useMemo(() => {
    const byCategory = new Map<ActivityCategory, number>();
    for (const { row } of rows)
      byCategory.set(row.category, (byCategory.get(row.category) ?? 0) + 1);
    return byCategory;
  }, [rows]);
  const shown = rows.filter(
    ({ entry, row }) =>
      (category === null || row.category === category) && activityMatches(entry, row, query),
  );

  const loadOlder = async () => {
    if (!data) return;
    setOlder("loading");
    try {
      await replace((signal) => fetchOlderPage(data, signal));
      setOlder("idle");
    } catch {
      setOlder("failed");
    }
  };

  const toggle = (id: string) =>
    setOpen((current) => {
      const next = new Set(current);
      if (!next.delete(id)) next.add(id);
      return next;
    });

  return (
    <Section
      heading="activityLogHeading"
      actions={
        <Button
          variant="outline"
          size="sm"
          onClick={() => {
            setOlder("idle");
            void refresh();
          }}
        >
          <RefreshCw className="console-nav-icon" aria-hidden />
          {t("refresh")}
        </Button>
      }
    >
      <p className="max-w-2xl text-dense leading-snug text-muted-foreground">
        {t("activityPageLead")}
      </p>
      {loading ? (
        <Skeleton className="h-48 w-full" />
      ) : failed ? (
        <ErrorState message="activityLogUnavailable" />
      ) : rows.length === 0 ? (
        <EmptyState message="activityLogEmpty" />
      ) : (
        <>
          <div className="flex flex-wrap items-center gap-3">
            <Input
              type="search"
              className="h-8 w-64 max-w-full"
              placeholder={t("search")}
              aria-label={t("activityPageSearchLabel")}
              value={query}
              onChange={(event) => setQuery(event.target.value)}
            />
            <div
              role="group"
              aria-label={t("activityPageCategoryLabel")}
              className="flex flex-wrap items-center gap-1.5"
            >
              <Button
                variant={category === null ? "secondary" : "ghost"}
                size="sm"
                aria-pressed={category === null}
                onClick={() => setCategory(null)}
              >
                {t("activityPageAllCategories")}
              </Button>
              {ACTIVITY_CATEGORIES.filter((c) => counts.has(c)).map((c) => (
                <Button
                  key={c}
                  variant={category === c ? "secondary" : "ghost"}
                  size="sm"
                  aria-pressed={category === c}
                  onClick={() => setCategory(category === c ? null : c)}
                >
                  {t(ACTIVITY_CATEGORY_LABELS[c])}
                  <span className="text-muted-foreground tabular-nums">{counts.get(c)}</span>
                </Button>
              ))}
            </div>
          </div>
          {shown.length === 0 ? (
            <EmptyLine message={t("searchEmpty")} />
          ) : (
            <DataTable
              columns={[
                { key: "when", label: t("activityPageWhen") },
                { key: "what", label: t("activityPageWhat") },
                { key: "details" },
              ]}
            >
              {shown.map(({ entry, row }) => (
                <ActivityEntryRows
                  key={entry.id}
                  entry={entry}
                  row={row}
                  locale={locale}
                  now={now}
                  t={t}
                  open={open.has(entry.id)}
                  onToggle={() => toggle(entry.id)}
                />
              ))}
            </DataTable>
          )}
          <div className="flex flex-wrap items-center gap-3">
            <p className="text-xs text-muted-foreground" aria-live="polite">
              {t("activityPageShowing", { shown: shown.length, total: rows.length })}
            </p>
            {data?.more && (
              <Button
                variant="outline"
                size="sm"
                disabled={older === "loading"}
                onClick={() => void loadOlder()}
              >
                {t("activityPageLoadOlder")}
              </Button>
            )}
            {older === "failed" && (
              <p role="alert" className="text-xs text-destructive">
                {t("activityPageLoadOlderFailed")}
              </p>
            )}
          </div>
        </>
      )}
    </Section>
  );
}

function ActivityEntryRows({
  entry,
  row,
  locale,
  now,
  t,
  open,
  onToggle,
}: {
  entry: ActivityRecord;
  row: ActivityRow;
  locale: string;
  now: Date;
  t: Translate;
  open: boolean;
  onToggle: () => void;
}) {
  const at = parseActivityTime(entry.occurredAt);
  const utc = at ? formatUtcTime(at) : entry.occurredAt;
  const context = [row.libraryName, row.deviceName].filter(Boolean).join(" · ");
  const detailsID = `activity-details-${entry.id}`;
  const Chevron = open ? ChevronDown : ChevronRight;

  return (
    <Fragment>
      <TableRow>
        <TableCell className="whitespace-nowrap align-top">
          {at ? (
            <time dateTime={entry.occurredAt} title={utc} className="block">
              <span className="block">{formatRelativeTime(at, now, locale)}</span>
              <span className="block text-xs text-muted-foreground">
                {formatLocalTime(at, locale)}
              </span>
            </time>
          ) : (
            entry.occurredAt
          )}
        </TableCell>
        <TableCell className="align-top">
          <span className="block font-medium">{row.sentence}</span>
          <span className="mt-0.5 flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
            <StatusBadge>{row.categoryLabel}</StatusBadge>
            {context}
          </span>
        </TableCell>
        <TableCell className="w-px align-top">
          <Button
            variant="ghost"
            size="sm"
            aria-expanded={open}
            aria-controls={open ? detailsID : undefined}
            aria-label={`${t(open ? "activityPageHideDetails" : "activityPageShowDetails")}: ${row.sentence}`}
            onClick={onToggle}
          >
            <Chevron className="console-nav-icon" aria-hidden />
          </Button>
        </TableCell>
      </TableRow>
      {open && (
        <TableRow id={detailsID}>
          <TableCell />
          <TableCell colSpan={2}>
            <dl className="grid grid-cols-[max-content_1fr] gap-x-4 gap-y-1 text-xs">
              <Detail term={t("activityPageRawAction")} value={entry.action} />
              <Detail term={t("activityPageExactTime")} value={utc} />
              <Detail
                term={t("activityPageTarget")}
                value={[entry.targetType, entry.targetID].filter(Boolean).join(": ")}
              />
              <Detail
                term={t("activityPageProfile")}
                value={entry.profileID}
                name={row.profileName}
              />
              <Detail term={t("activityPageDevice")} value={entry.deviceID} name={row.deviceName} />
              <Detail
                term={t("activityPageLibrary")}
                value={entry.libraryID}
                name={row.libraryName}
              />
              <Detail term={t("activityPageApiToken")} value={entry.tokenID} />
              <Detail term={t("activityPageApiKey")} value={entry.apiKeyID} />
            </dl>
          </TableCell>
        </TableRow>
      )}
    </Fragment>
  );
}

function Detail({ term, value, name }: { term: string; value?: string; name?: string }) {
  if (!value) return null;
  return (
    <>
      <dt className="text-muted-foreground">{term}</dt>
      <dd className="console-mono break-all">{name ? `${name} (${value})` : value}</dd>
    </>
  );
}
