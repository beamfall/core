import { type FormEvent, useId, useLayoutEffect, useRef, useState } from "react";
import { AdminGate } from "@/areas/console/AdminGate";
import { useAsync } from "@/areas/console/useAsync";
import { ErrorLine, ManageSection } from "@/components/beamfall/workbench";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  fetchCurrentSession,
  fetchProfileEntryPins,
  setProfileEntryPin,
  setProfileSubtitleLanguages,
} from "@/lib/graphql";
import { useT } from "@/lib/i18n";
import { useSession } from "@/lib/session";

interface PinSaveState {
  savingPin: boolean;
  setSavingPin: (busy: boolean) => void;
}

export function ProfileSettings({ savingPin, setSavingPin }: PinSaveState) {
  const t = useT();
  const [open, setOpen] = useState(false);
  return (
    <ManageSection
      title={t("manageProfileSettings")}
      actions={
        <Button
          variant="outline"
          size="sm"
          aria-expanded={open}
          data-setting="manage.profile.openSettings"
          disabled={savingPin}
          onClick={() => setOpen(!open)}
        >
          {t(open ? "consoleProfilesDone" : "manageProfileSettingsOpen")}
        </Button>
      }
    >
      {open && (
        <AdminGate
          copy={{
            title: "manageLockedTitle",
            lead: "manageLockedLead",
            submit: "manageUnlockSubmit",
          }}
        >
          <SettingsForms savingPin={savingPin} setSavingPin={setSavingPin} />
        </AdminGate>
      )}
    </ManageSection>
  );
}

function SettingsForms({ savingPin, setSavingPin }: PinSaveState) {
  const t = useT();
  const { session, refreshProfiles } = useSession();
  const { data, loading, failed, refresh } = useAsync(fetchProfileEntryPins);
  const current = useAsync(fetchCurrentSession);
  const adults = data?.filter(
    (profile) => profile.kind === "adult" && typeof profile.entryPinSet === "boolean",
  );
  const [selectedID, setSelectedID] = useState("");
  const selected = adults?.find((profile) => profile.id === (selectedID || session?.profileID));

  return (
    <div className="space-y-6">
      <section className="space-y-3" aria-labelledby="profile-entry-pin-heading">
        <h3 id="profile-entry-pin-heading" className="text-sm font-semibold">
          {t("manageProfileEntryPin")}
        </h3>
        <p className="text-sm text-muted-foreground">{t("manageProfileEntryPinHelp")}</p>
        {loading && !data && <p className="text-sm text-muted-foreground">{t("loading")}</p>}
        {failed && (
          <>
            <ErrorLine message={t("consoleProfilesUnavailable")} />
            <Button variant="outline" size="sm" onClick={() => void refresh()}>
              {t("tryAgain")}
            </Button>
          </>
        )}
        {!failed && adults && adults.length > 0 && (
          <>
            <div className="space-y-1">
              <Label htmlFor="entry-pin-profile">{t("profile")}</Label>
              <select
                id="entry-pin-profile"
                data-setting="manage.profile.entryPin"
                className="block h-9 max-w-full rounded-md border border-input bg-background px-2 text-sm"
                value={selected?.id ?? ""}
                disabled={savingPin}
                onChange={(event) => setSelectedID(event.target.value)}
              >
                {!selected && (
                  <option value="" disabled>
                    {t("manageProfileChoose")}
                  </option>
                )}
                {adults.map((profile) => (
                  <option key={profile.id} value={profile.id}>
                    {profile.name}
                  </option>
                ))}
              </select>
            </div>
            {selected && (
              <EntryPinForm
                key={selected.id}
                profileID={selected.id}
                pinSet={selected.entryPinSet === true}
                busy={savingPin}
                setBusy={setSavingPin}
                onSaved={async () => {
                  await Promise.all([refresh(), refreshProfiles().catch(() => undefined)]);
                }}
              />
            )}
          </>
        )}
        {!loading && !failed && adults?.length === 0 && (
          <p className="text-sm text-muted-foreground">{t("manageProfileEntryPinUnavailable")}</p>
        )}
      </section>
      {current.loading && <p className="text-sm text-muted-foreground">{t("loading")}</p>}
      {current.failed && (
        <>
          <ErrorLine message={t("manageProfileSubtitleLanguagesUnavailable")} />
          <Button variant="outline" size="sm" onClick={() => void current.refresh()}>
            {t("tryAgain")}
          </Button>
        </>
      )}
      {!current.failed && current.data && current.data.profileID === session?.profileID && (
        <SubtitleLanguagesForm
          key={current.data.profileID}
          profileID={current.data.profileID}
          languages={current.data.subtitleLanguages ?? []}
        />
      )}
    </div>
  );
}

function useFormScope() {
  const scope = useRef<AbortController | null>(null);
  useLayoutEffect(() => {
    const controller = new AbortController();
    scope.current = controller;
    return () => controller.abort();
  }, []);
  return scope;
}

