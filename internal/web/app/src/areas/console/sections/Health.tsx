import { ArrowLeft, RefreshCw } from "lucide-react";
import { useState } from "react";
import { Link } from "react-router";
import {
  DataTable,
  EmptyState,
  ErrorState,
  HelpTip,
  StatusChip,
} from "@/components/beamfall/workbench";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Skeleton } from "@/components/ui/skeleton";
import { TableCell, TableRow } from "@/components/ui/table";
import {
  fetchDuplicateGroups,
  fetchHealthDrilldown,
  fetchLibraryHealth,
  runHealthBulkOp,
  runHealthBulkOpSelected,
} from "@/lib/graphql";
import type { DuplicateGroupInfo, HealthItemInfo, LibraryHealthInfo } from "@/lib/graphql/types";
import { useT } from "@/lib/i18n";
import type { StringKey } from "@/strings";
import { ConfirmButton } from "../ConfirmButton";
import { Section } from "../Section";
import { useAsync } from "../useAsync";

// The launch health dimensions (ADR-0042) plus no-scraper (CORE-2383).
// `dimKey` is the server wire value; `countKey` is the raw key returned in
// LibraryHealthInfo.counts. The server omits a dimension that does not apply to
// a Library's plugin, so an absent key means "not applicable", never zero.
// `bulkNeeds` names the dimension whose presence the bulk action needs: rescans
// retry the automatic scrape, which exists exactly when scrape-failure applies.
interface HealthDimension {
  dimKey: string;
  countKey: string;
  label: StringKey;
  summaryBulkField?: string;
  selectedBulkField?: string;
  bulkLabel?: StringKey;
  confirmKey?: StringKey;
  bulkNeeds?: string;
  bulkNeedsReason?: StringKey;
}

const DIMENSIONS: HealthDimension[] = [
  {
    dimKey: "unmatched",
    countKey: "unmatched",
    label: "healthUnmatched",
    summaryBulkField: "bulkRescanUnmatched",
    selectedBulkField: "bulkRescanUnmatched",
    bulkLabel: "bulkRescanUnmatched",
    bulkNeeds: "scrape-failure",
    bulkNeedsReason: "healthRescanNeedsAutoIdentify",
  },
  {
    dimKey: "broken",
    countKey: "broken",
    label: "healthBroken",
    summaryBulkField: "bulkTrashBroken",
    selectedBulkField: "bulkTrashBroken",
    bulkLabel: "bulkTrashBroken",
    confirmKey: "confirmBulkTrashBroken",
  },
  {
    dimKey: "scrape-failure",
    countKey: "scrape-failure",
    label: "healthScrapeFailure",
    summaryBulkField: "bulkRescrapeScrapeFailures",
    selectedBulkField: "bulkRescrapeScrapeFailures",
    bulkLabel: "bulkRescrapeScrapeFailures",
  },
  {
    dimKey: "missing-artwork",
    countKey: "missing-artwork",
    label: "healthMissingArtwork",
    summaryBulkField: "bulkFetchMissingArtwork",
    selectedBulkField: "bulkFetchMissingArtwork",
    bulkLabel: "bulkFetchMissingArtwork",
  },
  {
    dimKey: "duplicate",
    countKey: "duplicate",
    label: "healthDuplicate",
    selectedBulkField: "bulkMergeDuplicates",
    bulkLabel: "bulkMergeDuplicates",
    confirmKey: "confirmBulkMergeDuplicates",
  },
  {
    dimKey: "no-scraper",
    countKey: "no-scraper",
    label: "healthNoScraper",
  },
];

// aggregateCounts sums each dimension over the Libraries it applies to. A
// dimension no Library reports stays absent, so the aggregate shows it as not
// applicable too.
function aggregateCounts(rows: LibraryHealthInfo[]): Record<string, number> {
  const total: Record<string, number> = {};
  for (const row of rows) {
    for (const dim of DIMENSIONS) {
      const count = row.counts[dim.countKey];
      if (count !== undefined) {
        total[dim.countKey] = (total[dim.countKey] ?? 0) + count;
      }
    }
  }
  return total;
}

// bulkBlockedReason returns why a dimension's bulk action cannot help this
// Library, or undefined when it can.
function bulkBlockedReason(
  counts: LibraryHealthInfo["counts"],
  dim: HealthDimension,
): StringKey | undefined {
  if (dim.bulkNeeds && counts[dim.bulkNeeds] === undefined) {
    return dim.bulkNeedsReason;
  }
  return undefined;
}

