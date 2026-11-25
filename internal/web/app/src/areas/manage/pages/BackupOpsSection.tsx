import { useEffect, useState } from "react";
import { withAdminEpoch } from "@/areas/console/adminEpoch";
import { EmptyLine, ErrorLine, ManageSection, StatusBadge } from "@/components/beamfall/workbench";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import { ApiError } from "@/lib/api";
import {
  acknowledgeBackupBanner,
  fetchBackupStatus,
  setBackupUpgradeGate,
  verifyBackupNow,
} from "@/lib/graphql";
import type { BackupBanner, BackupDrillInfo, BackupStatusInfo } from "@/lib/graphql/types";
import { useT } from "@/lib/i18n";
import { formatDateTime } from "../lib/schedule";

// Backup health (CORE-0778 BKUP-14/15, ADR-0192 §5 L3 and §6), to the approved
// console-ops-pack mockup backup-status-history.html:
//   - the two distinct incident banners, job failed and integrity failed, each
//     acknowledged on its own (a later verified backup clears both);
//   - recovery evidence: the last restore drill and the trusted recovery point;
//   - "Verify my backup now", the restore drill against the selected set. The
//     key lives only in this input and the one request, and is cleared after;
//   - the update gate: soft by default, the hard block an explicit opt-in.

function refusalMessage(err: unknown, fallback: string): string {
  return err instanceof ApiError && err.message ? err.message : fallback;
}

