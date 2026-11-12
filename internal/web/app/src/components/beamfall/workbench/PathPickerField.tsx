import {
  ArrowUp,
  ChevronRight,
  Folder,
  FolderOpen,
  FolderSearch,
  HardDrive,
  Lock,
  TriangleAlert,
  X,
} from "lucide-react";
import {
  Fragment,
  type KeyboardEvent,
  type ReactNode,
  useEffect,
  useId,
  useRef,
  useState,
} from "react";
import { CompositeItem, CompositeRoot } from "@/components/ui/astryx-primitives";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { getBeamfallHost } from "@/lib/beamfallHost";
import { addPathPickerRoot, fetchPathPicker } from "@/lib/graphql";
import type {
  PathPickerEntryInfo,
  PathPickerInfo,
  PathPickerRole,
  PathPickerRootInfo,
} from "@/lib/graphql/types";
import { useT } from "@/lib/i18n";
import { cn } from "@/lib/utils";
import type { StringKey } from "@/strings";

// Folder browser for server paths (CORE-1142). The server's pathPicker query
// answers one folder at a time: with no path it lists the browsable locations
// (roots); with a path it returns that folder as entries[0] and its contents in
// entries[0].children. This component shows one level at a time: a breadcrumb,
// an Up control, the subfolders (click to open) and one action that commits the
// folder being shown.

const LOCATIONS = "";

function separatorOf(path: string): string {
  return path.includes("\\") && !path.includes("/") ? "\\" : "/";
}

function isWithin(root: string, path: string): boolean {
  if (path === root) return true;
  const sep = separatorOf(root);
  return path.startsWith(root.endsWith(sep) ? root : root + sep);
}

/** The browsable location that contains path (the longest match), if any. */
export function pathPickerRootFor(
  path: string,
  roots: PathPickerRootInfo[],
): PathPickerRootInfo | undefined {
  let best: PathPickerRootInfo | undefined;
  for (const root of roots) {
    if (isWithin(root.path, path) && (!best || root.path.length > best.path.length)) best = root;
  }
  return best;
}

/** The folder above path, or "" (the list of locations) from a top-level location. */
export function pathPickerParent(path: string, roots: PathPickerRootInfo[]): string {
  const root = pathPickerRootFor(path, roots);
  if (!root || path === root.path) return LOCATIONS;
  const parent = path.slice(0, path.lastIndexOf(separatorOf(path)));
  return parent.length > root.path.length ? parent : root.path;
}

interface Crumb {
  label: string;
  path: string;
}

/** Breadcrumb for path: its location first, then one crumb per folder below it. */
export function pathPickerCrumbs(path: string, roots: PathPickerRootInfo[]): Crumb[] {
  if (!path) return [];
  const root = pathPickerRootFor(path, roots);
  if (!root) return [{ label: path, path }];
  const sep = separatorOf(path);
  const crumbs: Crumb[] = [{ label: root.path, path: root.path }];
  let acc = root.path.endsWith(sep) ? root.path.slice(0, -1) : root.path;
  for (const part of path.slice(root.path.length).split(sep).filter(Boolean)) {
    acc += sep + part;
    crumbs.push({ label: part, path: acc });
  }
  return crumbs;
}

const REASON_TEXT: Record<NonNullable<PathPickerEntryInfo["reasonCode"]>, StringKey> = {
  "outside-roots": "pathPickerOutsideRoots",
  "server-cannot-see-path": "pathPickerNotVisible",
  "permission-denied": "pathPickerNoPermission",
  blocklisted: "pathPickerReserved",
  "overlaps-library-root": "pathPickerOverlapsLibrary",
  "not-inside-library-root": "pathPickerNotInLibrary",
};

// Reasons that mean the folder itself cannot be listed, so the body shows the
// reason instead of subfolders.
const BLOCKING = new Set(["outside-roots", "server-cannot-see-path", "blocklisted"]);

function hostDistributionClass(): string | undefined {
  const host = getBeamfallHost();
  if (host?.clientClass === "ipad-manager-app-store") return "appStore";
  if (host?.clientClass === "apple-manager-macos-direct") return "directDistribution";
  return undefined;
}

interface View {
  path: string;
  info: PathPickerInfo;
}

interface Row {
  key: string;
  path: string;
  name: string;
  meta: string;
  location: boolean;
  locked: boolean;
}

