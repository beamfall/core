import { useEffect, useState } from "react";
import {
  ControlRow,
  EmptyState,
  ErrorLine,
  ErrorState,
  Fieldset,
  ManageField,
} from "@/components/beamfall/workbench";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Skeleton } from "@/components/ui/skeleton";
import { Switch } from "@/components/ui/switch";
import { Textarea } from "@/components/ui/textarea";
import { fetchJellyfinCompat, setJellyfinCompat, setProfileIntegrationSignIn } from "@/lib/graphql";
import type { JellyfinCompatInfo, JellyfinCompatUpdate } from "@/lib/graphql/types";
import { useT } from "@/lib/i18n";
import { Section } from "../Section";
import { useAsync } from "../useAsync";

// The Jellyfin-compatible API settings (ADR-0171, CORE-2683): the API that
// automation tools (Seerr, Radarr, Sonarr) read. It is not a playback surface.
// The page sits behind the App Store firewall (settingsIndex
// APP_STORE_FIREWALL_PATHS): an App Store host neither lists nor routes it,
// and an App Store server binary does not register the graph fields.

function cidrText(cidrs: string[]): string {
  return cidrs.join("\n");
}

function cidrList(text: string): string[] {
  return text
    .split(/[\s,]+/)
    .map((c) => c.trim())
    .filter(Boolean);
}

function errorReason(err: unknown): string {
  return err instanceof Error ? err.message : "";
}

