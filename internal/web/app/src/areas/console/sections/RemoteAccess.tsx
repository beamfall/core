import { ArrowRightLeft, ChevronLeft, ExternalLink, Inbox, RefreshCw, Wifi } from "lucide-react";
import { type ReactNode, useCallback, useEffect, useRef, useState } from "react";
import { Link } from "react-router";
import {
  DataTable,
  EmptyState,
  ErrorState,
  StatusChip,
  type StatusTone,
} from "@/components/beamfall/workbench";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Separator } from "@/components/ui/separator";
import { TableCell, TableRow } from "@/components/ui/table";
import { displayString } from "@/lib/display";
import type {
  ImporterInfo,
  JobInfo,
  LibraryInfo,
  ProfileSummary,
  RemoteAccessInfo,
  RemoteEndpointInfo,
} from "@/lib/graphql";
import {
  fetchImporters,
  fetchImportStatus,
  fetchLibraries,
  fetchProfiles,
  fetchQBittorrentIngest,
  fetchRemoteAccess,
  runImport,
  setScraperCredential,
} from "@/lib/graphql";
import { useDisplayLocale, useT } from "@/lib/i18n";
import {
  canRunImport,
  categoriesArgument,
  initialWizardConfig,
  isTerminalStatus,
  matchPercent,
  parseImportReport,
  toggleCategory,
  type WizardConfig,
} from "@/lib/migrationReport";
import type { StringKey } from "@/strings";
import { getAdminEpochSignal } from "../adminEpoch";
import { Section } from "../Section";
import { useAsync } from "../useAsync";

const CERT_STATE: Record<string, StringKey> = {
  pending: "certPending",
  issued: "certIssued",
};

const CERT_TONE: Record<string, StatusTone> = {
  pending: "amber",
  issued: "green",
};

const RELAY_STATE: Record<string, StringKey> = {
  disabled: "relayStateDisabled",
  disconnected: "relayStateDisconnected",
  connecting: "relayStateConnecting",
  connected: "relayStateConnected",
  error: "relayStateError",
};

const RELAY_TONE: Record<string, StatusTone> = {
  disabled: "neutral",
  disconnected: "neutral",
  connecting: "amber",
  connected: "cyan",
  error: "red",
};

export function isPublicCleartextConsoleLocation(
  loc: Pick<Location, "protocol" | "hostname"> = window.location,
) {
  if (loc.protocol !== "http:") return false;
  const host = loc.hostname.replace(/^\[|\]$/g, "").toLowerCase();
  if (
    host === "localhost" ||
    host.endsWith(".local") ||
    host.startsWith("127.") ||
    host.startsWith("10.") ||
    host.startsWith("192.168.") ||
    host === "::1" ||
    host.startsWith("fc") ||
    host.startsWith("fd") ||
    host.startsWith("fe80:")
  ) {
    return false;
  }
  const second = Number(host.split(".")[1]);
  if (host.startsWith("172.") && second >= 16 && second <= 31) return false;
  return true;
}

const CONNECTION_CLASS: Record<string, StringKey> = {
  lan: "remoteAccessLanDirect",
  direct: "remoteAccessDirectHTTPS",
  overlay: "remoteAccessOverlay",
  "self-hosted-relay": "remoteAccessRelay",
  hosted: "remoteAccessHostedRelay",
};

const QB_OUTCOME: Record<string, StringKey> = {
  imported: "qbittorrentImported",
  unrouted: "qbittorrentUnrouted",
  duplicate: "qbittorrentAlreadyInCatalog",
  error: "qbittorrentError",
};

const QB_TONE: Record<string, StatusTone> = {
  imported: "green",
  unrouted: "amber",
  duplicate: "neutral",
  error: "red",
};

// Human-readable labels for the ADR-0037 §2.2 fixed category vocabulary.
const CATEGORY_LABELS: Record<string, StringKey> = {
  "watch-state": "migrationCategoryWatchState",
  ratings: "migrationCategoryRatings",
  collections: "migrationCategoryCollections",
  subscriptions: "migrationCategorySubscriptions",
  "item-state": "migrationCategoryItemState",
};

function Field({ label, value }: { label: StringKey; value: string }) {
  const t = useT();
  if (!value) return null;
  return (
    <div className="flex items-center justify-between gap-4 text-sm">
      <span className="text-muted-foreground">{t(label)}</span>
      <span className="console-mono">{value}</span>
    </div>
  );
}

