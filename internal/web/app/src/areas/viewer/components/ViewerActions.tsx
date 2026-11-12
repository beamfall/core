import { Link, useLocation } from "react-router";
import { ProfileAvatar } from "@/components/beamfall/profile";
import { useT } from "@/lib/i18n";
import { useSession } from "@/lib/session";
import { HeaderSearch } from "./HeaderSearch";

// The viewer's header action cluster (right side of the AppShell): the search
// field (the search page has its own, so it is left out there), quick links to
// the My Data / Privacy panel (ADR-0036), and the current Profile's avatar,
// which is the one-click way back to "Who's watching?" when more than one
// Profile exists (ADR-0004, CORE-1196). Rendered into <AppShell
// actions={...}>; the Reveal "Revealed" chip and Manager links are owned by the
// AppShell itself.
export function ViewerActions() {
  const t = useT();
  const { profiles, session } = useSession();
  const { pathname } = useLocation();
  const currentProfile = profiles.find((p) => p.id === session?.profileID);
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
        {currentProfile && profiles.length > 1 && (
          <Link
            to="/profiles"
            className="bf-profile-link"
            aria-label={t("whosWatchingHeaderAria", { name: currentProfile.name })}
          >
            <ProfileAvatar profile={currentProfile} size="sm" />
            <span className="bf-profile-link-name">{currentProfile.name}</span>
          </Link>
        )}
        {currentProfile && profiles.length <= 1 && (
          <span className="bf-profile-link" title={currentProfile.name}>
            <ProfileAvatar profile={currentProfile} size="sm" />
            <span className="bf-profile-link-name">{currentProfile.name}</span>
          </span>
        )}
      </nav>
    </>
  );
}