export function BackupOpsSection({ envelope, setName }: { envelope: string; setName: string }) {
  const t = useT();
  const [status, setStatus] = useState<BackupStatusInfo | null>(null);
  const [loadFailed, setLoadFailed] = useState(false);
  const [busy, setBusy] = useState<"" | BackupBanner | "gate" | "drill">("");
  const [error, setError] = useState("");
  const [backupKey, setBackupKey] = useState("");
  const [drill, setDrill] = useState<BackupDrillInfo | null>(null);

  useEffect(() => {
    const controller = new AbortController();
    const signal = withAdminEpoch(controller.signal);
    fetchBackupStatus(signal)
      .then((next) => {
        if (!signal.aborted) setStatus(next);
      })
      .catch(() => {
        if (!signal.aborted) setLoadFailed(true);
      });
    return () => controller.abort();
  }, []);

  const onAcknowledge = async (banner: BackupBanner) => {
    setError("");
    setBusy(banner);
    try {
      setStatus(await acknowledgeBackupBanner(banner));
    } catch (err) {
      setError(refusalMessage(err, t("backupOpsAcknowledgeFailed")));
    } finally {
      setBusy("");
    }
  };

  const onGate = async (hard: boolean) => {
    setError("");
    setBusy("gate");
    try {
      setStatus(await setBackupUpgradeGate(hard ? "hard" : "soft"));
    } catch (err) {
      setError(refusalMessage(err, t("backupOpsGateFailed")));
    } finally {
      setBusy("");
    }
  };

  const onDrill = async () => {
    setError("");
    setDrill(null);
    setBusy("drill");
    try {
      const result = await verifyBackupNow(envelope, backupKey);
      setDrill(result);
      setStatus((prev) =>
        prev
          ? {
              ...prev,
              lastDrill: {
                at: result.at,
                verified: result.verified,
                set: result.set,
                reason: result.reason,
              },
            }
          : prev,
      );
    } catch (err) {
      setError(refusalMessage(err, t("backupOpsVerifyFailed")));
    } finally {
      setBackupKey("");
      setBusy("");
    }
  };

  // The policy defaults to soft: only an explicit "hard" from the server
  // turns the toggle on.
  const hard = status?.upgradeGate === "hard";
  const lastDrill = status?.lastDrill ?? null;
  const incidents = status ? status.backupFailedBanner || status.integrityFailedBanner : false;

  return (
    <ManageSection title={t("backupOpsHeading")} lead={t("backupOpsLead")}>
      <div className="space-y-4" data-backup-ops>
        {loadFailed && <ErrorLine message={t("backupOpsUnavailable")} />}

        <section aria-label={t("backupOpsIncidentsLabel")} className="space-y-2">
          {status?.backupFailedBanner && (
            <IncidentBanner
              kind="backup-failed"
              title={t("backupOpsJobFailedTitle")}
              body={t("backupOpsJobFailedBody", {
                time: formatDateTime(status.lastFailure?.at ?? ""),
                reason: status.lastFailure?.failureClass || "unknown",
              })}
              busy={busy}
              onAcknowledge={onAcknowledge}
            />
          )}
          {status?.integrityFailedBanner && (
            <IncidentBanner
              kind="integrity-failed"
              title={t("backupOpsIntegrityFailedTitle")}
              body={t("backupOpsIntegrityFailedBody", {
                time: formatDateTime(status.lastIntegrityFailure?.at ?? ""),
              })}
              busy={busy}
              onAcknowledge={onAcknowledge}
            />
          )}
          {status && !incidents && <EmptyLine message={t("backupOpsNoIncidents")} />}
        </section>

        <div className="space-y-2">
          <h3 className="text-sm font-semibold">{t("backupOpsEvidenceHeading")}</h3>
          <dl className="grid gap-2 text-sm sm:grid-cols-2">
            <div className="rounded border px-3 py-2" data-evidence="drill">
              <dt className="text-muted-foreground">{t("backupOpsLastDrill")}</dt>
              <dd className="m-0 flex items-center gap-2">
                {lastDrill ? (
                  <>
                    <StatusBadge state={lastDrill.verified ? "green" : "red"}>
                      {lastDrill.verified ? t("backupOpsDrillPass") : t("backupOpsDrillFail")}
                    </StatusBadge>
                    <span>{formatDateTime(lastDrill.at)}</span>
                  </>
                ) : (
                  t("backupOpsDrillNever")
                )}
              </dd>
            </div>
            <div className="rounded border px-3 py-2" data-evidence="recovery-point">
              <dt className="text-muted-foreground">{t("backupOpsTrustedPoint")}</dt>
              <dd className="m-0">
                {status?.lastSuccess ? (
                  <span>
                    <span className="manage-cell-num">{status.lastSuccess.set}</span> ·{" "}
                    {formatDateTime(status.lastSuccess.at)}
                  </span>
                ) : (
                  t("backupOpsTrustedNone")
                )}
              </dd>
            </div>
          </dl>
        </div>

        <div className="space-y-2">
          <div className="space-y-1">
            <h3 className="text-sm font-semibold">{t("backupOpsVerifyHeading")}</h3>
            <p className="text-sm text-muted-foreground">{t("backupOpsVerifyLead")}</p>
          </div>
          {!envelope ? (
            <EmptyLine message={t("backupOpsVerifyPickSet")} />
          ) : (
            <div className="flex flex-wrap items-end gap-3">
              <div className="grow space-y-1">
                <Label htmlFor="backup-drill-key">{t("backupOpsVerifyKeyLabel")}</Label>
                <Input
                  id="backup-drill-key"
                  type="password"
                  autoComplete="off"
                  value={backupKey}
                  onChange={(e) => setBackupKey(e.target.value)}
                />
              </div>
              <Button type="button" onClick={onDrill} disabled={busy !== "" || !backupKey.trim()}>
                {busy === "drill" ? t("backupOpsVerifyRunning") : t("backupOpsVerifyRun")}
              </Button>
            </div>
          )}
          {drill && (
            <p role="status" className="text-sm" data-drill-verified={drill.verified}>
              {drill.verified
                ? t("backupOpsVerifyPassed", {
                    databases: drill.databases ?? 0,
                    libraries: drill.libraries ?? 0,
                    set: drill.set || setName,
                  })
                : t("backupOpsVerifyFailedResult", {
                    set: drill.set || setName,
                    reason: drill.reason,
                  })}
            </p>
          )}
        </div>

        <div className="space-y-1">
          <h3 className="text-sm font-semibold">{t("backupOpsGateHeading")}</h3>
          <div className="flex items-start gap-3">
            <Switch
              id="backup-upgrade-gate"
              checked={hard}
              disabled={!status || busy !== ""}
              onCheckedChange={(next) => void onGate(next)}
              data-upgrade-gate={status?.upgradeGate ?? "soft"}
            />
            <div className="space-y-0.5">
              <Label htmlFor="backup-upgrade-gate">{t("backupOpsGateHardLabel")}</Label>
              <p className="m-0 text-sm text-muted-foreground">{t("backupOpsGateHardHelp")}</p>
            </div>
          </div>
        </div>

        {error && <ErrorLine message={error} />}
      </div>
    </ManageSection>
  );
}

function IncidentBanner({
  kind,
  title,
  body,
  busy,
  onAcknowledge,
}: {
  kind: BackupBanner;
  title: string;
  body: string;
  busy: string;
  onAcknowledge: (banner: BackupBanner) => void;
}) {
  const t = useT();
  return (
    <div
      role="alert"
      data-banner={kind}
      className="flex flex-wrap items-start justify-between gap-3 rounded-md border border-destructive/40 bg-destructive/5 px-3 py-2 text-sm"
    >
      <div className="space-y-0.5">
        <p className="m-0 font-semibold">{title}</p>
        <p className="m-0">{body}</p>
      </div>
      <Button
        type="button"
        variant="outline"
        size="sm"
        onClick={() => onAcknowledge(kind)}
        disabled={busy !== ""}
      >
        {busy === kind ? t("backupOpsAcknowledging") : t("backupOpsAcknowledge")}
      </Button>
    </div>
  );
}