function connectionClassLabels(endpoints: RemoteEndpointInfo[] = []) {
  return Array.from(
    new Set(
      endpoints
        .map((endpoint) => CONNECTION_CLASS[endpoint.kind])
        .filter((label): label is StringKey => Boolean(label)),
    ),
  );
}

function endpointHost(endpoint: RemoteEndpointInfo) {
  try {
    return new URL(endpoint.url).host.toLowerCase();
  } catch {
    return "";
  }
}

export function currentEndpointKind(
  endpoints: RemoteEndpointInfo[] = [],
  loc: Pick<Location, "host"> = window.location,
) {
  const host = loc.host.toLowerCase();
  return endpoints.find((endpoint) => endpointHost(endpoint) === host)?.kind ?? "";
}

function GuideStep({
  index,
  title,
  state,
  tone,
  children,
}: {
  index: number;
  title: StringKey;
  state: StringKey;
  tone: StatusTone;
  children: ReactNode;
}) {
  const t = useT();
  return (
    <div className="console-card console-card-pad space-y-3">
      <div className="flex items-center justify-between gap-3">
        <div className="flex items-center gap-2">
          <span className="grid h-6 w-6 place-items-center rounded-full border text-xs font-semibold">
            {index}
          </span>
          <h3 className="text-sm font-semibold">{t(title)}</h3>
        </div>
        <StatusChip tone={tone}>{t(state)}</StatusChip>
      </div>
      {children}
    </div>
  );
}

function directState(direct: RemoteAccessInfo["directHTTPS"]) {
  if (!direct?.enabled) {
    return { label: "watchAwayNotConfigured" as StringKey, tone: "neutral" as StatusTone };
  }
  if (direct.certState === "issued") {
    return { label: "watchAwayReady" as StringKey, tone: "green" as StatusTone };
  }
  return { label: "watchAwayPreflightPending" as StringKey, tone: "amber" as StatusTone };
}

function WatchAwayFlow({
  data,
  refresh,
}: {
  data: RemoteAccessInfo | undefined;
  refresh: () => Promise<void>;
}) {
  const t = useT();
  const endpoints = data?.endpoints ?? [];
  const overlay = endpoints.find((endpoint) => endpoint.kind === "overlay");
  const verified = currentEndpointKind(endpoints) === "overlay";
  const overlayState = verified
    ? { label: "watchAwayVerified" as StringKey, tone: "green" as StatusTone }
    : overlay
      ? { label: "watchAwayDetected" as StringKey, tone: "cyan" as StatusTone }
      : { label: "watchAwayNotDetected" as StringKey, tone: "amber" as StatusTone };
  const direct = directState(data?.directHTTPS);
  const relay = data?.relay;

  return (
    <div className="space-y-4">
      <div>
        <h2 className="text-base font-semibold">{t("watchAwayHeading")}</h2>
        <p className="text-sm text-muted-foreground">{t("watchAwayLead")}</p>
      </div>
      <GuideStep
        index={1}
        title="watchAwayOverlayTitle"
        state={overlayState.label}
        tone={overlayState.tone}
      >
        {verified ? (
          <p className="text-sm text-muted-foreground">{t("watchAwayOverlayVerifiedDetail")}</p>
        ) : overlay ? (
          <div className="space-y-2 text-sm text-muted-foreground">
            <p>{t("watchAwayOverlayDetectedDetail")}</p>
            <div className="rounded-md border px-3 py-2">
              <div className="text-xs">{t("watchAwayPhoneTest")}</div>
              <div className="console-mono break-all text-foreground">{overlay.url}</div>
            </div>
          </div>
        ) : (
          <div className="space-y-3 text-sm text-muted-foreground">
            <p>{t("watchAwayOverlayMissingDetail")}</p>
            <div className="flex flex-wrap gap-2">
              <Button variant="outline" size="sm" asChild>
                <a href="https://tailscale.com/download" target="_blank" rel="noreferrer">
                  <ExternalLink className="console-nav-icon" aria-hidden />
                  {t("watchAwayInstallTailscale")}
                </a>
              </Button>
              <Button variant="outline" size="sm" onClick={() => void refresh()}>
                <RefreshCw className="console-nav-icon" aria-hidden />
                {t("watchAwayPollOverlay")}
              </Button>
            </div>
          </div>
        )}
      </GuideStep>
      <GuideStep index={2} title="watchAwayDirectTitle" state={direct.label} tone={direct.tone}>
        <div className="space-y-2 text-sm text-muted-foreground">
          <p>{t("watchAwayDirectDetail")}</p>
          {data?.directHTTPS?.enabled && (
            <>
              <Field label="remoteAccessDomain" value={data.directHTTPS.domain} />
              <div className="flex items-center justify-between gap-4">
                <span>{t("remoteAccessConnection")}</span>
                <StatusChip tone={CERT_TONE[data.directHTTPS.certState] ?? "amber"}>
                  {t(CERT_STATE[data.directHTTPS.certState] ?? "certPending")}
                </StatusChip>
              </div>
            </>
          )}
        </div>
      </GuideStep>
      <details className="console-card console-card-pad">
        <summary className="cursor-pointer text-sm font-semibold">
          {t("watchAwayAdvancedRelay")}
        </summary>
        <div className="mt-3 space-y-3 text-sm text-muted-foreground">
          <p>{t("watchAwayRelayDetail")}</p>
          {relay?.enabled ? (
            <>
              <Field label="remoteAccessEndpoint" value={relay.endpoint} />
              <Field label="remoteAccessPublicHost" value={relay.publicHost} />
              <div className="flex items-center justify-between gap-4">
                <span>{t("remoteAccessConnection")}</span>
                <StatusChip tone={RELAY_TONE[relay.connectionState] ?? "neutral"}>
                  {t(RELAY_STATE[relay.connectionState] ?? "relayStateDisabled")}
                </StatusChip>
              </div>
              {relay.lastError && <Field label="remoteAccessLastError" value={relay.lastError} />}
            </>
          ) : (
            <p>{t("remoteAccessOff")}</p>
          )}
        </div>
      </details>
    </div>
  );
}

