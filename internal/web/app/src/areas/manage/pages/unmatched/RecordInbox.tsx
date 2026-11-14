import { useCallback, useEffect, useState } from "react";
import { EmptyLine, ErrorLine, ManageSection } from "@/components/beamfall/workbench";
import { Badge } from "@/components/ui/badge";
import { fetchUnmatchedRecords, fetchUnmatchedSuppressions } from "@/lib/graphql";
import type { UnmatchedRecordInfo, UnmatchedSuppressionInfo } from "@/lib/graphql/types";
import { useT } from "@/lib/i18n";
import { CreateMediaForm } from "./CreateMediaForm";
import { reasonLabel } from "./reasons";
import { SuppressionPanel } from "./SuppressionPanel";

/**
 * RecordInbox is the Media-INDEPENDENT Unmatched Record review surface
 * (ADR-0068). It is deliberately separate from the Media-keyed scan queue above
 * it: a record may back no Media at all, so the two inboxes carry different
 * identities and different resolutions — create-a-Media (METAPARITY-6) and
 * scoped suppression (METAPARITY-7) both key on the RECORD.
 */
export function RecordInbox({ libraryID }: { libraryID: string }) {
  const t = useT();
  const [records, setRecords] = useState<UnmatchedRecordInfo[]>([]);
  const [suppressions, setSuppressions] = useState<UnmatchedSuppressionInfo[]>([]);
  const [activeID, setActiveID] = useState("");
  const [notice, setNotice] = useState("");
  const [error, setError] = useState("");

  const load = useCallback(async () => {
    try {
      const [pending, active] = await Promise.all([
        fetchUnmatchedRecords(),
        fetchUnmatchedSuppressions(),
      ]);
      setRecords(pending);
      setSuppressions(active);
      setError("");
    } catch {
      setError(t("unmatchedUnavailable"));
    }
  }, [t]);

  useEffect(() => {
    void load();
  }, [load]);

  const active = records.find((record) => record.id === activeID);

  const onCreated = (title: string) => {
    setNotice(t("unmatchedCreateSucceeded", { title }));
    setActiveID("");
    void load();
  };

  if (error) {
    return (
      <ManageSection title={t("unmatchedHeading")} lead={t("unmatchedCreateLead")}>
        <ErrorLine message={error} />
      </ManageSection>
    );
  }
  if (records.length === 0) return null;

  return (
    <ManageSection title={t("unmatchedHeading")} lead={t("unmatchedCreateLead")}>
      {notice && <p className="mb-2 text-sm text-muted-foreground">{notice}</p>}
      <div className="grid gap-3 xl:grid-cols-[minmax(260px,1fr)_minmax(320px,1.2fr)]">
        <ul className="divide-y rounded-md border border-border bg-card">
          {records.map((record) => (
            <li key={record.id}>
              <button
                type="button"
                className="grid w-full gap-1 px-3 py-2 text-left hover:bg-muted/50 data-[active=true]:bg-muted"
                data-active={activeID === record.id}
                onClick={() => setActiveID(record.id)}
              >
                <div className="flex items-start justify-between gap-2">
                  <span className="min-w-0 break-words font-medium text-foreground">
                    {record.candidateTitle || record.sourceHandle || record.id}
                  </span>
                  <Badge variant="outline">{record.origin}</Badge>
                </div>
                {record.failureReason && (
                  <span className="text-xs text-muted-foreground">
                    {reasonLabel(t, record.failureReason)}
                  </span>
                )}
                {record.sourceHandle && (
                  <span className="text-xs text-muted-foreground">{record.sourceHandle}</span>
                )}
              </button>
            </li>
          ))}
        </ul>
        {active ? (
          <div className="grid gap-3">
            <CreateMediaForm
              key={active.id}
              record={active}
              libraryID={libraryID}
              onCreated={onCreated}
            />
            <SuppressionPanel
              record={active}
              suppressions={suppressions.filter(
                (suppression) => !suppression.recordID || suppression.recordID === active.id,
              )}
              onChanged={() => void load()}
            />
          </div>
        ) : (
          <EmptyLine message={t("unmatchedSearchByTitleYear")} />
        )}
      </div>
    </ManageSection>
  );
}
