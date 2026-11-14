import { RefreshCw } from "lucide-react";
import { type FormEvent, useEffect, useState } from "react";
import {
  ControlRow,
  ErrorState,
  Fieldset,
  StatusChip,
  type StatusTone,
} from "@/components/beamfall/workbench";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { Switch } from "@/components/ui/switch";
import {
  fetchConsoleConfig,
  fetchUpdateStatus,
  setConsoleConfig,
  type UpdateCheckState,
} from "@/lib/graphql";
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
                <span className="console-mono">{status.data?.currentVersion || "unknown"}</span>
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
      )}
    </Section>
  );
}
