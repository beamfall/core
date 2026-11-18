import { Search } from "lucide-react";
import { type KeyboardEvent, useEffect, useId, useMemo, useState } from "react";
import { Link, useLocation, useNavigate } from "react-router";
import { Input } from "@/components/ui/input";
import { setAdvancedSettings } from "@/lib/advancedSettings";
import { isBeamfallAppStoreHost } from "@/lib/beamfallHost";
import { useT } from "@/lib/i18n";
import { useSession } from "@/lib/session";
import { type SettingsArea, type SettingsSearchResult, searchSettings } from "@/lib/settingsIndex";

// The top of the Console and Manage menus (CORE-2445): what this area is for,
// a link to the other one, and a search over both areas' pages and settings.
// A setting result opens its page with `revealSetting` in the route state, and
// RevealSetting on that page scrolls to it.

type RevealState = { revealSetting?: string; revealAdvanced?: boolean; revealOpener?: string };

const OTHER_AREA: Record<
  SettingsArea,
  { to: string; link: "consoleMenuCrossLink" | "manageMenuCrossLink" }
> = {
  console: { to: "/manage", link: "consoleMenuCrossLink" },
  manage: { to: "/console", link: "manageMenuCrossLink" },
};

function resultTarget(result: SettingsSearchResult): { to: string; state?: RevealState } {
  const to = result.path ? `/${result.area}/${result.path}` : `/${result.area}`;
  if (!result.setting) return { to };
  return {
    to,
    state: {
      revealSetting: result.setting,
      revealAdvanced: result.advanced,
      revealOpener: result.opener,
    },
  };
}

export function SettingsMenuIntro({ area }: { area: SettingsArea }) {
  const t = useT();
  const navigate = useNavigate();
  const { isManager } = useSession();
  const [query, setQuery] = useState("");
  const listID = useId();

  const results = useMemo(() => {
    const all = searchSettings(query, t, { appStoreHost: isBeamfallAppStoreHost() });
    // Manage needs manager rights; a Console admin without them is sent home.
    return isManager ? all : all.filter((r) => r.area !== "manage");
  }, [query, t, isManager]);

  const other = OTHER_AREA[area];
  const searching = query.trim() !== "";

  const openFirst = () => {
    const first = results[0];
    if (!first) return;
    const target = resultTarget(first);
    setQuery("");
    navigate(target.to, { state: target.state });
  };

  const onInputKeyDown = (event: KeyboardEvent<HTMLInputElement>) => {
    if (event.key === "Enter") {
      event.preventDefault();
      openFirst();
    } else if (event.key === "Escape" && searching) {
      event.stopPropagation();
      setQuery("");
    }
  };

  return (
    <div className="settings-menu-intro">
      <p className="settings-menu-purpose">
        {t(area === "console" ? "consoleMenuPurpose" : "manageMenuPurpose")}
      </p>
      <Link to={other.to} className="settings-menu-crosslink">
        {t(other.link)}
      </Link>
      <div role="search" className="settings-search">
        <Search aria-hidden className="settings-search-icon" />
        <Input
          type="search"
          value={query}
          onChange={(event) => setQuery(event.target.value)}
          onKeyDown={onInputKeyDown}
          aria-label={t("settingsSearchLabel")}
          aria-controls={searching ? listID : undefined}
          placeholder={t("settingsSearchPlaceholder")}
          className="settings-search-input"
        />
        {searching &&
          (results.length > 0 ? (
            <ul
              id={listID}
              className="settings-search-results"
              aria-label={t("settingsSearchLabel")}
            >
              {results.map((result) => {
                const target = resultTarget(result);
                return (
                  <li key={result.key}>
                    <Link
                      to={target.to}
                      state={target.state}
                      className="settings-search-result"
                      onClick={() => setQuery("")}
                    >
                      <span className="settings-search-result-label">{result.label}</span>
                      <span className="settings-search-result-where">
                        {result.area !== area && (
                          <span className="settings-search-result-area">
                            {t(
                              result.area === "console"
                                ? "settingsSearchConsole"
                                : "settingsSearchManage",
                            )}
                          </span>
                        )}
                        {result.page}
                      </span>
                    </Link>
                  </li>
                );
              })}
            </ul>
          ) : (
            <p id={listID} className="settings-search-empty">
              {t("settingsSearchEmpty")}
            </p>
          ))}
        {/* Always mounted, so a screen reader hears each change in the count
            while focus stays in the search box. */}
        <p className="sr-only" role="status">
          {searching &&
            (results.length > 0
              ? t("settingsSearchCount", { count: results.length })
              : t("settingsSearchEmpty"))}
        </p>
      </div>
    </div>
  );
}