export function JellyfinCompat() {
  const t = useT();
  const { data, loading, failed, setData } = useAsync(fetchJellyfinCompat);
  const [allowed, setAllowed] = useState("");
  const [proxies, setProxies] = useState("");
  const [formLoaded, setFormLoaded] = useState(false);
  // One save at a time: each write sends or merges a snapshot of the settings,
  // so a second write started before the first returns could undo it.
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [saved, setSaved] = useState(false);

  // Fill the network fields once, from the first load; later updates (a switch,
  // a library tick) must not overwrite networks the operator is still editing.
  useEffect(() => {
    if (!data || formLoaded) return;
    setAllowed(cidrText(data.allowedCIDRs));
    setProxies(cidrText(data.trustedProxyCIDRs));
    setFormLoaded(true);
  }, [data, formLoaded]);

  async function apply(update: JellyfinCompatUpdate): Promise<JellyfinCompatInfo | null> {
    setBusy(true);
    setError("");
    setSaved(false);
    try {
      const next = await setJellyfinCompat(update);
      setData(next);
      return next;
    } catch (err) {
      setError(t("jellyfinCompatSaveFailed", { reason: errorReason(err) }));
      return null;
    } finally {
      setBusy(false);
    }
  }

  async function saveNetworks() {
    const next = await apply({
      allowedCIDRs: cidrList(allowed),
      trustedProxyCIDRs: cidrList(proxies),
    });
    if (!next) return;
    // Show the server's normalized form.
    setAllowed(cidrText(next.allowedCIDRs));
    setProxies(cidrText(next.trustedProxyCIDRs));
    setSaved(true);
  }

  function toggleLibrary(id: string, on: boolean) {
    if (!data) return;
    const libraryIDs = data.libraries
      .filter((l) => (l.id === id ? on : l.allowed))
      .map((l) => l.id);
    void apply({ libraryIDs });
  }

  async function toggleProfile(id: string, enabled: boolean) {
    if (!data) return;
    setBusy(true);
    setError("");
    setSaved(false);
    try {
      const profile = await setProfileIntegrationSignIn(id, enabled);
      setData({
        ...data,
        profiles: data.profiles.map((p) => (p.id === profile.id ? profile : p)),
      });
    } catch (err) {
      setError(t("jellyfinCompatSaveFailed", { reason: errorReason(err) }));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Section heading="navJellyfinCompat" lead="jellyfinCompatLead">
      <p className="max-w-[68ch] text-sm text-muted-foreground">{t("jellyfinCompatSetup")}</p>
      <p
        className="max-w-[68ch] text-sm text-muted-foreground"
        data-testid="jellyfin-compat-disclaimer"
      >
        {t("jellyfinCompatDisclaimer")}
      </p>
      {loading ? (
        <Skeleton className="h-32 w-full" />
      ) : failed || !data ? (
        <ErrorState message="jellyfinCompatUnavailable" />
      ) : (
        <div className="space-y-4">
          <div className="console-card console-card-pad">
            <ControlRow
              label="jellyfinCompatEnabled"
              help="jellyfinCompatEnabledHelp"
              setting="console.jellyfinCompat.enabled"
              control={
                <Switch
                  checked={data.enabled}
                  disabled={busy}
                  onCheckedChange={(c) => void apply({ enabled: c })}
                  aria-label={t("jellyfinCompatEnabled")}
                />
              }
            />
          </div>

          <Fieldset title="jellyfinCompatNetworksHeading">
            <ManageField
              id="jellyfin-compat-allowed"
              label={t("jellyfinCompatAllowedCIDRs")}
              setting="console.jellyfinCompat.allowedCIDRs"
            >
              <Textarea
                id="jellyfin-compat-allowed"
                value={allowed}
                className="font-mono"
                autoComplete="off"
                spellCheck={false}
                aria-describedby="jellyfin-compat-allowed-help"
                onChange={(e) => setAllowed(e.target.value)}
              />
              <p id="jellyfin-compat-allowed-help" className="console-control-help">
                {t("jellyfinCompatAllowedCIDRsHelp")}
              </p>
            </ManageField>
            <ManageField
              id="jellyfin-compat-proxies"
              label={t("jellyfinCompatProxyCIDRs")}
              setting="console.jellyfinCompat.trustedProxyCIDRs"
            >
              <Textarea
                id="jellyfin-compat-proxies"
                value={proxies}
                className="font-mono"
                autoComplete="off"
                spellCheck={false}
                aria-describedby="jellyfin-compat-proxies-help"
                onChange={(e) => setProxies(e.target.value)}
              />
              <p id="jellyfin-compat-proxies-help" className="console-control-help">
                {t("jellyfinCompatProxyCIDRsHelp")}
              </p>
            </ManageField>
            <div className="flex items-center gap-3">
              <Button type="button" disabled={busy} onClick={() => void saveNetworks()}>
                {t("jellyfinCompatSaveNetworks")}
              </Button>
              {saved && (
                <span role="status" className="text-sm text-muted-foreground">
                  {t("jellyfinCompatSaved")}
                </span>
              )}
            </div>
          </Fieldset>

          <Fieldset
            title="jellyfinCompatLibrariesHeading"
            help="jellyfinCompatLibrariesHelp"
            setting="console.jellyfinCompat.libraries"
          >
            {data.libraries.length === 0 ? (
              <EmptyState message="jellyfinCompatLibrariesEmpty" />
            ) : (
              data.libraries.map((library) => (
                <label key={library.id} className="flex items-center gap-2 text-sm">
                  <Checkbox
                    checked={library.allowed}
                    disabled={busy}
                    onCheckedChange={(c) => toggleLibrary(library.id, c === true)}
                  />
                  {library.title}
                </label>
              ))
            )}
          </Fieldset>

          <Fieldset
            title="jellyfinCompatProfilesHeading"
            help="jellyfinCompatProfilesHelp"
            setting="console.jellyfinCompat.profiles"
          >
            {data.profiles.length === 0 ? (
              <EmptyState message="jellyfinCompatProfilesEmpty" />
            ) : (
              data.profiles.map((profile) => (
                <div key={profile.id} className="console-control-row">
                  <span className="console-control-label min-w-0">{profile.name}</span>
                  <Switch
                    checked={profile.integrationSignIn}
                    disabled={busy}
                    onCheckedChange={(c) => void toggleProfile(profile.id, c)}
                    aria-label={profile.name}
                  />
                </div>
              ))
            )}
          </Fieldset>

          <ErrorLine message={error} />
        </div>
      )}
    </Section>
  );
}
