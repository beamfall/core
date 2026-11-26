import { RefreshCw } from "lucide-react";
import { type FormEvent, useCallback, useEffect, useState } from "react";
import {
  ControlRow,
  ErrorLine,
  ErrorState,
  Fieldset,
  StatusChip,
  type StatusTone,
} from "@/components/beamfall/workbench";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Skeleton } from "@/components/ui/skeleton";
import { Switch } from "@/components/ui/switch";
import {
  acknowledgeBackupBanner,
  authorizeBackupUpgradeBypass,
  cancelStagedCoreUpdate,
  fetchBackupStatus,
  fetchConsoleConfig,
  fetchStagedCoreUpdate,
  fetchUpdateStatus,
  setConsoleConfig,
  stageCoreUpdate,
  type UpdateCheckState,
} from "@/lib/graphql";
import type { BackupStatusInfo, StagedUpdateInfo } from "@/lib/graphql/types";
import { useT } from "@/lib/i18n";
import type { StringKey } from "@/strings";
import { Section } from "../Section";
import { Segmented } from "../Segmented";
import { useAsync } from "../useAsync";

/**
 * How each update-check state presents. "upToDate" is the ONLY entry that reads
 * as reassuring; a check that could not answer gets its own red presentation so
 * an unreachable or refused release manifest is never mistaken for a current
 * server. Lookup-table dispatch, so adding a state is one row rather than
 * another branch in the chip.
 */
const UPDATE_STATE_DISPLAY: Record<UpdateCheckState, { tone: StatusTone; label: StringKey }> = {
  upToDate: { tone: "green", label: "updateStatusUpToDate" },
  updateAvailable: { tone: "amber", label: "updateStatusUpdateAvailable" },
  disabled: { tone: "neutral", label: "updateStatusDisabled" },
  unavailable: { tone: "red", label: "updateStatusUnavailable" },
};

/**
 * Resolves the presentation for a state token. An absent or unrecognized token
 * falls back to the unavailable row — fail-closed, matching the server, which
 * also never lets an unknown value read as a version claim.
 */
function updateStateDisplay(state: string | undefined) {
  return UPDATE_STATE_DISPLAY[state as UpdateCheckState] ?? UPDATE_STATE_DISPLAY.unavailable;
}

type GateStepState = "done" | "pending" | "blocked";

/**
 * The backup gate a Core update passes (CORE-0778 BKUP-14, ADR-0192 §6), to
 * the approved console-ops-pack mockup update-backup-gate.html. A verified
 * Backup Set with no open failure banner clears it. Otherwise the soft policy
 * (the default) warns and permits; the opt-in hard policy blocks the install
 * step. A status that cannot be read is treated as not clear.
 */
export function backupGateState(status: BackupStatusInfo | null): {
  policy: "soft" | "hard";
  clear: boolean;
  steps: { backup: GateStepState; verify: GateStepState; install: GateStepState };
} {
  const policy = status?.upgradeGate === "hard" ? "hard" : "soft";
  // The gate clears only on a recorded integrity pass that no later failure
  // supersedes (backupStatus.updateGateClear); acknowledging a banner hides
  // it but never clears the gate.
  const clear = status?.updateGateClear === true;
  const backedUp = clear || (Boolean(status?.lastSuccess) && !status?.backupFailedBanner);
  const verified = clear;
  return {
    policy,
    clear,
    steps: {
      backup: backedUp ? "done" : "pending",
      verify: verified ? "done" : "pending",
      install: !clear && policy === "hard" ? "blocked" : "pending",
    },
  };
}

const EMPTY_STAGED_UPDATE: StagedUpdateInfo = {
  staged: false,
  status: "",
  stagedAt: "",
  targetVersion: "",
  channel: "",
  commit: "",
  artifactName: "",
  artifactSHA256: "",
  sourceDistDir: "",
  bypassAuthorized: false,
  appliedAt: "",
  rollbackBinary: "",
};

