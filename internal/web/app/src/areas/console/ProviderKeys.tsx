import { useId, useMemo, useRef, useState } from "react";
import {
  CredentialHelpLink,
  useProviderKeyFieldLabel,
} from "@/components/beamfall/CredentialHelpLink";
import { StatusChip, type StatusTone } from "@/components/beamfall/workbench";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
  disableScraperCredential,
  fetchConsoleConfig,
  setScraperCredential,
  validateScraperCredential,
} from "@/lib/graphql";
import { useT } from "@/lib/i18n";
import {
  groupProviderKeys,
  keyNeededForLibraries,
  libraryPluginIDs,
  type ProviderKey,
  providerKeyDisplayStatus,
} from "@/lib/providerKeys";
import { cn } from "@/lib/utils";
import type { StringKey } from "@/strings";
import { CARD } from "./sections/libraryStyles";
import { useAsync } from "./useAsync";

// Provider keys (CORE-1115, CORE-1534): one AuthRef-scoped scraper credential
// per row, shared by the Plugins section and the Libraries page so both
// surfaces set, validate and clear a key the same way.

const KEY_STATUS: Record<string, { label: StringKey; tone: StatusTone }> = {
  configured: { label: "consoleScraperConfigured", tone: "green" },
  missing: { label: "consoleScraperMissing", tone: "amber" },
  // A missing key the plugins can work without (CORE-2311).
  optional: { label: "consoleScraperOptional", tone: "neutral" },
  unavailable: { label: "consoleScraperUnavailable", tone: "neutral" },
  invalid: { label: "consoleScraperInvalid", tone: "red" },
  unreadable: { label: "consoleScraperUnreadable", tone: "red" },
  "not-required": { label: "consoleScraperNotRequired", tone: "neutral" },
  disabled: { label: "consoleScraperDisabled", tone: "neutral" },
};

function KeyStatusChip({ status }: { status: string }) {
  const t = useT();
  const entry = KEY_STATUS[status];
  return <StatusChip tone={entry?.tone ?? "neutral"}>{entry ? t(entry.label) : status}</StatusChip>;
}

const KEY_INPUT = "h-10 rounded-xl";

// ProviderKeyRow: the secret is write-only — the field never shows a stored
// value, only the server-reported status, and it empties after a save.
export function ProviderKeyRow({
  providerKey,
  onCredential,
}: {
  providerKey: ProviderKey;
  onCredential: (action: () => Promise<unknown>) => Promise<boolean>;
}) {
  const t = useT();
  const fieldLabel = useProviderKeyFieldLabel();
  const [secret, setSecret] = useState("");
  const [pending, setPending] = useState(false);
  const { authRef, hosts, status } = providerKey;
  const configured = status === "configured";
  const disabled = status === "disabled";
  // Disable stops the provider outright: the server never falls back to a key
  // built into the server (CORE-1540), and the copy says so.
  const hintID = useId();

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
          <div className="text-sm font-medium">{providerKey.label || fieldLabel(providerKey)}</div>
          <div className="text-xs text-muted-foreground">
            {t("pluginKeySentTo", { hosts: hosts.join(", ") })}
          </div>
          <CredentialHelpLink providerKey={providerKey} />
        </div>
        <KeyStatusChip status={providerKeyDisplayStatus(providerKey)} />
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
          placeholder={
            providerKey.credentialHelp && !configured
              ? fieldLabel(providerKey)
              : t(configured ? "pluginKeyReplacePlaceholder" : "pluginKeyPlaceholder")
          }
          aria-label={fieldLabel(providerKey)}
          onChange={(event) => setSecret(event.target.value)}
        />
        <Button type="submit" size="sm" className="px-4" disabled={pending || !secret.trim()}>
          {t("consoleScraperSave")}
        </Button>
        {status !== "missing" && !disabled && (
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
              aria-describedby={hintID}
              onClick={() => void run(() => disableScraperCredential(authRef), true)}
            >
              {t("consoleScraperDisable")}
            </Button>
          </>
        )}
      </form>
      {status !== "missing" && (
        <p id={hintID} className="mt-1.5 text-xs text-muted-foreground">
          {t(disabled ? "pluginKeyDisabledHint" : "pluginKeyDisableHint")}
        </p>
      )}
    </div>
  );
}

// MissingProviderKeys (CORE-1534): on the Libraries page, the keys an active
// plugin requires and nobody has configured, since without them a Library's
// items import unmatched. Only plugins some Library uses count (CORE-2311). A row the owner touches stays for the visit, so a
// save shows "configured" and a later Disable shows "missing" again in place.
export function MissingProviderKeys({
  libraries,
}: {
  /** The page's Libraries, or null until they load. */
  libraries: { pluginID: string | null }[] | null;
}) {
  const t = useT();
  const config = useAsync(fetchConsoleConfig);
  const touched = useRef(new Set<string>());
  const [actionFailed, setActionFailed] = useState(false);

  const keys = useMemo(
    () =>
      groupProviderKeys(
        (config.data?.plugins ?? [])
          .filter((plugin) => plugin.active)
          .flatMap((plugin) => plugin.scrapers ?? []),
      ),
    [config.data],
  );
  const pluginIDs = useMemo(() => libraryPluginIDs(libraries), [libraries]);
  const needed = (key: ProviderKey) => keyNeededForLibraries(key, pluginIDs);
  const shown = keys.filter((key) => needed(key) || touched.current.has(key.authRef));
  if (shown.length === 0) return null;

  async function credentialAction(authRef: string, action: () => Promise<unknown>) {
    touched.current.add(authRef);
    setActionFailed(false);
    try {
      await action();
      await config.refresh();
      return true;
    } catch {
      setActionFailed(true);
      return false;
    }
  }

  const missing = shown.some(needed);
  return (
    <section aria-labelledby="library-provider-keys" className={cn(CARD, "px-4 pt-3")}>
      <h3 id="library-provider-keys" className="m-0 text-sm font-semibold">
        {t(missing ? "libraryProviderKeysNeeded" : "libraryProviderKeysHeading")}
      </h3>
      <p className="m-0 mt-1 text-dense leading-snug text-muted-foreground">
        {t("libraryProviderKeysLead")}
      </p>
      <div className="divide-y divide-[var(--border-subtle)]">
        {shown.map((key) => (
          <ProviderKeyRow
            key={key.authRef}
            providerKey={key}
            onCredential={(action) => credentialAction(key.authRef, action)}
          />
        ))}
      </div>
      {actionFailed && (
        <p role="status" className="pb-3 text-sm text-[var(--state-danger)]">
          {t("consoleScraperActionFailed")}
        </p>
      )}
    </section>
  );
}