// Remote access (ADR-0012/0018/0025): direct-HTTPS/ACME + relay carrier status.
// Status only — no secret or token is ever shown (the query selects none).
export function RemoteAccessStatus() {
  const t = useT();
  const { data, failed, refresh } = useAsync(fetchRemoteAccess);
  const connectionClasses = connectionClassLabels(data?.endpoints);

  return (
    <Section
      heading="remoteAccessHeading"
      lead="remoteAccessLead"
      actions={
        <Button variant="outline" size="sm" onClick={() => void refresh()}>
          <RefreshCw className="console-nav-icon" aria-hidden />
          {t("remoteAccessRefresh")}
        </Button>
      }
    >
      {failed ? (
        <ErrorState message="remoteAccessUnavailable" />
      ) : (
        <div className="space-y-4">
          <WatchAwayFlow data={data ?? undefined} refresh={refresh} />
          {connectionClasses.length > 0 && (
            <div className="flex flex-wrap gap-2">
              {connectionClasses.map((label) => (
                <StatusChip key={label} tone="cyan">
                  {t(label)}
                </StatusChip>
              ))}
            </div>
          )}
          {data?.plainHTTPPublicWarning && (
            <div className="rounded-md border border-amber-400/60 bg-amber-500/10 px-3 py-2 text-sm text-amber-700">
              {t("remoteAccessPlainHTTPPublicWarning")}
            </div>
          )}
          <p className="flex items-center gap-2 text-sm text-muted-foreground">
            <Wifi className="console-nav-icon" aria-hidden />
            {t("remoteAccessLanDirectDetail")}
          </p>
        </div>
      )}
    </Section>
  );
}

