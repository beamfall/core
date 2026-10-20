import { useState } from "react";
import { EmptyLine, ErrorLine, ManageSection, StatusBadge } from "@/components/beamfall/workbench";
import { Button } from "@/components/ui/button";
import { useT } from "@/lib/i18n";
import { useSession } from "@/lib/session";

// Profile picker (ADR-0004, ported from index.html profile-view): switch the
// session's bound Profile. Managed Profiles carry a quiet hint (they have no
// reveal path — the hint explains its absence). The current binding is marked
// and never re-switched. Switching always relocks server-side; the session
// spine reloads everything on success.
export function ProfilePage() {
  const t = useT();
  const { profiles, session, switchProfile } = useSession();
  const [error, setError] = useState("");
  const [busyID, setBusyID] = useState("");

  const currentID = session?.profileID ?? "";

  const onSwitch = async (id: string) => {
    if (id === currentID) return;
    setError("");
    setBusyID(id);
    try {
      await switchProfile(id);
    } catch (err) {
      setError(err instanceof Error ? err.message : t("couldNotSwitchProfile"));
    } finally {
      setBusyID("");
    }
  };

  return (
    <ManageSection title={t("profilesTitle")} lead={t("profilesLead")}>
      {profiles.length === 0 ? (
        <EmptyLine message={t("profileUnavailable")} />
      ) : (
        <ul className="space-y-1.5">
          {profiles.map((profile) => {
            const current = profile.id === currentID;
            return (
              <li key={profile.id}>
                <Button
                  type="button"
                  variant={current ? "secondary" : "outline"}
                  size="sm"
                  className="w-full justify-start gap-2"
                  disabled={busyID === profile.id}
                  aria-current={current ? "true" : undefined}
                  onClick={() => onSwitch(profile.id)}
                >
                  <span className="font-semibold">{profile.name}</span>
                  {profile.kind === "managed" && (
                    <StatusBadge state="restricted">{t("managed")}</StatusBadge>
                  )}
                  {current && <span className="ml-auto text-xs text-[var(--accent)]">●</span>}
                </Button>
              </li>
            );
          })}
        </ul>
      )}
      <ErrorLine message={error} />
    </ManageSection>
  );
}
