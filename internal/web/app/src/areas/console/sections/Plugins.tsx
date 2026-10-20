import { Plug } from "lucide-react";
import { type ReactNode, useEffect, useMemo, useRef, useState } from "react";
import {
  DataTable,
  EmptyState,
  ErrorState,
  Fieldset,
  StatusChip,
  type StatusTone,
} from "@/components/beamfall/workbench";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Skeleton } from "@/components/ui/skeleton";
import { Switch } from "@/components/ui/switch";
import { TableCell, TableRow } from "@/components/ui/table";
import { graphql } from "@/lib/api";
import { displayString } from "@/lib/display";
import {
  disableScraperCredential,
  fetchConsoleConfig,
  type PluginActionDescriptor,
  type PluginActivationInfo,
  type PluginConfigFieldInfo,
  type PluginPermissionInfo,
  type PluginScraperInfo,
  type PluginStatusInfo,
  setPluginActive,
  setPluginConfig,
  setScraperCredential,
  uninstallPluginData,
  validateScraperCredential,
} from "@/lib/graphql";
import { useDisplayLocale, useT } from "@/lib/i18n";
import type { StringKey } from "@/strings";
import { ConfirmButton } from "../ConfirmButton";
import { Section } from "../Section";
import { useAsync } from "../useAsync";

// ponytail: mirror the compiled bundle list until the wire exposes installability.
const BUILT_IN_PLUGIN_IDS = new Set([
  "filmtv",
  "adult",
  "music",
  "podcasts",
  "photos",
  "audiobook",
  "familyarchive",
]);

const PLUGIN_STANDING_QUERY =
  "query ConsolePluginStanding { consoleConfig { plugins { id provenance trustState } } }";

const PROVENANCE_LABELS: Record<string, StringKey> = {
  "First-Party": "pluginProvenanceFirstParty",
  "Third-Party": "pluginProvenanceThirdParty",
  Sideloaded: "pluginProvenanceSideloaded",
};

type PluginStandingInfo = {
  id: string;
  provenance: string;
  trustState: string;
};

type PluginWithStanding = PluginActivationInfo & Omit<PluginStandingInfo, "id">;

function completeStanding(
  standing: Partial<PluginStandingInfo> | undefined,
): PluginStandingInfo | undefined {
  if (!standing?.id || !standing.provenance || !standing.trustState) return undefined;
  return { id: standing.id, provenance: standing.provenance, trustState: standing.trustState };
}

async function addPluginStanding(
  config: Awaited<ReturnType<typeof fetchConsoleConfig>>,
  previousPlugins: PluginWithStanding[] = [],
  signal?: AbortSignal,
) {
  const standingResult = await graphql<{
    consoleConfig: { plugins: PluginStandingInfo[] };
  }>(PLUGIN_STANDING_QUERY, {}, signal).catch(() => undefined);
  const fetchedByID = new Map(
    (standingResult?.consoleConfig?.plugins ?? []).flatMap((standing) => {
      const complete = completeStanding(standing);
      return complete ? [[complete.id, complete] as const] : [];
    }),
  );
  const previousByID = new Map(previousPlugins.map((plugin) => [plugin.id, plugin]));

  let pluginStandingUnavailable = false;
  return {
    ...config,
    plugins: config.plugins.map((plugin): PluginWithStanding => {
      const freshStanding =
        fetchedByID.get(plugin.id) ??
        completeStanding(plugin as PluginActivationInfo & Partial<PluginStandingInfo>);
      const standing = freshStanding ?? completeStanding(previousByID.get(plugin.id));
      pluginStandingUnavailable ||= freshStanding === undefined;
      return {
        ...plugin,
        provenance: standing?.provenance ?? "",
        trustState: standing?.trustState ?? "",
      };
    }),
    pluginStandingUnavailable,
  };
}

async function fetchConsoleConfigWithStanding(
  signal?: AbortSignal,
  previousPlugins: PluginWithStanding[] = [],
) {
  return addPluginStanding(await fetchConsoleConfig(signal), previousPlugins, signal);
}

