import { SlidersHorizontal } from "lucide-react";
import { useCallback, useEffect, useRef, useState } from "react";
import { MetadataAttributionText } from "@/components/beamfall/MetadataAttributionText";
import { AvatarChooser, PROFILE_AVATAR_PREF_KEY } from "@/components/beamfall/profile";
import { CollapsibleCard, IconHeading } from "@/components/beamfall/workbench";
import { AppShell } from "@/components/chrome/AppShell";
import { Label } from "@/components/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Switch } from "@/components/ui/switch";
import { Advanced, AdvancedSettingsSwitch } from "@/lib/advancedSettings";
import {
  fetchClientPrefs,
  fetchMetadataCredits,
  type MetadataAttribution,
  setClientPref,
} from "@/lib/graphql";
import { useT } from "@/lib/i18n";
import { useSession } from "@/lib/session";
import type { StringKey } from "@/strings";
import {
  DOWNSTREAM_AUDIO_DEFAULT,
  DOWNSTREAM_AUDIO_MODES,
  type DownstreamAudioMode,
  encodeBool,
  PLAYBACK_SPEEDS,
  type PlaybackSpeed,
  PRE_PLAY_EXTRAS,
  PREF_KEYS,
  type PrePlayExtras,
  resolvePreferences,
  SKIP_INTERVAL_DEFAULT,
  SKIP_INTERVALS,
  type SkipInterval,
  STILL_WATCHING_OFF,
  STILL_WATCHING_OPTIONS,
  type ViewerPreferences,
} from "../lib/preferences";

