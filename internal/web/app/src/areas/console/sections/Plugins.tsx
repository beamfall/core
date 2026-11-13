import { ChevronDown } from "lucide-react";
import { type ReactNode, useEffect, useMemo, useRef, useState } from "react";
import {
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
import { graphql } from "@/lib/api";
import { displayString } from "@/lib/display";
import {
  disableScraperCredential,
  fetchConsoleConfig,
  type PluginActionDescriptor,
  type PluginActivationInfo,
  type PluginConfigFieldInfo,
  type PluginPermissionInfo,
  type PluginStatusInfo,
  setPluginActive,
  setPluginConfig,
  setScraperCredential,
  uninstallPluginData,
  validateScraperCredential,
} from "@/lib/graphql";
import { useDisplayLocale, useT } from "@/lib/i18n";
import { groupProviderKeys, keyNeeded, type ProviderKey } from "@/lib/providerKeys";
import { cn } from "@/lib/utils";
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

// Plugins (ADR-0013 / ADR-0003, CORE-1115): one compact card per installed
// plugin. The header carries activation and standing; provider keys (scraper
// credentials grouped by AuthRef) sit directly under it so a missing key is
// never buried; everything else lives behind a Details toggle.
export function Plugins() {
  const t = useT();
  const displayLocale = useDisplayLocale();
  const previousPlugins = useRef<PluginWithStanding[]>([]);
  const { data, loading, failed, refresh, replace } = useAsync((signal) =>
    fetchConsoleConfigWithStanding(signal, previousPlugins.current),
  );
  const [actionFailed, setActionFailed] = useState(false);
  const plugins = data?.plugins ?? [];
  useEffect(() => {
    previousPlugins.current = plugins;
  }, [plugins]);
  const pluginStandingUnavailable = data?.pluginStandingUnavailable ?? false;
  const hasUnrecognizedTrust = plugins.some((plugin) => plugin.trustState === "Unrecognized");

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

  async function credentialAction(action: () => Promise<unknown>): Promise<boolean> {
    setActionFailed(false);
    try {
      await action();
      await refresh();
      return true;
    } catch {
      setActionFailed(true);
      return false;
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
      {loading && !data ? (
        // Only the first load shows a skeleton. A later refresh keeps the cards
        // mounted, so it cannot discard a typed key or an unsaved config draft.
        <Skeleton className="h-32 w-full" />
      ) : failed ? (
        <ErrorState message="consoleConfigUnavailable" />
      ) : plugins.length === 0 ? (
        <EmptyState message="none" />
      ) : (
        <>
          {pluginStandingUnavailable && (
            <div
              role="alert"
              className="rounded-xl border border-[var(--amber-500)]/45 bg-[var(--amber-500)]/10 px-3 py-2 text-sm"
            >
              {t("pluginTrustUnavailable")}
            </div>
          )}
          {hasUnrecognizedTrust && (
            <div
              role="status"
              className="rounded-xl border border-[var(--amber-500)]/45 bg-[var(--amber-500)]/10 px-3 py-2 text-sm"
            >
              {t("pluginTrustRefreshServer")}
            </div>
          )}
          <div className="grid gap-3">
            {plugins.map((p) => (
              <PluginCard
                key={p.id}
                plugin={p}
                displayLocale={displayLocale}
                onToggle={(active) => toggle(p.id, active)}
                onCredential={credentialAction}
                onSaveConfig={saveConfig}
                onRunAction={runAction}
                onDeactivate={() => toggle(p.id, false)}
                onUninstall={() => uninstall(p.id)}
              />
            ))}
          </div>
        </>
      )}
      {actionFailed && (
        <p role="status" className="text-sm text-[var(--red-500)]">
          {t("consoleScraperActionFailed")}
        </p>
      )}
    </Section>
  );
}

const KEY_STATUS: Record<string, { label: StringKey; tone: StatusTone }> = {
  configured: { label: "consoleScraperConfigured", tone: "green" },
  missing: { label: "consoleScraperMissing", tone: "amber" },
  unavailable: { label: "consoleScraperUnavailable", tone: "neutral" },
  invalid: { label: "consoleScraperInvalid", tone: "red" },
  unreadable: { label: "consoleScraperUnreadable", tone: "red" },
  "not-required": { label: "consoleScraperNotRequired", tone: "neutral" },
};

function keyStatusChip(status: string, t: ReturnType<typeof useT>) {
  const entry = KEY_STATUS[status];
  return <StatusChip tone={entry?.tone ?? "neutral"}>{entry ? t(entry.label) : status}</StatusChip>;
}

const CARD =
  "rounded-[var(--radius-surface)] border border-[var(--border-subtle)] bg-[var(--bg-surface)]";
const KEY_INPUT = "h-10 rounded-xl";

function PluginCard({
  plugin,
  displayLocale,
  onToggle,
  onCredential,
  onSaveConfig,
  onRunAction,
  onDeactivate,
  onUninstall,
}: {
  plugin: PluginWithStanding;
  displayLocale: string;
  onToggle: (active: boolean) => Promise<void>;
  onCredential: (action: () => Promise<unknown>) => Promise<boolean>;
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
  const [expanded, setExpanded] = useState(false);
  const name = displayString(plugin.displayName, displayLocale);
  const builtIn = BUILT_IN_PLUGIN_IDS.has(plugin.id);
  const keys = useMemo(() => groupProviderKeys(plugin.scrapers ?? []), [plugin.scrapers]);
  const missingKey = keys.some(keyNeeded);
  const detailsID = `plugin-details-${plugin.id}`;

  return (
    <article className={CARD} aria-label={name}>
      <div className="console-control-row console-plugin-head">
        <div className="flex min-w-0 items-center gap-3">
          <div className="min-w-0 space-y-1">
            <div className="break-words text-[15px] font-semibold">{name}</div>
            <div className="flex flex-wrap items-center gap-1.5">
              {provenanceChip(plugin.provenance, t)}
              {trustBadge(plugin.trustState, t)}
              {plugin.active ? (
                <StatusChip tone="green">{t("consoleConfigActive")}</StatusChip>
              ) : (
                <StatusChip tone="neutral">{t("consoleConfigInactive")}</StatusChip>
              )}
              {missingKey && <StatusChip tone="amber">{t("pluginKeyNeeded")}</StatusChip>}
            </div>
          </div>
        </div>
        <div className="flex shrink-0 items-center gap-2">
          <Button
            type="button"
            size="sm"
            variant="ghost"
            aria-expanded={expanded}
            aria-controls={detailsID}
            aria-label={t("pluginDetailsToggle", { name })}
            onClick={() => setExpanded((open) => !open)}
          >
            {t("pluginDetails")}
            <ChevronDown
              aria-hidden
              className={cn("size-4 transition-transform", expanded && "rotate-180")}
            />
          </Button>
          <div title={builtIn ? "Built-in plugins cannot be disabled." : undefined}>
            <Switch
              checked={plugin.active}
              disabled={builtIn}
              onCheckedChange={builtIn ? undefined : (c) => void onToggle(c)}
              aria-label={name}
            />
          </div>
        </div>
      </div>

      {keys.length > 0 && (
        <div className="border-t border-[var(--border-subtle)] px-4 pt-3">
          <h3 className="m-0 text-sm font-semibold">{t("pluginKeysHeading")}</h3>
          {missingKey && (
            <p className="m-0 mt-1 text-[13px] leading-snug text-muted-foreground">
              {t("pluginKeysLead")}
            </p>
          )}
          <div className="divide-y divide-[var(--border-subtle)]">
            {keys.map((key) => (
              <ProviderKeyRow key={key.authRef} providerKey={key} onCredential={onCredential} />
            ))}
          </div>
        </div>
      )}

      {/* Collapsed Details stay mounted and hidden, so closing them keeps an
          unsaved config draft instead of restoring the saved values. */}
      <div
        id={detailsID}
        hidden={!expanded}
        className="space-y-4 border-t border-[var(--border-subtle)] px-4 py-4"
      >
        {plugin.controlPolicy && (
          <p className="m-0 text-[12px] text-muted-foreground">
            {t("pluginControlPolicy", { policy: plugin.controlPolicy })}
          </p>
        )}
        <PluginDetail
          plugin={plugin}
          displayLocale={displayLocale}
          onSaveConfig={onSaveConfig}
          onRunAction={onRunAction}
        />
        <PluginLifecycle
          plugin={plugin}
          displayLocale={displayLocale}
          onDeactivate={onDeactivate}
          onUninstall={onUninstall}
        />
      </div>
    </article>
  );
}

// ProviderKeyRow: one AuthRef-scoped credential. The secret is write-only —
// the field never shows a stored value, only the server-reported status.
function ProviderKeyRow({
  providerKey,
  onCredential,
}: {
  providerKey: ProviderKey;
  onCredential: (action: () => Promise<unknown>) => Promise<boolean>;
}) {
  const t = useT();
  const [secret, setSecret] = useState("");
  const [pending, setPending] = useState(false);
  const { authRef, label, hosts, status } = providerKey;
  const configured = status === "configured";

  async function run(action: () => Promise<unknown>, clear: boolean) {
    setPending(true);
    const ok = await onCredential(action);
    if (ok && clear) setSecret("");
    setPending(false);
  }

  return (
    <div className="py-3">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div className="min-w-0">
          <div className="text-sm font-medium">{label}</div>
          <div className="text-xs text-muted-foreground">
            {t("pluginKeySentTo", { hosts: hosts.join(", ") })}
          </div>
        </div>
        {keyStatusChip(status, t)}
      </div>
      <form
        className="mt-2.5 flex flex-wrap items-center gap-2"
        onSubmit={(event) => {
          event.preventDefault();
          if (secret.trim()) void run(() => setScraperCredential(authRef, secret), true);
        }}
      >
        <Input
          type="password"
          autoComplete="off"
          value={secret}
          className={cn(KEY_INPUT, "min-w-0 flex-1 basis-56")}
          placeholder={t(configured ? "pluginKeyReplacePlaceholder" : "pluginKeyPlaceholder")}
          aria-label={t("pluginKeyField", { provider: label })}
          onChange={(event) => setSecret(event.target.value)}
        />
        <Button type="submit" size="sm" className="px-4" disabled={pending || !secret.trim()}>
          {t("consoleScraperSave")}
        </Button>
        {status !== "missing" && (
          <>
            <Button
              type="button"
              size="sm"
              variant="ghost"
              disabled={pending}
              onClick={() => void run(() => validateScraperCredential(authRef), false)}
            >
              {t("consoleScraperValidate")}
            </Button>
            <Button
              type="button"
              size="sm"
              variant="ghost"
              disabled={pending}
              onClick={() => void run(() => disableScraperCredential(authRef), true)}
            >
              {t("consoleScraperDisable")}
            </Button>
          </>
        )}
      </form>
    </div>
  );
}

// PluginDetail (WEB-5I / ADR-0003 §Plugin, ADR-0013 §5): the operator surface
// beyond bare activation, shown when a card's Details are open. Four blocks,
// each driven by the consoleConfig substrate: editable typed (non-secret)
// config saved through setPluginConfig, the Core-owned permission inventory,
// runtime/declaration status rows, and the server-declared Action Descriptors
// rendered as trusted, risk-tinted controls. Declaration-free plugins render
// only the lifecycle block (in PluginCard).
function PluginDetail({
  plugin,
  displayLocale,
  onSaveConfig,
  onRunAction,
}: {
  plugin: PluginActivationInfo;
  displayLocale: string;
  onSaveConfig: (
    pluginID: string,
    values: Record<string, string>,
    clear: string[],
  ) => Promise<void>;
  onRunAction: (pluginID: string, actionID: string) => Promise<void>;
}) {
  const t = useT();
  const config = plugin.config ?? [];
  const permissions = plugin.permissions ?? [];
  const status = plugin.status ?? [];
  const actions = plugin.actions ?? [];

  const hasDeclarations =
    config.length > 0 || permissions.length > 0 || status.length > 0 || actions.length > 0;
  if (!hasDeclarations) return null;

  return (
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
        <SelectTrigger className="w-48" aria-label={label}>
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
