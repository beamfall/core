import { type FormEvent, useState } from "react";
import { EmptyState, ErrorState } from "@/components/beamfall/workbench";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { createProfile, disableProfile, fetchProfilesAdmin, updateProfile } from "@/lib/graphql";
import type { ProfileAdminInfo } from "@/lib/graphql/types";
import { useT } from "@/lib/i18n";
import { ConfirmButton } from "../ConfirmButton";
import { Section } from "../Section";
import { useAsync } from "../useAsync";

// The Console Profiles lane (ADR-0086 §46 — a distinct axis from Devices and
// Library policy): the household's Profile identity lifecycle. Lists Profiles,
// creates adult/managed Profiles, and offboards (disables) one with an explicit
// effect explanation behind a confirm dialog. Every mutation is Admin-Session-
// gated server-side. A Profile is not an Account (ADR-0004); no account or
// entitlement data is touched here.
//
// Disable is one-way in the web Console today: the server `profiles` query
// excludes disabled Profiles, so an offboarded Profile drops off the list and
// cannot yet be re-enabled here (server follow-up). The confirm copy is honest
// about this.
export function Profiles() {
  const profiles = useAsync(fetchProfilesAdmin);
  const rows = profiles.data ?? [];

  if (profiles.failed) {
    return (
      <Section heading="consoleProfilesHeading" lead="consoleProfilesLead">
        <ErrorState message="consoleProfilesUnavailable" />
      </Section>
    );
  }

  return (
    <Section heading="consoleProfilesHeading" lead="consoleProfilesLead">
      <div className="space-y-6">
        <CreateProfileForm onCreated={() => void profiles.refresh()} />

        {rows.length === 0 ? (
          <EmptyState message="consoleProfilesEmpty" />
        ) : (
          <ul className="space-y-2">
            {rows.map((p) => (
              <ProfileRow key={p.id} profile={p} onDisabled={() => void profiles.refresh()} />
            ))}
          </ul>
        )}
      </div>
    </Section>
  );
}

function CreateProfileForm({ onCreated }: { onCreated: () => void }) {
  const t = useT();
  const [name, setName] = useState("");
  const [kind, setKind] = useState("adult");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState(false);

  async function submit(e: FormEvent) {
    e.preventDefault();
    const trimmed = name.trim();
    if (!trimmed) return;
    setBusy(true);
    setError(false);
    try {
      await createProfile(trimmed, kind);
      setName("");
      setKind("adult");
      onCreated();
    } catch {
      setError(true);
    } finally {
      setBusy(false);
    }
  }

  return (
    <form
      onSubmit={submit}
      className="console-card console-card-pad flex flex-wrap items-end gap-3"
    >
      <div className="min-w-48 flex-1 space-y-1">
        <Label htmlFor="new-profile-name">{t("consoleProfilesNameLabel")}</Label>
        <Input
          id="new-profile-name"
          value={name}
          onChange={(e) => setName(e.target.value)}
          autoComplete="off"
        />
      </div>
      <div className="space-y-1">
        <Label>{t("consoleProfilesKindLabel")}</Label>
        <Select value={kind} onValueChange={setKind}>
          <SelectTrigger className="w-40" aria-label={t("consoleProfilesKindLabel")}>
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="adult">{t("consoleProfilesKindAdult")}</SelectItem>
            <SelectItem value="managed">{t("consoleProfilesKindManaged")}</SelectItem>
          </SelectContent>
        </Select>
      </div>
      <Button type="submit" disabled={busy || !name.trim()}>
        {t("consoleProfilesCreate")}
      </Button>
      {error && (
        <p role="alert" className="w-full text-sm text-[var(--red-500)]">
          {t("consoleProfilesCreateFailed")}
        </p>
      )}
    </form>
  );
}

function ProfileRow({
  profile,
  onDisabled,
}: {
  profile: ProfileAdminInfo;
  onDisabled: () => void;
}) {
  const t = useT();
  const [name, setName] = useState(profile.name);
  const [kind, setKind] = useState(profile.kind);
  const [busy, setBusy] = useState(false);
  const dirty = name.trim() !== profile.name || kind !== profile.kind;

  async function save() {
    const trimmed = name.trim();
    if (!trimmed || !dirty) return;
    setBusy(true);
    try {
      await updateProfile(profile.id, trimmed, kind);
      onDisabled();
    } finally {
      setBusy(false);
    }
  }

  return (
    <li className="console-card console-card-pad space-y-3">
      <div className="flex flex-wrap items-end gap-3">
        <div className="min-w-48 flex-1 space-y-1">
          <Label htmlFor={`profile-name-${profile.id}`}>{t("consoleProfilesNameLabel")}</Label>
          <Input
            id={`profile-name-${profile.id}`}
            value={name}
            onChange={(e) => setName(e.target.value)}
            autoComplete="off"
          />
        </div>
        <div className="space-y-1">
          <Label>{t("consoleProfilesKindLabel")}</Label>
          <Select value={kind} onValueChange={setKind}>
            <SelectTrigger className="w-40" aria-label={t("consoleProfilesKindLabel")}>
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="adult">{t("consoleProfilesKindAdult")}</SelectItem>
              <SelectItem value="managed">{t("consoleProfilesKindManaged")}</SelectItem>
            </SelectContent>
          </Select>
        </div>
        <Badge variant={kind === "managed" ? "secondary" : "outline"}>
          {kind === "managed" ? t("consoleProfilesKindManaged") : t("consoleProfilesKindAdult")}
        </Badge>
        <Button
          type="button"
          size="sm"
          disabled={busy || !dirty || !name.trim()}
          onClick={() => void save()}
        >
          {t("consoleProfilesSave")}
        </Button>
        <ConfirmButton
          label={t("consoleProfilesDisable")}
          confirm="consoleProfilesDisableConfirm"
          variant="outline"
          size="sm"
          onConfirm={async () => {
            await disableProfile(profile.id);
            onDisabled();
          }}
        >
          {t("consoleProfilesDisableEffect")}
        </ConfirmButton>
      </div>
    </li>
  );
}
