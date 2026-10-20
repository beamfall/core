import { type FormEvent, useEffect, useId, useRef, useState } from "react";
import beamfallLogo from "@/assets/beamfall-logo.png";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import { ApiError } from "@/lib/api";
import { displayString } from "@/lib/display";
import { browseServerFolders, fetchJobs, fetchPlugins } from "@/lib/graphql";
import type { FolderEntryInfo, JobInfo, PluginSummary } from "@/lib/graphql/types";
import { useI18n } from "@/lib/i18n";
import { jobProgressDetail, jobStatusLabel } from "@/lib/jobDisplay";
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
  SetupTokenError,
  type UpdateCheckChannel,
} from "./orchestrate";

// First-run setup wizard (WEB-1, ADR-0007 §2; closed under ADR-0080). A 6-step,
// token-gated first-run flow that ORCHESTRATES existing server mutations — it
// never duplicates them. Faithful to the approved mockup (setup-wizard.html): a
// brand bar, an "unlocked" banner once the token is verified, a step rail,
// Library-type tiles, and the cinema token palette (rendered via the app's
// shadcn semantic classes, which resolve to the same cinema theme as the
// mockup's raw vars).
//
// Visual step order: token → admin password → owner → plugins → Library →
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
  "setupRailProfile",
  "setupRailPlugins",
  "setupRailLibrary",
  "setupRailScan",
];

// Step indices, named so the step-boundary logic in onNext/render reads
// intention rather than magic numbers.
const STEP_PROFILE = 2;
const STEP_PLUGINS = 3;
const STEP_LIBRARY = 4;
const STEP_SCAN = 5;

