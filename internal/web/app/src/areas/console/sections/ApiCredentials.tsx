import { Copy, KeyRound, Plus } from "lucide-react";
import { type FormEvent, useState } from "react";
import { DataTable, EmptyState, ErrorState, StatusChip } from "@/components/beamfall/workbench";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Separator } from "@/components/ui/separator";
import { Skeleton } from "@/components/ui/skeleton";
import { TableCell, TableRow } from "@/components/ui/table";
import {
  createAPIKey,
  createAPIToken,
  fetchAPIKeys,
  fetchAPITokens,
  revokeAPIKey,
  revokeAPIToken,
} from "@/lib/graphql";
import { useT } from "@/lib/i18n";
import type { StringKey } from "@/strings";
import { ConfirmButton } from "../ConfirmButton";
import { Section } from "../Section";
import { useAsync } from "../useAsync";

// The five-scope closed vocabulary (ADR-0040 §2.2). Order + labels mirror app.js.
const SCOPES: { id: string; label: StringKey }[] = [
  { id: "read-catalog", label: "scopeReadCatalog" },
  { id: "read-sessions", label: "scopeReadSessions" },
  { id: "ingest", label: "scopeIngest" },
  { id: "events-subscribe", label: "scopeEventsSubscribe" },
  { id: "playback-control", label: "scopePlaybackControl" },
];

const API_SCOPE_LABELS = new Map(SCOPES.map((scope) => [scope.id, scope.label]));

function humanizeScope(scope: string): string {
  const words = scope.trim().replace(/[-_]+/g, " ").replace(/\s+/g, " ").toLowerCase();
  if (!words) return "";
  return words.charAt(0).toUpperCase() + words.slice(1);
}

export function apiScopeLabel(scope: string, labels: Partial<Record<StringKey, string>>): string {
  const key = API_SCOPE_LABELS.get(scope);
  return key ? (labels[key] ?? humanizeScope(scope)) : humanizeScope(scope);
}

// The "copy this once" banner for a freshly minted secret (token or key). The
// secret is never re-fetchable — the server returns it only on creation, so
// this is the one moment the screen must be unmissable (beam keyline).
function SecretBanner({ secret }: { secret: string }) {
  const t = useT();
  return (
    <div className="console-secret">
      <p className="console-secret-lead">
        <KeyRound className="console-nav-icon" aria-hidden />
        {t("apiTokenShownOnce")}
      </p>
      <div className="console-secret-row">
        <code className="console-secret-value">{secret}</code>
        <Button
          size="sm"
          variant="outline"
          onClick={() => void navigator.clipboard?.writeText(secret)}
        >
          <Copy className="console-nav-icon" aria-hidden />
          {t("copy")}
        </Button>
      </div>
    </div>
  );
}

// Admin API tokens (ADR-0013): create (shown once) + revoke (behind confirm).
function ApiTokens() {
  const t = useT();
  const { data, loading, failed, refresh } = useAsync(fetchAPITokens);
  const tokens = data ?? [];
  const [name, setName] = useState("");
  const [secret, setSecret] = useState<string | null>(null);
  const [error, setError] = useState<StringKey | null>(null);

  async function onCreate(e: FormEvent) {
    e.preventDefault();
    if (!name.trim()) return;
    setError(null);
    try {
      const created = await createAPIToken(name.trim());
      setSecret(created.token);
      setName("");
      await refresh();
    } catch {
      setError("couldNotCreateAPIToken");
    }
  }

  async function revoke(id: string) {
    try {
      await revokeAPIToken(id);
      await refresh();
    } catch {
      setError("couldNotRevokeAPIToken");
    }
  }

  return (
    <Section heading="apiTokens" lead="apiTokensLead">
      <div className="console-card console-card-pad">
        <form onSubmit={onCreate} className="flex items-end gap-2">
          <div className="flex-1 space-y-1.5">
            <Label htmlFor="api-token-name">{t("apiTokenName")}</Label>
            <Input id="api-token-name" value={name} onChange={(e) => setName(e.target.value)} />
          </div>
          <Button type="submit" disabled={!name.trim()}>
            <Plus className="console-nav-icon" aria-hidden />
            {t("createAPIToken")}
          </Button>
        </form>
        {secret && <SecretBanner secret={secret} />}
        {error && <p className="mt-2 text-sm text-[var(--red-500)]">{t(error)}</p>}
      </div>

      {loading ? (
        <Skeleton className="h-32 w-full" />
      ) : failed ? (
        <ErrorState message="apiTokensUnavailable" />
      ) : tokens.length === 0 ? (
        <EmptyState message="noAPITokens" icon={KeyRound} />
      ) : (
        <DataTable
          columns={[
            { key: "name", label: t("apiTokenName") },
            { key: "created", label: t("created") },
            { key: "used", label: t("lastUsed") },
            { key: "action", label: "" },
          ]}
        >
          {tokens.map((tok) => (
            <TableRow key={tok.id}>
              <TableCell className="font-medium">{tok.name}</TableCell>
              <TableCell className="console-mono">{tok.createdAt}</TableCell>
              <TableCell className="console-mono">{tok.lastUsedAt ?? t("never")}</TableCell>
              <TableCell className="text-right">
                {tok.revoked ? (
                  <StatusChip tone="red">{t("revoked")}</StatusChip>
                ) : (
                  <ConfirmButton
                    size="sm"
                    label={t("revoke")}
                    confirm="confirmRevokeAPIToken"
                    onConfirm={() => revoke(tok.id)}
                  />
                )}
              </TableCell>
            </TableRow>
          ))}
        </DataTable>
      )}
    </Section>
  );
}