function provenanceChip(provenance: string, t: ReturnType<typeof useT>): ReactNode {
  const label = PROVENANCE_LABELS[provenance];
  if (!label) return null;
  return (
    <StatusChip tone="cyan" dot={false}>
      {t(label)}
    </StatusChip>
  );
}

function trustBadge(trustState: string, t: ReturnType<typeof useT>): ReactNode {
  if (trustState === "Trusted") {
    return <StatusChip tone="green">{t("pluginTrustTrusted")}</StatusChip>;
  }
  if (trustState === "Unsigned") {
    return <StatusChip tone="neutral">{t("pluginTrustUnsigned")}</StatusChip>;
  }
  if (trustState !== "Tampered" && trustState !== "Revoked") return null;

  const detail =
    trustState === "Tampered" ? "pluginTrustSignatureInvalid" : "pluginTrustKeyRevoked";
  return (
    <details className="shrink-0">
      <summary className="cursor-pointer list-none">
        <StatusChip tone="red">{t("pluginTrustVerificationFailed")}</StatusChip>
      </summary>
      <span className="mt-1 block text-xs text-[var(--red-500)]">{t(detail)}</span>
    </details>
  );
}

// Plugins (ADR-0013 / ADR-0003): the installed plugins with their activation
// state. Activation rides setPluginActive, which returns the full config — we
// fold the result back in so the toggle reflects the server truth.
export function Plugins() {
  const t = useT();
  const displayLocale = useDisplayLocale();
  const previousPlugins = useRef<PluginWithStanding[]>([]);
  const { data, loading, failed, refresh, replace } = useAsync((signal) =>
    fetchConsoleConfigWithStanding(signal, previousPlugins.current),
  );
  const [credentialInputs, setCredentialInputs] = useState<Record<string, string>>({});
  const [pendingCredential, setPendingCredential] = useState("");
  const [actionFailed, setActionFailed] = useState(false);
  const plugins = data?.plugins ?? [];
  useEffect(() => {
    previousPlugins.current = plugins;
  }, [plugins]);
  const pluginStandingUnavailable = data?.pluginStandingUnavailable ?? false;
  const hasUnrecognizedTrust = plugins.some((plugin) => plugin.trustState === "Unrecognized");
  const scrapers = useMemo(
    () =>
      plugins.flatMap((plugin) =>
        (plugin.scrapers ?? []).map((scraper) => ({
          ...scraper,
          pluginName: displayString(plugin.displayName, displayLocale),
        })),
      ),
    [displayLocale, plugins],
  );
  const credentialRefs = useMemo(() => {
    const grouped = new Map<string, PluginScraperInfo[]>();
    for (const scraper of scrapers) {
      if (!scraper.authRef) continue;
      const rows = grouped.get(scraper.authRef) ?? [];
      rows.push(scraper);
      grouped.set(scraper.authRef, rows);
    }
    return [...grouped.entries()];
  }, [scrapers]);

  async function publishMutation(
    mutation: () => Promise<Awaited<ReturnType<typeof fetchConsoleConfig>>>,
  ) {
    await replace(async (signal) => addPluginStanding(await mutation(), plugins, signal));
  }

  async function toggle(pluginID: string, active: boolean) {
    await publishMutation(() => setPluginActive(pluginID, active));
  }

  async function saveConfig(pluginID: string, values: Record<string, string>, clear: string[]) {
    await publishMutation(() => setPluginConfig(pluginID, values, clear));
  }

  async function uninstall(pluginID: string) {
    await publishMutation(() => uninstallPluginData(pluginID));
  }

  async function saveCredential(authRef: string) {
    const secret = credentialInputs[authRef] ?? "";
    if (!secret.trim()) return;
    await credentialAction(authRef, async () => {
      await setScraperCredential(authRef, secret);
      setCredentialInputs((current) => ({ ...current, [authRef]: "" }));
    });
  }

  async function validateCredential(authRef: string) {
    await credentialAction(authRef, () => validateScraperCredential(authRef));
  }

  async function disableCredential(authRef: string) {
    await credentialAction(authRef, async () => {
      await disableScraperCredential(authRef);
      setCredentialInputs((current) => ({ ...current, [authRef]: "" }));
    });
  }

  async function credentialAction(authRef: string, action: () => Promise<unknown>) {
    setPendingCredential(authRef);
    setActionFailed(false);
    try {
      await action();
      await refresh();
    } catch {
      setActionFailed(true);
    } finally {
      setPendingCredential("");
    }
  }

  async function runAction(pluginID: string, actionID: string) {
    await graphql(
      'mutation RunPluginAction($pluginID: String!, $actionID: String!) { runPluginLifecycleAction(pluginID: $pluginID, action: "run-action", actionID: $actionID) { pluginID } }',
      { pluginID, actionID },
    );
    await refresh();
  }

  return (
    <Section heading="consoleConfigPlugins">
      {loading ? (
        <Skeleton className="h-32 w-full" />
      ) : failed ? (
        <ErrorState message="consoleConfigUnavailable" />
      ) : plugins.length === 0 ? (
        <EmptyState message="none" icon={Plug} />
      ) : (
        <>
          {pluginStandingUnavailable && (
            <div
              role="alert"
              className="rounded-md border border-[var(--amber-500)]/45 bg-[var(--amber-500)]/10 px-3 py-2 text-sm"
            >
              {t("pluginTrustUnavailable")}
            </div>
          )}
          {hasUnrecognizedTrust && (
            <div
              role="status"
              className="rounded-md border border-[var(--amber-500)]/45 bg-[var(--amber-500)]/10 px-3 py-2 text-sm"
            >
              {t("pluginTrustRefreshServer")}
            </div>
          )}
          <div className="console-card console-card-pad space-y-3">
            {plugins.map((p) => {
              const builtIn = BUILT_IN_PLUGIN_IDS.has(p.id);
              return (
                <div key={p.id} className="console-control-row">
                  <div className="min-w-0 flex flex-wrap items-center gap-2">
                    <span className="console-control-label font-medium">
                      {displayString(p.displayName, displayLocale)}
                    </span>
                    {provenanceChip(p.provenance, t)}
                    {trustBadge(p.trustState, t)}
                    {p.active ? (
                      <StatusChip tone="green">{t("consoleConfigActive")}</StatusChip>
                    ) : (
                      <StatusChip tone="neutral">{t("consoleConfigInactive")}</StatusChip>
                    )}
                  </div>
                  <div
                    className="flex shrink-0 items-center gap-2"
                    title={builtIn ? "Built-in plugins cannot be disabled." : undefined}
                  >
                    <Switch
                      checked={p.active}
                      disabled={builtIn}
                      onCheckedChange={builtIn ? undefined : (c) => void toggle(p.id, c)}
                      aria-label={displayString(p.displayName, displayLocale)}
                    />
                  </div>
                </div>
              );
            })}
          </div>
        </>
      )}
      {!loading &&
        !failed &&
        plugins.map((p) => (
          <PluginDetail
            key={`detail-${p.id}`}
            plugin={p}
            displayLocale={displayLocale}
            onSaveConfig={saveConfig}
            onRunAction={runAction}
            onDeactivate={() => toggle(p.id, false)}
            onUninstall={() => uninstall(p.id)}
          />
        ))}
      {!loading && !failed && scrapers.length > 0 && (
        <DataTable
          columns={[
            { key: "plugin", label: t("consoleScraperPlugin") },
            { key: "endpoint", label: t("consoleScraperEndpoint") },
            { key: "credential", label: t("consoleScraperCredential") },
            { key: "state", label: t("consoleScraperState") },
          ]}
        >
          {scrapers.map((scraper) => (
            <TableRow key={`${scraper.pluginID}:${scraper.scraperID}`}>
              <TableCell className="font-medium">{scraper.pluginName}</TableCell>
              <TableCell>
                <div className="grid gap-1">
                  <span>{scraper.displayName || scraper.scraperID}</span>
                  <span className="font-mono text-xs text-muted-foreground">
                    {scraper.endpoint}
                  </span>
                </div>
              </TableCell>
              <TableCell>
                {scraper.credentialRequired ? (
                  <div className="grid gap-1 text-xs">
                    <span>
                      {t("consoleScraperAuthRef")}: {scraper.authRef}
                    </span>
                    <span className="text-muted-foreground">
                      {t("consoleScraperAuthStyle")}: {scraper.authStyle}
                    </span>
                  </div>
                ) : (
                  t("consoleScraperNotRequired")
                )}
              </TableCell>
              <TableCell>{credentialStatus(scraper, t)}</TableCell>
            </TableRow>
          ))}
        </DataTable>
      )}
      {!loading && !failed && credentialRefs.length > 0 && (
        <DataTable
          columns={[
            { key: "authRef", label: t("consoleScraperAuthRef") },
            { key: "scope", label: t("consoleScraperScope") },
            { key: "secret", label: t("consoleScraperCredential") },
            { key: "actions", label: "", numeric: true },
          ]}
        >
          {credentialRefs.map(([authRef, rows]) => {
            const pending = pendingCredential === authRef;
            return (
              <TableRow key={authRef}>
                <TableCell className="font-mono text-xs">{authRef}</TableCell>
                <TableCell>{rows.map((row) => row.scraperID).join(", ")}</TableCell>
                <TableCell>
                  <Input
                    type="password"
                    value={credentialInputs[authRef] ?? ""}
                    placeholder={t("consoleScraperSecret")}
                    aria-label={`${t("consoleScraperSecret")} ${authRef}`}
                    onChange={(event) =>
                      setCredentialInputs((current) => ({
                        ...current,
                        [authRef]: event.target.value,
                      }))
                    }
                  />
                </TableCell>
                <TableCell className="text-right">
                  <div className="flex flex-wrap justify-end gap-2">
                    <Button
                      type="button"
                      size="sm"
                      disabled={pending || !(credentialInputs[authRef] ?? "").trim()}
                      onClick={() => void saveCredential(authRef)}
                    >
                      {t("consoleScraperSave")}
                    </Button>
                    <Button
                      type="button"
                      size="sm"
                      variant="outline"
                      disabled={pending}
                      onClick={() => void validateCredential(authRef)}
                    >
                      {t("consoleScraperValidate")}
                    </Button>
                    <Button
                      type="button"
                      size="sm"
                      variant="outline"
                      disabled={pending}
                      onClick={() => void disableCredential(authRef)}
                    >
                      {t("consoleScraperDisable")}
                    </Button>
                  </div>
                </TableCell>
              </TableRow>
            );
          })}
        </DataTable>
      )}
      {actionFailed && (
        <p role="status" className="text-sm text-[var(--red-500)]">
          {t("consoleScraperActionFailed")}
        </p>
      )}
    </Section>
  );
}