// qBittorrent ingest (ADR-0020): category→Library routing + recent ingest
// status (read-only).
function QBittorrentIngest() {
  const t = useT();
  const { data, failed, refresh } = useAsync(fetchQBittorrentIngest);
  const config = data?.config;
  const status = data?.status ?? [];

  return (
    <Section
      heading="qbittorrentHeading"
      lead="qbittorrentLead"
      actions={
        <Button variant="outline" size="sm" onClick={() => void refresh()}>
          <RefreshCw className="console-nav-icon" aria-hidden />
          {t("refresh")}
        </Button>
      }
    >
      {failed ? (
        <ErrorState message="qbittorrentUnavailable" />
      ) : !config?.enabled ? (
        <EmptyState message="qbittorrentEmpty" icon={Inbox} />
      ) : (
        <div className="space-y-4">
          {config.routes.length === 0 ? (
            <EmptyState message="qbittorrentNoRoutes" />
          ) : (
            <DataTable
              columns={[
                { key: "category", label: t("migrationWizardConflict") },
                { key: "library", label: t("library") },
              ]}
            >
              {config.routes.map((r) => (
                <TableRow key={`${r.category}:${r.libraryID}`}>
                  <TableCell className="font-medium">{r.category}</TableCell>
                  <TableCell className="console-mono">{r.libraryID}</TableCell>
                </TableRow>
              ))}
            </DataTable>
          )}
          <Separator />
          <h3 className="text-sm font-semibold">{t("qbittorrentStatusHeading")}</h3>
          <DataTable
            columns={[
              { key: "release", label: t("title") },
              { key: "outcome", label: t("active") },
            ]}
          >
            {status.map((s, i) => (
              <TableRow key={`${s.releaseName}:${i}`}>
                <TableCell className="font-medium">{s.releaseName}</TableCell>
                <TableCell>
                  <StatusChip tone={QB_TONE[s.outcome] ?? "red"}>
                    {t(QB_OUTCOME[s.outcome] ?? "qbittorrentError")}
                  </StatusChip>
                </TableCell>
              </TableRow>
            ))}
          </DataTable>
        </div>
      )}
    </Section>
  );
}

// Migration Wizard (ADR-0037): multi-step flow for one Importer.
// Phase machine: idle → configuring → running → done | error.
// "configuring" shows source-path input (for export-file importers) and
// category checkboxes; "running" polls importStatus every 3 s; "done" shows
// the match-rate report + a link to Manage unmatched queue.
// Re-runnable: the "Back" button resets to idle so the same importer can be
// run again (additive semantics — ADR-0037 §2.4).
type WizardPhase = "configuring" | "running" | "done" | "error";

// Credential step for a live-source ("credentials") importer (ADR-0037 §2.1,
// invariant #5): collect the secret and store it under the importer's
// credentialRef via the shared secure-store mutation. The secret is never
// re-fetched — only its configured/required status drives the chip and Run gate.
function CredentialStep({
  importer,
  onConfigured,
}: {
  importer: ImporterInfo;
  onConfigured: () => void;
}) {
  const t = useT();
  const [secret, setSecret] = useState("");
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");

  const save = async () => {
    if (secret.trim() === "") return;
    setSaving(true);
    setError("");
    try {
      await setScraperCredential(importer.credentialRef, secret.trim());
      setSecret("");
      onConfigured();
    } catch {
      setError(t("migrationWizardCredentialError"));
    } finally {
      setSaving(false);
    }
  };

  return (
    <div className="console-card console-card-pad space-y-3">
      <div className="flex items-center justify-between gap-3">
        <p className="text-sm font-medium">{t("migrationWizardCredentials")}</p>
        <StatusChip tone={importer.credentialConfigured ? "green" : "amber"}>
          {importer.credentialConfigured
            ? t("migrationWizardCredentialConfigured")
            : t("migrationWizardCredentialMissing")}
        </StatusChip>
      </div>
      <p className="text-xs text-muted-foreground">{t("migrationWizardCredentialsDesc")}</p>
      <div className="space-y-1">
        <Label htmlFor="migration-credential-secret">{t("migrationWizardCredentialSecret")}</Label>
        <Input
          id="migration-credential-secret"
          type="password"
          value={secret}
          autoComplete="off"
          spellCheck={false}
          placeholder={t("migrationWizardCredentialSecretPlaceholder")}
          onChange={(e) => setSecret(e.target.value)}
        />
      </div>
      {error && <p className="text-sm text-destructive">{error}</p>}
      <Button
        size="sm"
        variant="outline"
        disabled={saving || secret.trim() === ""}
        onClick={() => void save()}
      >
        {t("migrationWizardCredentialSave")}
      </Button>
    </div>
  );
}