function BackupUpdateGate({ defaultTargetVersion = "" }: { defaultTargetVersion?: string }) {
  const t = useT();
  const backup = useAsync(fetchBackupStatus);
  const fetchStaged = useCallback(
    (s: AbortSignal) =>
      typeof fetchStagedCoreUpdate === "function"
        ? fetchStagedCoreUpdate(s)
        : Promise.resolve(EMPTY_STAGED_UPDATE),
    [],
  );
  const staged = useAsync(fetchStaged);

  const [reviewOpen, setReviewOpen] = useState(false);
  const [manualTargetVersion, setManualTargetVersion] = useState("");
  const [password, setPassword] = useState("");
  const [confirmation, setConfirmation] = useState("");
  const [reason, setReason] = useState("");
  const [bypassBusy, setBypassBusy] = useState(false);
  const [bypassError, setBypassError] = useState<string | null>(null);

  const [distDir, setDistDir] = useState("");
  const [stageBusy, setStageBusy] = useState(false);
  const [stageError, setStageError] = useState<string | null>(null);
  const [bannerBusy, setBannerBusy] = useState(false);

  if (backup.loading && !backup.data) return null;
  const gate = backupGateState(backup.data);
  const steps: [string, StringKey, GateStepState][] = [
    ["boundary", "updateBackupGateStepBoundary", "done"],
    ["backup", "updateBackupGateStepBackup", gate.steps.backup],
    ["verify", "updateBackupGateStepVerify", gate.steps.verify],
    ["install", "updateBackupGateStepInstall", gate.steps.install],
  ];
  const tone: StatusTone = gate.clear ? "green" : gate.policy === "hard" ? "red" : "amber";
  const failedSet = backup.data?.failedSet ?? "";
  const trimmedDefaultTargetVersion = defaultTargetVersion.trim();
  const effectiveVersion = trimmedDefaultTargetVersion || manualTargetVersion.trim();
  const activeBypass = backup.data?.lastUpgradeBypass;
  const latestFailureAt = [backup.data?.lastFailure?.at, backup.data?.lastIntegrityFailure?.at]
    .filter((value): value is string => Boolean(value))
    .reduce<number>((latest, value) => {
      const parsed = Date.parse(value);
      return Number.isFinite(parsed) && parsed > latest ? parsed : latest;
    }, Number.NEGATIVE_INFINITY);
  const bypassAt = activeBypass?.at ? Date.parse(activeBypass.at) : Number.NaN;
  const bypassSupersededByFailure =
    Number.isFinite(latestFailureAt) && (!Number.isFinite(bypassAt) || latestFailureAt > bypassAt);
  const serverBypassValid = activeBypass?.valid ?? true;
  const matchingBypass =
    gate.policy === "soft" &&
    Boolean(activeBypass) &&
    activeBypass?.policy === "soft" &&
    !activeBypass.appliedAt &&
    serverBypassValid &&
    !bypassSupersededByFailure &&
    (!effectiveVersion || activeBypass.targetVersion === effectiveVersion);
  const canStage = gate.clear || matchingBypass;
  const stagedInfo = staged.data;

  async function onAuthorizeBypass(e: FormEvent) {
    e.preventDefault();
    if (bypassBusy || !effectiveVersion || !confirmation.trim() || !reason.trim() || !password) {
      return;
    }
    setBypassBusy(true);
    setBypassError(null);
    const submittedPassword = password;
    setPassword("");
    try {
      await authorizeBackupUpgradeBypass({
        targetVersion: effectiveVersion,
        confirmation: confirmation.trim(),
        reason: reason.trim(),
        password: submittedPassword,
      });
      setConfirmation("");
      setReason("");
      setReviewOpen(false);
      backup.refresh();
    } catch (err) {
      setBypassError(
        t("backupBypassFailed", {
          detail: err instanceof Error ? err.message : t("couldNotReachServer"),
        }),
      );
    } finally {
      setBypassBusy(false);
    }
  }

  async function onStageUpdate(e: FormEvent) {
    e.preventDefault();
    if (stageBusy || !distDir.trim()) return;
    setStageBusy(true);
    setStageError(null);
    try {
      await stageCoreUpdate(distDir.trim());
      setDistDir("");
      staged.refresh();
    } catch (err) {
      setStageError(
        t("backupStageFailed", {
          detail: err instanceof Error ? err.message : t("couldNotReachServer"),
        }),
      );
    } finally {
      setStageBusy(false);
    }
  }

  async function onCancelStage() {
    if (stageBusy) return;
    setStageBusy(true);
    setStageError(null);
    try {
      await cancelStagedCoreUpdate();
      staged.refresh();
    } catch (err) {
      setStageError(
        t("backupStageFailed", {
          detail: err instanceof Error ? err.message : t("couldNotReachServer"),
        }),
      );
    } finally {
      setStageBusy(false);
    }
  }

  async function onDismissUnverifiedBanner() {
    if (bannerBusy) return;
    setBannerBusy(true);
    try {
      await acknowledgeBackupBanner("unverified-upgrade");
      backup.refresh();
    } finally {
      setBannerBusy(false);
    }
  }

  return (
    <section
      aria-label={t("updateBackupGateHeading")}
      data-backup-gate={gate.policy}
      data-backup-gate-clear={gate.clear}
      className="space-y-3 rounded-md border px-3 py-3 text-sm"
    >
      <div className="flex flex-wrap items-center justify-between gap-2">
        <p className="m-0 font-semibold">{t("updateBackupGateHeading")}</p>
        <StatusChip tone={gate.policy === "hard" ? "red" : "neutral"}>
          {t("backupGatePolicyLabel")}:{" "}
          {gate.policy === "hard" ? t("backupGatePolicyHardBlock") : t("backupGatePolicySoft")}
        </StatusChip>
      </div>

      {backup.data?.unverifiedUpgradeBanner && (
        <div role="alert" className="space-y-1 rounded-md border px-3 py-2 text-xs">
          <div className="flex flex-wrap items-center justify-between gap-2">
            <span className="font-semibold">
              {t("backupUnverifiedUpgradeTitle", {
                from: backup.data.lastUnverifiedUpgrade?.fromVersion ?? "?",
                to:
                  backup.data.lastUnverifiedUpgrade?.toVersion ??
                  backup.data.lastBinaryVersion ??
                  "?",
              })}
            </span>
            <Button
              type="button"
              size="sm"
              variant="outline"
              disabled={bannerBusy}
              onClick={onDismissUnverifiedBanner}
            >
              {t("backupOpsAcknowledge")}
            </Button>
          </div>
          <p className="m-0 text-muted-foreground">
            {t("backupUnverifiedUpgradeBody", {
              reason: backup.data.lastUnverifiedUpgrade?.reason ?? "unverified",
            })}
          </p>
        </div>
      )}

      <ol className="m-0 space-y-1 pl-5">
        {steps.map(([id, label, state]) => (
          <li key={id} data-gate-step={id} data-gate-step-state={state}>
            {t(label)}
          </li>
        ))}
      </ol>

      <p className="m-0">
        <StatusChip tone={tone}>
          {gate.clear
            ? t("updateBackupGateClear", {
                time: new Date(backup.data?.lastSuccess?.at ?? "").toLocaleString(),
              })
            : gate.policy === "hard"
              ? t("updateBackupGateBlocked")
              : t("updateBackupGateWarn")}
        </StatusChip>
      </p>
      <p className="m-0 text-muted-foreground">
        {gate.policy === "hard" ? t("updateBackupGateHard") : t("updateBackupGateSoft")}
      </p>

      {!gate.clear && matchingBypass && activeBypass && (
        <p className="m-0 text-xs font-medium" role="status">
          {t("backupBypassAuthorized", {
            version: activeBypass.targetVersion,
            actor: activeBypass.actorProfileID || "admin",
          })}
        </p>
      )}

      {!gate.clear && (
        <div className="space-y-2 border-t pt-2">
          {gate.policy === "hard" ? (
            <p className="m-0 text-xs text-muted-foreground">{t("backupBypassHardDisabled")}</p>
          ) : (
            <>
              <div className="flex flex-wrap items-center gap-2">
                <Button
                  type="button"
                  size="sm"
                  variant="outline"
                  onClick={() => {
                    setReviewOpen((open) => !open);
                    setBypassError(null);
                  }}
                >
                  {reviewOpen ? t("backupBypassCancelAction") : t("backupBypassReviewAction")}
                </Button>
              </div>
              {reviewOpen && (
                <form
                  onSubmit={onAuthorizeBypass}
                  aria-label={t("backupBypassReviewAction")}
                  className="space-y-2 rounded-md border px-3 py-2"
                >
                  <p className="m-0 text-xs font-medium">{t("backupBypassReauthRequired")}</p>
                  {!trimmedDefaultTargetVersion && (
                    <div className="space-y-1">
                      <Label htmlFor="backup-bypass-version" className="text-xs">
                        {t("updateStatusLatest")}
                      </Label>
                      <Input
                        id="backup-bypass-version"
                        value={manualTargetVersion}
                        onChange={(e) => setManualTargetVersion(e.target.value)}
                        placeholder="v1.0.0"
                        required
                      />
                    </div>
                  )}
                  <div className="space-y-1">
                    <Label htmlFor="backup-bypass-password" className="text-xs">
                      {t("backupBypassPasswordLabel")}
                    </Label>
                    <Input
                      id="backup-bypass-password"
                      type="password"
                      autoComplete="current-password"
                      value={password}
                      onChange={(e) => setPassword(e.target.value)}
                      required
                    />
                  </div>
                  <div className="space-y-1">
                    <Label htmlFor="backup-bypass-confirmation" className="text-xs">
                      {t("backupBypassConfirmationLabel")}
                    </Label>
                    <Input
                      id="backup-bypass-confirmation"
                      value={confirmation}
                      onChange={(e) => setConfirmation(e.target.value)}
                      placeholder={
                        failedSet
                          ? `${effectiveVersion || "v1.0.0"} ${failedSet}`
                          : effectiveVersion || "v1.0.0"
                      }
                      required
                    />
                    <p className="m-0 text-xs text-muted-foreground">
                      {failedSet
                        ? t("backupBypassConfirmationHint", {
                            version: effectiveVersion || "v1.0.0",
                            set: failedSet,
                          })
                        : t("backupBypassConfirmationHintNoSet", {
                            version: effectiveVersion || "v1.0.0",
                          })}
                    </p>
                  </div>
                  <div className="space-y-1">
                    <Label htmlFor="backup-bypass-reason" className="text-xs">
                      {t("backupBypassReasonLabel")}
                    </Label>
                    <Input
                      id="backup-bypass-reason"
                      value={reason}
                      onChange={(e) => setReason(e.target.value)}
                      placeholder={t("backupBypassReasonPlaceholder")}
                      required
                    />
                  </div>
                  {bypassError && <ErrorLine message={bypassError} />}
                  <Button
                    type="submit"
                    size="sm"
                    disabled={
                      bypassBusy ||
                      !password ||
                      !effectiveVersion ||
                      !confirmation.trim() ||
                      !reason.trim()
                    }
                  >
                    {t("backupBypassSubmitAction")}
                  </Button>
                </form>
              )}
            </>
          )}
        </div>
      )}

      <div className="space-y-2 border-t pt-2">
        <p className="m-0 text-xs font-semibold">{t("backupStageHeading")}</p>
        {stagedInfo?.staged ? (
          <div className="space-y-2">
            <p className="m-0 text-xs text-muted-foreground">
              {t("backupStageReadyHint", {
                version: stagedInfo.targetVersion,
                artifact: stagedInfo.artifactName,
              })}
            </p>
            <Button
              type="button"
              size="sm"
              variant="outline"
              disabled={stageBusy}
              onClick={onCancelStage}
            >
              {t("backupStageCancelAction")}
            </Button>
          </div>
        ) : (
          <form onSubmit={onStageUpdate} className="space-y-2">
            {stagedInfo?.status === "applied" && stagedInfo.appliedAt && (
              <p className="m-0 text-xs text-muted-foreground">
                {t("backupStageAppliedHint", {
                  version: stagedInfo.targetVersion,
                  rollback: stagedInfo.rollbackBinary || "—",
                })}
              </p>
            )}
            <div className="space-y-1">
              <Label htmlFor="backup-stage-dist" className="text-xs">
                {t("backupStageDistLabel")}
              </Label>
              <Input
                id="backup-stage-dist"
                value={distDir}
                onChange={(e) => setDistDir(e.target.value)}
                placeholder={t("backupStageDistPlaceholder")}
                disabled={!canStage || stageBusy}
              />
            </div>
            {!canStage && (
              <p className="m-0 text-xs text-muted-foreground">{t("backupStageBlockedHint")}</p>
            )}
            <Button type="submit" size="sm" disabled={!canStage || stageBusy || !distDir.trim()}>
              {t("backupStageAction")}
            </Button>
          </form>
        )}
        {stageError && <ErrorLine message={stageError} />}
      </div>
    </section>
  );
}