function credentialStatus(scraper: PluginScraperInfo, t: ReturnType<typeof useT>) {
  const labels: Record<string, string> = {
    configured: t("consoleScraperConfigured"),
    missing: t("consoleScraperMissing"),
    unavailable: t("consoleScraperUnavailable"),
    invalid: t("consoleScraperInvalid"),
    unreadable: t("consoleScraperUnreadable"),
    "not-required": t("consoleScraperNotRequired"),
  };
  const tones: Record<string, "neutral" | "green" | "amber" | "red"> = {
    configured: "green",
    missing: "amber",
    unavailable: "neutral",
    invalid: "red",
    unreadable: "red",
    "not-required": "neutral",
  };
  const status = scraper.credentialStatus || "unavailable";
  return <StatusChip tone={tones[status] ?? "neutral"}>{labels[status] ?? status}</StatusChip>;
}

// PluginDetail (WEB-5I / ADR-0003 §Plugin, ADR-0013 §5): the operator surface
// beyond bare activation. Four blocks, each driven by the consoleConfig
// substrate: editable typed (non-secret) config saved through setPluginConfig,
// the Core-owned permission inventory, runtime/declaration status rows, and the
// server-declared Action Descriptors rendered as trusted, risk-tinted controls.
function PluginDetail({
  plugin,
  displayLocale,
  onSaveConfig,
  onRunAction,
  onDeactivate,
  onUninstall,
}: {
  plugin: PluginActivationInfo;
  displayLocale: string;
  onSaveConfig: (
    pluginID: string,
    values: Record<string, string>,
    clear: string[],
  ) => Promise<void>;
  onRunAction: (pluginID: string, actionID: string) => Promise<void>;
  onDeactivate: () => Promise<void>;
  onUninstall: () => Promise<void>;
}) {
  const t = useT();
  const config = plugin.config ?? [];
  const permissions = plugin.permissions ?? [];
  const status = plugin.status ?? [];
  const actions = plugin.actions ?? [];

  // The Lifecycle fieldset (Deactivate / Uninstall) renders for every plugin —
  // a plugin can store Entity data without declaring any config/permissions/
  // status/actions, and that is exactly the data Uninstall purges. The other
  // four fieldsets render only when the plugin declares them.
  const hasDeclarations =
    config.length > 0 || permissions.length > 0 || status.length > 0 || actions.length > 0;

  const name = displayString(plugin.displayName, displayLocale);

  return (
    <div className="console-card console-card-pad space-y-4">
      <div className="flex items-center justify-between gap-3">
        <div className="space-y-0.5">
          <h3 className="text-[14px] font-semibold leading-snug">{name}</h3>
          {plugin.controlPolicy && (
            <p className="text-[12px] text-muted-foreground">
              {t("pluginControlPolicy", { policy: plugin.controlPolicy })}
            </p>
          )}
        </div>
        {plugin.active ? (
          <StatusChip tone="green">{t("consoleConfigActive")}</StatusChip>
        ) : (
          <StatusChip tone="neutral">{t("consoleConfigInactive")}</StatusChip>
        )}
      </div>

      {hasDeclarations && (
        <>
          <PluginConfigEditor
            plugin={plugin}
            fields={config}
            displayLocale={displayLocale}
            onSave={onSaveConfig}
          />

          <Fieldset title="pluginPermissionsHeading" help="pluginPermissionsLead">
            {permissions.length === 0 ? (
              <p className="console-control-help">{t("pluginPermissionsEmpty")}</p>
            ) : (
              <div className="space-y-2">
                {permissions.map((perm) => (
                  <PermissionRow key={perm.kind} permission={perm} displayLocale={displayLocale} />
                ))}
              </div>
            )}
          </Fieldset>

          <Fieldset title="pluginStatusHeading">
            {status.length === 0 ? (
              <p className="console-control-help">{t("pluginStatusEmpty")}</p>
            ) : (
              <div className="space-y-2">
                {status.map((row) => (
                  <StatusRow key={row.id} status={row} displayLocale={displayLocale} />
                ))}
              </div>
            )}
          </Fieldset>

          <Fieldset title="pluginActionsHeading" help="pluginActionsLead">
            {actions.length === 0 ? (
              <p className="console-control-help">{t("pluginActionsEmpty")}</p>
            ) : (
              <div className="space-y-2">
                {actions.map((action) => (
                  <ActionRow
                    key={action.id}
                    pluginID={plugin.id}
                    action={action}
                    active={plugin.active}
                    displayLocale={displayLocale}
                    onRun={onRunAction}
                  />
                ))}
              </div>
            )}
          </Fieldset>
        </>
      )}

      <PluginLifecycle
        plugin={plugin}
        displayLocale={displayLocale}
        onDeactivate={onDeactivate}
        onUninstall={onUninstall}
      />
    </div>
  );
}