// Profile/Library mapping step (ADR-0037 §2.2): pick the target Profile whose
// engagement the import writes to, and optionally a single target Library to
// match against. Empty values map to the server defaults (this session's
// Profile; match across every Library).
function MappingStep({
  config,
  profiles,
  libraries,
  onChange,
}: {
  config: WizardConfig;
  profiles: ProfileSummary[];
  libraries: LibraryInfo[];
  onChange: (next: Partial<WizardConfig>) => void;
}) {
  const t = useT();
  const PROFILE_DEFAULT = "__default__";
  const LIBRARY_ALL = "__all__";
  return (
    <div className="space-y-3">
      <div>
        <p className="text-sm font-medium">{t("migrationWizardMapping")}</p>
        <p className="text-xs text-muted-foreground">{t("migrationWizardMappingDesc")}</p>
      </div>
      <div className="grid gap-3 sm:grid-cols-2">
        <div className="space-y-1">
          <Label>{t("migrationWizardProfile")}</Label>
          <Select
            value={config.profileID === "" ? PROFILE_DEFAULT : config.profileID}
            onValueChange={(v) => onChange({ profileID: v === PROFILE_DEFAULT ? "" : v })}
          >
            <SelectTrigger>
              <SelectValue placeholder={t("migrationWizardProfileDefault")} />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value={PROFILE_DEFAULT}>{t("migrationWizardProfileDefault")}</SelectItem>
              {profiles.map((p) => (
                <SelectItem key={p.id} value={p.id}>
                  {p.name}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>
        <div className="space-y-1">
          <Label>{t("migrationWizardLibrary")}</Label>
          <Select
            value={config.libraryID === "" ? LIBRARY_ALL : config.libraryID}
            onValueChange={(v) => onChange({ libraryID: v === LIBRARY_ALL ? "" : v })}
          >
            <SelectTrigger>
              <SelectValue placeholder={t("migrationWizardLibraryAll")} />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value={LIBRARY_ALL}>{t("migrationWizardLibraryAll")}</SelectItem>
              {libraries.map((lib) => (
                <SelectItem key={lib.id} value={lib.id}>
                  {lib.title}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>
      </div>
    </div>
  );
}

// The unified match-rate report (ADR-0037 §2.3): the overall matched/unmatched
// counts, the derived rate, and the per-category breakdown — derived from the
// job progress row by parseImportReport. Shown both while running (live) and at
// completion, with the Manage unmatched route offered when anything is unmatched.
function MatchRateReport({ job }: { job: JobInfo | null }) {
  const t = useT();
  const report = parseImportReport(job);
  if (report.total === 0) return null;
  return (
    <div className="console-card console-card-pad space-y-3">
      <p className="text-sm font-medium">{t("migrationWizardReportHeading")}</p>
      <div className="grid grid-cols-3 gap-3 text-sm">
        <div>
          <div className="text-muted-foreground">{t("migrationWizardReportMatched")}</div>
          <div className="console-mono text-base">{report.matched}</div>
        </div>
        <div>
          <div className="text-muted-foreground">{t("migrationWizardReportUnmatched")}</div>
          <div className="console-mono text-base">{report.unmatched}</div>
        </div>
        <div>
          <div className="text-muted-foreground">{t("migrationWizardReportRate")}</div>
          <div className="console-mono text-base">{matchPercent(report)}%</div>
        </div>
      </div>
      <div className="h-2 w-full overflow-hidden rounded-full bg-muted">
        <div
          className="h-full bg-primary transition-all"
          style={{ width: `${matchPercent(report)}%` }}
        />
      </div>
      {report.perCategory.length > 0 && (
        <div className="space-y-1">
          <div className="text-xs text-muted-foreground">
            {t("migrationWizardReportByCategory")}
          </div>
          <div className="flex flex-wrap gap-2">
            {report.perCategory.map((c) => {
              const labelKey = CATEGORY_LABELS[c.category] ?? "migrationCategoryWatchState";
              return (
                <StatusChip key={c.category} tone="neutral">
                  {t(labelKey)}: {c.count}
                </StatusChip>
              );
            })}
          </div>
        </div>
      )}
    </div>
  );
}

function ImporterWizard({
  importer: initialImporter,
  onBack,
}: {
  importer: ImporterInfo;
  onBack: () => void;
}) {
  const t = useT();
  // The importer can change in place after a credential save (its
  // credentialConfigured flag flips), so it is local state, not just a prop.
  const [importer, setImporter] = useState<ImporterInfo>(initialImporter);
  const [phase, setPhase] = useState<WizardPhase>("configuring");
  const [config, setConfig] = useState<WizardConfig>(() => initialWizardConfig(initialImporter));
  const [job, setJob] = useState<JobInfo | null>(null);
  const [errorMsg, setErrorMsg] = useState("");
  const { data: profileData } = useAsync(fetchProfiles);
  const { data: libraryData } = useAsync(fetchLibraries);
  const profiles = profileData ?? [];
  const libraries = libraryData ?? [];
  const pollRef = useRef<ReturnType<typeof setInterval> | null>(null);
  // The poll outlives a single render and is not owned by useAsync, so it
  // carries the two guards useAsync would have applied: the Admin-Session epoch
  // (an expired/locked/Profile-switched session must not keep polling operator
  // job state) and a mounted flag (a request already in flight when the wizard
  // closes must not write back to it).
  const mountedRef = useRef(true);

  const clearPoll = useCallback(() => {
    if (pollRef.current !== null) {
      clearInterval(pollRef.current);
      pollRef.current = null;
    }
  }, []);

  useEffect(() => {
    mountedRef.current = true;
    return () => {
      mountedRef.current = false;
      clearPoll();
    };
  }, [clearPoll]);

  const patchConfig = (next: Partial<WizardConfig>) => setConfig((prev) => ({ ...prev, ...next }));

  const canRun = canRunImport(importer, config);

  const startRun = async () => {
    setErrorMsg("");
    setPhase("running");
    setJob(null);
    try {
      const ref = await runImport(
        importer.pluginID,
        importer.id,
        importer.input === "export-file" ? config.sourcePath.trim() : undefined,
        categoriesArgument(importer, config.selectedCategories),
        { profileID: config.profileID, libraryID: config.libraryID },
      );
      // Poll importStatus every 3 s until the job leaves the active states.
      pollRef.current = setInterval(() => {
        void (async () => {
          const epoch = getAdminEpochSignal();
          if (epoch?.aborted || !mountedRef.current) {
            clearPoll();
            return;
          }
          try {
            const status = await fetchImportStatus(ref.id, epoch);
            if (epoch?.aborted || !mountedRef.current) return;
            setJob(status);
            if (isTerminalStatus(status.status)) {
              clearPoll();
              setPhase(status.status === "done" ? "done" : "error");
            }
          } catch {
            // Transient poll failure — keep polling; surface the error only
            // once the poll gives up or the component unmounts.
          }
        })();
      }, 3000);
    } catch {
      setPhase("error");
      setErrorMsg(t("migrationWizardError"));
    }
  };

  // Re-run resets to the initial configure state (additive — ADR-0037 §2.4): the
  // same importer is immediately runnable again.
  const reset = () => {
    clearPoll();
    setPhase("configuring");
    setConfig(initialWizardConfig(importer));
    setJob(null);
    setErrorMsg("");
  };

  if (phase === "configuring") {
    return (
      <div className="space-y-4">
        <button
          type="button"
          className="flex items-center gap-1 text-sm text-muted-foreground hover:text-foreground"
          onClick={onBack}
        >
          <ChevronLeft className="h-3 w-3" />
          {t("migrationWizardBack")}
        </button>

        {importer.credentialRequired && (
          <CredentialStep
            importer={importer}
            onConfigured={() =>
              setImporter((prev) => ({
                ...prev,
                credentialConfigured: true,
                credentialStatus: "configured",
              }))
            }
          />
        )}

        {importer.input === "export-file" && (
          <div className="space-y-1">
            <Label htmlFor="migration-source-path">{t("migrationWizardSourcePath")}</Label>
            <Input
              id="migration-source-path"
              value={config.sourcePath}
              autoComplete="off"
              spellCheck={false}
              placeholder={t("migrationWizardSourcePathPlaceholder")}
              onChange={(e) => patchConfig({ sourcePath: e.target.value })}
            />
          </div>
        )}

        <div className="space-y-2">
          <p className="text-sm font-medium">{t("migrationWizardCategories")}</p>
          <p className="text-xs text-muted-foreground">{t("migrationWizardCategoryDesc")}</p>
          {importer.categories.map((cat) => {
            const labelKey = CATEGORY_LABELS[cat] ?? "migrationCategoryWatchState";
            return (
              <div key={cat} className="flex items-center gap-2">
                <Checkbox
                  id={`cat-${cat}`}
                  checked={config.selectedCategories.includes(cat)}
                  onCheckedChange={() =>
                    patchConfig({
                      selectedCategories: toggleCategory(config.selectedCategories, cat),
                    })
                  }
                />
                <Label htmlFor={`cat-${cat}`} className="cursor-pointer">
                  {t(labelKey)}
                </Label>
              </div>
            );
          })}
        </div>

        <MappingStep
          config={config}
          profiles={profiles}
          libraries={libraries}
          onChange={patchConfig}
        />

        <div className="flex gap-2">
          <Button size="sm" disabled={!canRun} onClick={() => void startRun()}>
            {t("migrationWizardRun")}
          </Button>
        </div>
      </div>
    );
  }

  if (phase === "running") {
    return (
      <div className="space-y-3">
        <p className="text-sm font-medium">{t("migrationWizardRunning")}</p>
        <MatchRateReport job={job} />
      </div>
    );
  }

  // done or error
  const report = parseImportReport(job);
  return (
    <div className="space-y-3">
      <p className="text-sm font-medium">
        {phase === "done" ? t("migrationWizardComplete") : t("migrationWizardError")}
      </p>
      {phase === "done" && <MatchRateReport job={job} />}
      {errorMsg && <p className="text-sm text-destructive">{errorMsg}</p>}
      {phase === "done" && report.hasUnmatched && (
        <Link
          to="/manage/unmatched"
          className="inline-flex items-center gap-1 text-sm text-primary underline underline-offset-2"
        >
          {t("migrationWizardViewUnmatched")}
        </Link>
      )}
      <div className="flex gap-2">
        <Button variant="outline" size="sm" onClick={reset}>
          {t("migrationWizardRun")}
        </Button>
        <Button variant="ghost" size="sm" onClick={onBack}>
          {t("migrationWizardBack")}
        </Button>
      </div>
    </div>
  );
}

// Migration Wizard (ADR-0037): list installed Importers, configure + run one
// (additive, re-runnable). Each run enqueues a job that surfaces in the
// Operations queue and in the inline progress view here.
function MigrationWizard() {
  const t = useT();
  const displayLocale = useDisplayLocale();
  const { data, failed } = useAsync(fetchImporters);
  const importers = data ?? [];
  const [active, setActive] = useState<ImporterInfo | null>(null);

  if (active) {
    return (
      <Section heading="migrationWizardHeading" lead="migrationWizardLead">
        <ImporterWizard importer={active} onBack={() => setActive(null)} />
      </Section>
    );
  }

  return (
    <Section heading="migrationWizardHeading" lead="migrationWizardLead">
      {failed ? (
        <ErrorState message="migrationWizardUnavailable" />
      ) : importers.length === 0 ? (
        <EmptyState message="migrationWizardEmpty" icon={ArrowRightLeft} />
      ) : (
        <DataTable
          columns={[
            { key: "name", label: t("title") },
            { key: "input", label: t("migrationWizardInput") },
            { key: "conflict", label: t("migrationWizardConflict") },
            { key: "action", label: "" },
          ]}
        >
          {importers.map((imp) => (
            <TableRow key={`${imp.pluginID}:${imp.id}`}>
              <TableCell className="font-medium">
                {displayString(imp.displayName, displayLocale)}
              </TableCell>
              <TableCell>{imp.input}</TableCell>
              <TableCell>{imp.conflict}</TableCell>
              <TableCell className="text-right">
                <Button size="sm" variant="outline" onClick={() => setActive(imp)}>
                  {t("migrationWizardConfigure")}
                </Button>
              </TableCell>
            </TableRow>
          ))}
        </DataTable>
      )}
    </Section>
  );
}

// The remote-access / ingest / migration screen: how the server is reachable,
// what the ingest seam is doing, and the migration Importers.
export function RemoteAccess({
  suppressDispatchSurfaces = false,
}: {
  suppressDispatchSurfaces?: boolean;
}) {
  return (
    <div className="space-y-8">
      <RemoteAccessStatus />
      {!suppressDispatchSurfaces && (
        <>
          <QBittorrentIngest />
          <MigrationWizard />
        </>
      )}
    </div>
  );
}