function UpdateAvailableGuidance({
  currentVersion,
  latestVersion,
}: {
  currentVersion: string;
  latestVersion: string;
}) {
  const t = useT();

  return (
    <section
      role="status"
      aria-label={t("updateMigrationCautionAria")}
      data-backup-freshness="unknown"
      className="space-y-3 rounded-md border border-[var(--state-warning)]/45 bg-[var(--state-warning)]/10 px-3 py-3 text-sm"
    >
      <div>
        <p className="m-0 font-semibold">{t("updateMigrationCautionLabel")}</p>
        <p className="mt-1 mb-0">{t("updateMigrationImpactUnknown")}</p>
      </div>

      <dl className="grid gap-2 sm:grid-cols-3">
        <div data-update-scope="core" className="rounded border bg-background/50 px-3 py-2">
          <dt className="font-semibold">{t("updateScopeCore")}</dt>
          <dd className="m-0 text-muted-foreground">
            {currentVersion} to {latestVersion}
          </dd>
        </div>
        <div data-update-scope="plugin" className="rounded border bg-background/50 px-3 py-2">
          <dt className="font-semibold">{t("updateScopePlugins")}</dt>
          <dd className="m-0 text-muted-foreground">{t("updatePluginCompatibilityUnknown")}</dd>
        </div>
        <div
          data-update-scope="compatibility"
          className="rounded border bg-background/50 px-3 py-2"
        >
          <dt className="font-semibold">{t("updateScopeCompatibility")}</dt>
          <dd className="m-0 text-muted-foreground">{t("updateCompatibilityReview")}</dd>
        </div>
      </dl>

      <div className="space-y-1 rounded-[var(--radius-field)] bg-[color-mix(in_oklab,var(--state-warning)_10%,transparent)] px-4 py-3">
        <p className="m-0">{t("updateMigrationPossible")}</p>
        <p className="m-0 font-semibold">{t("updateBackupRequired")}</p>
        <p className="m-0">{t("updateExportNotBackup")}</p>
        <p className="m-0">{t("updateRollbackSnapshotNotice")}</p>
        <p className="m-0">{t("updateDowngradeOffline")}</p>
      </div>

      <div aria-label={t("updateRecoveryArtifactsHeading")}>
        <p className="m-0 font-semibold">{t("updateRecoveryArtifactsHeading")}</p>
        <ul className="mt-2 mb-0 space-y-2 pl-5 text-muted-foreground">
          <li data-artifact-class="v2-backup-set">
            <strong className="text-foreground">{t("updateArtifactV2Title")}</strong> —{" "}
            {t("updateArtifactV2Body")}
          </li>
          <li data-artifact-class="v1-backup">
            <strong className="text-foreground">{t("updateArtifactV1Title")}</strong> —{" "}
            {t("updateArtifactV1Body")}
          </li>
          <li data-artifact-class="rollback-snapshot">
            <strong className="text-foreground">{t("updateArtifactRollbackTitle")}</strong> —{" "}
            {t("updateArtifactRollbackBody")}
          </li>
        </ul>
      </div>

      <div className="flex flex-wrap gap-2">
        <Button asChild variant="outline" size="sm">
          <a href="/console/backups">{t("updateCreateVerifyBackup")}</a>
        </Button>
        <Button asChild variant="outline" size="sm">
          <a href="/console/backups">{t("updateExportMetadata")}</a>
        </Button>
        <Button asChild variant="outline" size="sm">
          <a href="#offline-restore-guidance">{t("updateOfflineRestoreGuidance")}</a>
        </Button>
      </div>

      <section
        id="offline-restore-guidance"
        role="region"
        aria-labelledby="offline-restore-guidance-title"
        tabIndex={-1}
        className="space-y-2 rounded border bg-background/50 px-3 py-3"
      >
        <h3 id="offline-restore-guidance-title" className="m-0 text-sm font-semibold">
          {t("updateOfflineRestoreGuidance")}
        </h3>
        <ol className="m-0 space-y-1 pl-5 text-muted-foreground">
          <li>{t("updateRollbackSelectVersion")}</li>
          <li>{t("updateRollbackStop")}</li>
          <li>
            {t("updateRollbackRun")}
            <code className="mt-1 block break-all text-foreground">
              {t("updateRollbackCommand")}
            </code>
          </li>
          <li>{t("updateRollbackRestart")}</li>
        </ol>
        <a
          href={t("updateCanonicalRestoreHref")}
          target="_blank"
          rel="noreferrer"
          className="underline underline-offset-2"
        >
          {t("updateCanonicalRestoreProcedure")}
        </a>
      </section>
    </section>
  );
}

