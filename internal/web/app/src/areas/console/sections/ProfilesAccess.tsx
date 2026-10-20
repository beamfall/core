import { type FormEvent, useState } from "react";
import { EmptyState, ErrorState } from "@/components/beamfall/workbench";
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
import { Switch } from "@/components/ui/switch";
import { displayString } from "@/lib/display";
import {
  accessGrantsForProfile,
  fetchAccessSchedule,
  fetchEnforcedFilters,
  fetchLibraries,
  revokeAccessGrant,
  setAccessSchedule,
  setCertificationCeiling,
  setExtensionPreauthorization,
  setProfileLibraries,
  setProfileStreamCap,
} from "@/lib/graphql";
import type {
  AccessGrantInfo,
  AccessWindowInput,
  EnforcedFilterPlugin,
  LibraryInfo,
} from "@/lib/graphql/types";
import { useDisplayLocale, useT } from "@/lib/i18n";
import { useSession } from "@/lib/session";
import type { StringKey } from "@/strings";
import { ConfirmButton } from "../ConfirmButton";
import { formatMinute, nextTransition } from "../lib/schedulePreview";
import { Section } from "../Section";
import { useAsync } from "../useAsync";

const WEEKDAYS: StringKey[] = [
  "weekdaySunday",
  "weekdayMonday",
  "weekdayTuesday",
  "weekdayWednesday",
  "weekdayThursday",
  "weekdayFriday",
  "weekdaySaturday",
];

const NO_CEILING = "__none__";