function NotApplicableCell() {
  const t = useT();
  return <span className="text-muted-foreground">{t("healthNotApplicable")}</span>;
}

// A count cell: zero is muted, any positive count is an amber StatusChip (a
// dimension that needs attention) — the operator scans for color, not numbers.
function CountCell({
  count,
  label,
  onClick,
}: {
  count: number;
  label?: string;
  onClick?: () => void;
}) {
  if (count === 0) {
    return <span className="text-muted-foreground">0</span>;
  }
  if (onClick) {
    return (
      <button
        type="button"
        className="cursor-pointer rounded focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
        onClick={onClick}
        aria-label={label ? `${label}: ${count}` : String(count)}
      >
        <StatusChip tone="amber" dot={false}>
          {count}
        </StatusChip>
      </button>
    );
  }
  return (
    <StatusChip tone="amber" dot={false}>
      {count}
    </StatusChip>
  );
}

// Drilldown state: which library + dimension is open.
interface DrillTarget {
  libraryID: string;
  libraryTitle: string;
  dimKey: string;
  dimLabel: StringKey;
  selectedBulkField?: string;
  bulkLabel?: StringKey;
  confirmKey?: StringKey;
  bulkBlockedReason?: StringKey;
}

// HealthDrillPanel: the per-item list for one (library, dimension) pair.
// Items are checkboxed; select-all and a bulk-action button operate over
// checked IDs. Duplicate dimension uses libraryDuplicateGroups; all others
// use libraryHealthItems.
function HealthDrillPanel({
  target,
  onBack,
  onDone,
}: {
  target: DrillTarget;
  onBack: () => void;
  onDone: () => void;
}) {
  const t = useT();
  const [checked, setChecked] = useState<Set<string>>(new Set());

  const isDuplicate = target.dimKey === "duplicate";
  const {
    data: plainItems,
    loading: plainLoading,
    failed: plainFailed,
  } = useAsync(
    (signal?: AbortSignal) =>
      isDuplicate
        ? Promise.resolve([] as HealthItemInfo[])
        : fetchHealthDrilldown(target.libraryID, target.dimKey, signal),
    [target.libraryID, target.dimKey],
  );
  const {
    data: dupGroups,
    loading: dupLoading,
    failed: dupFailed,
  } = useAsync(
    (signal?: AbortSignal) =>
      isDuplicate
        ? fetchDuplicateGroups(target.libraryID, signal)
        : Promise.resolve([] as DuplicateGroupInfo[]),
    [target.libraryID, target.dimKey],
  );

  const loading = isDuplicate ? dupLoading : plainLoading;
  const failed = isDuplicate ? dupFailed : plainFailed;

  const items: HealthItemInfo[] = isDuplicate
    ? (dupGroups ?? []).flatMap((g) => g.items.slice(1))
    : (plainItems ?? []);
  const canSelect = Boolean(target.selectedBulkField && target.bulkLabel);

  function toggleItem(mediaID: string) {
    setChecked((prev) => {
      const next = new Set(prev);
      if (next.has(mediaID)) {
        next.delete(mediaID);
      } else {
        next.add(mediaID);
      }
      return next;
    });
  }

  function toggleAll() {
    if (checked.size === items.length && items.length > 0) {
      setChecked(new Set());
    } else {
      setChecked(new Set(items.map((i) => i.mediaID)));
    }
  }

  function toggleGroup(mediaIDs: string[]) {
    setChecked((prev) => {
      const next = new Set(prev);
      const groupChecked = mediaIDs.every((id) => next.has(id));
      for (const id of mediaIDs) {
        if (groupChecked) {
          next.delete(id);
        } else {
          next.add(id);
        }
      }
      return next;
    });
  }

  async function runSelected() {
    const ids = Array.from(checked);
    if (ids.length === 0 || !target.selectedBulkField) return;
    await runHealthBulkOpSelected(target.selectedBulkField, target.libraryID, ids);
    onDone();
  }

  const allChecked = items.length > 0 && checked.size === items.length;

  return (
    <div className="space-y-3">
      <div className="flex items-center gap-3">
        <Button variant="ghost" size="sm" onClick={onBack}>
          <ArrowLeft className="mr-1 h-4 w-4" aria-hidden />
          {t("healthDrillBack")}
        </Button>
        <span className="text-[15px] font-semibold leading-snug">
          {t("healthDrillTitle", {
            library: target.libraryTitle,
            dimension: t(target.dimLabel),
          })}
        </span>
      </div>

      {loading ? (
        <Skeleton className="h-48 w-full" />
      ) : failed ? (
        <ErrorState message="healthDrillUnavailable" />
      ) : items.length === 0 ? (
        <EmptyState message="healthDrillEmpty" />
      ) : (
        <>
          {canSelect && target.selectedBulkField && target.bulkLabel && (
            <div className="flex items-center gap-3">
              <Button variant="outline" size="sm" onClick={toggleAll}>
                {t(allChecked ? "healthDrillClearSelection" : "healthDrillSelectAll")}
              </Button>
              {target.confirmKey ? (
                <ConfirmButton
                  size="sm"
                  variant="destructive"
                  label={t(target.bulkLabel)}
                  confirm={target.confirmKey}
                  disabled={checked.size === 0}
                  onConfirm={runSelected}
                />
              ) : (
                <Button
                  size="sm"
                  variant="outline"
                  disabled={checked.size === 0 || Boolean(target.bulkBlockedReason)}
                  title={target.bulkBlockedReason ? t(target.bulkBlockedReason) : undefined}
                  onClick={() => void runSelected()}
                >
                  {t(target.bulkLabel)}
                </Button>
              )}
              {target.bulkBlockedReason && (
                <span className="text-dense text-muted-foreground">
                  {t(target.bulkBlockedReason)}
                </span>
              )}
            </div>
          )}

          <div className="console-table">
            <ul className="divide-y divide-border">
              {isDuplicate
                ? (dupGroups ?? []).map((group) => {
                    const redundantIDs = group.items.slice(1).map((item) => item.mediaID);
                    const groupChecked =
                      redundantIDs.length > 0 && redundantIDs.every((id) => checked.has(id));
                    const survivor = group.items[0];
                    return (
                      <li
                        key={group.items.map((item) => item.mediaID).join(":")}
                        className="space-y-1 px-3 py-2"
                      >
                        <label className="flex cursor-pointer items-center gap-2 text-dense text-muted-foreground">
                          {canSelect ? (
                            <Checkbox
                              checked={groupChecked}
                              onCheckedChange={() => toggleGroup(redundantIDs)}
                              aria-label={survivor?.title || survivor?.mediaID}
                            />
                          ) : (
                            <span className="w-4 shrink-0" />
                          )}
                          <span className="truncate">{survivor?.title || survivor?.mediaID}</span>
                          <span className="ml-auto shrink-0 text-[11px]">
                            {t("healthDuplicateSurvivor")}
                          </span>
                        </label>
                        {group.items.slice(1).map((item) => (
                          <div
                            key={item.mediaID}
                            className="flex items-center gap-2 pl-6 text-dense"
                          >
                            <span className="truncate">{item.title || item.mediaID}</span>
                          </div>
                        ))}
                      </li>
                    );
                  })
                : items.map((item) => (
                    <li key={item.mediaID} className="px-3 py-2">
                      <label className="flex cursor-pointer items-center gap-2 text-dense">
                        {canSelect && (
                          <Checkbox
                            checked={checked.has(item.mediaID)}
                            onCheckedChange={() => toggleItem(item.mediaID)}
                            aria-label={item.title || item.mediaID}
                          />
                        )}
                        <span className="truncate">{item.title || item.mediaID}</span>
                      </label>
                    </li>
                  ))}
            </ul>
          </div>
        </>
      )}
    </div>
  );
}