const TYPE_LABEL: Record<LibraryType, StringKey> = {
  films: "setupTypeFilms",
  tvshows: "setupTypeTVShows",
  mixed: "setupTypeMixed",
  music: "setupTypeMusic",
  podcasts: "setupTypePodcasts",
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
  // Visual step index (0..5). The bootstrap POST fires when we leave the owner
  // profile step (index 2) forward, because that's the first point where token
  // + admin password + profile name are all collected.
  const [step, setStep] = useState(0);

  // Step 1 — token (a CREDENTIAL: autoComplete=off; startup may include it
  // only inside the one-click setup URL).
  const initialTokenRef = useRef<string | null>(null);
  if (initialTokenRef.current === null) initialTokenRef.current = consumeSetupToken();
  const [token, setToken] = useState(initialTokenRef.current);
  // Step 2 — admin password (CREDENTIAL: type=password).
  const [adminPassword, setAdminPassword] = useState("");
  const [adminPasswordConfirm, setAdminPasswordConfirm] = useState("");
  // Step 5 — first Library.
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
  // Step 3 — owner profile.
  const [profileName, setProfileName] = useState(t("setupProfileDefault"));
  // Step 4 — plugin activation.
  const [plugins, setPlugins] = useState<PluginSummary[]>([]);
  const [activePlugins, setActivePlugins] = useState<Set<string>>(new Set());
  const [updateCheckEnabled, setUpdateCheckEnabled] = useState(false);
  const [updateCheckChannel, setUpdateCheckChannel] = useState<UpdateCheckChannel>("stable");

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
          // Fresh server: run the wizard from the token step as normal.
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
      });
      setBootstrapped(true);
      return true;
    } catch (err) {
      if (err instanceof SetupTokenError) {
        // Wrong/consumed token: bounce to step 1 with the inline message and
        // do NOT advance.
        setTokenError(t("setupTokenInvalid"));
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
      return true;
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
        return token.trim() ? null : t("setupTokenInvalid");
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

    // Leaving the owner profile step (2) forward is the bootstrap boundary.
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
    setStep((s) => Math.max(s - 1, 0));
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
    return (
      <main className="theme-cinema flex min-h-screen items-center justify-center bg-background p-7" />
    );
  }

  return (
    <main className="theme-cinema flex min-h-screen items-center justify-center bg-background p-7">
      <div className="w-[880px] max-w-full overflow-hidden rounded-[var(--radius-card)] border bg-card text-card-foreground">
        {/* Brand bar */}
        <div className="flex items-center justify-between border-b bg-muted/40 px-[18px] py-3.5">
          <div className="flex items-center gap-2.5">
            <img src={beamfallLogo} alt="" width={22} height={22} className="shrink-0" />
            <span className="text-[15px] font-semibold">{t("brand")}</span>
            <span className="text-[13px] text-muted-foreground">{t("setupBrandSuffix")}</span>
          </div>
          <span className="text-xs text-muted-foreground">{t("setupFreshServer")}</span>
        </div>

        {/* Unlocked banner — shown once the token POST succeeds */}
        {bootstrapped && (
          <div className="mx-[18px] mt-3 rounded-lg border border-primary/35 bg-primary/10 px-3 py-2 text-[12.5px] text-primary">
            {t("setupUnlockedBanner")}
          </div>
        )}

        <div className="grid grid-cols-[220px_1fr]">
          {/* Step rail */}
          <nav className="border-r py-2 pl-[18px] pr-3.5">
            <ol className="m-0 flex list-none flex-col gap-0.5 p-0">
              {STEP_KEYS.map((key, i) => {
                const done = i < step;
                const cur = i === step;
                return (
                  <li
                    key={key}
                    className={cn(
                      "flex items-center gap-2.5 rounded-lg px-2 py-2.5",
                      cur && "border bg-muted",
                    )}
                  >
                    <span
                      className={cn(
                        "inline-flex h-[22px] w-[22px] items-center justify-center rounded-full border text-xs text-muted-foreground",
                        done && "border-0 bg-primary text-primary-foreground",
                        cur && "border-0 bg-primary font-semibold text-primary-foreground",
                      )}
                    >
                      {done ? "✓" : i + 1}
                    </span>
                    <span
                      className={cn(
                        "text-[13px] text-muted-foreground",
                        cur && "font-semibold text-foreground",
                      )}
                    >
                      {t(key)}
                    </span>
                  </li>
                );
              })}
            </ol>
          </nav>

          {/* Active pane */}
          <form onSubmit={onNext} className="px-[22px] pb-[22px] pt-4" noValidate>
            {step === 0 && (
              <section>
                <h1 className="m-0 text-lg font-semibold">{t("setupTokenTitle")}</h1>
                <p className="mb-4 mt-0.5 text-[13px] text-muted-foreground">
                  {t("setupTokenLead")}
                </p>
                <Label htmlFor={tokenID} className="mb-1.5 block text-xs">
                  {t("setupToken")}
                </Label>
                <Input
                  id={tokenID}
                  value={token}
                  onChange={(e) => {
                    setToken(e.target.value);
                    setTokenError(null);
                  }}
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
                <h1 className="m-0 text-lg font-semibold">{t("setupAdminPasswordTitle")}</h1>
                <p className="mb-4 mt-0.5 text-[13px] text-muted-foreground">
                  {t("setupAdminPasswordLead")}
                </p>
                <Label htmlFor={adminID} className="mb-1.5 block text-xs">
                  {t("setupAdminPassword")}
                </Label>
                <Input
                  id={adminID}
                  type="password"
                  value={adminPassword}
                  onChange={(e) => setAdminPassword(e.target.value)}
                  autoComplete="new-password"
                  required
                  autoFocus
                />
                <Label htmlFor={adminConfirmID} className="mb-1.5 mt-3.5 block text-xs">
                  {t("setupAdminPasswordConfirm")}
                </Label>
                <Input
                  id={adminConfirmID}
                  type="password"
                  value={adminPasswordConfirm}
                  onChange={(e) => setAdminPasswordConfirm(e.target.value)}
                  autoComplete="new-password"
                  required
                />
              </section>
            )}

            {step === STEP_LIBRARY && (
              <section>
                <h1 className="m-0 text-lg font-semibold">{t("setupLibraryTitle")}</h1>
                <p className="mb-4 mt-0.5 text-[13px] text-muted-foreground">
                  {t("setupLibraryLead")}
                </p>
                <Label className="mb-1.5 block text-xs">{t("setupLibraryType")}</Label>
                <div className="mb-3.5 grid grid-cols-5 gap-[7px]">
                  {libraryTypes.map((spec) => {
                    const sel = spec.type === libraryType;
                    return (
                      <button
                        key={spec.type}
                        type="button"
                        onClick={() => setLibraryType(spec.type)}
                        aria-pressed={sel}
                        className={cn(
                          "rounded-lg border px-[5px] py-2.5 text-center",
                          sel && "border-[1.5px] border-primary bg-primary/10",
                        )}
                      >
                        <div className="mt-[3px] text-xs font-semibold">
                          {t(TYPE_LABEL[spec.type])}
                        </div>
                        <div className="text-[10px] text-muted-foreground">
                          {t(PLUGIN_LABEL[spec.pluginID])}
                        </div>
                      </button>
                    );
                  })}
                </div>
                <Label htmlFor={libNameID} className="mb-1.5 block text-xs">
                  {t("setupLibraryName")}
                </Label>
                <Input
                  id={libNameID}
                  value={libraryName}
                  onChange={(e) => setLibraryName(e.target.value)}
                  placeholder={t(TYPE_LABEL[libraryType])}
                  autoFocus
                />
                <Label htmlFor={rootID} className="mb-1.5 mt-3.5 block text-xs">
                  {t("setupLibraryRoot")}
                </Label>
                <div className="flex items-stretch gap-2">
                  <Input
                    id={rootID}
                    value={rootPath}
                    onChange={(e) => setRootPath(e.target.value)}
                    className="flex-1 font-mono"
                    placeholder="/media/films"
                    required
                    disabled={firstLibraryProgress.rootsSet}
                  />
                  <Button
                    type="button"
                    variant="outline"
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
                <h1 className="m-0 text-lg font-semibold">{t("setupProfileTitle")}</h1>
                <p className="mb-4 mt-0.5 text-[13px] text-muted-foreground">
                  {t("setupProfileLead")}
                </p>
                <Label htmlFor={profileID} className="mb-1.5 block text-xs">
                  {t("setupProfileName")}
                </Label>
                <Input
                  id={profileID}
                  value={profileName}
                  onChange={(e) => setProfileName(e.target.value)}
                  placeholder={t("setupProfileDefault")}
                  autoFocus
                />
              </section>
            )}

            {step === STEP_PLUGINS && (
              <section>
                <h1 className="m-0 text-lg font-semibold">{t("setupPluginsTitle")}</h1>
                <p className="mb-4 mt-0.5 text-[13px] text-muted-foreground">
                  {t("setupPluginsLead")}
                </p>
                {plugins.length === 0 ? (
                  <p className="text-[13px] text-muted-foreground">{t("setupPluginsEmpty")}</p>
                ) : (
                  <ul className="m-0 flex list-none flex-col gap-2 p-0">
                    {plugins.map((p) => {
                      const name = displayString(p.displayName, locale);
                      return (
                        <li
                          key={p.id}
                          className="flex items-center justify-between rounded-lg border px-3 py-2.5"
                        >
                          <span className="text-[13px]">{name}</span>
                          <Switch
                            checked={activePlugins.has(p.id)}
                            onCheckedChange={() => togglePlugin(p.id)}
                            aria-label={name}
                          />
                        </li>
                      );
                    })}
                  </ul>
                )}
                <div className="mt-4 rounded-lg border bg-muted/25 p-3">
                  <div className="flex items-start justify-between gap-4">
                    <div className="space-y-1">
                      <h2 className="m-0 text-[13px] font-semibold">{t("setupUpdateTitle")}</h2>
                      <p className="m-0 text-[12px] leading-snug text-muted-foreground">
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
                          "rounded-md border px-3 py-1.5 text-xs font-medium disabled:opacity-45",
                          updateCheckChannel === channel &&
                            "border-primary bg-primary/10 text-primary",
                        )}
                      >
                        {t(channel === "stable" ? "updateChannelStable" : "updateChannelBeta")}
                      </button>
                    ))}
                  </div>
                  <p className="mb-0 mt-2 text-[11.5px] leading-snug text-muted-foreground">
                    {t("setupUpdateDisclosure")}
                  </p>
                </div>
              </section>
            )}

            {isScanStep && (
              <section>
                <h1 className="m-0 text-lg font-semibold">{t("setupScanTitle")}</h1>
                <p className="mb-4 mt-0.5 text-[13px] text-muted-foreground">
                  {t("setupScanLead")}
                </p>
                <div className="rounded-lg border bg-muted/25 p-3">
                  <p className="m-0 text-sm font-semibold">
                    {t("setupScanStatus", {
                      status: scanJob ? jobStatusLabel(scanJob.status, t) : t("jobStatusQueued"),
                    })}
                  </p>
                  <p className="m-0 mt-1 text-[13px] text-muted-foreground">
                    {scanJob && jobProgressDetail(scanJob, t)
                      ? jobProgressDetail(scanJob, t)
                      : t("setupScanWaiting")}
                  </p>
                </div>
              </section>
            )}

            <div className="mt-[18px] flex items-center gap-2">
              {step > 0 && !bootstrapped && (
                <Button type="button" variant="outline" onClick={onBack} disabled={busy}>
                  {t("setupBack")}
                </Button>
              )}
              <Button type="submit" disabled={busy}>
                {busy ? t("setupWorking") : isLast ? t("setupBrowseReady") : t("setupContinue")}
              </Button>
              <span className="ml-auto text-xs text-muted-foreground">
                {t("setupStepCount", { n: step + 1 })}
              </span>
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