// Brings the setting a search opened into view: turns Advanced settings on if
// the setting needs it (or if it has not appeared after a second, in case the
// index is out of date), opens any folded <details> around it, scrolls to it,
// gives it a brief ring and focuses its control. A setting that only appears
// after an action (a form's Add button, a picked profile, an existing row) has
// an opener: once the page has had a second to load, the opener is shown
// instead. Render it inside the page area, below any admin lock, so it starts
// once the page can show.
const REVEAL_POLL_MS = 100;
const REVEAL_ADVANCED_AFTER = 10;
const REVEAL_OPENER_AFTER = 10;
const REVEAL_GIVE_UP_AFTER = 50;

export function RevealSetting() {
  const location = useLocation();
  const navigate = useNavigate();
  const state = location.state as RevealState | null;
  const id = state?.revealSetting;
  const advanced = state?.revealAdvanced === true;
  const opener = state?.revealOpener;
  const { pathname, search, hash } = location;

  useEffect(() => {
    if (!id) return;
    if (advanced) setAdvancedSettings(true);
    let advancedOn = advanced;
    let tries = 0;
    let timer: ReturnType<typeof setTimeout> | undefined;
    // Clear the state so going back or reloading does not reveal it again.
    const done = () => navigate({ pathname, search, hash }, { replace: true, state: null });

    const find = (anchor: string) =>
      document.querySelector<HTMLElement>(`[data-setting="${CSS.escape(anchor)}"]`);
    const tick = () => {
      const el = find(id);
      if (el) {
        revealElement(el);
        done();
        return;
      }
      tries += 1;
      const openerEl = opener && tries >= REVEAL_OPENER_AFTER ? find(opener) : null;
      if (openerEl) {
        revealElement(openerEl);
        done();
        return;
      }
      if (tries === REVEAL_ADVANCED_AFTER && !advancedOn && !opener) {
        setAdvancedSettings(true);
        advancedOn = true;
      }
      if (tries < REVEAL_GIVE_UP_AFTER) timer = setTimeout(tick, REVEAL_POLL_MS);
      else done();
    };
    tick();
    return () => clearTimeout(timer);
  }, [id, advanced, opener, pathname, search, hash, navigate]);

  return null;
}

function revealElement(el: HTMLElement) {
  for (let node = el.parentElement; node; node = node.parentElement) {
    if (node instanceof HTMLDetailsElement) node.open = true;
  }
  const reduceMotion = window.matchMedia?.("(prefers-reduced-motion: reduce)").matches ?? false;
  el.scrollIntoView?.({ block: "center", behavior: reduceMotion ? "auto" : "smooth" });
  el.classList.remove("bf-setting-found");
  // Restart the ring animation when the same setting is opened twice.
  void el.offsetWidth;
  el.classList.add("bf-setting-found");
  el.addEventListener("animationend", () => el.classList.remove("bf-setting-found"), {
    once: true,
  });
  // Skip the (?) help button that sits beside a label: focus the setting itself,
  // or the anchor when it is the control (an opener's Add button).
  const focusable =
    "input:not([type=hidden]), select, textarea, button:not([data-help-tip]), [role=combobox], [role=switch], [tabindex]:not([tabindex='-1']):not([data-help-tip])";
  const control = el.matches(focusable) ? el : el.querySelector<HTMLElement>(focusable);
  control?.focus({ preventScroll: true });
}
