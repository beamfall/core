import { RefreshCw, ScrollText } from "lucide-react";
import { DataTable, EmptyState, ErrorState } from "@/components/beamfall/workbench";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { TableCell, TableRow } from "@/components/ui/table";
import { fetchActivityLog } from "@/lib/graphql";
import { useT } from "@/lib/i18n";
import { Section } from "../Section";
import { useAsync } from "../useAsync";

// Activity log (ADR-0012 §11 / ADR-0040): the append-only, reveal-filtered
// operator view of recent server actions (latest 50). Read-only.
export function ActivityLog() {
  const t = useT();
  const { data, loading, failed, refresh } = useAsync(fetchActivityLog);
  const entries = data ?? [];

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
      {loading ? (
        <Skeleton className="h-48 w-full" />
      ) : failed ? (
        <ErrorState message="activityLogUnavailable" />
      ) : entries.length === 0 ? (
        <EmptyState message="activityLogEmpty" icon={ScrollText} />
      ) : (
        <DataTable
          columns={[
            { key: "when", label: t("activityLogHeading") },
            { key: "action", label: t("title") },
          ]}
        >
          {entries.map((entry) => (
            <TableRow key={entry.id}>
              <TableCell className="console-mono whitespace-nowrap">{entry.occurredAt}</TableCell>
              <TableCell>
                <span className="font-medium">{entry.action}</span>
                {entry.targetType && (
                  <span className="text-muted-foreground">
                    {" — "}
                    {entry.targetType}
                    {entry.targetID ? `:${entry.targetID}` : ""}
                  </span>
                )}
              </TableCell>
            </TableRow>
          ))}
        </DataTable>
      )}
    </Section>
  );
}
