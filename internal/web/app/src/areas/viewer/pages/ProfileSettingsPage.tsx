import { useCallback, useEffect, useRef, useState } from "react";
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
import { fetchClientPrefs, setClientPref } from "@/lib/graphql";
import { useT } from "@/lib/i18n";
import { useSession } from "@/lib/session";
import type { StringKey } from "@/strings";
import {
  DOWNSTREAM_AUDIO_MODES,
  type DownstreamAudioMode,
  encodeBool,
  PLAYBACK_SPEEDS,
  type PlaybackSpeed,
  PREF_KEYS,
  resolvePreferences,
  SKIP_INTERVALS,
  type SkipInterval,
  type ViewerPreferences,
} from "../lib/preferences";

// Profile Settings (ADR-0082): the viewer-resident, self-service preference
// surface owned by the selected Profile. Every preference persists through the
// generic per-Profile clientPrefs seam under a canonical key (see
// lib/preferences.ts). Operator policy (stream caps, enforced filters, Library
// access, transcode policy) stays Console-owned and never appears here.
//
// Managed Profiles see only the harmless subset (ADR-0082 §37): playback
// auto-skip, default speed, and reduced motion. Recommendations opt-in (which
// uses the Profile's activity) is adult-only here; My Data deletion, Reveal,
// and policy are intentionally absent for every Profile on this surface.
export function ProfileSettingsPage() {
  const t = useT();
  const { session } = useSession();
  const profileID = session?.profileID || "";
  const managed = session?.profile === "managed";

  const [prefs, setPrefs] = useState<ViewerPreferences | null>(null);
  const [failed, setFailed] = useState(false);
  const [saveError, setSaveError] = useState(false);
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
      <div className="mx-auto flex max-w-2xl flex-col gap-8">
        <header className="flex flex-col gap-2">
          <h1 className="bf-page-title">{t("settingsTitle")}</h1>
          <p className="bf-lead">{t("settingsLead")}</p>
        </header>

        {saveError && (
          <p className="text-sm text-[var(--red-500)]" role="alert">
            {t("settingsSaveFailed")}
          </p>
        )}

        <section className="flex flex-col gap-4" aria-labelledby="settings-playback">
          <h2 id="settings-playback" className="bf-section-title">
            {t("settingsPlaybackHeading")}
          </h2>

          <div className="flex flex-col gap-3 rounded-lg border border-[var(--border)] p-4">
            <div className="flex flex-col gap-1">
              <span className="font-medium">{t("settingsAutoSkipHeading")}</span>
              <span className="text-sm text-[var(--text-muted)]">{t("settingsAutoSkipLead")}</span>
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

          <div className="flex items-center justify-between gap-4 rounded-lg border border-[var(--border)] p-4">
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

          <div className="flex flex-col gap-1 rounded-lg border border-[var(--border)] p-4">
            <ToggleRow
              labelKey="settingsTrimSilence"
              checked={prefs.trimSilence}
              onChange={(on) =>
                persist(PREF_KEYS.trimSilence, encodeBool(on), {
                  trimSilence: on,
                })
              }
            />
            <span className="text-sm text-[var(--text-muted)]">{t("settingsTrimSilenceLead")}</span>
          </div>

          <div className="flex items-center justify-between gap-4 rounded-lg border border-[var(--border)] p-4">
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

          <div className="flex items-center justify-between gap-4 rounded-lg border border-[var(--border)] p-4">
            <div className="flex flex-col gap-1">
              <Label htmlFor="settings-downstream-audio">{t("settingsDownstreamAudioLabel")}</Label>
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
        </section>

        <section className="flex flex-col gap-4" aria-labelledby="settings-a11y">
          <h2 id="settings-a11y" className="bf-section-title">
            {t("settingsAccessibilityHeading")}
          </h2>
          <div className="flex flex-col gap-1 rounded-lg border border-[var(--border)] p-4">
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
        </section>

        {/* Recommendations opt-in uses the Profile's activity — adult-only on
            this self-service surface (managed-Profile recommendation policy is
            operator-owned, ADR-0082 §37). */}
        {!managed && (
          <section className="flex flex-col gap-4" aria-labelledby="settings-recommend">
            <h2 id="settings-recommend" className="bf-section-title">
              {t("settingsRecommendHeading")}
            </h2>
            <div className="flex flex-col gap-1 rounded-lg border border-[var(--border)] p-4">
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