function EntryPinForm({
  profileID,
  pinSet,
  busy,
  setBusy,
  onSaved,
}: {
  profileID: string;
  pinSet: boolean;
  busy: boolean;
  setBusy: (busy: boolean) => void;
  onSaved: () => Promise<void>;
}) {
  const t = useT();
  const id = useId();
  const [pin, setPin] = useState("");
  const [confirmation, setConfirmation] = useState("");
  const [result, setResult] = useState<"saved" | "failed" | "mismatch" | null>(null);
  const scope = useFormScope();

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const signal = scope.current?.signal;
    if (busy || !signal || signal.aborted) return;
    const value = pin.trim();
    if (!value || value !== confirmation.trim()) {
      setResult("mismatch");
      return;
    }
    setPin("");
    setConfirmation("");
    setResult(null);
    setBusy(true);
    try {
      await setProfileEntryPin(profileID, value, signal);
      if (signal.aborted) return;
      await onSaved();
      if (!signal.aborted) setResult("saved");
    } catch {
      if (!signal.aborted) setResult("failed");
    } finally {
      setBusy(false);
    }
  }

  return (
    <form onSubmit={submit} className="space-y-2">
      <fieldset disabled={busy} className="flex flex-wrap items-end gap-3">
        <div className="space-y-1">
          <Label htmlFor={`${id}-pin`}>{t("restrictionsNewPin")}</Label>
          <Input
            id={`${id}-pin`}
            type="password"
            inputMode="numeric"
            autoComplete="new-password"
            value={pin}
            onChange={(event) => setPin(event.target.value)}
            required
            className="w-40"
          />
        </div>
        <div className="space-y-1">
          <Label htmlFor={`${id}-confirm`}>{t("restrictionsConfirmPin")}</Label>
          <Input
            id={`${id}-confirm`}
            type="password"
            inputMode="numeric"
            autoComplete="new-password"
            value={confirmation}
            onChange={(event) => setConfirmation(event.target.value)}
            required
            className="w-40"
          />
        </div>
        <Button type="submit" size="sm" disabled={!pin.trim() || !confirmation.trim()}>
          {t(pinSet ? "restrictionsChangePin" : "restrictionsSetPin")}
        </Button>
      </fieldset>
      {result === "saved" && <p role="status">{t("restrictionsPinSaved")}</p>}
      {result === "failed" && <ErrorLine message={t("restrictionsPinNotSaved")} />}
      {result === "mismatch" && <ErrorLine message={t("restrictionsPinMismatch")} />}
    </form>
  );
}

function SubtitleLanguagesForm({
  profileID,
  languages,
}: {
  profileID: string;
  languages: string[];
}) {
  const t = useT();
  const { refreshSession } = useSession();
  const [saved, setSaved] = useState(languages.join(", "));
  const [draft, setDraft] = useState(saved);
  const [result, setResult] = useState<"saved" | "failed" | null>(null);
  const [busy, setBusy] = useState(false);
  const scope = useFormScope();
  const preferences = draft
    .split(",")
    .map((tag) => tag.trim())
    .filter(Boolean);
  const changed = preferences.join(", ") !== saved;

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const signal = scope.current?.signal;
    if (busy || !changed || !signal || signal.aborted) return;
    setBusy(true);
    setResult(null);
    try {
      const next = (await setProfileSubtitleLanguages(profileID, preferences, signal)).join(", ");
      if (signal.aborted) return;
      setSaved(next);
      setDraft(next);
      setResult("saved");
      void refreshSession().catch(() => {});
    } catch {
      if (!signal.aborted) setResult("failed");
    } finally {
      if (!signal.aborted) setBusy(false);
    }
  }

  return (
    <form onSubmit={submit} className="space-y-2" data-setting="manage.profile.subtitleLanguages">
      <Label htmlFor="profile-subtitle-languages">{t("manageProfileSubtitleLanguages")}</Label>
      <p id="profile-subtitle-languages-help" className="text-sm text-muted-foreground">
        {t("manageProfileSubtitleLanguagesHelp")}
      </p>
      <div className="flex flex-wrap items-center gap-3">
        <Input
          id="profile-subtitle-languages"
          aria-describedby="profile-subtitle-languages-help"
          className="max-w-sm"
          value={draft}
          disabled={busy}
          onChange={(event) => {
            setDraft(event.target.value);
            setResult(null);
          }}
        />
        <Button type="submit" size="sm" disabled={busy || !changed}>
          {t("save")}
        </Button>
      </div>
      {result === "saved" && <p role="status">{t("manageProfileSubtitleLanguagesSaved")}</p>}
      {result === "failed" && <ErrorLine message={t("manageProfileSubtitleLanguagesFailed")} />}
    </form>
  );
}