export function PathPickerField({
  id,
  label,
  role,
  value,
  onChange,
  libraryID,
  placeholder,
  disabled,
  className,
  inputClassName,
  suppressAppStoreDownload = false,
  actions,
  selectLabel,
  onSelect,
}: {
  id: string;
  label: ReactNode;
  role: PathPickerRole;
  value: string;
  onChange: (value: string) => void;
  libraryID?: string;
  placeholder?: string;
  disabled?: boolean;
  className?: string;
  inputClassName?: string;
  suppressAppStoreDownload?: boolean;
  /** Extra controls for the input row (the browser opens below the whole row). */
  actions?: ReactNode;
  /** Label for the action that commits the shown folder. Defaults to "Use this folder". */
  selectLabel?: string;
  /** Commit handler. Defaults to writing the folder into the field. */
  onSelect?: (path: string) => void | Promise<void>;
}) {
  const t = useT();
  const panelID = useId();
  const distributionClass = hostDistributionClass();
  const suppressed =
    suppressAppStoreDownload && role === "downloadDestination" && distributionClass === "appStore";
  const [open, setOpen] = useState(false);
  const [view, setView] = useState<View | null>(null);
  const [current, setCurrent] = useState(0);
  const [busy, setBusy] = useState(false);
  const [failed, setFailed] = useState<{ path: string; message: string } | null>(null);
  const request = useRef(0);
  const refocus = useRef(false);
  const browseButton = useRef<HTMLButtonElement>(null);
  const list = useRef<HTMLUListElement>(null);
  const upButton = useRef<HTMLButtonElement>(null);
  const crumbBar = useRef<HTMLOListElement>(null);

  // After each navigation put focus on the first folder (or Up when there is
  // none) so keyboard users continue from the new level, and keep the deepest
  // breadcrumb in view on narrow screens.
  useEffect(() => {
    if (crumbBar.current) crumbBar.current.scrollLeft = crumbBar.current.scrollWidth;
    if (!refocus.current) return;
    refocus.current = false;
    const first = list.current?.querySelector<HTMLButtonElement>("button:not(:disabled)");
    (first ?? upButton.current)?.focus();
  }, [view]);

  if (suppressed) return null;

  async function load(path: string) {
    const seq = ++request.current;
    setBusy(true);
    setFailed(null);
    try {
      const info = await fetchPathPicker({
        role,
        path: path || undefined,
        libraryID,
        distributionClass,
        depth: 1,
      });
      if (seq !== request.current) return;
      refocus.current = true;
      setView({ path, info });
      setCurrent(0);
    } catch (err) {
      if (seq !== request.current) return;
      setFailed({
        path,
        message: err instanceof Error && err.message ? err.message : t("pathPickerLoadFailed"),
      });
    } finally {
      if (seq === request.current) setBusy(false);
    }
  }

  function close() {
    request.current++;
    setOpen(false);
    setBusy(false);
    browseButton.current?.focus();
  }

  async function allowBrowsing(path: string) {
    const seq = ++request.current;
    setBusy(true);
    setFailed(null);
    try {
      const root = await addPathPickerRoot(path);
      if (seq !== request.current) return;
      await load(root.path);
    } catch (err) {
      if (seq !== request.current) return;
      setBusy(false);
      setFailed({
        path,
        message: err instanceof Error && err.message ? err.message : t("pathPickerLoadFailed"),
      });
    }
  }

  async function commit(path: string) {
    if (onSelect) await onSelect(path);
    else onChange(path);
    close();
  }

  const roots = view?.info.roots ?? [];
  const roleAvailable = !view || view.info.roles.includes(role);
  const atLocations = !view || view.path === LOCATIONS;
  const node = atLocations ? undefined : view?.info.entries[0];
  const blocked = node?.reasonCode && BLOCKING.has(node.reasonCode) ? node.reasonCode : undefined;
  const unreadable = node?.accessError !== undefined && node.accessError !== "none";
  const crumbs = view ? pathPickerCrumbs(view.path, roots) : [];
  const parent = view && !atLocations ? pathPickerParent(view.path, roots) : null;

  const rows: Row[] = atLocations
    ? roots.map((root) => ({
        key: root.path,
        path: root.path,
        name: root.name,
        // A location named after its own last path segment needs no second line.
        meta: root.path.endsWith(separatorOf(root.path) + root.name) ? "" : root.path,
        location: true,
        locked: false,
      }))
    : (node?.children ?? [])
        .filter((child) => child.kind === "folder")
        .map((child) => {
          const locked = child.accessError !== "none";
          let meta = "";
          if (locked) meta = t("pathPickerNoAccess");
          else if (child.mediaCount === 1) meta = t("pathPickerOneMediaFile");
          else if (child.mediaCount > 1)
            meta = t("pathPickerMediaFiles", { count: child.mediaCount });
          return {
            key: child.path,
            path: child.path,
            name: child.name,
            meta,
            location: false,
            locked,
          };
        });

  let status = "";
  if (busy) status = t("pathPickerLoading");
  else if (view && roleAvailable && !blocked) {
    status = atLocations
      ? t("pathPickerLocationsStatus", { count: rows.length })
      : t("pathPickerFoldersStatus", { count: rows.length, path: view.path });
  }

  const canUse = !!node && node.selectable && !blocked && !busy;
  let footNote = "";
  if (atLocations) footNote = t("pathPickerPickLocation");
  else if (node && !node.selectable && !blocked && node.reasonCode)
    footNote = t(REASON_TEXT[node.reasonCode]);

  return (
    <div className={cn("space-y-1", className)}>
      <Label htmlFor={id}>{label}</Label>
      <div className="flex flex-wrap items-center gap-2">
        <Input
          id={id}
          value={value}
          autoComplete="off"
          spellCheck={false}
          placeholder={placeholder}
          disabled={disabled}
          onChange={(e) => onChange(e.target.value)}
          className={inputClassName}
        />
        <Button
          ref={browseButton}
          type="button"
          size="sm"
          variant="outline"
          disabled={disabled}
          aria-expanded={open}
          aria-controls={open ? panelID : undefined}
          onClick={() => {
            if (open) {
              close();
              return;
            }
            setOpen(true);
            void load(value.trim());
          }}
        >
          <FolderOpen className="console-nav-icon" aria-hidden />
          {t("browse")}
        </Button>
        {actions}
      </div>
      {open && (
        <div
          id={panelID}
          role="group"
          aria-label={t("pathPickerBrowser")}
          className="path-browser"
          onKeyDown={(e) => {
            if (e.key !== "Escape") return;
            e.stopPropagation();
            close();
          }}
        >
          <div className="path-browser-head">
            <button
              ref={upButton}
              type="button"
              className="path-browser-icon-button"
              aria-label={t("pathPickerUp")}
              title={t("pathPickerUp")}
              disabled={parent === null || busy}
              onClick={() => parent !== null && void load(parent)}
            >
              <ArrowUp aria-hidden />
            </button>
            <nav aria-label={t("pathPickerLocation")} className="path-browser-crumbs-wrap">
              <ol ref={crumbBar} className="path-browser-crumbs">
                <li>
                  {atLocations ? (
                    <span aria-current="location" className="path-browser-crumb is-current">
                      {t("pathPickerLocations")}
                    </span>
                  ) : (
                    <button
                      type="button"
                      className="path-browser-crumb"
                      onClick={() => void load(LOCATIONS)}
                    >
                      {t("pathPickerLocations")}
                    </button>
                  )}
                </li>
                {crumbs.map((crumb, i) => (
                  <Fragment key={crumb.path}>
                    <li aria-hidden className="path-browser-crumb-sep">
                      <ChevronRight />
                    </li>
                    <li>
                      {i === crumbs.length - 1 ? (
                        <span aria-current="location" className="path-browser-crumb is-current">
                          {crumb.label}
                        </span>
                      ) : (
                        <button
                          type="button"
                          className="path-browser-crumb"
                          onClick={() => void load(crumb.path)}
                        >
                          {crumb.label}
                        </button>
                      )}
                    </li>
                  </Fragment>
                ))}
              </ol>
            </nav>
            <button
              type="button"
              className="path-browser-icon-button"
              aria-label={t("pathPickerClose")}
              title={t("pathPickerClose")}
              onClick={close}
            >
              <X aria-hidden />
            </button>
          </div>

          <div className="path-browser-body" aria-busy={busy} data-busy={busy || undefined}>
            {!view && !failed && <BrowserSkeleton />}
            {view && !roleAvailable && (
              <BrowserMessage icon={<TriangleAlert aria-hidden />} tone="warn">
                {t("pathPickerUnavailable")}
              </BrowserMessage>
            )}
            {view && roleAvailable && blocked && (
              <BrowserMessage
                icon={
                  blocked === "outside-roots" ? <Lock aria-hidden /> : <TriangleAlert aria-hidden />
                }
                tone="warn"
                title={view.path}
              >
                {blocked === "server-cannot-see-path" && view.info.runtime === "container"
                  ? t("pathPickerNotVisibleContainer")
                  : t(REASON_TEXT[blocked as keyof typeof REASON_TEXT])}
                {blocked === "outside-roots" && (
                  <Button
                    type="button"
                    size="sm"
                    variant="outline"
                    className="mt-3"
                    disabled={busy}
                    onClick={() => void allowBrowsing(view.path)}
                  >
                    {t("pathPickerAllow")}
                  </Button>
                )}
              </BrowserMessage>
            )}
            {view && roleAvailable && !blocked && unreadable && (
              <BrowserMessage icon={<Lock aria-hidden />} tone="warn" title={view.path}>
                {t("pathPickerNoPermission")}
              </BrowserMessage>
            )}
            {view && roleAvailable && !blocked && !unreadable && rows.length === 0 && (
              <BrowserMessage icon={<FolderSearch aria-hidden />}>
                {atLocations ? t("pathPickerNoLocations") : t("pathPickerEmpty")}
              </BrowserMessage>
            )}
            {view && roleAvailable && !blocked && !unreadable && rows.length > 0 && (
              <CompositeRoot<Record<string, never>, CompositeRoot.State>
                key={view.path}
                tag="ul"
                orientation="vertical"
                loopFocus={false}
                enableHomeAndEndKeys
                rootRef={list}
                highlightedIndex={current}
                onHighlightedIndexChange={(index) => {
                  // A row focused before the list has numbered it reports -1.
                  if (index >= 0) setCurrent(index);
                }}
                props={[
                  {
                    className: "path-browser-list",
                    "aria-label": atLocations
                      ? t("pathPickerLocations")
                      : t("pathPickerFoldersIn", { path: view.path }),
                    onKeyDown: (e: KeyboardEvent) => {
                      if (e.key !== "Backspace" || parent === null) return;
                      e.preventDefault();
                      void load(parent);
                    },
                  },
                ]}
              >
                {rows.map((row) => (
                  <li key={row.key}>
                    <CompositeItem<Record<string, never>, CompositeItem.State>
                      tag="button"
                      props={[
                        {
                          type: "button",
                          className: "path-browser-row",
                          disabled: row.locked,
                          onClick: () => void load(row.path),
                        },
                      ]}
                    >
                      <span className="path-browser-tile" aria-hidden>
                        {row.locked ? <Lock /> : row.location ? <HardDrive /> : <Folder />}
                      </span>
                      <span className="path-browser-row-text">
                        <span className="path-browser-row-name">
                          {row.location && !row.meta ? row.path : row.name}
                        </span>
                        {row.meta && (
                          <span className={cn("path-browser-row-meta", row.location && "is-path")}>
                            {row.meta}
                          </span>
                        )}
                      </span>
                      {!row.locked && (
                        <ChevronRight className="path-browser-row-chevron" aria-hidden />
                      )}
                    </CompositeItem>
                  </li>
                ))}
              </CompositeRoot>
            )}
          </div>

          {failed && (
            <div role="alert" className="path-browser-error">
              <TriangleAlert aria-hidden />
              <span className="min-w-0 flex-1">{failed.message}</span>
              <Button
                type="button"
                size="sm"
                variant="outline"
                disabled={busy}
                onClick={() => void load(failed.path)}
              >
                {t("pathPickerRetry")}
              </Button>
            </div>
          )}

          <div className="path-browser-foot">
            <p className="path-browser-foot-text">
              {!atLocations && view && <span className="path-browser-foot-path">{view.path}</span>}
              {footNote && <span className="path-browser-foot-note">{footNote}</span>}
            </p>
            <Button
              type="button"
              size="sm"
              disabled={!canUse}
              onClick={() => view && void commit(view.path)}
            >
              {selectLabel ?? t("pathPickerUse")}
            </Button>
          </div>
          <p className="sr-only" role="status">
            {status}
          </p>
        </div>
      )}
    </div>
  );
}

function BrowserSkeleton() {
  return (
    <div className="path-browser-skeleton" aria-hidden>
      <span />
      <span />
      <span />
    </div>
  );
}

function BrowserMessage({
  icon,
  tone,
  title,
  children,
}: {
  icon: ReactNode;
  tone?: "warn";
  title?: string;
  children: ReactNode;
}) {
  return (
    <div className="path-browser-message" data-tone={tone}>
      <span className="path-browser-message-icon">{icon}</span>
      {title && <p className="path-browser-message-title">{title}</p>}
      <div className="path-browser-message-text">{children}</div>
    </div>
  );
}