// Access schedules + enforced certification filters share a profile picker, so
// this screen loads the enforced-filters payload (profiles + plugins-with-scale)
// once and drives both editors off the selected profile.
export function ProfilesAccess() {
  const t = useT();
  const filters = useAsync(fetchEnforcedFilters);
  const libraries = useAsync(fetchLibraries);
  const profiles = filters.data?.profiles ?? [];
  const plugins = (filters.data?.plugins ?? []).filter(
    (p) => (p.certificationScale?.length ?? 0) > 0,
  );
  const [profileID, setProfileID] = useState("");

  const selectedProfile = profiles.find((p) => p.id === profileID);

  if (filters.failed) {
    return (
      <Section heading="enforcedFiltersHeading" lead="enforcedFiltersLead">
        <ErrorState message="enforcedFiltersUnavailable" />
      </Section>
    );
  }

  return (
    <div className="space-y-8">
      <Section heading="accessSchedulesHeading" lead="accessSchedulesLead">
        <div className="console-card console-card-pad space-y-4">
          {/* Supervisor-authored policy, distinct from the managed Profile's own
              Mute (ADR-0087 §29). */}
          <p className="text-[13px] text-muted-foreground">{t("managedPolicyFraming")}</p>
          <div className="max-w-sm space-y-2">
            <Label>{t("accessScheduleProfileLabel")}</Label>
            <Select value={profileID} onValueChange={setProfileID}>
              <SelectTrigger>
                <SelectValue placeholder={t("accessScheduleProfileLabel")} />
              </SelectTrigger>
              <SelectContent>
                {profiles.map((p) => (
                  <SelectItem key={p.id} value={p.id}>
                    {p.name}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
          {profileID && <AccessScheduleEditor profileID={profileID} />}
          {selectedProfile && (
            <ManagedPolicyControls
              profileID={selectedProfile.id}
              managed={selectedProfile.kind === "managed"}
            />
          )}
          {selectedProfile && (
            <LibraryGrantEditor
              key={selectedProfile.id}
              profileID={selectedProfile.id}
              granted={selectedProfile.libraryIDs ?? []}
              libraries={libraries.data ?? []}
              failed={libraries.failed}
              onChanged={() => void filters.refresh()}
            />
          )}
        </div>
      </Section>

      <Separator />

      <Section heading="enforcedFiltersHeading" lead="enforcedFiltersLead">
        {plugins.length === 0 ? (
          <EmptyState message="enforcedFiltersEmpty" />
        ) : !selectedProfile ? (
          <EmptyState message="enforcedFiltersProfileLabel" />
        ) : (
          <div className="space-y-3">
            {plugins.map((plugin) => (
              <CeilingRow
                key={plugin.id}
                plugin={plugin}
                profileID={selectedProfile.id}
                current={
                  selectedProfile.certificationCeilings.find((c) => c.pluginID === plugin.id)
                    ?.ceiling ?? ""
                }
                onChanged={() => void filters.refresh()}
              />
            ))}
          </div>
        )}
      </Section>
    </div>
  );
}

function LibraryGrantEditor({
  profileID,
  granted,
  libraries,
  failed,
  onChanged,
}: {
  profileID: string;
  granted: string[];
  libraries: LibraryInfo[];
  failed: boolean;
  onChanged: () => void;
}) {
  const t = useT();
  const { revealed } = useSession();
  const [selected, setSelected] = useState<string[]>(granted);
  const [busy, setBusy] = useState(false);

  function toggle(id: string, checked: boolean) {
    setSelected((current) =>
      checked ? [...new Set([...current, id])] : current.filter((v) => v !== id),
    );
  }

  async function save() {
    setBusy(true);
    try {
      await setProfileLibraries(profileID, selected);
      onChanged();
    } finally {
      setBusy(false);
    }
  }

  if (failed) return <ErrorState message="libraryAccessUnavailable" />;

  return (
    <div className="space-y-2 border-t border-[var(--border)] pt-4">
      <div className="space-y-1">
        <Label className="text-xs">{t("libraryAccessHeading")}</Label>
        <p className="text-xs text-muted-foreground">{t("libraryAccessHint")}</p>
      </div>
      {libraries.map((library) => {
        const disabled = library.restricted && !revealed;
        return (
          <label key={library.id} className="flex items-center gap-2 text-sm">
            <Checkbox
              checked={selected.includes(library.id)}
              disabled={disabled}
              onCheckedChange={(value) => toggle(library.id, value === true)}
            />
            <span>{library.title}</span>
            {library.restricted && (
              <span className="text-xs text-muted-foreground">
                {t(disabled ? "libraryAccessRevealRequired" : "libraryAccessRestricted")}
              </span>
            )}
          </label>
        );
      })}
      <Button size="sm" disabled={busy} onClick={() => void save()}>
        {t("libraryAccessSave")}
      </Button>
    </div>
  );
}

// One per-plugin certification ceiling row: a Select over the plugin's ordered
// scale (plus a "no ceiling" option), writing through setCertificationCeiling.
function CeilingRow({
  plugin,
  profileID,
  current,
  onChanged,
}: {
  plugin: EnforcedFilterPlugin;
  profileID: string;
  current: string;
  onChanged: () => void;
}) {
  const t = useT();
  const displayLocale = useDisplayLocale();
  const scale = plugin.certificationScale ?? [];

  async function onChange(value: string) {
    await setCertificationCeiling(profileID, plugin.id, value === NO_CEILING ? "" : value);
    onChanged();
  }

  return (
    <div className="console-card console-card-pad flex items-center justify-between gap-4">
      <span className="text-sm font-medium">
        {displayString(plugin.displayName, displayLocale)}
      </span>
      <Select value={current || NO_CEILING} onValueChange={(v) => void onChange(v)}>
        <SelectTrigger className="w-48">
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          <SelectItem value={NO_CEILING}>{t("enforcedFilterNoCeiling")}</SelectItem>
          {scale.map((step) => (
            <SelectItem key={step} value={step}>
              {step}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
    </div>
  );
}

// The weekly access-window editor for one managed Profile. An empty schedule
// means "always allowed"; windows are weekday + start/end minute-of-day.
function AccessScheduleEditor({ profileID }: { profileID: string }) {
  const t = useT();
  const { data, failed, setData } = useAsync(
    (signal) => fetchAccessSchedule(profileID, signal),
    [profileID],
  );
  const windows = data?.windows ?? [];

  function update(next: AccessWindowInput[]) {
    setData({ profileID, windows: next });
  }

  function addWindow() {
    update([...windows, { weekday: 0, startMinute: 0, endMinute: 1440 }]);
  }

  function removeWindow(i: number) {
    update(windows.filter((_, idx) => idx !== i));
  }

  function editWindow(i: number, patch: Partial<AccessWindowInput>) {
    update(windows.map((w, idx) => (idx === i ? { ...w, ...patch } : w)));
  }

  async function save(e: FormEvent) {
    e.preventDefault();
    const saved = await setAccessSchedule(profileID, windows);
    setData(saved);
  }

  if (failed) return <ErrorState message="accessSchedulesUnavailable" />;

  return (
    <form onSubmit={save} className="space-y-3">
      {windows.length === 0 ? (
        <EmptyState message="accessScheduleAlwaysAllowed" />
      ) : (
        windows.map((w, i) => (
          <div key={i} className="flex flex-wrap items-end gap-2">
            <div className="space-y-1">
              <Label className="text-xs">{t("accessScheduleWeekday")}</Label>
              <Select
                value={String(w.weekday)}
                onValueChange={(v) => editWindow(i, { weekday: Number(v) })}
              >
                <SelectTrigger className="w-36">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {WEEKDAYS.map((day, idx) => (
                    <SelectItem key={day} value={String(idx)}>
                      {t(day)}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
            <div className="space-y-1">
              <Label className="text-xs">{t("accessScheduleStart")}</Label>
              <Input
                type="number"
                min={0}
                max={1440}
                className="w-24"
                value={w.startMinute}
                onChange={(e) => editWindow(i, { startMinute: Number(e.target.value) })}
              />
            </div>
            <div className="space-y-1">
              <Label className="text-xs">{t("accessScheduleEnd")}</Label>
              <Input
                type="number"
                min={0}
                max={1440}
                className="w-24"
                value={w.endMinute}
                onChange={(e) => editWindow(i, { endMinute: Number(e.target.value) })}
              />
            </div>
            <Button type="button" size="sm" variant="outline" onClick={() => removeWindow(i)}>
              {t("accessScheduleRemoveWindow")}
            </Button>
          </div>
        ))
      )}
      <div className="flex gap-2">
        <Button type="button" size="sm" variant="outline" onClick={addWindow}>
          {t("accessScheduleAddWindow")}
        </Button>
        <Button type="submit" size="sm">
          {t("accessScheduleSave")}
        </Button>
      </div>
      <SchedulePreview windows={windows} />
    </form>
  );
}

// The next allow / next block preview (ADR-0087 §30), computed client-side from
// the windows. The boundary is FRAMED as server-local time — the server stores
// no timezone, so this does not claim "blocked right now" (which would be wrong
// when the Console user's timezone differs from the server's); it shows the next
// boundary and labels it server-local.
function SchedulePreview({ windows }: { windows: AccessWindowInput[] }) {
  const t = useT();
  if (windows.length === 0) return null;
  const now = new Date();
  const next = nextTransition(windows, now.getDay(), now.getHours() * 60 + now.getMinutes());
  return (
    <div className="space-y-1 border-t border-[var(--border)] pt-3 text-[13px] text-muted-foreground">
      <p>{t("accessScheduleServerLocalNote")}</p>
      {next ? (
        <p>
          {t(next.allowed ? "accessScheduleNextAllow" : "accessScheduleNextBlock", {
            day: t(WEEKDAYS[next.weekday]),
            time: formatMinute(next.minute),
          })}
        </p>
      ) : (
        <p>{t("accessScheduleConstant")}</p>
      )}
    </div>
  );
}

// The managed-Profile policy controls (ADR-0087 §30/§31): the concurrent-stream
// cap (any Profile) and the extension-request pre-authorization toggle (managed
// only — the server rejects an adult Profile). Both are write-through: the
// server exposes no read query for the current values yet (the inputs start
// blank and report "applied" on save), so the controls set policy rather than
// reflect it.
function ManagedPolicyControls({ profileID, managed }: { profileID: string; managed: boolean }) {
  const t = useT();
  const [cap, setCap] = useState("");
  const [capApplied, setCapApplied] = useState(false);
  const [preauth, setPreauth] = useState(false);
  const [busy, setBusy] = useState(false);

  async function applyCap(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setCapApplied(false);
    try {
      await setProfileStreamCap(profileID, Number(cap) || 0);
      setCapApplied(true);
    } finally {
      setBusy(false);
    }
  }

  async function togglePreauth(value: boolean) {
    setPreauth(value);
    await setExtensionPreauthorization(profileID, value);
  }

  return (
    <div className="space-y-4 border-t border-[var(--border)] pt-4">
      <form onSubmit={applyCap} className="flex flex-wrap items-end gap-2">
        <div className="space-y-1">
          <Label htmlFor="stream-cap" className="text-xs">
            {t("streamCapLabel")}
          </Label>
          <Input
            id="stream-cap"
            type="number"
            min={0}
            className="w-24"
            value={cap}
            placeholder="0"
            onChange={(e) => {
              setCap(e.target.value);
              setCapApplied(false);
            }}
          />
        </div>
        <Button type="submit" size="sm" disabled={busy}>
          {t("streamCapApply")}
        </Button>
        {capApplied && (
          <span className="text-xs text-[var(--text-secondary)]" role="status">
            {t("streamCapApplied")}
          </span>
        )}
        <p className="w-full text-xs text-muted-foreground">{t("streamCapHint")}</p>
      </form>

      {managed && (
        <div className="flex items-start justify-between gap-4">
          <div className="space-y-1">
            <Label htmlFor="extension-preauth">{t("extensionPreauthLabel")}</Label>
            <p className="text-xs text-muted-foreground">{t("extensionPreauthHint")}</p>
          </div>
          <Switch
            id="extension-preauth"
            checked={preauth}
            onCheckedChange={(v) => void togglePreauth(v)}
          />
        </div>
      )}

      {managed && <AccessGrantList profileID={profileID} />}
    </div>
  );
}

// AccessGrantList shows the active per-title access grants for a managed Profile
// (ACCESS-3, ADR-0113 §2.3) and lets an adult revoke individual grants.
function AccessGrantList({ profileID }: { profileID: string }) {
  const t = useT();
  const { data, failed, refresh } = useAsync(
    (signal) => accessGrantsForProfile(profileID, signal),
    [profileID],
  );
  const [revoking, setRevoking] = useState<string | null>(null);

  async function revoke(grant: AccessGrantInfo) {
    setRevoking(grant.mediaID);
    try {
      await revokeAccessGrant(profileID, grant.mediaID);
      await refresh();
    } finally {
      setRevoking(null);
    }
  }

  if (failed) {
    return (
      <div className="border-t border-[var(--border)] pt-4">
        <p className="text-xs text-muted-foreground">{t("accessGrantsUnavailable")}</p>
      </div>
    );
  }

  return (
    <div className="border-t border-[var(--border)] pt-4 space-y-2">
      <div className="space-y-1">
        <Label className="text-xs">{t("accessGrantsHeading")}</Label>
        <p className="text-xs text-muted-foreground">{t("accessGrantsHint")}</p>
      </div>
      {data && data.length === 0 && (
        <p className="text-xs text-muted-foreground">{t("accessGrantsEmpty")}</p>
      )}
      {data &&
        data.map((grant) => (
          <div
            key={grant.mediaID}
            className="flex items-center justify-between gap-2 rounded border border-[var(--border)] px-3 py-2"
          >
            <span className="truncate font-mono text-xs">{grant.mediaID}</span>
            <ConfirmButton
              size="sm"
              variant="outline"
              disabled={revoking === grant.mediaID}
              confirm="accessGrantRevokeConfirm"
              label={revoking === grant.mediaID ? t("accessGrantRevoking") : t("accessGrantRevoke")}
              onConfirm={() => revoke(grant)}
            >
              {t("accessGrantRevokeEffect")}
            </ConfirmButton>
          </div>
        ))}
    </div>
  );
}