// Scoped API keys (ADR-0040): create with a scope checklist (shown once) +
// revoke (behind confirm). The Reveal contract applies at all scopes server-side.
function ApiKeys() {
  const t = useT();
  const { data, loading, failed, refresh } = useAsync(fetchAPIKeys);
  const keys = data ?? [];
  const [name, setName] = useState("");
  const [scopes, setScopes] = useState<string[]>([]);
  const [secret, setSecret] = useState<string | null>(null);
  const [error, setError] = useState<StringKey | null>(null);
  const scopeLabels = Object.fromEntries(
    SCOPES.map((scope) => [scope.label, t(scope.label)]),
  ) as Partial<Record<StringKey, string>>;

  function toggleScope(id: string, on: boolean) {
    setScopes((prev) => (on ? [...prev, id] : prev.filter((s) => s !== id)));
  }

  async function onCreate(e: FormEvent) {
    e.preventDefault();
    if (!name.trim() || scopes.length === 0) {
      setError("apiKeyNeedsNameAndScope");
      return;
    }
    setError(null);
    try {
      const created = await createAPIKey(name.trim(), scopes);
      setSecret(created.key);
      setName("");
      setScopes([]);
      await refresh();
    } catch {
      setError("couldNotCreateAPIKey");
    }
  }

  async function revoke(id: string) {
    try {
      await revokeAPIKey(id);
      await refresh();
    } catch {
      setError("couldNotRevokeAPIKey");
    }
  }

  return (
    <Section heading="apiKeysHeading" lead="apiKeysLead">
      <div className="console-card console-card-pad">
        <form onSubmit={onCreate} className="space-y-3">
          <div className="max-w-sm space-y-1.5">
            <Label htmlFor="api-key-name">{t("apiKeyNameLabel")}</Label>
            <Input id="api-key-name" value={name} onChange={(e) => setName(e.target.value)} />
          </div>
          <fieldset className="space-y-2">
            <legend className="text-xs font-semibold uppercase tracking-wide text-muted-foreground">
              {t("apiKeyScopesLegend")}
            </legend>
            <div className="flex flex-wrap gap-x-5 gap-y-2">
              {SCOPES.map((scope) => (
                <label key={scope.id} className="flex items-center gap-2 text-sm">
                  <Checkbox
                    checked={scopes.includes(scope.id)}
                    onCheckedChange={(c) => toggleScope(scope.id, c === true)}
                  />
                  {t(scope.label)}
                </label>
              ))}
            </div>
          </fieldset>
          <Button type="submit" disabled={!name.trim() || scopes.length === 0}>
            <Plus className="console-nav-icon" aria-hidden />
            {t("createAPIKey")}
          </Button>
        </form>
        {secret && <SecretBanner secret={secret} />}
        {error && <p className="mt-2 text-sm text-[var(--red-500)]">{t(error)}</p>}
      </div>

      {loading ? (
        <Skeleton className="h-32 w-full" />
      ) : failed ? (
        <ErrorState message="apiKeysUnavailable" />
      ) : keys.length === 0 ? (
        <EmptyState message="noAPIKeys" icon={KeyRound} />
      ) : (
        <DataTable
          columns={[
            { key: "name", label: t("apiKeyNameLabel") },
            { key: "scopes", label: t("apiKeyScopesLegend") },
            { key: "used", label: t("lastUsed") },
            { key: "action", label: "" },
          ]}
        >
          {keys.map((key) => (
            <TableRow key={key.id}>
              <TableCell className="font-medium">{key.name}</TableCell>
              <TableCell>
                {key.scopes.length > 0 ? (
                  <span className="flex flex-wrap gap-1.5">
                    {key.scopes.map((scope) => (
                      <StatusChip key={scope} tone="cyan" dot={false}>
                        {apiScopeLabel(scope, scopeLabels)}
                      </StatusChip>
                    ))}
                  </span>
                ) : (
                  <span className="text-muted-foreground">{t("noScopes")}</span>
                )}
              </TableCell>
              <TableCell className="console-mono">{key.lastUsedAt ?? t("never")}</TableCell>
              <TableCell className="text-right">
                {key.revoked ? (
                  <StatusChip tone="red">{t("revoked")}</StatusChip>
                ) : (
                  <ConfirmButton
                    size="sm"
                    label={t("revoke")}
                    confirm="confirmRevokeAPIKey"
                    onConfirm={() => revoke(key.id)}
                  />
                )}
              </TableCell>
            </TableRow>
          ))}
        </DataTable>
      )}
    </Section>
  );
}

// API tokens + scoped API keys (ADR-0040).
export function ApiCredentials() {
  return (
    <div className="space-y-8">
      <ApiTokens />
      <Separator />
      <ApiKeys />
    </div>
  );
}
