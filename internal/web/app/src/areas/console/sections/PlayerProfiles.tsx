import { useState } from "react";
import { ProfileAvatar } from "@/components/beamfall/profile";
import { EmptyState, ErrorState } from "@/components/beamfall/workbench";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Label } from "@/components/ui/label";
import {
  disableProfile,
  type PlayerProfileAdminInfo,
  setPlayerControllers,
  setPlayerWalkUp,
} from "@/lib/graphql";
import type { ProfileAdminInfo } from "@/lib/graphql/types";
import { useT } from "@/lib/i18n";
import { getAdminEpochSignal } from "../adminEpoch";
import { ConfirmButton } from "../ConfirmButton";
import type { AsyncState } from "../useAsync";

type HouseholdProfile = Pick<ProfileAdminInfo, "id" | "name" | "kind">;

// The household's Player Profiles (ADR-0240 D1): a TV or other shared display's
// own identity. Their kind is fixed, so no kind selector or entry PIN is
// offered. Each one edits who may control it (D4) and walk-up (D7), which
// needs the owner to confirm what it opens up.
export function PlayerProfiles({
  players,
  household,
  onChanged,
}: {
  players: AsyncState<PlayerProfileAdminInfo[]>;
  household: HouseholdProfile[];
  onChanged: () => void;
}) {
  const t = useT();
  const rows = players.data ?? [];
  return (
    <section className="space-y-2" aria-labelledby="player-profiles-heading">
      <h3 id="player-profiles-heading" className="m-0 text-base font-semibold">
        {t("consolePlayerProfilesHeading")}
      </h3>
      <p className="m-0 text-sm text-muted-foreground">{t("consolePlayerProfilesLead")}</p>
      {players.failed && players.data === null ? (
        <div className="space-y-2">
          <ErrorState message="consoleProfilesUnavailable" />
          <Button variant="outline" size="sm" onClick={() => void players.refresh()}>
            {t("tryAgain")}
          </Button>
        </div>
      ) : rows.length === 0 ? (
        !players.loading && <EmptyState message="consolePlayerProfilesEmpty" />
      ) : (
        <ul className="console-card divide-y divide-border">
          {rows.map((p) => (
            <PlayerProfileRow
              key={`${p.id}:${p.walkUpEnabled ? 1 : 0}:${controllersKey(p)}`}
              profile={p}
              household={household}
              onChanged={onChanged}
            />
          ))}
        </ul>
      )}
    </section>
  );
}

function controllersKey(p: PlayerProfileAdminInfo): string {
  const c = p.playerControllers;
  if (!c) return "";
  return `${c.allAdults}|${c.include.join(",")}|${c.exclude.join(",")}`;
}

function PlayerProfileRow({
  profile,
  household,
  onChanged,
}: {
  profile: PlayerProfileAdminInfo;
  household: HouseholdProfile[];
  onChanged: () => void;
}) {
  const t = useT();
  return (
    <li className="console-card-pad space-y-4" data-player-profile-id={profile.id}>
      <div className="flex flex-wrap items-center gap-3">
        <ProfileAvatar profile={profile} size="md" />
        <div className="min-w-0 flex-1">
          <p className="m-0 font-medium">{profile.name}</p>
          <p className="m-0 text-sm text-muted-foreground">{t("consoleProfilesKindPlayer")}</p>
        </div>
        <ConfirmButton
          label={t("consoleProfilesDisable")}
          confirm="consoleProfilesDisableConfirm"
          variant="outline"
          size="sm"
          onConfirm={async () => {
            await disableProfile(profile.id);
            onChanged();
          }}
        >
          {t("consoleProfilesDisableEffect")}
        </ConfirmButton>
      </div>
      <WalkUp profile={profile} onChanged={onChanged} />
      <Controllers profile={profile} household={household} onChanged={onChanged} />
    </li>
  );
}

function WalkUp({
  profile,
  onChanged,
}: {
  profile: PlayerProfileAdminInfo;
  onChanged: () => void;
}) {
  const t = useT();
  const [failed, setFailed] = useState(false);
  const on = profile.walkUpEnabled === true;

  async function turnOff() {
    setFailed(false);
    try {
      await setPlayerWalkUp(profile.id, false, getAdminEpochSignal());
      onChanged();
    } catch {
      setFailed(true);
    }
  }

  return (
    <div className="flex flex-wrap items-center gap-3">
      <p className="m-0 flex-1 text-sm">
        {on ? t("consolePlayerWalkUpOn") : t("consolePlayerWalkUpOff")}
      </p>
      {on ? (
        <Button variant="outline" size="sm" onClick={() => void turnOff()}>
          {t("consolePlayerWalkUpDisable")}
        </Button>
      ) : (
        <ConfirmButton
          label={t("consolePlayerWalkUpEnable")}
          confirm="consolePlayerWalkUpConfirm"
          variant="outline"
          size="sm"
          onConfirm={async () => {
            await setPlayerWalkUp(profile.id, true, getAdminEpochSignal());
            onChanged();
          }}
        >
          {t("consolePlayerWalkUpWarning", { name: profile.name })}
        </ConfirmButton>
      )}
      {failed && (
        <p role="alert" className="m-0 w-full text-sm text-[var(--state-danger)]">
          {t("consolePlayerWalkUpFailed")}
        </p>
      )}
    </div>
  );
}