// PluginLifecycle (PANEL-8 / ADR-0104): the two distinct lifecycle operations.
// Deactivate is reversible and preserves stored data (it reuses the activation
// toggle's path). Uninstall is the explicit, operator-confirmed purge — the
// only path that permanently removes the plugin's stored Entity data — so it
// routes through ConfirmButton (HARD invariant #6) and names the irreversibility
// in the dialog. Deactivate is a no-op when already inactive; Uninstall remains
// available for inactive plugins because their stored data can still be purged.
function PluginLifecycle({
  plugin,
  displayLocale,
  onDeactivate,
  onUninstall,
}: {
  plugin: PluginActivationInfo;
  displayLocale: string;
  onDeactivate: () => Promise<void>;
  onUninstall: () => Promise<void>;
}) {
  const t = useT();
  const [busy, setBusy] = useState(false);
  const [failed, setFailed] = useState(false);
  const name = displayString(plugin.displayName, displayLocale);

  async function uninstall() {
    setBusy(true);
    setFailed(false);
    try {
      await onUninstall();
    } catch {
      setFailed(true);
    } finally {
      setBusy(false);
    }
  }

  async function deactivate() {
    setBusy(true);
    setFailed(false);
    try {
      await onDeactivate();
    } finally {
      setBusy(false);
    }
  }

  return (
    <Fieldset title="pluginLifecycleHeading" help="pluginLifecycleLead">
      <div className="space-y-2">
        <div className="console-control-row">
          <div className="min-w-0">
            <div className="console-control-label">{t("pluginDeactivate")}</div>
            <p className="console-control-help">{t("pluginDeactivateHelp")}</p>
          </div>
          <div className="flex shrink-0 items-center gap-2">
            <StatusChip tone="neutral">{t("pluginLifecycleReversible")}</StatusChip>
            <Button
              type="button"
              size="sm"
              variant="outline"
              disabled={!plugin.active || busy}
              onClick={() => void deactivate()}
            >
              {t("pluginDeactivate")}
            </Button>
          </div>
        </div>
        <div className="console-control-row">
          <div className="min-w-0">
            <div className="console-control-label">{t("pluginUninstall")}</div>
            <p className="console-control-help">{t("pluginUninstallHelp")}</p>
            {failed && (
              <p role="status" className="text-xs text-[var(--red-500)]">
                {t("pluginUninstallFailed")}
              </p>
            )}
          </div>
          <div className="flex shrink-0 items-center gap-2">
            <StatusChip tone="red">{t("pluginLifecycleIrreversible")}</StatusChip>
            <ConfirmButton
              size="sm"
              variant="destructive"
              label={t("pluginUninstall")}
              confirm="pluginUninstallConfirm"
              disabled={busy}
              onConfirm={uninstall}
            >
              {t("pluginUninstallConfirmBody", { name })}
            </ConfirmButton>
          </div>
        </div>
      </div>
    </Fieldset>
  );
}

