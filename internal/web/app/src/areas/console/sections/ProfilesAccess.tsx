import { type FormEvent, useRef, useState } from "react";
import {
  EmptyState,
  ErrorState,
  HelpTip,
  PresetSelect,
  TimeOfDaySelect,
} from "@/components/beamfall/workbench";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
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
import { Advanced, useShowAdvanced } from "@/lib/advancedSettings";
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
  setProfileRemoteBitrateCap,
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
import { useBitratePresets } from "./RemoteAccessBitrate";

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

// Stream-cap choices besides "No limit" (0).
const STREAM_CAPS = [1, 2, 3, 4, 5];

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
          <p className="text-dense text-muted-foreground">{t("accessHelpManagedFraming")}</p>
          <div className="max-w-sm space-y-2" data-setting="console.schedules.profile">
            <Label>{t("accessScheduleProfileLabel")}</Label>
            <Select value={profileID} onValueChange={setProfileID}>
              <SelectTrigger aria-label={t("accessScheduleProfileLabel")}>
                <SelectValue placeholder={t("accessSchedulePickProfile")} />
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
              key={`policy:${selectedProfile.id}`}
              profileID={selectedProfile.id}
              managed={selectedProfile.kind === "managed"}
              remoteMaxBitrate={selectedProfile.remoteMaxBitrate ?? 0}
              onChanged={() => void filters.refresh()}
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
          <EmptyState message="enforcedFiltersPickProfile" />
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