// Library Health Dashboard (ADR-0042): per-Library counts across the
// dimensions each Library's plugin can check, plus an aggregate row. Non-zero counts open per-item drilldowns;
// destructive selected operations stay confirm-gated.
export function Health() {
  const t = useT();
  const { data, loading, failed, refresh } = useAsync(fetchLibraryHealth);
  const rows = data ?? [];
  const [drill, setDrill] = useState<DrillTarget | null>(null);

  async function runBulk(field: string, libraryID: string) {
    await runHealthBulkOp(field, libraryID);
    await refresh();
  }

  function openDrill(row: LibraryHealthInfo, dim: HealthDimension) {
    setDrill({
      libraryID: row.libraryID,
      libraryTitle: row.title,
      dimKey: dim.dimKey,
      dimLabel: dim.label,
      selectedBulkField: dim.selectedBulkField,
      bulkLabel: dim.bulkLabel,
      confirmKey: dim.confirmKey,
      bulkBlockedReason: bulkBlockedReason(row.counts, dim),
    });
  }

  function closeDrill() {
    setDrill(null);
  }

  async function drillDone() {
    setDrill(null);
    await refresh();
  }

  const columns = [
    { key: "library", label: t("library") },
    ...DIMENSIONS.map((dim) => ({
      key: dim.countKey,
      label: t(dim.label),
      numeric: true,
    })),
  ];

  // When a drilldown is open, show only the drill panel.
  if (drill) {
    return (
      <Section heading="libraryHealthHeading">
        <HealthDrillPanel target={drill} onBack={closeDrill} onDone={() => void drillDone()} />
      </Section>
    );
  }

  return (
    <Section
      heading="libraryHealthHeading"
      lead="libraryHealthLead"
      actions={
        <>
          <HelpTip label={t("libraryHealthHeading")} text="healthColumnsTip" />
          <Button variant="outline" size="sm" onClick={() => void refresh()}>
            <RefreshCw className="console-nav-icon" aria-hidden />
            {t("libraryHealthRefresh")}
          </Button>
        </>
      }
    >
      {loading ? (
        <Skeleton className="h-48 w-full" />
      ) : failed ? (
        <ErrorState message="libraryHealthUnavailable" />
      ) : rows.length === 0 ? (
        <EmptyState message="libraryHealthEmpty" />
      ) : (
        // Wraps the shared DataTable so health-only CSS can wrap the per-cell
        // actions and stack rows on phones instead of clipping the right columns.
        <div className="console-health-table">
          <DataTable columns={columns}>
            <TableRow className="is-aggregate">
              <TableCell>{t("libraryHealthAggregate")}</TableCell>
              {(() => {
                const agg = aggregateCounts(rows);
                return DIMENSIONS.map((dim) => (
                  <TableCell key={dim.countKey} className="console-num" data-label={t(dim.label)}>
                    {agg[dim.countKey] === undefined ? (
                      <NotApplicableCell />
                    ) : (
                      <CountCell count={agg[dim.countKey]} />
                    )}
                  </TableCell>
                ));
              })()}
            </TableRow>
            {rows.map((row) => (
              <TableRow key={row.libraryID}>
                <TableCell className="font-medium">
                  {row.title}
                  {row.counts["no-scraper"] !== undefined && (
                    <span className="block text-dense font-normal text-muted-foreground">
                      {t("healthNoPluginIdentifies")}
                    </span>
                  )}
                </TableCell>
                {DIMENSIONS.map((dim) => {
                  const raw = row.counts[dim.countKey];
                  if (raw === undefined) {
                    return (
                      <TableCell
                        key={dim.countKey}
                        className="console-num"
                        data-label={t(dim.label)}
                      >
                        <NotApplicableCell />
                      </TableCell>
                    );
                  }
                  const count = raw;
                  const blocked = bulkBlockedReason(row.counts, dim);
                  return (
                    <TableCell key={dim.countKey} className="console-num" data-label={t(dim.label)}>
                      <div className="console-health-cell flex flex-wrap items-center justify-end gap-2">
                        <CountCell
                          count={count}
                          label={t(dim.label)}
                          onClick={count > 0 ? () => openDrill(row, dim) : undefined}
                        />
                        {dim.dimKey === "unmatched" && count > 0 && (
                          <Button asChild size="sm" variant="outline">
                            <Link to="/manage/unmatched">{t("unmatchedFixMatch")}</Link>
                          </Button>
                        )}
                        {dim.summaryBulkField &&
                          dim.bulkLabel &&
                          count > 0 &&
                          (dim.confirmKey ? (
                            <ConfirmButton
                              size="sm"
                              variant="outline"
                              label={t(dim.bulkLabel)}
                              confirm={dim.confirmKey}
                              onConfirm={() => runBulk(dim.summaryBulkField!, row.libraryID)}
                            />
                          ) : (
                            <Button
                              size="sm"
                              variant="outline"
                              disabled={Boolean(blocked)}
                              title={blocked ? t(blocked) : undefined}
                              aria-description={blocked ? t(blocked) : undefined}
                              onClick={() => void runBulk(dim.summaryBulkField!, row.libraryID)}
                            >
                              {t(dim.bulkLabel)}
                            </Button>
                          ))}
                      </div>
                    </TableCell>
                  );
                })}
              </TableRow>
            ))}
          </DataTable>
        </div>
      )}
    </Section>
  );
}
