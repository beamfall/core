import { Plus } from "lucide-react";
import { useState } from "react";
import {
  DataTable,
  EmptyState,
  ErrorState,
  HelpTip,
  StatusChip,
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
import { Skeleton } from "@/components/ui/skeleton";
import { Switch } from "@/components/ui/switch";
import { TableCell, TableRow } from "@/components/ui/table";
import {
  deleteWebhook,
  fetchWebhookDeliveries,
  fetchWebhookSettings,
  fetchWebhooks,
  saveWebhook,
  setWebhookSettings,
  testWebhook,
  type WebhookDraft,
} from "@/lib/graphql";
import type { WebhookEndpointInfo } from "@/lib/graphql/types";
import { useT } from "@/lib/i18n";
import type { StringKey } from "@/strings";
import { ConfirmButton } from "../ConfirmButton";
import { Section } from "../Section";
import { useAsync } from "../useAsync";

const FORMAT_LABELS: Record<string, StringKey> = {
  discord: "webhookFormatDiscord",
  slack: "webhookFormatSlack",
  ntfy: "webhookFormatNtfy",
  gotify: "webhookFormatGotify",
  email: "webhookFormatEmail",
  generic: "webhookFormatGeneric",
};

const URL_HINTS: Record<string, StringKey> = {
  discord: "webhookURLHintDiscord",
  slack: "webhookURLHintSlack",
  ntfy: "webhookURLHintNtfy",
  gotify: "webhookURLHintGotify",
  email: "webhookURLHintEmail",
  generic: "webhookURLHintGeneric",
};

// Discord and Slack carry their credential in the URL and take no secret.
const SECRET_HINTS: Record<string, StringKey> = {
  ntfy: "webhookSecretHintNtfy",
  gotify: "webhookSecretHintGotify",
  email: "webhookSecretHintEmail",
  generic: "webhookSecretHintGeneric",
};

const KIND_LABELS: Record<string, StringKey> = {
  "media.added": "webhookKindMediaAdded",
  "playback.started": "webhookKindPlaybackStarted",
  "playback.stopped": "webhookKindPlaybackStopped",
  "profile.switched": "webhookKindProfileSwitched",
  "admin.signed_in": "webhookKindAdminSignedIn",
  "admin.sign_in_failed": "webhookKindAdminSignInFailed",
  "device.enrolled": "webhookKindDeviceEnrolled",
  "device.revoked": "webhookKindDeviceRevoked",
  "session.terminated": "webhookKindSessionTerminated",
  "webhook.test": "webhookKindTest",
};

interface Draft {
  id?: string;
  name: string;
  format: string;
  url: string;
  secret: string;
  secretSet: boolean;
  eventKinds: string[];
  disabled: boolean;
  allowPrivateNetwork: boolean;
  from: string;
  to: string;
  username: string;
}

const EMPTY_DRAFT: Draft = {
  name: "",
  format: "discord",
  url: "",
  secret: "",
  secretSet: false,
  eventKinds: [],
  disabled: false,
  allowPrivateNetwork: false,
  from: "",
  to: "",
  username: "",
};

function draftFrom(hook: WebhookEndpointInfo): Draft {
  return {
    id: hook.id,
    name: hook.name,
    format: hook.format,
    // The saved URL never comes back in full; blank keeps it.
    url: "",
    secret: "",
    secretSet: hook.secretSet,
    eventKinds: hook.eventKinds,
    disabled: hook.disabled,
    allowPrivateNetwork: hook.allowPrivateNetwork,
    from: hook.from,
    to: hook.to.join(", "),
    username: hook.username,
  };
}

// toInput sends only what the owner set: a blank URL or secret on an edit keeps
// the saved value, and a format that takes no secret never sends one.
function toInput(draft: Draft): WebhookDraft {
  const input: WebhookDraft = {
    name: draft.name.trim(),
    format: draft.format,
    eventKinds: draft.eventKinds,
    disabled: draft.disabled,
    allowPrivateNetwork: draft.allowPrivateNetwork,
  };
  if (draft.id) input.id = draft.id;
  if (draft.url.trim() !== "") input.url = draft.url.trim();
  if (SECRET_HINTS[draft.format] && draft.secret !== "") input.secret = draft.secret;
  if (draft.format === "email") {
    input.from = draft.from.trim();
    input.to = draft.to
      .split(",")
      .map((addr) => addr.trim())
      .filter((addr) => addr !== "");
    input.username = draft.username.trim();
  }
  return input;
}

function errorReason(err: unknown): string {
  return err instanceof Error ? err.message : "";
}

/** Webhooks (CORE-1697): add, test and remove notification endpoints. */
export function Webhooks() {
  const t = useT();
  const hooks = useAsync(fetchWebhooks);
  const settings = useAsync(fetchWebhookSettings);
  const deliveries = useAsync(fetchWebhookDeliveries);
  const [draft, setDraft] = useState<Draft | null>(null);
  const [saveError, setSaveError] = useState<string | null>(null);
  const [testing, setTesting] = useState<string | null>(null);
  const [testMessage, setTestMessage] = useState<string | null>(null);

  const formats = settings.data?.formats ?? Object.keys(FORMAT_LABELS);
  const kinds = settings.data?.eventKinds ?? [];
  const names = new Map((hooks.data ?? []).map((hook) => [hook.id, hook.name]));

  function edit(next: Draft | null) {
    setDraft(next);
    setSaveError(null);
  }

  async function handleSave(e: React.FormEvent) {
    e.preventDefault();
    if (!draft) return;
    try {
      await saveWebhook(toInput(draft));
    } catch (err) {
      setSaveError(t("webhookSaveFailed", { reason: errorReason(err) }));
      return;
    }
    edit(null);
    await hooks.refresh();
  }

  async function handleTest(hook: WebhookEndpointInfo) {
    setTesting(hook.id);
    setTestMessage(null);
    try {
      const result = await testWebhook(hook.id);
      setTestMessage(
        result.ok
          ? t("webhookTestDelivered", { ms: result.durationMs })
          : t("webhookTestFailed", { reason: result.diagnostic }),
      );
    } catch (err) {
      setTestMessage(t("webhookTestFailed", { reason: errorReason(err) }));
    } finally {
      setTesting(null);
    }
    await deliveries.refresh();
  }

  async function handleDelete(hook: WebhookEndpointInfo) {
    await deleteWebhook(hook.id);
    await hooks.refresh();
  }

  function toggleKind(kind: string, on: boolean) {
    setDraft((d) =>
      d
        ? {
            ...d,
            eventKinds: on ? [...d.eventKinds, kind] : d.eventKinds.filter((k) => k !== kind),
          }
        : d,
    );
  }

  const secretHint = draft ? SECRET_HINTS[draft.format] : undefined;

  return (
    <div className="space-y-8">
      <Section
        heading="webhooksHeading"
        lead="webhooksLead"
        actions={
          <Button size="sm" variant="outline" onClick={() => edit(draft ? null : EMPTY_DRAFT)}>
            <Plus className="mr-1 h-4 w-4" />
            {t("webhooksAdd")}
          </Button>
        }
      >
        {draft && (
          <form
            onSubmit={handleSave}
            aria-label={t("webhooksAdd")}
            className="mb-4 flex flex-col gap-3 rounded-md border p-4"
          >
            <div className="grid gap-3 sm:grid-cols-2">
              <div className="space-y-1">
                <Label htmlFor="webhook-name">{t("webhookName")}</Label>
                <Input
                  id="webhook-name"
                  value={draft.name}
                  onChange={(e) => setDraft({ ...draft, name: e.target.value })}
                  required
                />
              </div>
              <div className="space-y-1">
                <Label>{t("webhookFormat")}</Label>
                <Select
                  value={draft.format}
                  onValueChange={(format) => setDraft({ ...draft, format })}
                >
                  <SelectTrigger aria-label={t("webhookFormat")}>
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {formats.map((format) => (
                      <SelectItem key={format} value={format}>
                        {FORMAT_LABELS[format] ? t(FORMAT_LABELS[format]) : format}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </div>
            </div>
            <div className="space-y-1">
              <Label htmlFor="webhook-url">{t("webhookURL")}</Label>
              <Input
                id="webhook-url"
                type="url"
                autoComplete="off"
                value={draft.url}
                onChange={(e) => setDraft({ ...draft, url: e.target.value })}
                required={!draft.id}
              />
              <p className="text-xs text-muted-foreground">
                {draft.id
                  ? t("webhookURLKeep")
                  : URL_HINTS[draft.format] && t(URL_HINTS[draft.format])}
              </p>
            </div>
            {secretHint && (
              <div className="space-y-1" data-setting="console.webhooks.secret">
                <span className="bf-label-with-tip">
                  <Label htmlFor="webhook-secret">{t("webhookSecret")}</Label>
                  <HelpTip label={t("webhookSecret")} text="accessHelpWebhookSecretTip" />
                </span>
                <Input
                  id="webhook-secret"
                  type="password"
                  autoComplete="new-password"
                  value={draft.secret}
                  onChange={(e) => setDraft({ ...draft, secret: e.target.value })}
                />
                <p className="text-xs text-muted-foreground">
                  {draft.secretSet ? t("webhookSecretKeep") : t(secretHint)}
                </p>
              </div>
            )}
            {draft.format === "email" && (
              <div className="grid gap-3 sm:grid-cols-3">
                <div className="space-y-1">
                  <Label htmlFor="webhook-from">{t("webhookFrom")}</Label>
                  <Input
                    id="webhook-from"
                    type="email"
                    value={draft.from}
                    onChange={(e) => setDraft({ ...draft, from: e.target.value })}
                    required
                  />
                </div>
                <div className="space-y-1">
                  <Label htmlFor="webhook-to">{t("webhookTo")}</Label>
                  <Input
                    id="webhook-to"
                    value={draft.to}
                    onChange={(e) => setDraft({ ...draft, to: e.target.value })}
                    required
                  />
                </div>
                <div className="space-y-1">
                  <Label htmlFor="webhook-username">{t("webhookUsername")}</Label>
                  <Input
                    id="webhook-username"
                    autoComplete="off"
                    value={draft.username}
                    onChange={(e) => setDraft({ ...draft, username: e.target.value })}
                  />
                </div>
              </div>
            )}
            {kinds.length > 0 && (
              <fieldset className="space-y-2" data-setting="console.webhooks.events">
                <legend className="text-sm font-medium">{t("webhookEvents")}</legend>
                <div className="grid gap-2 sm:grid-cols-3">
                  {kinds.map((kind) => (
                    <label key={kind} className="flex items-center gap-2 text-sm">
                      <Checkbox
                        checked={draft.eventKinds.includes(kind)}
                        onCheckedChange={(c) => toggleKind(kind, c === true)}
                      />
                      {KIND_LABELS[kind] ? t(KIND_LABELS[kind]) : kind}
                    </label>
                  ))}
                </div>
                <p className="text-xs text-muted-foreground">{t("webhookEventsAll")}</p>
              </fieldset>
            )}
            <div className="flex flex-wrap gap-6">
              <label className="flex items-center gap-2 text-sm">
                <Switch
                  checked={draft.disabled}
                  onCheckedChange={(disabled) => setDraft({ ...draft, disabled })}
                  aria-label={t("webhookPausedToggle")}
                />
                {t("webhookPausedToggle")}
              </label>
              <div
                className="flex items-center gap-1"
                data-setting="console.webhooks.allowPrivateNetwork"
              >
                <label className="flex items-center gap-2 text-sm">
                  <Switch
                    checked={draft.allowPrivateNetwork}
                    onCheckedChange={(allowPrivateNetwork) =>
                      setDraft({ ...draft, allowPrivateNetwork })
                    }
                    aria-label={t("webhookAllowPrivate")}
                  />
                  {t("webhookAllowPrivate")}
                </label>
                <HelpTip label={t("webhookAllowPrivate")} text="accessHelpWebhookPrivateTip" />
              </div>
            </div>
            {saveError !== null && (
              <p role="alert" className="text-sm text-destructive">
                {saveError}
              </p>
            )}
            <div className="flex gap-2">
              <Button type="submit" size="sm">
                {t("webhookSave")}
              </Button>
              <Button type="button" size="sm" variant="ghost" onClick={() => edit(null)}>
                {t("cancel")}
              </Button>
            </div>
          </form>
        )}
        {hooks.loading && <Skeleton className="h-32 w-full" />}
        {hooks.failed && <ErrorState message="webhooksLoadError" />}
        {!hooks.loading && !hooks.failed && hooks.data?.length === 0 && (
          <EmptyState message="webhooksEmpty" />
        )}
        {!hooks.loading && !hooks.failed && hooks.data && hooks.data.length > 0 && (
          <DataTable
            columns={[
              { key: "name", label: t("webhookColName") },
              { key: "format", label: t("webhookColFormat") },
              { key: "destination", label: t("webhookColDestination") },
              { key: "status", label: t("webhookColStatus") },
              { key: "actions", numeric: true },
            ]}
          >
            {hooks.data.map((hook) => (
              <TableRow key={hook.id}>
                <TableCell className="font-medium">{hook.name || hook.id}</TableCell>
                <TableCell>
                  {FORMAT_LABELS[hook.format] ? t(FORMAT_LABELS[hook.format]) : hook.format}
                </TableCell>
                <TableCell className="text-sm text-muted-foreground">{hook.url}</TableCell>
                <TableCell>
                  {hook.problem ? (
                    <span title={hook.problem}>
                      <StatusChip tone="red">{t("webhookNotRunning")}</StatusChip>
                    </span>
                  ) : hook.disabled ? (
                    <StatusChip tone="amber">{t("webhookPaused")}</StatusChip>
                  ) : (
                    <StatusChip tone="green">{t("webhookActive")}</StatusChip>
                  )}
                </TableCell>
                <TableCell className="text-right">
                  <div className="flex justify-end gap-2">
                    <Button
                      size="sm"
                      variant="outline"
                      disabled={testing !== null}
                      onClick={() => handleTest(hook)}
                    >
                      {testing === hook.id ? t("webhookTesting") : t("webhookTest")}
                    </Button>
                    <Button size="sm" variant="ghost" onClick={() => edit(draftFrom(hook))}>
                      {t("webhookEdit")}
                    </Button>
                    <ConfirmButton
                      size="sm"
                      onConfirm={() => handleDelete(hook)}
                      label={t("webhookDelete")}
                      confirm="webhookDeleteConfirm"
                    />
                  </div>
                </TableCell>
              </TableRow>
            ))}
          </DataTable>
        )}
        <p role="status" aria-live="polite" className="text-sm">
          {testMessage}
        </p>
      </Section>

      <PublicBaseURL
        current={settings.data?.publicBaseURL ?? ""}
        loaded={settings.data !== null}
        onSaved={settings.refresh}
      />

      <Section heading="webhookDeliveriesHeading">
        {deliveries.loading && <Skeleton className="h-24 w-full" />}
        {!deliveries.loading && deliveries.data?.length === 0 && (
          <EmptyState message="webhookDeliveriesEmpty" />
        )}
        {!deliveries.loading && deliveries.data && deliveries.data.length > 0 && (
          <DataTable
            columns={[
              { key: "time", label: t("webhookColTime") },
              { key: "webhook", label: t("webhookColWebhook") },
              { key: "event", label: t("webhookColEvent") },
              { key: "outcome", label: t("webhookColOutcome") },
              { key: "attempts", label: t("webhookColAttempts"), numeric: true },
            ]}
          >
            {deliveries.data.map((row, i) => (
              <TableRow key={`${row.at}-${row.endpointID}-${i}`}>
                <TableCell className="text-sm text-muted-foreground">
                  {new Date(row.at).toLocaleString()}
                </TableCell>
                <TableCell>{names.get(row.endpointID) ?? row.endpointID}</TableCell>
                <TableCell>
                  {KIND_LABELS[row.eventKind] ? t(KIND_LABELS[row.eventKind]) : row.eventKind}
                </TableCell>
                <TableCell>
                  {row.outcome === "delivered" ? (
                    <StatusChip tone="green">{t("webhookOutcomeDelivered")}</StatusChip>
                  ) : (
                    <StatusChip tone="red">
                      {t("webhookOutcomeFailed")}
                      {row.statusClass ? ` · ${row.statusClass}` : ""}
                    </StatusChip>
                  )}
                </TableCell>
                <TableCell className="text-right">{row.attempts}</TableCell>
              </TableRow>
            ))}
          </DataTable>
        )}
      </Section>
    </div>
  );
}

// PublicBaseURL edits the origin notifications link to. It remounts on each
// load (keyed by the saved value) so the field starts from the server's value.
function PublicBaseURL({
  current,
  loaded,
  onSaved,
}: {
  current: string;
  loaded: boolean;
  onSaved: () => Promise<void>;
}) {
  const t = useT();
  const [value, setValue] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const shown = value ?? current;

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    try {
      await setWebhookSettings(shown.trim());
    } catch (err) {
      setError(t("webhookSaveFailed", { reason: errorReason(err) }));
      return;
    }
    setError(null);
    setValue(null);
    await onSaved();
  }

  return (
    <Section heading="webhookPublicBaseURL" lead="webhookPublicBaseURLHint">
      <form
        onSubmit={handleSubmit}
        className="flex max-w-xl flex-wrap items-center gap-2"
        data-setting="console.webhooks.publicServerUrl"
      >
        <Input
          type="url"
          aria-label={t("webhookPublicBaseURL")}
          placeholder="https://media.example.net"
          className="min-w-0 flex-1"
          value={shown}
          disabled={!loaded}
          onChange={(e) => setValue(e.target.value)}
        />
        <HelpTip label={t("webhookPublicBaseURL")} text="accessHelpWebhookPublicUrlTip" />
        <Button type="submit" size="sm" variant="outline" disabled={!loaded}>
          {t("webhookSettingsSave")}
        </Button>
      </form>
      {error !== null && (
        <p role="alert" className="text-sm text-destructive">
          {error}
        </p>
      )}
    </Section>
  );
}
