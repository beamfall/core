import { useState } from "react";
import { useNavigate } from "react-router";
import { AppShell } from "@/components/chrome/AppShell";
import { Badge } from "@/components/ui/badge";
import { useT } from "@/lib/i18n";
import { useSession } from "@/lib/session";

// Profile picker (ADR-0004 profile switch), ported from the vanilla
// renderProfileCard / switchProfile (app.js). Each card shows the Profile name;
// managed Profiles carry a quiet "Managed" hint (they have no reveal path — the
// hint explains its absence); the session's current binding is marked and never
// re-switched (switching to yourself is a no-op that just returns to browse).
// Switching always relocks server-side and triggers a full session refresh, so
// per-Profile state re-renders as the new Profile.
export function ProfilePickerPage() {
  const t = useT();
  const navigate = useNavigate();
  const { profiles, session, switchProfile } = useSession();
  const [error, setError] = useState<string | null>(null);
  const [busyID, setBusyID] = useState<string | null>(null);
  const currentProfileID = session?.profileID || "";

  async function onSwitch(id: string) {
    setError(null);
    setBusyID(id);
    try {
      await switchProfile(id);
      navigate("/", { replace: true });
    } catch {
      setError(t("couldNotSwitchProfile"));
    } finally {
      setBusyID(null);
    }
  }

  return (
    <AppShell>
      <div className="mx-auto flex max-w-2xl flex-col gap-5">
        <div className="flex flex-col gap-2">
          <h1 className="bf-page-title">{t("profilesTitle")}</h1>
          <p className="bf-lead">{t("profilesLead")}</p>
        </div>
        {error && <p className="text-sm text-[var(--red-500)]">{error}</p>}
        {profiles.length === 0 ? (
          <p className="text-sm text-[var(--text-muted)]">{t("profileUnavailable")}</p>
        ) : (
          <ul className="grid grid-cols-2 gap-3 sm:grid-cols-3">
            {profiles.map((profile) => {
              const current = profile.id === currentProfileID;
              return (
                <li key={profile.id} className="list-none">
                  <button
                    type="button"
                    disabled={busyID === profile.id}
                    onClick={() => (current ? navigate("/") : onSwitch(profile.id))}
                    className={"bf-profile-card" + (current ? " is-current" : "")}
                  >
                    <span className="font-semibold text-[var(--text-primary)]">{profile.name}</span>
                    <span className="flex flex-wrap gap-2">
                      {profile.kind === "managed" && (
                        <Badge variant="secondary">{t("managed")}</Badge>
                      )}
                      {current && <Badge variant="outline">{t("myDataCurrent")}</Badge>}
                    </span>
                  </button>
                </li>
              );
            })}
          </ul>
        )}
      </div>
    </AppShell>
  );
}
