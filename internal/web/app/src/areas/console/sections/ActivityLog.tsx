import { ChevronDown, ChevronRight, RefreshCw, ScrollText } from "lucide-react";
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
  ACTIVITY_TEXT,
  type ActivityCategory,
  type ActivityNames,
  type ActivityRow,
  activityMatches,
  describeActivity,
  formatLocalTime,
  formatRelativeTime,
  formatUtcTime,
  parseActivityTime,
} from "@/lib/activityDisplay";
import { fetchDevices, fetchLibraries, fetchProfilesAdmin } from "@/lib/graphql";
import { type ActivityRecord, fetchActivityRecords } from "@/lib/graphql/activity";
import { useDisplayLocale, useT } from "@/lib/i18n";
import { Section } from "../Section";
import { useAsync } from "../useAsync";

interface ActivityPage {
  entries: ActivityRecord[];
  names: ActivityNames;
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
  return { entries, names: { profiles, devices, libraries } };
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
// operator view of the latest server actions, as sentences in the viewer's
// own time. Read-only.
export function ActivityLog() {
  const t = useT();
  const locale = useDisplayLocale();
  const now = useMinuteClock();
  const { data, loading, failed, refresh } = useAsync(fetchActivityPage);
  const [query, setQuery] = useState("");
  const [category, setCategory] = useState<ActivityCategory | null>(null);
  const [open, setOpen] = useState<ReadonlySet<string>>(new Set());

  const rows = useMemo(
    () =>
      (data?.entries ?? []).map((entry) => ({ entry, row: describeActivity(entry, data?.names) })),
    [data],
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
        <Button variant="outline" size="sm" onClick={() => void refresh()}>
          <RefreshCw className="console-nav-icon" aria-hidden />
          {t("refresh")}
        </Button>
      }
    >
      <p className="max-w-2xl text-[13px] leading-snug text-muted-foreground">
        {ACTIVITY_TEXT.lead}
      </p>
      {loading ? (
        <Skeleton className="h-48 w-full" />
      ) : failed ? (
        <ErrorState message="activityLogUnavailable" />
      ) : rows.length === 0 ? (
        <EmptyState message="activityLogEmpty" icon={ScrollText} />
      ) : (
        <>
          <div className="flex flex-wrap items-center gap-3">
            <Input
              type="search"
              className="h-8 w-64 max-w-full"
              placeholder={t("search")}
              aria-label={ACTIVITY_TEXT.searchLabel}
              value={query}
              onChange={(event) => setQuery(event.target.value)}
            />
            <div
              role="group"
              aria-label={ACTIVITY_TEXT.categoryLabel}
              className="flex flex-wrap items-center gap-1.5"
            >
              <Button
                variant={category === null ? "secondary" : "ghost"}
                size="sm"
                aria-pressed={category === null}
                onClick={() => setCategory(null)}
              >
                {ACTIVITY_TEXT.allCategories}
              </Button>
              {ACTIVITY_CATEGORIES.filter((c) => counts.has(c)).map((c) => (
                <Button
                  key={c}
                  variant={category === c ? "secondary" : "ghost"}
                  size="sm"
                  aria-pressed={category === c}
                  onClick={() => setCategory(category === c ? null : c)}
                >
                  {ACTIVITY_CATEGORY_LABELS[c]}
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
                { key: "when", label: ACTIVITY_TEXT.when },
                { key: "what", label: ACTIVITY_TEXT.what },
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
                  open={open.has(entry.id)}
                  onToggle={() => toggle(entry.id)}
                />
              ))}
            </DataTable>
          )}
          <p className="text-[12px] text-muted-foreground" aria-live="polite">
            {ACTIVITY_TEXT.showing
              .replace("{shown}", String(shown.length))
              .replace("{total}", String(rows.length))}
          </p>
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
  open,
  onToggle,
}: {
  entry: ActivityRecord;
  row: ActivityRow;
  locale: string;
  now: Date;
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
              <span className="block text-[12px] text-muted-foreground">
                {formatLocalTime(at, locale)}
              </span>
            </time>
          ) : (
            entry.occurredAt
          )}
        </TableCell>
        <TableCell className="align-top">
          <span className="block font-medium">{row.sentence}</span>
          <span className="mt-0.5 flex flex-wrap items-center gap-2 text-[12px] text-muted-foreground">
            <StatusBadge>{ACTIVITY_CATEGORY_LABELS[row.category]}</StatusBadge>
            {context}
          </span>
        </TableCell>
        <TableCell className="w-px align-top">
          <Button
            variant="ghost"
            size="sm"
            aria-expanded={open}
            aria-controls={open ? detailsID : undefined}
            aria-label={`${open ? ACTIVITY_TEXT.hideDetails : ACTIVITY_TEXT.showDetails}: ${row.sentence}`}
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
            <dl className="grid grid-cols-[max-content_1fr] gap-x-4 gap-y-1 text-[12px]">
              <Detail term={ACTIVITY_TEXT.rawAction} value={entry.action} />
              <Detail term={ACTIVITY_TEXT.exactTime} value={utc} />
              <Detail
                term={ACTIVITY_TEXT.target}
                value={[entry.targetType, entry.targetID].filter(Boolean).join(": ")}
              />
              <Detail term={ACTIVITY_TEXT.profile} value={entry.profileID} name={row.profileName} />
              <Detail term={ACTIVITY_TEXT.device} value={entry.deviceID} name={row.deviceName} />
              <Detail term={ACTIVITY_TEXT.library} value={entry.libraryID} name={row.libraryName} />
              <Detail term={ACTIVITY_TEXT.apiToken} value={entry.tokenID} />
              <Detail term={ACTIVITY_TEXT.apiKey} value={entry.apiKeyID} />
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