// Update awareness (ADR-0079): the update-check switch, channel and the
// current/latest versions. It owns the Maintain › Updates destination.
export function UpdateAwareness() {
  const t = useT();
  const config = useAsync(fetchConsoleConfig);
  const status = useAsync(fetchUpdateStatus);
  const [enabled, setEnabled] = useState(false);
  const [channel, setChannel] = useState("stable");

  useEffect(() => {
    if (!config.data) return;
    setEnabled(config.data.updateCheckEnabled);
    setChannel(config.data.updateCheckChannel || "stable");
  }, [config.data]);

  async function onSave(e: FormEvent) {
    e.preventDefault();
    const next = await setConsoleConfig({
      updateCheckEnabled: enabled,
      updateCheckChannel: channel,
    });
    config.setData(next);
    await status.refresh();
  }

  const latest = status.data?.latestVersion || t("updateStatusNoneKnown");
  const display = updateStateDisplay(status.data?.state);
  // Covers unrecognized tokens too: they resolve to the same unavailable row.
  const checkFailed = status.data != null && display === UPDATE_STATE_DISPLAY.unavailable;
  const availableUpdate =
    status.data?.state === "updateAvailable" && status.data.updateAvailable === true
      ? status.data
      : null;

  return (
    <Section
      heading="updateAwarenessHeading"
      lead="updateAwarenessLead"
      actions={
        <Button variant="outline" size="sm" onClick={() => void status.refresh()}>
          <RefreshCw className="console-nav-icon" aria-hidden />
          {t("updateStatusRefresh")}
        </Button>
      }
    >
      {config.loading ? (
        <Skeleton className="h-32 w-full" />
      ) : config.failed ? (
        <ErrorState message="updateAwarenessUnavailable" />
      ) : (
        <div className="space-y-4">
          <form onSubmit={onSave} className="space-y-3">
            <Fieldset title="updateAwarenessHeading" help="updateAwarenessHelp">
              <ControlRow
                label="updateCheckEnabled"
                help="updateCheckEnabledHelp"
                control={
                  <Switch
                    checked={enabled}
                    onCheckedChange={setEnabled}
                    aria-label={t("updateCheckEnabled")}
                  />
                }
              />
              <ControlRow
                label="updateCheckChannel"
                help="updateCheckChannelHelp"
                control={
                  <Segmented
                    label={t("updateCheckChannel")}
                    value={channel}
                    disabled={!enabled}
                    onChange={setChannel}
                    options={[
                      { value: "stable", label: t("updateChannelStable") },
                      { value: "beta", label: t("updateChannelBeta") },
                    ]}
                  />
                }
              />
            </Fieldset>
            <div className="console-card console-card-pad space-y-3">
              {availableUpdate && (
                <>
                  <div className="rounded-md border border-[var(--state-warning)]/45 bg-[var(--state-warning)]/10 px-3 py-2 text-sm">
                    {t("updateAvailableBanner", {
                      version: availableUpdate.latestVersion,
                      channel: availableUpdate.channel,
                    })}
                  </div>
                  <UpdateAvailableGuidance
                    currentVersion={availableUpdate.currentVersion}
                    latestVersion={availableUpdate.latestVersion}
                  />
                </>
              )}
              {checkFailed && (
                <div
                  role="status"
                  className="rounded-md border border-[var(--state-danger)]/45 bg-[var(--state-danger)]/10 px-3 py-2 text-sm"
                >
                  {t("updateStatusUnavailableNotice")}
                </div>
              )}
              <ControlRow
                label="updateStatusState"
                control={<StatusChip tone={display.tone}>{t(display.label)}</StatusChip>}
              />
              <ControlRow
                label="updateStatusCurrent"
                control={
                  <span className="console-mono">
                    {status.data?.currentVersion || t("updateVersionUnknown")}
                  </span>
                }
              />
              <ControlRow
                label="updateStatusLatest"
                control={<span className="console-mono">{latest}</span>}
              />
              <p className="m-0 text-[12px] leading-snug text-muted-foreground">
                {t("updateAwarenessDisclosure")}
              </p>
            </div>
            <Button type="submit">{t("updateAwarenessSave")}</Button>
          </form>
        </div>
      )}
      <BackupUpdateGate defaultTargetVersion={availableUpdate?.latestVersion ?? ""} />
    </Section>
  );
}