// Profile Settings (ADR-0082): the viewer-resident, self-service preference
// surface owned by the selected Profile. Every preference persists through the
// generic per-Profile clientPrefs seam under a canonical key (see
// lib/preferences.ts). Operator policy (stream caps, enforced filters, Library
// access, transcode policy) stays Console-owned and never appears here.
//
// Managed Profiles see only the harmless subset (ADR-0082 §37): playback
// auto-skip, default speed, "still watching", and reduced motion. Recommendations opt-in (which
// uses the Profile's activity) is adult-only here; My Data deletion, Reveal,
// and policy are intentionally absent for every Profile on this surface.
//
// Trim silence, skip interval and downstream audio are Advanced (CORE-1117): the
// header's switch shows them, and each stays visible while it is not at its
// default, so a setting in force is never out of sight.
export function ProfileSettingsPage() {
  const t = useT();
  const { session, profiles, refreshProfiles } = useSession();
  const profileID = session?.profileID || "";
  const profile = profiles.find((p) => p.id === profileID);
  const managed = session?.profile === "managed";

  const [prefs, setPrefs] = useState<ViewerPreferences | null>(null);
  const [failed, setFailed] = useState(false);
  const [saveError, setSaveError] = useState(false);
  const [avatarError, setAvatarError] = useState(false);
  const [credits, setCredits] = useState<MetadataAttribution[]>([]);
  const loadToken = useRef(0);

  const load = useCallback(async () => {
    const token = ++loadToken.current;
    const current = () => loadToken.current === token;
    setFailed(false);
    setPrefs(null);
    try {
      const next = resolvePreferences(await fetchClientPrefs());
      if (current()) setPrefs(next);
    } catch {
      if (current()) setFailed(true);
    }
  }, [profileID]);

  useEffect(() => {
    void load();
  }, [load]);

  // Credits are server-wide and optional: a failed read just omits the section.
  useEffect(() => {
    const controller = new AbortController();
    fetchMetadataCredits(controller.signal)
      .then(setCredits)
      .catch(() => undefined);
    return () => controller.abort();
  }, []);

  // Persist one key, optimistically reflecting it locally. A failed write
  // surfaces an error and reloads the canonical state so the UI never drifts
  // from the server.
  const persist = useCallback(
    async (key: string, value: string, next: Partial<ViewerPreferences>) => {
      setSaveError(false);
      setPrefs((p) => (p ? { ...p, ...next } : p));
      try {
        await setClientPref(key, value);
      } catch {
        setSaveError(true);
        await load();
      }
    },
    [load],
  );

  // The avatar is a per-Profile pref like the rest, but it is shown from the
  // Profile list (picker, header), so a save refreshes that list. Only the
  // write decides whether the save worked: a refresh that fails afterwards
  // leaves the choice saved, and the list catches up on its next load.
  const saveAvatar = useCallback(
    async (token: string) => {
      setAvatarError(false);
      try {
        await setClientPref(PROFILE_AVATAR_PREF_KEY, token);
      } catch (err) {
        setAvatarError(true);
        throw err;
      }
      await refreshProfiles().catch(() => undefined);
    },
    [refreshProfiles],
  );

  if (failed) {
    return (
      <AppShell>
        <p className="bf-lead">{t("settingsUnavailable")}</p>
      </AppShell>
    );
  }
  if (!prefs) {
    return (
      <AppShell>
        <p className="bf-lead">…</p>
      </AppShell>
    );
  }

  return (
    <AppShell>
      <div className="mx-auto flex max-w-2xl flex-col gap-4">
        <header className="flex flex-wrap items-start justify-between gap-x-6">
          <IconHeading
            icon={SlidersHorizontal}
            title={t("settingsTitle")}
            lead={t("settingsLead")}
          />
          <AdvancedSettingsSwitch className="mb-5 sm:mt-2" />
        </header>

        {saveError && (
          <p className="text-sm text-[var(--state-danger)]" role="alert">
            {t("settingsSaveFailed")}
          </p>
        )}

        {profile && (
          <section aria-label={t("avatarHeading")}>
            <CollapsibleCard title={t("avatarHeading")} lead={t("avatarLead")}>
              {avatarError && (
                <p className="m-0 text-sm text-[var(--state-danger)]" role="alert">
                  {t("avatarSaveFailed")}
                </p>
              )}
              <AvatarChooser profile={profile} onSave={saveAvatar} />
            </CollapsibleCard>
          </section>
        )}

        <section aria-label={t("settingsPlaybackHeading")}>
          <CollapsibleCard title={t("settingsPlaybackHeading")} defaultOpen>
            <div className="settings-row flex flex-col gap-3">
              <div className="flex flex-col gap-1">
                <span className="font-medium">{t("settingsAutoSkipHeading")}</span>
                <span className="text-sm text-[var(--text-muted)]">
                  {t("settingsAutoSkipLead")}
                </span>
              </div>
              <ToggleRow
                labelKey="settingsAutoSkipIntro"
                checked={prefs.autoSkipIntro}
                onChange={(on) =>
                  persist(PREF_KEYS.autoSkipIntro, encodeBool(on), {
                    autoSkipIntro: on,
                  })
                }
              />
              <ToggleRow
                labelKey="settingsAutoSkipRecap"
                checked={prefs.autoSkipRecap}
                onChange={(on) =>
                  persist(PREF_KEYS.autoSkipRecap, encodeBool(on), {
                    autoSkipRecap: on,
                  })
                }
              />
              <ToggleRow
                labelKey="settingsAutoSkipCredits"
                checked={prefs.autoSkipCredits}
                onChange={(on) =>
                  persist(PREF_KEYS.autoSkipCredits, encodeBool(on), {
                    autoSkipCredits: on,
                  })
                }
              />
            </div>

            <div className="settings-row flex items-center justify-between gap-4">
              <Label htmlFor="settings-speed">{t("settingsPlaybackSpeedLabel")}</Label>
              <Select
                value={prefs.playbackSpeed}
                onValueChange={(v) =>
                  persist(PREF_KEYS.playbackSpeedDefault, v, {
                    playbackSpeed: v as PlaybackSpeed,
                  })
                }
              >
                <SelectTrigger id="settings-speed" className="w-28">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {PLAYBACK_SPEEDS.map((s) => (
                    <SelectItem key={s} value={s}>
                      {s}×
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>

            <div className="settings-row flex items-center justify-between gap-4">
              <div className="flex flex-col gap-1">
                <Label htmlFor="settings-still-watching">{t("settingsStillWatchingLabel")}</Label>
                <span className="text-sm text-[var(--text-muted)]">
                  {t("settingsStillWatchingLead")}
                </span>
              </div>
              <Select
                value={stillWatchingValue(prefs.stillWatchingAfter)}
                onValueChange={(v) =>
                  persist(PREF_KEYS.stillWatchingAfter, v, {
                    stillWatchingAfter: v === STILL_WATCHING_OFF ? 0 : Number(v),
                  })
                }
              >
                <SelectTrigger id="settings-still-watching" className="w-36">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {stillWatchingOptions(prefs.stillWatchingAfter).map((o) => (
                    <SelectItem key={o} value={o}>
                      {o === STILL_WATCHING_OFF
                        ? t("settingsStillWatchingNever")
                        : t("settingsStillWatchingAfter", { count: Number(o) })}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
            <div className="settings-row flex flex-col gap-3">
              <div className="flex flex-col gap-1">
                <ToggleRow
                  labelKey="settingsPrePlay"
                  checked={prefs.prePlayEnabled}
                  onChange={(on) =>
                    persist(PREF_KEYS.prePlayEnabled, encodeBool(on), {
                      prePlayEnabled: on,
                    })
                  }
                />
                <span className="text-sm text-[var(--text-muted)]">{t("settingsPrePlayLead")}</span>
              </div>
              {prefs.prePlayEnabled ? (
                <div className="flex items-center justify-between gap-4">
                  <Label htmlFor="settings-preplay-extras">{t("settingsPrePlayExtrasLabel")}</Label>
                  <Select
                    value={prefs.prePlayExtras}
                    onValueChange={(v) =>
                      persist(PREF_KEYS.prePlayExtras, v, {
                        prePlayExtras: v as PrePlayExtras,
                      })
                    }
                  >
                    <SelectTrigger id="settings-preplay-extras" className="w-28">
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      {PRE_PLAY_EXTRAS.map((n) => (
                        <SelectItem key={n} value={n}>
                          {n}
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                </div>
              ) : null}
            </div>

            <Advanced keep={prefs.trimSilence}>
              <div className="settings-row flex flex-col gap-1">
                <ToggleRow
                  labelKey="settingsTrimSilence"
                  checked={prefs.trimSilence}
                  onChange={(on) =>
                    persist(PREF_KEYS.trimSilence, encodeBool(on), {
                      trimSilence: on,
                    })
                  }
                />
                <span className="text-sm text-[var(--text-muted)]">
                  {t("settingsTrimSilenceLead")}
                </span>
              </div>
            </Advanced>

            <Advanced keep={prefs.skipInterval !== SKIP_INTERVAL_DEFAULT}>
              <div className="settings-row flex items-center justify-between gap-4">
                <Label htmlFor="settings-skip-interval">{t("settingsSkipIntervalLabel")}</Label>
                <Select
                  value={prefs.skipInterval}
                  onValueChange={(v) =>
                    persist(PREF_KEYS.skipInterval, v, {
                      skipInterval: v as SkipInterval,
                    })
                  }
                >
                  <SelectTrigger id="settings-skip-interval" className="w-28">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {SKIP_INTERVALS.map((s) => (
                      <SelectItem key={s} value={s}>
                        {t("secondsShort", { seconds: s })}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </div>
            </Advanced>

            <Advanced keep={prefs.downstreamAudio !== DOWNSTREAM_AUDIO_DEFAULT}>
              <div className="settings-row flex items-center justify-between gap-4">
                <div className="flex flex-col gap-1">
                  <Label htmlFor="settings-downstream-audio">
                    {t("settingsDownstreamAudioLabel")}
                  </Label>
                  <span className="text-sm text-[var(--text-muted)]">
                    {t("settingsDownstreamAudioLead")}
                  </span>
                </div>
                <Select
                  value={prefs.downstreamAudio}
                  onValueChange={(v) =>
                    persist(PREF_KEYS.downstreamAudio, v, {
                      downstreamAudio: v as DownstreamAudioMode,
                    })
                  }
                >
                  <SelectTrigger id="settings-downstream-audio" className="w-32">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {DOWNSTREAM_AUDIO_MODES.map((mode) => (
                      <SelectItem key={mode} value={mode}>
                        {t(
                          mode === "surround"
                            ? "settingsDownstreamAudioSurround"
                            : "settingsDownstreamAudioStereo",
                        )}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </div>
            </Advanced>
          </CollapsibleCard>
        </section>

        <section aria-label={t("settingsAccessibilityHeading")}>
          <CollapsibleCard title={t("settingsAccessibilityHeading")} defaultOpen>
            <div className="settings-row flex flex-col gap-1">
              <ToggleRow
                labelKey="settingsReduceMotion"
                checked={prefs.reduceMotion}
                onChange={(on) =>
                  persist(PREF_KEYS.reduceMotion, encodeBool(on), {
                    reduceMotion: on,
                  })
                }
              />
              <span className="text-sm text-[var(--text-muted)]">
                {t("settingsReduceMotionLead")}
              </span>
            </div>
          </CollapsibleCard>
        </section>

        {/* Recommendations opt-in uses the Profile's activity — adult-only on
            this self-service surface (managed-Profile recommendation policy is
            operator-owned, ADR-0082 §37). */}
        {!managed && (
          <section aria-label={t("settingsRecommendHeading")}>
            <CollapsibleCard title={t("settingsRecommendHeading")} defaultOpen>
              <div className="settings-row flex flex-col gap-1">
                <ToggleRow
                  labelKey="settingsRecommendOptIn"
                  checked={prefs.recommendOptIn}
                  onChange={(on) =>
                    persist(PREF_KEYS.recommendOptIn, encodeBool(on), {
                      recommendOptIn: on,
                    })
                  }
                />
                <span className="text-sm text-[var(--text-muted)]">
                  {t("settingsRecommendOptInLead")}
                </span>
              </div>
            </CollapsibleCard>
          </section>
        )}

        {credits.length > 0 && (
          <section aria-label={t("settingsCreditsHeading")}>
            <CollapsibleCard title={t("settingsCreditsHeading")} lead={t("settingsCreditsLead")}>
              <div className="settings-row flex flex-col gap-2">
                <ul className="m-0 flex list-none flex-col gap-1 p-0 text-sm">
                  {credits.map((credit) => (
                    <li key={`${JSON.stringify(credit.text)}|${credit.url}`}>
                      <MetadataAttributionText
                        attribution={credit}
                        className="text-[var(--text-primary)] underline underline-offset-2"
                      />
                    </li>
                  ))}
                </ul>
              </div>
            </CollapsibleCard>
          </section>
        )}

        {managed && <p className="text-sm text-[var(--text-muted)]">{t("settingsManagedNote")}</p>}
      </div>
    </AppShell>
  );
}

function ToggleRow({
  labelKey,
  checked,
  onChange,
}: {
  labelKey: StringKey;
  checked: boolean;
  onChange: (on: boolean) => void;
}) {
  const t = useT();
  const id = `settings-${labelKey}`;
  return (
    <div className="flex items-center justify-between gap-4">
      <Label htmlFor={id}>{t(labelKey)}</Label>
      <Switch id={id} checked={checked} onCheckedChange={onChange} />
    </div>
  );
}

function stillWatchingValue(after: number): string {
  return after > 0 ? String(after) : STILL_WATCHING_OFF;
}

// The offered choices, plus a value another client stored outside them (6..10).
function stillWatchingOptions(after: number): string[] {
  const options: string[] = [...STILL_WATCHING_OPTIONS];
  const current = stillWatchingValue(after);
  return options.includes(current) ? options : [...options, current];
}