function Controllers({
  profile,
  household,
  onChanged,
}: {
  profile: PlayerProfileAdminInfo;
  household: HouseholdProfile[];
  onChanged: () => void;
}) {
  const t = useT();
  const initial = profile.playerControllers;
  const [allAdults, setAllAdults] = useState(initial?.allAdults ?? true);
  const [include, setInclude] = useState<string[]>(initial?.include ?? []);
  const [exclude, setExclude] = useState<string[]>(initial?.exclude ?? []);
  const [status, setStatus] = useState<"idle" | "saved" | "failed">("idle");
  const [busy, setBusy] = useState(false);

  // With "All adults" on, adults can be excepted and other Profiles added;
  // with it off, any Profile can be added by name.
  const adults = household.filter((p) => p.kind === "adult");
  const includable = allAdults ? household.filter((p) => p.kind !== "adult") : household;

  function toggle(list: string[], id: string, checked: boolean): string[] {
    return checked ? [...list.filter((x) => x !== id), id] : list.filter((x) => x !== id);
  }

  async function save() {
    setBusy(true);
    setStatus("idle");
    const includableIDs = new Set(includable.map((p) => p.id));
    const adultIDs = new Set(adults.map((p) => p.id));
    try {
      await setPlayerControllers(
        profile.id,
        {
          allAdults,
          include: include.filter((id) => includableIDs.has(id)),
          exclude: allAdults ? exclude.filter((id) => adultIDs.has(id)) : [],
        },
        getAdminEpochSignal(),
      );
      setStatus("saved");
      onChanged();
    } catch {
      setStatus("failed");
    } finally {
      setBusy(false);
    }
  }

  return (
    <fieldset className="m-0 space-y-3 border-0 p-0">
      <legend className="mb-2 text-sm font-semibold">{t("consolePlayerControllersHeading")}</legend>
      <ControllerOption
        id={`${profile.id}-all-adults`}
        label={t("consolePlayerControllersAllAdults")}
        checked={allAdults}
        onChange={setAllAdults}
      />
      {allAdults && adults.length > 0 && (
        <ProfileChecklist
          heading={t("consolePlayerControllersExclude")}
          idPrefix={`${profile.id}-exclude`}
          profiles={adults}
          selected={exclude}
          onChange={(id, checked) => setExclude((l) => toggle(l, id, checked))}
        />
      )}
      {includable.length > 0 && (
        <ProfileChecklist
          heading={t("consolePlayerControllersInclude")}
          idPrefix={`${profile.id}-include`}
          profiles={includable}
          selected={include}
          onChange={(id, checked) => setInclude((l) => toggle(l, id, checked))}
        />
      )}
      <div className="flex flex-wrap items-center gap-3">
        <Button size="sm" disabled={busy} onClick={() => void save()}>
          {t("consolePlayerControllersSave")}
        </Button>
        {status === "saved" && (
          <p role="status" className="m-0 text-sm">
            {t("consolePlayerControllersSaved")}
          </p>
        )}
        {status === "failed" && (
          <p role="alert" className="m-0 text-sm text-[var(--state-danger)]">
            {t("consolePlayerControllersFailed")}
          </p>
        )}
      </div>
    </fieldset>
  );
}

function ProfileChecklist({
  heading,
  idPrefix,
  profiles,
  selected,
  onChange,
}: {
  heading: string;
  idPrefix: string;
  profiles: HouseholdProfile[];
  selected: string[];
  onChange: (id: string, checked: boolean) => void;
}) {
  return (
    <div className="space-y-1.5 ps-6">
      <p className="m-0 text-sm text-muted-foreground">{heading}</p>
      {profiles.map((p) => (
        <ControllerOption
          key={p.id}
          id={`${idPrefix}-${p.id}`}
          label={p.name}
          checked={selected.includes(p.id)}
          onChange={(checked) => onChange(p.id, checked)}
        />
      ))}
    </div>
  );
}

function ControllerOption({
  id,
  label,
  checked,
  onChange,
}: {
  id: string;
  label: string;
  checked: boolean;
  onChange: (checked: boolean) => void;
}) {
  return (
    <div className="flex items-center gap-2">
      <Checkbox id={id} checked={checked} onCheckedChange={(v) => onChange(v === true)} />
      <Label htmlFor={id} className="font-normal">
        {label}
      </Label>
    </div>
  );
}
