import {
  Blocks,
  Check,
  Clapperboard,
  Film,
  FolderOpen,
  House,
  KeyRound,
  LoaderCircle,
  LockKeyhole,
  type LucideIcon,
  Music,
  Podcast,
  ScanSearch,
  ShieldCheck,
  Sparkles,
  Tv,
  UserRound,
} from "lucide-react";
import { type FormEvent, useEffect, useId, useRef, useState } from "react";
import beamfallLogo from "@/assets/beamfall-logo.png";
import { CinemaBackdrop } from "@/components/beamfall/CinemaBackdrop";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import { ApiError, type SignInSecurityMode } from "@/lib/api";
import { displayString } from "@/lib/display";
import { browseServerFolders, fetchJobs, fetchPlugins, setScraperCredential } from "@/lib/graphql";
import type { FolderEntryInfo, JobInfo, PluginSummary } from "@/lib/graphql/types";
import { useI18n } from "@/lib/i18n";
import { jobProgressDetail, jobStatusLabel } from "@/lib/jobDisplay";
import { keyNeeded, type ProviderKey } from "@/lib/providerKeys";
import { cn } from "@/lib/utils";
import type { StringKey } from "@/strings";
import { FolderPicker } from "./FolderPicker";
import {
  activatePlugins,
  bootstrapResume,
  bootstrapSetup,
  classifySetupError,
  configureUpdateCheck,
  createFirstLibrary,
  FirstLibraryError,
  type FirstLibraryProgress,
  LIBRARY_TYPES,
  type LibraryType,
  loadProviderKeys,
  SetupTokenError,
  type UpdateCheckChannel,
} from "./orchestrate";

// First-run setup wizard (WEB-1, ADR-0007 §2; closed under ADR-0080). A 7-step,
// token-gated first-run flow that ORCHESTRATES existing server mutations — it
// never duplicates them. CORE-1114 restyle: a full-bleed branded
// backdrop (static CSS; animated effects belong in Oscillux), one centred step
// with a segmented progress bar, icon tiles for choices, and the cinema token
// palette via the app's shadcn semantic classes.
//
// Visual step order: token → admin password → sign-in security → owner →
// plugins → Library →
// first scan. Plugins are activated BEFORE the Library so the
// Library step can offer only types backed by a plugin the operator enabled;
// the owner profile follows the admin password. The API shape differs: we
// collect token + admin password + owner profile first, fire ONE POST
// /api/setup, then setPluginActive (plugin step) and createLibrary +
// setLibraryRoots + scanLibrary (Library step) over the now-MANAGER session,
// then present the live first scan before handing off to the app.
//
// Resumability (ADR-0080): on mount the wizard probes the server. The setup
// token is single-use and in-memory, so if it was already consumed (reload or
// server restart mid-setup), the token/admin/owner steps are skipped and the
// wizard resumes at the first incomplete provisioning step.

const STEP_KEYS: StringKey[] = [
  "setupRailToken",
  "setupRailAdminPassword",
  "setupRailSecurity",
  "setupRailProfile",
  "setupRailPlugins",
  "setupRailLibrary",
  "setupRailScan",
];

// Step indices, named so the step-boundary logic in onNext/render reads
// intention rather than magic numbers.
const STEP_SECURITY = 2;
const STEP_PROFILE = 3;
const STEP_PLUGINS = 4;
const STEP_LIBRARY = 5;
const STEP_SCAN = 6;

const SECURITY_MODES: { mode: SignInSecurityMode; label: StringKey; detail: StringKey }[] = [
  { mode: "home", label: "setupSecurityHome", detail: "setupSecurityHomeDetail" },
  { mode: "strict", label: "setupSecurityStrict", detail: "setupSecurityStrictDetail" },
];

const TYPE_LABEL: Record<LibraryType, StringKey> = {
  films: "setupTypeFilms",
  tvshows: "setupTypeTVShows",
  mixed: "setupTypeMixed",
  music: "setupTypeMusic",
  podcasts: "setupTypePodcasts",
};

const TYPE_ICON: Record<LibraryType, LucideIcon> = {
  films: Film,
  tvshows: Tv,
  mixed: Clapperboard,
  music: Music,
  podcasts: Podcast,
};

const PLUGIN_ICON: Record<string, LucideIcon> = {
  filmtv: Clapperboard,
  music: Music,
  podcasts: Podcast,
};

const PLUGIN_LABEL: Record<string, StringKey> = {
  filmtv: "setupPluginFilmTV",
  music: "setupPluginMusic",
  podcasts: "setupPluginPodcast",
};

function consumeSetupToken(): string {
  if (typeof window === "undefined") return "";

  const url = new URL(window.location.href);
  const token = url.searchParams.get("token") ?? "";
  if (!url.searchParams.has("token")) return token;

  url.searchParams.delete("token");
  window.history.replaceState(null, "", `${url.pathname}${url.search}${url.hash}`);
  return token;
}