// PluginConfigEditor — a local-edit buffer over the plugin's typed config
// fields. The buffer starts from the persisted value (or the default), and Save
// diffs it: changed non-empty values go to setPluginConfig's `values`, fields
// emptied back out go to `clear`. Inactive plugins render read-only.
function PluginConfigEditor({
  plugin,
  fields,
  displayLocale,
  onSave,
}: {
  plugin: PluginActivationInfo;
  fields: PluginConfigFieldInfo[];
  displayLocale: string;
  onSave: (pluginID: string, values: Record<string, string>, clear: string[]) => Promise<void>;
}) {
  const t = useT();
  const initial = useMemo(() => fieldValues(fields), [fields]);
  const [draft, setDraft] = useState<Record<string, string>>(initial);
  const [busy, setBusy] = useState(false);
  const [saved, setSaved] = useState(false);
  const [failed, setFailed] = useState(false);

  // Re-seed the buffer whenever the server truth changes (e.g. after a save
  // folds the fresh config back in).
  const initialKey = JSON.stringify(initial);
  const [seenKey, setSeenKey] = useState(initialKey);
  if (seenKey !== initialKey) {
    setSeenKey(initialKey);
    setDraft(initial);
  }

  const dirty = fields.some((f) => (draft[f.name] ?? "") !== (initial[f.name] ?? ""));

  async function save() {
    const values: Record<string, string> = {};
    const clear: string[] = [];
    for (const field of fields) {
      const next = (draft[field.name] ?? "").trim();
      const prev = (initial[field.name] ?? "").trim();
      if (next === prev) continue;
      if (next === "") clear.push(field.name);
      else values[field.name] = next;
    }
    setBusy(true);
    setSaved(false);
    setFailed(false);
    try {
      await onSave(plugin.id, values, clear);
      setSaved(true);
    } catch {
      setFailed(true);
    } finally {
      setBusy(false);
    }
  }

  return (
    <Fieldset title="pluginConfigHeading" help="pluginConfigLead">
      {fields.length === 0 ? (
        <p className="console-control-help">{t("pluginConfigEmpty")}</p>
      ) : (
        <div className="space-y-3">
          {fields.map((field) => (
            <ConfigField
              key={field.name}
              field={field}
              value={draft[field.name] ?? ""}
              disabled={!plugin.active}
              displayLocale={displayLocale}
              onChange={(v) => setDraft((current) => ({ ...current, [field.name]: v }))}
            />
          ))}
          {!plugin.active && (
            <p className="console-control-help">{t("pluginConfigInactiveHelp")}</p>
          )}
          <div className="flex items-center justify-end gap-3">
            {saved && (
              <span role="status" className="text-xs text-[var(--green-500)]">
                {t("pluginConfigSaved")}
              </span>
            )}
            {failed && (
              <span role="status" className="text-xs text-[var(--red-500)]">
                {t("pluginConfigFailed")}
              </span>
            )}
            <Button
              type="button"
              size="sm"
              disabled={!plugin.active || busy || !dirty}
              onClick={() => void save()}
            >
              {t("pluginConfigSave")}
            </Button>
          </div>
        </div>
      )}
    </Fieldset>
  );
}

