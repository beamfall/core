import { useState } from "react";
import { ProfileAvatar } from "@/components/beamfall/profile";
import { EmptyState, ErrorState } from "@/components/beamfall/workbench";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Label } from "@/components/ui/label";
import {
  disableProfile,
  fetchLibraries,
  type PlayerProfileAdminInfo,
  setPlayerControllers,
  setPlayerWalkUp,
} from "@/lib/graphql";
import type { LibraryInfo, ProfileAdminInfo } from "@/lib/graphql/types";
import { useT } from "@/lib/i18n";
import { getAdminEpochSignal } from "../adminEpoch";
import { ConfirmButton } from "../ConfirmButton";
import { type AsyncState, useAsync } from "../useAsync";
import { LibraryGrantEditor } from "./ProfilesAccess";

type HouseholdProfile = Pick<ProfileAdminInfo, "id" | "name" | "kind">;

// The household's Player Profiles (ADR-0240 D1): a TV or other shared display's
// own identity. Their kind is fixed, so no kind selector or entry PIN is
// offered. Each one edits its Library grants, who may control it (D4) and
// walk-up (D7), which needs the owner to confirm what it opens up.
export function PlayerProfiles({
  players,
  household,
  onChanged,
}: {
  players: AsyncState<PlayerProfileAdminInfo[]>;
  /** The household's Profiles; null until they have loaded. */
  household: HouseholdProfile[] | null;
  onChanged: () => void;
}) {
  const t = useT();
  const rows = players.data ?? [];
  const libraries = useAsync(fetchLibraries);
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
              key={`${p.id}:${p.walkUpEnabled ? 1 : 0}:${controllersKey(p)}:${p.libraryIDs.join(",")}`}
              profile={p}
              libraries={libraries}
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
  libraries,
  household,
  onChanged,
}: {
  profile: PlayerProfileAdminInfo;
  libraries: AsyncState<LibraryInfo[]>;
  household: HouseholdProfile[] | null;
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
      <LibraryGrantEditor
        profileID={profile.id}
        granted={profile.libraryIDs}
        libraries={libraries.data ?? []}
        failed={libraries.failed && libraries.data === null}
        onChanged={onChanged}
        noRestricted
      />
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
  household: HouseholdProfile[] | null;
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
  const loaded = household ?? [];
  const adults = loaded.filter((p) => p.kind === "adult");
  const includable = allAdults ? loaded.filter((p) => p.kind !== "adult") : loaded;

  function toggle(list: string[], id: string, checked: boolean): string[] {
    return checked ? [...list.filter((x) => x !== id), id] : list.filter((x) => x !== id);
  }

  async function save() {
    setBusy(true);
    setStatus("idle");
    // Send the lists as edited, not filtered against the household shown:
    // a stored rule for a Profile this list doesn't offer (its kind changed,
    // or it isn't listed) stays until the owner removes it. Exceptions only
    // mean something with "All adults" on, so turning that off clears them.
    try {
      await setPlayerControllers(
        profile.id,
        { allAdults, include, exclude: allAdults ? exclude : [] },
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
          onChange={(id, checked) => {
            setExclude((l) => toggle(l, id, checked));
            // A Profile can't be both added and excepted; the server refuses
            // the whole edit if it is, so the newer choice wins.
            if (checked) setInclude((l) => l.filter((x) => x !== id));
          }}
        />
      )}
      {includable.length > 0 && (
        <ProfileChecklist
          heading={t("consolePlayerControllersInclude")}
          idPrefix={`${profile.id}-include`}
          profiles={includable}
          selected={include}
          onChange={(id, checked) => {
            setInclude((l) => toggle(l, id, checked));
            if (checked) setExclude((l) => l.filter((x) => x !== id));
          }}
        />
      )}
      <div className="flex flex-wrap items-center gap-3">
        {/* Saving waits for the household: without it the checklists are
            empty and the owner can't see what the save keeps. */}
        <Button size="sm" disabled={busy || household === null} onClick={() => void save()}>
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