export function SetupWizard() {
  const { t, locale } = useI18n();
  // Visual step index (0..6). The bootstrap POST fires when we leave the owner
  // profile step (index 3) forward, because that's the first point where token
  // + admin password + security mode + profile name are all collected.
  const [step, setStep] = useState(0);

  // Step 1 — token (a CREDENTIAL: autoComplete=off; startup may include it
  // only inside the one-click setup URL).
  const initialTokenRef = useRef<string | null>(null);
  if (initialTokenRef.current === null) initialTokenRef.current = consumeSetupToken();
  const [token, setToken] = useState(initialTokenRef.current);
  // Step 2 — admin password (CREDENTIAL: type=password).
  const [adminPassword, setAdminPassword] = useState("");
  const [adminPasswordConfirm, setAdminPasswordConfirm] = useState("");
  // Sign-in security (CORE-1110): Home keeps admin-password sign-in, Strict
  // turns it off. Sent with the bootstrap POST.
  const [securityMode, setSecurityMode] = useState<SignInSecurityMode>("home");
  // Step 6 — first Library.
  const [libraryType, setLibraryType] = useState<LibraryType>("films");
  const [libraryName, setLibraryName] = useState("");
  const [rootPath, setRootPath] = useState("");
  const [scanJob, setScanJob] = useState<JobInfo | null>(null);
  const [firstLibraryProgress, setFirstLibraryProgress] = useState<FirstLibraryProgress>({
    rootsSet: false,
  });
  // Folder picker (WEB-6): a directories-only browse of the server filesystem,
  // opened from the root field. pickerNode is the current directory with its
  // subfolders; null while closed.
  const [pickerNode, setPickerNode] = useState<FolderEntryInfo | null>(null);
  const [pickerBusy, setPickerBusy] = useState(false);
  const [pickerError, setPickerError] = useState<string | null>(null);
  // Step 4 — owner profile.
  const [profileName, setProfileName] = useState(t("setupProfileDefault"));
  // Step 5 — plugin activation.
  const [plugins, setPlugins] = useState<PluginSummary[]>([]);
  const [activePlugins, setActivePlugins] = useState<Set<string>>(new Set());
  const [updateCheckEnabled, setUpdateCheckEnabled] = useState(false);
  const [updateCheckChannel, setUpdateCheckChannel] = useState<UpdateCheckChannel>("stable");
  // Provider keys the installed plugins' scrapers need (TMDB-style API keys),
  // offered on the Plugins step so the owner is not sent hunting in the Console.
  const [providerKeys, setProviderKeys] = useState<ProviderKey[]>([]);
  const [keySecrets, setKeySecrets] = useState<Record<string, string>>({});

  // True once POST /api/setup has succeeded — drives the "unlocked" banner and
  // guards against re-bootstrapping when navigating back/forward.
  const [bootstrapped, setBootstrapped] = useState(false);
  // True when the wizard mounted onto an already-bootstrapped server (a reload
  // or server restart consumed the one-time token before provisioning
  // finished). In that case the admin password was never collected this run, so
  // the update-check step (which Admin-Session-gates on the password) is
  // skipped during resumed provisioning (ADR-0080 resume path).
  const [resumedFromRestart, setResumedFromRestart] = useState(false);
  // Gates the wizard body until the resume probe settles, so a fresh load never
  // briefly shows the token step before bouncing a resumed server forward.
  const [probing, setProbing] = useState(true);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  // When the token POST returns 401, we bounce the user back to step 1 and show
  // the invalid-token message inline there.
  const [tokenError, setTokenError] = useState<string | null>(null);
  // True when the server says this browser needs no setup token (a LAN browser
  // while setup is pending, CORE-1105): the wizard starts at the admin step.
  const [tokenStepSkipped, setTokenStepSkipped] = useState(false);
  const firstStep = tokenStepSkipped ? 1 : 0;

  const errorRef = useRef<HTMLParagraphElement>(null);
  const tokenID = useId();
  const adminID = useId();
  const adminConfirmID = useId();
  const libNameID = useId();
  const rootID = useId();
  const profileID = useId();
  const errorID = useId();

  useEffect(
    () => () => {
      initialTokenRef.current = "";
    },
    [],
  );

  // Resume probe (ADR-0080). On mount, ask the server whether setup already ran
  // and, if so, which provisioning steps remain. The one-time token is gone
  // after a reload/restart, so we cannot re-bootstrap — we resume forward.
  useEffect(() => {
    let cancelled = false;
    void bootstrapResume()
      .then((resume) => {
        if (cancelled) return;
        if (!resume.alreadyBootstrapped) {
          // Fresh server: run the wizard from the token step, or from the admin
          // step when this browser needs no token and none was handed to it.
          if (resume.tokenRequired === false && !initialTokenRef.current) {
            setTokenStepSkipped(true);
            setStep(1);
          }
          setProbing(false);
          return;
        }
        if (resume.firstLibraryProgress) {
          setFirstLibraryProgress(resume.firstLibraryProgress);
        }
        // The token was already consumed. If setup is fully complete, the
        // wizard is not replayable — hand off to the app root (the session
        // spine re-routes an unenrolled browser to /enroll).
        if (resume.state?.complete) {
          window.location.assign(import.meta.env.BASE_URL);
          return;
        }
        // Resume at the first incomplete provisioning step: plugins if they
        // were not activated yet, otherwise the Library step.
        setBootstrapped(true);
        setResumedFromRestart(true);
        setStep(resume.state && !resume.state.pluginsActivated ? STEP_PLUGINS : STEP_LIBRARY);
        setProbing(false);
      })
      .catch(() => {
        if (cancelled) return;
        // Probe itself failed (offline / unexpected): fall back to the fresh
        // flow rather than dead-ending. fetchSetupRequired already swallows
        // most errors to false, so this is a belt-and-suspenders guard.
        setProbing(false);
      });
    return () => {
      cancelled = true;
    };
  }, []);

  // After the bootstrap succeeds the browser holds a MANAGER session, so load
  // the installed plugins for the activation step. On a fresh run the Film & TV
  // plugin (the most common first Library) is pre-selected so the list isn't
  // all-off; on a resumed run we leave the selection empty (the operator's real
  // choices are already on the server) and let the Library step fall back to
  // all types.
  useEffect(() => {
    if (!bootstrapped) return;
    let cancelled = false;
    void fetchPlugins().then((list) => {
      if (cancelled) return;
      setPlugins(list);
      if (!resumedFromRestart && list.some((p) => p.id === "filmtv")) {
        setActivePlugins((prev) => (prev.has("filmtv") ? prev : new Set(prev).add("filmtv")));
      }
    });
    return () => {
      cancelled = true;
    };
  }, [bootstrapped, resumedFromRestart]);

  // Provider keys need the Admin Session, which only a fresh run can open (a
  // resumed run never collected the password); those owners add keys in the
  // Console. Optional: a failure leaves the list empty and setup carries on.
  useEffect(() => {
    if (!bootstrapped || resumedFromRestart || !adminPassword) return;
    let cancelled = false;
    void loadProviderKeys(adminPassword)
      .then((keys) => {
        if (!cancelled) setProviderKeys(keys);
      })
      .catch(() => {});
    return () => {
      cancelled = true;
    };
  }, [bootstrapped, resumedFromRestart, adminPassword]);

  // Keys still missing for a plugin the owner has switched on.
  const neededKeys = providerKeys.filter(
    (key) => keyNeeded(key) && key.pluginIDs.some((id) => activePlugins.has(id)),
  );

  // The Library-type tiles offered are limited to types backed by a plugin the
  // operator just enabled. If nothing is enabled (or on a resumed run where the
  // local selection is empty), fall back to all types so the step stays usable.
  const enabledTypes = LIBRARY_TYPES.filter((s) => activePlugins.has(s.pluginID));
  const libraryTypes = enabledTypes.length > 0 ? enabledTypes : LIBRARY_TYPES;

  // Keep the selected type valid as the enabled set changes (e.g. the operator
  // disables the plugin behind the current selection).
  useEffect(() => {
    if (!libraryTypes.some((s) => s.type === libraryType)) {
      setLibraryType(libraryTypes[0].type);
    }
  }, [libraryTypes, libraryType]);

  useEffect(() => {
    const scanJobID = scanJob?.id;
    if (step !== STEP_SCAN || !scanJobID) return;
    let cancelled = false;
    let refreshing = false;
    let pollErrorAnnounced = false;
    async function refreshScan() {
      if (refreshing) return;
      refreshing = true;
      try {
        const jobs = await fetchJobs();
        const live = jobs.find((job) => job.id === scanJobID);
        if (!cancelled) {
          if (live) setScanJob(live);
          if (pollErrorAnnounced) {
            pollErrorAnnounced = false;
            setError(null);
          }
        }
      } catch {
        if (!cancelled) {
          pollErrorAnnounced = true;
          announceError(t("couldNotReachServer"));
        }
      } finally {
        refreshing = false;
      }
    }
    void refreshScan();
    const timer = window.setInterval(() => void refreshScan(), 2000);
    return () => {
      cancelled = true;
      window.clearInterval(timer);
    };
  }, [scanJob?.id, step, t]);

  function announceError(message: string) {
    setError(message);
    requestAnimationFrame(() => errorRef.current?.focus());
  }

  // Run the bootstrap once we have token + admin password + profile name.
  async function runBootstrap(): Promise<boolean> {
    setBusy(true);
    setError(null);
    try {
      await bootstrapSetup({
        token: token.trim(),
        deviceName: t("webBrowser"),
        profileName: profileName.trim() || t("setupProfileDefault"),
        adminPassword,
        securityMode,
      });
      setBootstrapped(true);
      return true;
    } catch (err) {
      if (err instanceof SetupTokenError) {
        // Wrong/consumed token: bounce to step 1 with the inline message and
        // do NOT advance.
        setTokenError(t("setupTokenInvalid"));
        setTokenStepSkipped(false);
        setStep(0);
        return false;
      }
      announceError(
        err instanceof ApiError || err instanceof Error
          ? t("setupFailed")
          : t("couldNotReachServer"),
      );
      return false;
    } finally {
      initialTokenRef.current = "";
      setToken("");
      setBusy(false);
    }
  }

  // Activate the chosen plugins (+ the update-check preference) after the
  // session is live. Runs on leaving the plugin step; the Library is created
  // later, on the now-following Library step. This does NOT navigate away.
  async function runPluginActivation(): Promise<boolean> {
    setBusy(true);
    setError(null);
    try {
      // The update-check toggle is Admin-Session-gated on the admin password.
      // On a resumed (restart) run the password was never collected this
      // session, so skip it — the preference stays at the server default and is
      // reconfigurable later in the Console (it is an optional step).
      if (!resumedFromRestart) {
        await configureUpdateCheck({
          adminPassword,
          enabled: updateCheckEnabled,
          channel: updateCheckChannel,
        });
      }
      await activatePlugins([...activePlugins]);
      return await saveProviderKeys();
    } catch (err) {
      announceError(
        err instanceof ApiError || err instanceof Error
          ? t(classifySetupError(err))
          : t("couldNotReachServer"),
      );
      return false;
    } finally {
      setBusy(false);
    }
  }

  // Save each provider key the owner typed. Runs after activation; a key that
  // fails keeps the owner on the step with its name so they can fix or clear it.
  async function saveProviderKeys(): Promise<boolean> {
    for (const key of neededKeys) {
      const secret = keySecrets[key.authRef]?.trim();
      if (!secret) continue;
      try {
        const saved = await setScraperCredential(key.authRef, secret);
        setProviderKeys((prev) =>
          prev.map((k) =>
            k.authRef === key.authRef ? { ...k, status: saved.credentialStatus } : k,
          ),
        );
        setKeySecrets((prev) => ({ ...prev, [key.authRef]: "" }));
      } catch {
        announceError(t("setupKeySaveFailed", { provider: key.label }));
        return false;
      }
    }
    return true;
  }

  // Create the first Library after the plugins are active. Runs on leaving the
  // Library step; on success the wizard advances to the migration offer (Seam
  // A), which owns the final hand-off. Split from navigation so the offer step
  // is reachable (it previously navigated immediately, unmounting before any
  // later step could render).
  async function runLibraryCreation(): Promise<boolean> {
    setBusy(true);
    setError(null);
    try {
      const scan = await createFirstLibrary({
        title: libraryName.trim() || t(TYPE_LABEL[libraryType]),
        type: libraryType,
        rootPath: rootPath.trim(),
        progress: firstLibraryProgress,
      });
      setFirstLibraryProgress({ libraryID: scan.libraryID, rootsSet: true });
      setScanJob({
        id: scan.scanJobID,
        kind: "scan",
        status: scan.scanStatus,
        progress: { current: 0, total: 0, unit: "" },
      });
      return true;
    } catch (err) {
      if (err instanceof FirstLibraryError) {
        setFirstLibraryProgress(err.progress);
        if (err.progress.rootPath) setRootPath(err.progress.rootPath);
      }
      announceError(
        err instanceof FirstLibraryError
          ? err.message
          : err instanceof ApiError || err instanceof Error
            ? err.message
            : t("couldNotReachServer"),
      );
      return false;
    } finally {
      setBusy(false);
    }
  }

  // Final hand-off: leave the wizard so SessionProvider re-bootstraps the
  // freshly-minted MANAGER session. Called by the migration offer's "skip" and
  // "go to importer" actions; setup completion is "first browse", and this is
  // the door to it.
  function finishSetup() {
    window.location.assign(import.meta.env.BASE_URL);
  }

  // Per-step validation; returns an inline message or null when valid.
  function validate(current: number): string | null {
    switch (current) {
      case 0:
        return token.trim() || tokenStepSkipped ? null : t("setupTokenInvalid");
      case 1:
        if (!adminPassword) return t("setupAdminPasswordRequired");
        if (adminPassword !== adminPasswordConfirm) return t("setupAdminPasswordMismatch");
        return null;
      case STEP_LIBRARY:
        return rootPath.trim() ? null : t("setupLibraryRootRequired");
      default:
        return null;
    }
  }

  async function onNext(e: FormEvent) {
    e.preventDefault();
    if (busy) return;
    setTokenError(null);

    if (isLast) {
      finishSetup();
      return;
    }

    const invalid = validate(step);
    if (invalid) {
      if (step === 0) setTokenError(invalid);
      else announceError(invalid);
      return;
    }
    setError(null);

    // Leaving the owner profile step forward is the bootstrap boundary.
    if (step === STEP_PROFILE && !bootstrapped) {
      const ok = await runBootstrap();
      if (!ok) return;
      setStep(STEP_PLUGINS);
      return;
    }

    // Leaving the plugin step activates the chosen plugins (+ update-check),
    // then advances to the Library step.
    if (step === STEP_PLUGINS) {
      const ok = await runPluginActivation();
      if (!ok) return;
      setStep(STEP_LIBRARY);
      return;
    }

    // Leaving the Library step creates the first Library, then advances to the
    // migration offer (Seam A) — it no longer finishes the wizard.
    if (step === STEP_LIBRARY) {
      const ok = await runLibraryCreation();
      if (!ok) return;
      setStep(STEP_SCAN);
      return;
    }

    setStep((s) => Math.min(s + 1, STEP_KEYS.length - 1));
  }

  function onBack() {
    if (busy) return;
    setError(null);
    setStep((s) => Math.max(s - 1, firstStep));
  }

  // Load a directory into the picker. An undefined path lists the filesystem
  // root. Failures (unreadable/missing) keep the previous view and show an
  // inline message, so the picker never dead-ends.
  async function loadPickerFolder(path?: string) {
    setPickerBusy(true);
    setPickerError(null);
    try {
      const node = await browseServerFolders(path);
      setPickerNode(node);
    } catch {
      setPickerError(t("setupLibraryPickerError"));
    } finally {
      setPickerBusy(false);
    }
  }

  function openPicker() {
    // Seed from the typed path when present so the picker opens where the
    // operator was, otherwise at the filesystem root.
    void loadPickerFolder(rootPath.trim() || undefined);
  }

  function closePicker() {
    setPickerNode(null);
    setPickerError(null);
  }

  function usePickerFolder() {
    if (pickerNode) setRootPath(pickerNode.path);
    closePicker();
  }

  function togglePlugin(id: string) {
    setActivePlugins((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  }

  const isScanStep = step === STEP_SCAN;
  const isLast = isScanStep;

  // Hold the body blank while the resume probe runs so a resumed server never
  // flashes the token step before bouncing forward.
  if (probing) {
    return <main className="theme-cinema min-h-screen bg-background" />;
  }

  // Steps the wizard actually walks: a LAN browser that skipped the token step
  // never sees it in the progress bar.
  const visibleSteps = STEP_KEYS.slice(firstStep);
  const visibleIndex = step - firstStep;

  return (
    <main className="theme-cinema relative isolate flex min-h-screen flex-col overflow-hidden bg-background text-foreground">
      <CinemaBackdrop />

      {/* Brand bar */}
      <header className="flex items-center justify-between px-5 py-4 sm:px-8 sm:py-6">
        <div className="flex items-center gap-2.5">
          <img src={beamfallLogo} alt="" width={28} height={28} className="shrink-0" />
          <span className="text-base font-semibold tracking-tight">{t("brand")}</span>
          <span className="hidden text-sm text-muted-foreground sm:inline">
            {t("setupBrandSuffix")}
          </span>
        </div>
        <span className="rounded-full border border-white/10 bg-white/5 px-3 py-1 text-xs text-muted-foreground backdrop-blur">
          {t("setupFreshServer")}
        </span>
      </header>

      <div className="flex flex-1 items-start justify-center px-4 pb-10 pt-2 sm:items-center sm:px-8 sm:pt-0">
        <div className="w-full max-w-[600px]">
          {/* Progress: one segment per visible step; names stay for assistive tech. */}
          <ol className="m-0 mb-6 flex list-none gap-1.5 p-0" aria-label={t("setupBrandSuffix")}>
            {visibleSteps.map((key, i) => (
              <li
                key={key}
                aria-current={i === visibleIndex ? "step" : undefined}
                className={cn(
                  "h-1 flex-1 rounded-full bg-white/10 transition-colors duration-500",
                  i < visibleIndex && "bg-primary/70",
                  i === visibleIndex && "bg-primary shadow-[0_0_12px_var(--accent)]",
                )}
              >
                <span className="sr-only">{t(key)}</span>
              </li>
            ))}
          </ol>

          <div className="mb-3 flex items-center justify-between text-xs font-medium uppercase tracking-[0.14em]">
            <span className="text-primary">{t(STEP_KEYS[step])}</span>
            <span className="text-muted-foreground">
              {t("setupStepCount", { n: visibleIndex + 1, total: visibleSteps.length })}
            </span>
          </div>

          {/* Unlocked banner — shown once the bootstrap POST succeeds */}
          {bootstrapped && (
            <div className="mb-4 flex items-center gap-2 rounded-full border border-primary/30 bg-primary/10 px-3.5 py-1.5 text-[12.5px] text-primary">
              <Check aria-hidden className="size-3.5 shrink-0" />
              {t("setupUnlockedBanner")}
            </div>
          )}

          <form
            key={step}
            onSubmit={onNext}
            noValidate
            className="animate-in fade-in slide-in-from-bottom-2 rounded-2xl border border-white/10 bg-card/70 p-5 shadow-[0_30px_80px_-20px_rgba(0,0,0,0.7)] backdrop-blur-xl duration-300 sm:p-8"
          >
            {step === 0 && (
              <section>
                <StepHeading
                  icon={KeyRound}
                  title={t("setupTokenTitle")}
                  lead={t("setupTokenLead")}
                />
                <Label htmlFor={tokenID} className={FIELD_LABEL}>
                  {t("setupToken")}
                </Label>
                <Input
                  id={tokenID}
                  value={token}
                  onChange={(e) => {
                    setToken(e.target.value);
                    setTokenError(null);
                  }}
                  className={cn(FIELD, "font-mono tracking-wider")}
                  autoComplete="off"
                  autoCapitalize="off"
                  autoCorrect="off"
                  spellCheck={false}
                  autoFocus
                  required
                  aria-invalid={tokenError != null}
                  aria-describedby={tokenError ? errorID : undefined}
                />
                {tokenError && (
                  <p
                    ref={errorRef}
                    id={errorID}
                    role="alert"
                    tabIndex={-1}
                    className="mt-2 text-sm text-destructive outline-none"
                  >
                    {tokenError}
                  </p>
                )}
              </section>
            )}

            {step === 1 && (
              <section>
                <StepHeading
                  icon={LockKeyhole}
                  title={t("setupAdminPasswordTitle")}
                  lead={t("setupAdminPasswordLead")}
                />
                <Label htmlFor={adminID} className={FIELD_LABEL}>
                  {t("setupAdminPassword")}
                </Label>
                <Input
                  id={adminID}
                  type="password"
                  value={adminPassword}
                  onChange={(e) => setAdminPassword(e.target.value)}
                  className={FIELD}
                  autoComplete="new-password"
                  required
                  autoFocus
                />
                <Label htmlFor={adminConfirmID} className={cn(FIELD_LABEL, "mt-4")}>
                  {t("setupAdminPasswordConfirm")}
                </Label>
                <Input
                  id={adminConfirmID}
                  type="password"
                  value={adminPasswordConfirm}
                  onChange={(e) => setAdminPasswordConfirm(e.target.value)}
                  className={FIELD}
                  autoComplete="new-password"
                  required
                />
              </section>
            )}

            {step === STEP_SECURITY && (
              <fieldset className="m-0 border-0 p-0">
                <legend className="sr-only">{t("setupSecurityTitle")}</legend>
                <StepHeading
                  icon={ShieldCheck}
                  title={t("setupSecurityTitle")}
                  lead={t("setupSecurityLead")}
                />
                <div className="grid gap-3 sm:grid-cols-2">
                  {SECURITY_MODES.map(({ mode, label, detail }) => {
                    const Icon = mode === "home" ? House : LockKeyhole;
                    const sel = securityMode === mode;
                    return (
                      <label
                        key={mode}
                        className={cn(
                          TILE,
                          "items-start gap-2 p-4 text-left",
                          sel && TILE_SELECTED,
                        )}
                      >
                        <span className="flex w-full items-center justify-between">
                          <Icon
                            aria-hidden
                            className={cn("size-5", sel ? "text-primary" : "text-muted-foreground")}
                          />
                          <input
                            type="radio"
                            name="setup-security-mode"
                            value={mode}
                            checked={sel}
                            onChange={() => setSecurityMode(mode)}
                            className="size-4 accent-[var(--accent)]"
                          />
                        </span>
                        <span className="flex items-center gap-2 text-sm font-semibold text-foreground">
                          {t(label)}
                          {mode === "home" && (
                            <Badge variant="outline" className="border-primary/40 text-primary">
                              {t("setupSecurityRecommended")}
                            </Badge>
                          )}
                        </span>
                        <span className="text-xs leading-relaxed text-muted-foreground">
                          {t(detail)}
                        </span>
                      </label>
                    );
                  })}
                </div>
              </fieldset>
            )}

            {step === STEP_LIBRARY && (
              <section>
                <StepHeading
                  icon={FolderOpen}
                  title={t("setupLibraryTitle")}
                  lead={t("setupLibraryLead")}
                />
                <Label className={FIELD_LABEL}>{t("setupLibraryType")}</Label>
                <div className="mb-5 grid grid-cols-2 gap-2 sm:grid-cols-5">
                  {libraryTypes.map((spec) => {
                    const sel = spec.type === libraryType;
                    const Icon = TYPE_ICON[spec.type];
                    return (
                      <button
                        key={spec.type}
                        type="button"
                        onClick={() => setLibraryType(spec.type)}
                        aria-pressed={sel}
                        className={cn(
                          TILE,
                          "items-center gap-1.5 px-2 py-3.5 text-center",
                          sel && TILE_SELECTED,
                        )}
                      >
                        <Icon
                          aria-hidden
                          className={cn("size-6", sel ? "text-primary" : "text-muted-foreground")}
                        />
                        <span className="text-[13px] font-semibold">
                          {t(TYPE_LABEL[spec.type])}
                        </span>
                        <span className="text-[10.5px] text-muted-foreground">
                          {t(PLUGIN_LABEL[spec.pluginID])}
                        </span>
                      </button>
                    );
                  })}
                </div>
                <Label htmlFor={libNameID} className={FIELD_LABEL}>
                  {t("setupLibraryName")}
                </Label>
                <Input
                  id={libNameID}
                  value={libraryName}
                  onChange={(e) => setLibraryName(e.target.value)}
                  className={FIELD}
                  placeholder={t(TYPE_LABEL[libraryType])}
                  autoFocus
                />
                <Label htmlFor={rootID} className={cn(FIELD_LABEL, "mt-4")}>
                  {t("setupLibraryRoot")}
                </Label>
                <div className="flex items-stretch gap-2">
                  <Input
                    id={rootID}
                    value={rootPath}
                    onChange={(e) => setRootPath(e.target.value)}
                    className={cn(FIELD, "flex-1 font-mono")}
                    placeholder="/media/films"
                    required
                    disabled={firstLibraryProgress.rootsSet}
                  />
                  <Button
                    type="button"
                    variant="outline"
                    className="h-11 rounded-xl"
                    onClick={openPicker}
                    aria-expanded={pickerNode != null}
                    disabled={firstLibraryProgress.rootsSet}
                  >
                    {t("setupLibraryBrowse")}
                  </Button>
                </div>

                {pickerNode != null && (
                  <FolderPicker
                    node={pickerNode}
                    busy={pickerBusy}
                    error={pickerError}
                    onOpenFolder={(path) => void loadPickerFolder(path)}
                    onUse={usePickerFolder}
                    onClose={closePicker}
                    labels={{
                      use: t("setupLibraryPickerUse"),
                      loading: t("setupLibraryPickerLoading"),
                      empty: t("setupLibraryPickerEmpty"),
                      close: t("setupLibraryPickerClose"),
                    }}
                  />
                )}
              </section>
            )}

            {step === STEP_PROFILE && (
              <section>
                <StepHeading
                  icon={UserRound}
                  title={t("setupProfileTitle")}
                  lead={t("setupProfileLead")}
                />
                <Label htmlFor={profileID} className={FIELD_LABEL}>
                  {t("setupProfileName")}
                </Label>
                <Input
                  id={profileID}
                  value={profileName}
                  onChange={(e) => setProfileName(e.target.value)}
                  className={FIELD}
                  placeholder={t("setupProfileDefault")}
                  autoFocus
                />
              </section>
            )}

            {step === STEP_PLUGINS && (
              <section>
                <StepHeading
                  icon={Blocks}
                  title={t("setupPluginsTitle")}
                  lead={t("setupPluginsLead")}
                />
                {plugins.length === 0 ? (
                  <p className="text-sm text-muted-foreground">{t("setupPluginsEmpty")}</p>
                ) : (
                  <ul className="m-0 grid list-none gap-2 p-0 sm:grid-cols-2">
                    {plugins.map((p) => {
                      const name = displayString(p.displayName, locale);
                      const on = activePlugins.has(p.id);
                      const Icon = PLUGIN_ICON[p.id] ?? Blocks;
                      return (
                        <li
                          key={p.id}
                          className={cn(
                            TILE,
                            "flex-row items-center gap-3 px-3.5 py-3",
                            on && TILE_SELECTED,
                          )}
                        >
                          <Icon
                            aria-hidden
                            className={cn(
                              "size-5 shrink-0",
                              on ? "text-primary" : "text-muted-foreground",
                            )}
                          />
                          <span className="flex-1 text-sm font-medium">{name}</span>
                          <Switch
                            checked={on}
                            onCheckedChange={() => togglePlugin(p.id)}
                            aria-label={name}
                          />
                        </li>
                      );
                    })}
                  </ul>
                )}
                {neededKeys.length > 0 && (
                  <div className="mt-5 rounded-xl border border-white/10 bg-white/[0.03] p-4">
                    <div className="flex items-start gap-3">
                      <KeyRound aria-hidden className="mt-0.5 size-4 shrink-0 text-primary" />
                      <div className="space-y-1">
                        <h2 className="m-0 text-sm font-semibold">{t("setupKeysTitle")}</h2>
                        <p className="m-0 text-[12.5px] leading-snug text-muted-foreground">
                          {t("setupKeysLead")}
                        </p>
                      </div>
                    </div>
                    <div className="mt-3 grid gap-3">
                      {neededKeys.map((key) => {
                        const fieldId = `setup-key-${key.authRef}`;
                        return (
                          <div key={key.authRef}>
                            <Label htmlFor={fieldId} className={FIELD_LABEL}>
                              {t("pluginKeyField", { provider: key.label })}
                            </Label>
                            <Input
                              id={fieldId}
                              type="password"
                              autoComplete="off"
                              className={FIELD}
                              placeholder={t("setupKeyOptional")}
                              value={keySecrets[key.authRef] ?? ""}
                              onChange={(e) =>
                                setKeySecrets((prev) => ({
                                  ...prev,
                                  [key.authRef]: e.target.value,
                                }))
                              }
                            />
                            <p className="mb-0 mt-1.5 text-[11.5px] leading-snug text-muted-foreground">
                              {t("pluginKeySentTo", { hosts: key.hosts.join(", ") })}
                            </p>
                          </div>
                        );
                      })}
                    </div>
                  </div>
                )}
                <div className="mt-5 rounded-xl border border-white/10 bg-white/[0.03] p-4">
                  <div className="flex items-start justify-between gap-4">
                    <div className="space-y-1">
                      <h2 className="m-0 text-sm font-semibold">{t("setupUpdateTitle")}</h2>
                      <p className="m-0 text-[12.5px] leading-snug text-muted-foreground">
                        {t("setupUpdateLead")}
                      </p>
                    </div>
                    <Switch
                      checked={updateCheckEnabled}
                      onCheckedChange={setUpdateCheckEnabled}
                      aria-label={t("setupUpdateToggle")}
                    />
                  </div>
                  <div className="mt-3 flex flex-wrap items-center gap-2">
                    {(["stable", "beta"] as const).map((channel) => (
                      <button
                        key={channel}
                        type="button"
                        disabled={!updateCheckEnabled}
                        aria-pressed={updateCheckChannel === channel}
                        onClick={() => setUpdateCheckChannel(channel)}
                        className={cn(
                          "rounded-full border border-white/10 px-3.5 py-1.5 text-xs font-medium transition-colors disabled:opacity-45",
                          updateCheckChannel === channel &&
                            "border-primary bg-primary/10 text-primary",
                        )}
                      >
                        {t(channel === "stable" ? "updateChannelStable" : "updateChannelBeta")}
                      </button>
                    ))}
                  </div>
                  <p className="mb-0 mt-2.5 text-[11.5px] leading-snug text-muted-foreground">
                    {t("setupUpdateDisclosure")}
                  </p>
                </div>
              </section>
            )}

            {isScanStep && (
              <section>
                <StepHeading
                  icon={ScanSearch}
                  title={t("setupScanTitle")}
                  lead={t("setupScanLead")}
                />
                <div className="flex items-center gap-3.5 rounded-xl border border-white/10 bg-white/[0.03] p-4">
                  <span className="relative flex size-10 shrink-0 items-center justify-center rounded-full bg-primary/15">
                    <span className="absolute inset-0 animate-ping rounded-full bg-primary/20 motion-reduce:hidden" />
                    <Sparkles aria-hidden className="size-5 text-primary" />
                  </span>
                  <div>
                    <p className="m-0 text-sm font-semibold">
                      {t("setupScanStatus", {
                        status: scanJob ? jobStatusLabel(scanJob.status, t) : t("jobStatusQueued"),
                      })}
                    </p>
                    <p className="m-0 mt-0.5 text-[13px] text-muted-foreground">
                      {scanJob && jobProgressDetail(scanJob, t)
                        ? jobProgressDetail(scanJob, t)
                        : t("setupScanWaiting")}
                    </p>
                  </div>
                </div>
              </section>
            )}

            <div className="mt-7 flex items-center gap-2">
              {step > firstStep && !bootstrapped && (
                <Button
                  type="button"
                  variant="ghost"
                  className="h-11 rounded-full px-5"
                  onClick={onBack}
                  disabled={busy}
                >
                  {t("setupBack")}
                </Button>
              )}
              <Button
                type="submit"
                disabled={busy}
                className="ml-auto h-11 min-w-[148px] rounded-full px-6 text-[15px] font-semibold shadow-[0_8px_30px_-8px_var(--accent)]"
              >
                {busy && <LoaderCircle aria-hidden className="size-4 animate-spin" />}
                {busy ? t("setupWorking") : isLast ? t("setupBrowseReady") : t("setupContinue")}
              </Button>
            </div>

            {error && step !== 0 && (
              <p
                ref={errorRef}
                id={errorID}
                role="alert"
                tabIndex={-1}
                className="mt-3 text-sm text-destructive outline-none"
              >
                {error}
              </p>
            )}
          </form>
        </div>
      </div>
    </main>
  );
}

const FIELD = "h-11 rounded-xl border-white/10 bg-white/[0.04] text-[15px]";
const FIELD_LABEL = "mb-2 block text-[13px] font-medium text-foreground/80";
const TILE =
  "flex flex-col rounded-xl border border-white/10 bg-white/[0.03] transition-all duration-200 hover:border-white/25 hover:bg-white/[0.06] cursor-pointer";
const TILE_SELECTED =
  "border-primary bg-primary/10 shadow-[0_0_0_1px_var(--accent),0_10px_30px_-12px_var(--accent)] hover:border-primary hover:bg-primary/10";

function StepHeading({
  icon: Icon,
  title,
  lead,
}: {
  icon: LucideIcon;
  title: string;
  lead: string;
}) {
  return (
    <div className="mb-6 flex gap-4">
      <span className="flex size-11 shrink-0 items-center justify-center rounded-xl bg-primary/15 text-primary ring-1 ring-primary/25">
        <Icon aria-hidden className="size-5" />
      </span>
      <div>
        <h1 className="m-0 text-2xl font-semibold tracking-tight sm:text-[28px] sm:leading-tight">
          {title}
        </h1>
        <p className="mb-0 mt-1.5 text-sm leading-relaxed text-muted-foreground">{lead}</p>
      </div>
    </div>
  );
}
