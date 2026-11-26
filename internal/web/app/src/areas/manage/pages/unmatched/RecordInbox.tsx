import { useCallback, useEffect, useState } from "react";
import { reasonLabel } from "@/components/assign/matchLabels";
import { EmptyLine, ErrorLine, ManageSection } from "@/components/beamfall/workbench";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  fetchUnmatchedRecords,
  fetchUnmatchedSuppressions,
  resetUnmatchedSuppression,
  type SuppressionScope,
} from "@/lib/graphql";
import type {
  LibraryInfo,
  UnmatchedRecordInfo,
  UnmatchedSuppressionInfo,
} from "@/lib/graphql/types";
import { useT } from "@/lib/i18n";
import { formatDateTime } from "../../lib/schedule";
import { CreateMediaForm } from "./CreateMediaForm";
import { SuppressionPanel, suppressionScopeLabel } from "./SuppressionPanel";

/**
 * RecordInbox is the Media-INDEPENDENT Unmatched Record review surface
 * (ADR-0068). It is deliberately separate from the Media-keyed scan queue above
 * it: a record may back no Media at all, so the two inboxes carry different
 * identities and different resolutions — create-a-Media (METAPARITY-6) and
 * scoped suppression (METAPARITY-7) both key on the RECORD.
 */
export function RecordInbox({
  libraryID,
  libraries,
}: {
  libraryID: string;
  libraries?: LibraryInfo[];
}) {
  const t = useT();
  const [records, setRecords] = useState<UnmatchedRecordInfo[]>([]);
  const [suppressions, setSuppressions] = useState<UnmatchedSuppressionInfo[]>([]);
  const [activeID, setActiveID] = useState("");
  const [notice, setNotice] = useState("");
  const [error, setError] = useState("");
  const [resetError, setResetError] = useState("");
  const [busyReset, setBusyReset] = useState(false);

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

  const active = records.find((record) => record.id === activeID) ?? records[0];

  const onCreated = (title: string) => {
    setNotice(t("unmatchedCreateSucceeded", { title }));
    setActiveID("");
    void load();
  };

  if (error) {
    return (
      <ManageSection title={t("unmatchedHeading")}>
        <ErrorLine message={error} />
      </ManageSection>
    );
  }
  if (records.length === 0 && suppressions.length === 0) return null;

  const sectionTitle =
    records.length === 0 ? t("unmatchedSuppressActive") : t("unmatchedCreateHeading");
  const sectionLead = records.length === 0 ? undefined : t("unmatchedCreateLead");

  return (
    <ManageSection title={sectionTitle} lead={sectionLead}>
      {notice && <p className="mb-2 text-sm text-muted-foreground">{notice}</p>}
      {records.length === 0 ? (
        <div className="grid gap-2 rounded-md border border-border bg-card p-3">
          <ErrorLine message={resetError} />
          <ul className="grid gap-2">
            {suppressions.map((item) => (
              <li key={`${item.scope}:${item.key}`} className="grid gap-1">
                <div className="flex items-center justify-between gap-2">
                  <span className="text-sm text-foreground">
                    {t(suppressionScopeLabel(item.scope))}
                  </span>
                  <Button
                    size="sm"
                    variant="ghost"
                    disabled={busyReset}
                    onClick={() => {
                      setResetError("");
                      setBusyReset(true);
                      const scope = item.scope as SuppressionScope;
                      const target =
                        scope === "file"
                          ? {
                              scope,
                              recordID: item.recordID || undefined,
                              fingerprint: item.key,
                              sourceHandle: item.sourceHandle,
                            }
                          : scope === "candidate"
                            ? {
                                scope,
                                recordID: item.recordID,
                                candidateID: item.candidateID || item.key,
                              }
                            : { scope, recordID: item.recordID || item.key };
                      void resetUnmatchedSuppression(target)
                        .then(() => load())
                        .catch(() => setResetError(t("unmatchedSuppressFailed")))
                        .finally(() => setBusyReset(false));
                    }}
                  >
                    {t("unmatchedSuppressReset")}
                  </Button>
                </div>
                <span className="text-xs text-muted-foreground">
                  {t("unmatchedSuppressProvenance", {
                    actor: item.actorProfileID || t("unmatchedSuppressUnknownActor"),
                    when: item.createdAt ? formatDateTime(item.createdAt) : "",
                  })}
                  {item.sourceHandle ? ` · ${item.sourceHandle}` : ""}
                </span>
              </li>
            ))}
          </ul>
        </div>
      ) : (
        <div className="grid gap-3 lg:grid-cols-[minmax(220px,0.8fr)_minmax(320px,1.4fr)]">
          <ul className="divide-y self-start rounded-md border border-border bg-card lg:max-h-[65vh] lg:overflow-y-auto">
            {records.map((record) => (
              <li key={record.id}>
                <button
                  type="button"
                  className="grid w-full gap-1 px-3 py-2 text-left hover:bg-muted/50 data-[active=true]:bg-muted"
                  data-active={active?.id === record.id}
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
            <div key={active.id} className="grid gap-3">
              <CreateMediaForm
                record={active}
                libraryID={libraryID}
                libraries={libraries}
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
            <EmptyLine message={t("unmatchedEmpty")} />
          )}
        </div>
      )}
    </ManageSection>
  );
}
