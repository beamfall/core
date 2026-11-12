import { Link, useLocation } from "react-router";
import { useT } from "@/lib/i18n";
import { useSession } from "@/lib/session";
import { HeaderSearch } from "./HeaderSearch";

// The viewer's header action cluster (right side of the AppShell): the search
// field (the search page has its own, so it is left out there), quick links to
// the My Data / Privacy panel (ADR-0036), and the profile picker
// when more than one Profile exists (ADR-0004). Rendered into <AppShell
// actions={...}>; the Reveal "Revealed" chip and Manager links are owned by the
// AppShell itself.
export function ViewerActions() {
  const t = useT();
  const { profiles, session } = useSession();
  const { pathname } = useLocation();
  const currentProfile = profiles.find((p) => p.id === session?.profileID);
  const profileLabel = currentProfile
    ? t("profileActionLabel", { name: currentProfile.name })
    : t("profile");
  return (
    <>
      {pathname !== "/search" && <HeaderSearch />}
      <nav aria-label={t("navLabel")} className="flex items-center gap-3">
        <Link to="/mydata" className="bf-action-link">
          {t("myData")}
        </Link>
        <Link to="/settings" className="bf-action-link">
          {t("settingsTitle")}
        </Link>
        {profiles.length > 1 && (
          <Link to="/profiles" className="bf-action-link">
            {profileLabel}
          </Link>
        )}
        {profiles.length <= 1 && currentProfile && (
          <span className="text-sm text-[var(--text-secondary)]">{profileLabel}</span>
        )}
      </nav>
    </>
  );
}