// One typed config control: bool → Switch, enumerated (options) → Select,
// everything else → a text/number Input. Secrets never reach this surface
// (they ride the scraper-credential seam), so there is no password control.
function ConfigField({
  field,
  value,
  disabled,
  displayLocale,
  onChange,
}: {
  field: PluginConfigFieldInfo;
  value: string;
  disabled: boolean;
  displayLocale: string;
  onChange: (value: string) => void;
}) {
  const t = useT();
  const label = displayString(field.label, displayLocale) || field.name;
  const help = field.required ? t("pluginConfigRequired") : undefined;

  let control: ReactNode;
  if (field.type === "bool") {
    control = (
      <Switch
        checked={value === "true"}
        disabled={disabled}
        onCheckedChange={(c) => onChange(c ? "true" : "false")}
        aria-label={label}
      />
    );
  } else if (field.options && field.options.length > 0) {
    control = (
      <Select value={value} disabled={disabled} onValueChange={(v) => onChange(v)}>
        <SelectTrigger className="w-48">
          <SelectValue placeholder={t("pluginConfigDefault")} />
        </SelectTrigger>
        <SelectContent>
          {field.options.map((opt) => (
            <SelectItem key={opt.value} value={opt.value}>
              {displayString(opt.label, displayLocale) || opt.value}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
    );
  } else {
    control = (
      <Input
        type={field.type === "number" ? "number" : "text"}
        value={value}
        disabled={disabled}
        placeholder={field.default || ""}
        aria-label={label}
        className="w-48"
        onChange={(e) => onChange(e.target.value)}
      />
    );
  }

  return (
    <div className="console-control-row">
      <div className="min-w-0">
        <div className="console-control-label">
          {label}
          {field.required && <span className="ml-1 text-[var(--amber-500)]">*</span>}
        </div>
        {help && <p className="console-control-help">{help}</p>}
      </div>
      <div className="flex shrink-0 items-center gap-2">{control}</div>
    </div>
  );
}

function PermissionRow({
  permission,
  displayLocale,
}: {
  permission: PluginPermissionInfo;
  displayLocale: string;
}) {
  const t = useT();
  return (
    <div className="console-control-row">
      <div className="min-w-0">
        <div className="console-control-label">
          {displayString(permission.label, displayLocale) || permission.kind}{" "}
          <code className="font-mono text-xs text-muted-foreground">{permission.kind}</code>
        </div>
        {permission.description && (
          <p className="console-control-help">
            {displayString(permission.description, displayLocale)}
          </p>
        )}
      </div>
      <StatusChip tone={permission.highRisk ? "iris" : "neutral"}>
        {t(permission.highRisk ? "pluginPermissionHighRisk" : "pluginPermissionStandard")}
      </StatusChip>
    </div>
  );
}

const STATUS_TONES: Record<string, StatusTone> = {
  ok: "green",
  healthy: "green",
  warn: "amber",
  degraded: "amber",
  error: "red",
  failed: "red",
};

function StatusRow({ status, displayLocale }: { status: PluginStatusInfo; displayLocale: string }) {
  const label = displayString(status.label, displayLocale) || status.id;
  return (
    <div className="console-control-row">
      <div className="min-w-0">
        <div className="console-control-label">{label}</div>
        {status.detail && <p className="console-control-help">{status.detail}</p>}
      </div>
      <StatusChip tone={STATUS_TONES[status.state] ?? "neutral"}>{status.state}</StatusChip>
    </div>
  );
}

const RISK_TONES: Record<string, StatusTone> = {
  info: "neutral",
  low: "neutral",
  medium: "amber",
  high: "red",
};

const RISK_LABELS: Record<
  string,
  "pluginRiskInfo" | "pluginRiskLow" | "pluginRiskMedium" | "pluginRiskHigh"
> = {
  info: "pluginRiskInfo",
  low: "pluginRiskLow",
  medium: "pluginRiskMedium",
  high: "pluginRiskHigh",
};

// ActionRow renders one server-declared Action Descriptor as a trusted control.
// High-risk actions route through the confirm dialog (HARD invariant #6).
function ActionRow({
  pluginID,
  action,
  active,
  displayLocale,
  onRun,
}: {
  pluginID: string;
  action: PluginActionDescriptor;
  active: boolean;
  displayLocale: string;
  onRun: (pluginID: string, actionID: string) => Promise<void>;
}) {
  const t = useT();
  const [notice, setNotice] = useState(false);
  const [pending, setPending] = useState(false);
  const label = displayString(action.label, displayLocale) || action.id;
  const tone = RISK_TONES[action.risk] ?? "neutral";
  const riskLabel = RISK_LABELS[action.risk];
  const perms = action.requiredPermissions ?? [];
  const highRisk = action.risk === "high";

  async function run() {
    setPending(true);
    setNotice(false);
    try {
      await onRun(pluginID, action.id);
    } catch {
      setNotice(true);
    } finally {
      setPending(false);
    }
  }

  return (
    <div className="console-control-row">
      <div className="min-w-0">
        <div className="console-control-label">{label}</div>
        {perms.length > 0 && (
          <p className="console-control-help">
            {t("pluginActionRequires", { permissions: perms.join(", ") })}
          </p>
        )}
        {notice && (
          <p role="status" className="text-xs text-muted-foreground">
            {t("pluginActionUnavailable")}
          </p>
        )}
      </div>
      <div className="flex shrink-0 items-center gap-2">
        <StatusChip tone={tone}>{riskLabel ? t(riskLabel) : action.risk}</StatusChip>
        {highRisk ? (
          <ConfirmButton
            size="sm"
            variant="destructive"
            label={t("pluginActionRun")}
            confirm={action.confirmation ? "pluginActionRun" : "pluginActionUnavailable"}
            disabled={!active || pending}
            onConfirm={() => void run()}
          />
        ) : (
          <Button
            type="button"
            size="sm"
            variant="outline"
            disabled={!active || pending}
            onClick={() => void run()}
          >
            {t("pluginActionRun")}
          </Button>
        )}
      </div>
    </div>
  );
}

// fieldValues seeds the config edit buffer from the persisted value, falling
// back to the declared default so the control shows the effective value.
function fieldValues(fields: PluginConfigFieldInfo[]): Record<string, string> {
  const out: Record<string, string> = {};
  for (const field of fields) {
    out[field.name] = field.value ?? field.default ?? "";
  }
  return out;
}
