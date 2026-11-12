import { Database, LayoutDashboard, Settings, Users, Wrench } from "lucide-react";
import { useEffect, useId, useRef, useState } from "react";
import { Link, useLocation } from "react-router";
import { SignOutButton } from "@/areas/enroll";
import { useT } from "@/lib/i18n";
import { useSession } from "@/lib/session";

// The avatar button and its account menu (CORE-1131): the profile, My Data,
// Profile settings, the manager surfaces and Sign out, out of the header bar.
// A disclosure, not an ARIA menu: plain links in tab order, closed by Escape,
// an outside click or navigation.
export function AccountMenu() {
  const t = useT();
  const location = useLocation();
  const { profiles, session, isManager } = useSession();
  const [open, setOpen] = useState(false);
  const panelId = useId();
  const rootRef = useRef<HTMLDivElement>(null);
  const buttonRef = useRef<HTMLButtonElement>(null);

  const currentProfile = profiles.find((p) => p.id === session?.profileID);
  const name = currentProfile?.name ?? t("profile");
  const initial = (currentProfile?.name.trim()[0] ?? "?").toUpperCase();

  // Navigating away closes the menu.
  useEffect(() => {
    setOpen(false);
  }, [location.pathname]);

  useEffect(() => {
    if (!open) return;
    function onPointer(event: PointerEvent) {
      if (!rootRef.current?.contains(event.target as Node)) setOpen(false);
    }
    function onKey(event: KeyboardEvent) {
      if (event.key !== "Escape") return;
      setOpen(false);
      buttonRef.current?.focus();
    }
    document.addEventListener("pointerdown", onPointer);
    document.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("pointerdown", onPointer);
      document.removeEventListener("keydown", onKey);
    };
  }, [open]);

  const current = (path: string) => (location.pathname === path ? "page" : undefined);

  return (
    <div ref={rootRef} className="bf-account">
      <button
        ref={buttonRef}
        type="button"
        className="bf-avatar-button"
        aria-expanded={open}
        aria-controls={panelId}
        aria-label={t("accountMenu", { name })}
        onClick={() => setOpen((value) => !value)}
      >
        <span aria-hidden className="bf-avatar">
          {initial}
        </span>
      </button>
      {open && (
        <div id={panelId} className="bf-account-panel">
          <div className="bf-account-head">
            <span aria-hidden className="bf-avatar bf-avatar-lg">
              {initial}
            </span>
            <div className="min-w-0">
              <div className="bf-account-name">{name}</div>
              {isManager && <div className="bf-account-role">{t("managerRole")}</div>}
            </div>
          </div>
          <div>
            {profiles.length > 1 && (
              <Link to="/profiles" className="bf-account-item" aria-current={current("/profiles")}>
                <Users aria-hidden />
                {t("switchProfile")}
              </Link>
            )}
            <Link to="/mydata" className="bf-account-item" aria-current={current("/mydata")}>
              <Database aria-hidden />
              {t("myData")}
            </Link>
            <Link to="/settings" className="bf-account-item" aria-current={current("/settings")}>
              <Settings aria-hidden />
              {t("settingsTitle")}
            </Link>
            {isManager && (
              <>
                <hr className="bf-account-rule" />
                <Link to="/console" className="bf-account-item">
                  <LayoutDashboard aria-hidden />
                  {t("consoleHeading")}
                </Link>
                <Link to="/manage" className="bf-account-item">
                  <Wrench aria-hidden />
                  {t("manage")}
                </Link>
              </>
            )}
          </div>
          {session && (
            <>
              <hr className="bf-account-rule" />
              <SignOutButton className="bf-account-signout w-full" />
            </>
          )}
        </div>
      )}
    </div>
  );
}