export function LibraryGrantEditor({
  profileID,
  granted,
  libraries,
  failed,
  onChanged,
  noRestricted = false,
}: {
  profileID: string;
  granted: string[];
  libraries: LibraryInfo[];
  failed: boolean;
  onChanged: () => void;
  /** A Player Profile can never hold a restricted Library (ADR-0240). */
  noRestricted?: boolean;
}) {
  const t = useT();
  const { revealed } = useSession();
  const [selected, setSelected] = useState<string[]>(granted);
  const [busy, setBusy] = useState(false);
  const [saveFailed, setSaveFailed] = useState(false);

  function toggle(id: string, checked: boolean) {
    setSelected((current) =>
      checked ? [...new Set([...current, id])] : current.filter((v) => v !== id),
    );
  }

  async function save() {
    setBusy(true);
    setSaveFailed(false);
    try {
      await setProfileLibraries(profileID, selected);
      onChanged();
    } catch {
      setSaveFailed(true);
    } finally {
      setBusy(false);
    }
  }

  if (failed) return <ErrorState message="libraryAccessUnavailable" />;

  return (
    <div
      className="space-y-2 border-t border-[var(--border-subtle)] pt-4"
      data-setting="console.schedules.libraryAccess"
    >
      <div className="space-y-1">
        <Label className="text-xs">{t("libraryAccessHeading")}</Label>
        <p className="text-xs text-muted-foreground">{t("accessHelpLibraryAccessHint")}</p>
      </div>
      {libraries.map((library) => {
        const checked = selected.includes(library.id);
        // A Player Profile lists a restricted Library only while it still
        // holds it (granted before the Library became restricted), so the
        // owner can remove it; it can never be added.
        if (noRestricted && library.restricted && !checked && !granted.includes(library.id)) {
          return null;
        }
        const disabled = library.restricted && !noRestricted && !revealed;
        const addOnly = library.restricted && noRestricted && !checked;
        return (
          <label key={library.id} className="flex items-center gap-2 text-sm">
            <Checkbox
              checked={checked}
              disabled={disabled || addOnly}
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
      {saveFailed && (
        <p role="alert" className="m-0 text-sm text-[var(--state-danger)]">
          {t("libraryAccessSaveFailed")}
        </p>
      )}
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
    <div
      className="console-card console-card-pad flex items-center justify-between gap-4"
      data-setting="console.schedules.ratingLimit"
    >
      <span className="bf-label-with-tip">
        <span className="text-sm font-medium">
          {displayString(plugin.displayName, displayLocale)}
        </span>
        <HelpTip
          label={displayString(plugin.displayName, displayLocale)}
          text="accessHelpRatingLimitTip"
        />
      </span>
      <Select value={current || NO_CEILING} onValueChange={(v) => void onChange(v)}>
        <SelectTrigger
          className="w-48"
          aria-label={displayString(plugin.displayName, displayLocale)}
        >
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          <SelectItem value={NO_CEILING}>{t("accessHelpNoLimit")}</SelectItem>
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
  const { data, failed, mutate, refresh, replace } = useAsync(
    (signal) => fetchAccessSchedule(profileID, signal),
    [profileID],
  );
  const windows = data?.windows ?? [];
  // Editing waits for the Profile's schedule: an edit discards the load in
  // flight, so a draft begun before it lands would start from no windows and
  // a save would erase the stored ones.
  const ready = data !== null;
  // Which Profile is selected, how many times the selection changed, and how
  // many edits were made since. A save that resolves after the admin left and
  // came back has its response dropped, so it reloads (unless a new draft
  // began) rather than leave a fetch that raced the write on screen.
  const selection = useRef({ profileID, switches: 0, edits: 0 });
  if (selection.current.profileID !== profileID) {
    selection.current = { profileID, switches: selection.current.switches + 1, edits: 0 };
  }

  // Edits and the save response share useAsync's fence (CORE-0756): an edit
  // discards a save still in flight, and a Profile switch or an ended Admin
  // Session drops its response, so a late save for one Profile can never
  // repaint the windows shown (and later saved) for another.
  function update(next: AccessWindowInput[]) {
    if (!ready) return;
    selection.current.edits++;
    mutate({ profileID, windows: next });
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
    // The save is not aborted on a switch, only its response is ignored: the
    // write belongs to the Profile it was made for.
    if (!ready) return;
    const switches = selection.current.switches;
    await replace(() => setAccessSchedule(profileID, windows));
    const now = selection.current;
    if (now.profileID === profileID && now.switches !== switches && now.edits === 0) {
      void refresh();
    }
  }

  if (failed) return <ErrorState message="accessSchedulesUnavailable" />;

  return (
    <form onSubmit={save} className="space-y-3" data-setting="console.schedules.accessSchedule">
      <span className="bf-label-with-tip">
        <Label className="text-xs">{t("accessHelpScheduleLabel")}</Label>
        <HelpTip label={t("accessHelpScheduleLabel")} text="accessHelpScheduleTip" />
      </span>
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
                <SelectTrigger className="w-36" aria-label={t("accessScheduleWeekday")}>
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
              <Label htmlFor={`schedule-start-${i}`} className="text-xs">
                {t("accessHelpScheduleFrom")}
              </Label>
              <TimeOfDaySelect
                id={`schedule-start-${i}`}
                className="min-w-24"
                value={w.startMinute}
                onChange={(startMinute) => editWindow(i, { startMinute })}
              />
            </div>
            <div className="space-y-1">
              <Label htmlFor={`schedule-end-${i}`} className="text-xs">
                {t("accessHelpScheduleUntil")}
              </Label>
              <TimeOfDaySelect
                id={`schedule-end-${i}`}
                endOfDay
                className="min-w-24"
                value={w.endMinute}
                onChange={(endMinute) => editWindow(i, { endMinute })}
              />
            </div>
            <Button type="button" size="sm" variant="outline" onClick={() => removeWindow(i)}>
              {t("accessScheduleRemoveWindow")}
            </Button>
          </div>
        ))
      )}
      <div className="flex gap-2">
        <Button type="button" size="sm" variant="outline" onClick={addWindow} disabled={!ready}>
          {t("accessScheduleAddWindow")}
        </Button>
        <Button type="submit" size="sm" disabled={!ready}>
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
    <div className="space-y-1 border-t border-[var(--border-subtle)] pt-3 text-dense text-muted-foreground">
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
// cap and the remote bitrate cap (any Profile, CORE-1717) and the extension-request
// pre-authorization toggle (managed only — the server rejects an adult Profile).
// The remote cap reads back from the profiles query and shows the stored value
// (CORE-1853). The stream cap and pre-authorization are still write-through: the
// console does not read them back, so those inputs start blank and report
// "applied" on save.
function ManagedPolicyControls({
  profileID,
  managed,
  remoteMaxBitrate,
  onChanged,
}: {
  profileID: string;
  managed: boolean;
  remoteMaxBitrate: number;
  onChanged: () => void;
}) {
  const t = useT();
  const [cap, setCap] = useState("");
  const [capApplied, setCapApplied] = useState(false);
  const bitrate = useBitratePresets();
  const [storedRemoteCap, setStoredRemoteCap] = useState(remoteMaxBitrate);
  // A profiles reread (after an Apply, possibly from an earlier mount of this
  // control) replaces the stored cap; an unsaved draft is left alone.
  const [seenRemoteMaxBitrate, setSeenRemoteMaxBitrate] = useState(remoteMaxBitrate);
  if (remoteMaxBitrate !== seenRemoteMaxBitrate) {
    setSeenRemoteMaxBitrate(remoteMaxBitrate);
    setStoredRemoteCap(remoteMaxBitrate);
  }
  const [remoteCapApplied, setRemoteCapApplied] = useState(false);
  const [preauth, setPreauth] = useState(false);
  const [busy, setBusy] = useState(false);
  const showAdvanced = useShowAdvanced();

  // The stream cap is chosen from a short list and saved as soon as it changes.
  async function applyCap(value: string) {
    setCap(value);
    setBusy(true);
    setCapApplied(false);
    try {
      await setProfileStreamCap(profileID, Number(value) || 0);
      setCapApplied(true);
    } finally {
      setBusy(false);
    }
  }

  // The remote cap is stored in bits per second (CORE-1717) and chosen from
  // quality presets (CORE-2445); 0 clears it. It saves as soon as it changes.
  async function applyRemoteCap(bps: number) {
    setBusy(true);
    setRemoteCapApplied(false);
    try {
      const saved = await setProfileRemoteBitrateCap(profileID, bps);
      setStoredRemoteCap(saved.remoteMaxBitrate);
      setRemoteCapApplied(true);
      onChanged();
    } finally {
      setBusy(false);
    }
  }

  async function togglePreauth(value: boolean) {
    setPreauth(value);
    await setExtensionPreauthorization(profileID, value);
  }

  // The stream cap and extension pre-authorization are Advanced (CORE-1117). The
  // console reads neither back, so neither can be kept in view while set.
  if (!managed && !showAdvanced) return null;
  return (
    <div className="space-y-4 border-t border-[var(--border-subtle)] pt-4">
      <Advanced>
        <div className="flex flex-wrap items-end gap-2" data-setting="console.schedules.streamCap">
          <div className="space-y-1">
            <span className="bf-label-with-tip">
              <Label htmlFor="stream-cap" className="text-xs">
                {t("accessHelpStreamCapLabel")}
              </Label>
              <HelpTip label={t("accessHelpStreamCapLabel")} text="accessHelpStreamCapTip" />
            </span>
            <Select value={cap} onValueChange={(v) => void applyCap(v)} disabled={busy}>
              <SelectTrigger id="stream-cap" className="min-w-40">
                <SelectValue placeholder={t("accessHelpStreamCapPlaceholder")} />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="0">{t("accessHelpNoLimit")}</SelectItem>
                {STREAM_CAPS.map((count) => (
                  <SelectItem key={count} value={String(count)}>
                    {t("accessHelpStreamCount", { count })}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
          {capApplied && (
            <span className="text-xs text-[var(--text-secondary)]" role="status">
              {t("streamCapApplied")}
            </span>
          )}
          <p className="w-full text-xs text-muted-foreground">{t("streamCapHint")}</p>
        </div>

        <div
          className="flex flex-wrap items-end gap-2"
          data-setting="console.schedules.remoteBitrateLimit"
        >
          <div className="space-y-1">
            <span className="bf-label-with-tip">
              <Label htmlFor="remote-bitrate-cap" className="text-xs">
                {t("accessHelpRemoteCapLabel")}
              </Label>
              <HelpTip label={t("accessHelpRemoteCapLabel")} text="accessHelpProfileRemoteCapTip" />
            </span>
            <PresetSelect
              id="remote-bitrate-cap"
              value={storedRemoteCap}
              presets={bitrate.presets}
              format={bitrate.format}
              disabled={busy}
              onChange={(bps) => void applyRemoteCap(bps)}
            />
          </div>
          {remoteCapApplied && (
            <span className="text-xs text-[var(--text-secondary)]" role="status">
              {t("streamCapApplied")}
            </span>
          )}
        </div>

        {managed && (
          <div
            className="flex items-start justify-between gap-4"
            data-setting="console.schedules.extraTimeAutoApprove"
          >
            <div className="space-y-1">
              <span className="bf-label-with-tip">
                <Label htmlFor="extension-preauth">{t("accessHelpExtraTimeLabel")}</Label>
                <HelpTip label={t("accessHelpExtraTimeLabel")} text="accessHelpExtraTimeTip" />
              </span>
              <p className="text-xs text-muted-foreground">{t("extensionPreauthHint")}</p>
            </div>
            <Switch
              id="extension-preauth"
              checked={preauth}
              onCheckedChange={(v) => void togglePreauth(v)}
            />
          </div>
        )}
      </Advanced>

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
      <div className="border-t border-[var(--border-subtle)] pt-4">
        <p className="text-xs text-muted-foreground">{t("accessGrantsUnavailable")}</p>
      </div>
    );
  }

  return (
    <div className="border-t border-[var(--border-subtle)] pt-4 space-y-2">
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
            className="flex items-center justify-between gap-2 rounded border border-[var(--border-subtle)] px-3 py-2"
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
