import {
  Archive,
  Bookmark,
  FileCog,
  FileQuestion,
  FolderTree,
  ImageIcon,
  LibraryBig,
  ListChecks,
  ListTodo,
  Lock,
  type LucideProps,
  PanelsTopLeft,
  PencilLine,
  Rss,
  Shapes,
  ShieldAlert,
  Sparkles,
  Tags,
  Trash2,
  UserCog,
} from "lucide-react";
import {
  type ComponentType,
  type ReactNode,
  useCallback,
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
} from "react";
import { Navigate, NavLink, Route, Routes, useLocation } from "react-router";
import { AppShell } from "@/components/chrome/AppShell";
import { setAdminUnlockHandler } from "@/lib/api";
import { useT } from "@/lib/i18n";
import { useRegister } from "@/lib/register";
import { useSession } from "@/lib/session";
import type { StringKey } from "@/strings";
import { AdminGate, type AdminGateCopy } from "../console/AdminGate";
import "./manage.css";
import { ArtworkPage } from "./pages/ArtworkPage";
import { BackupExportPage } from "./pages/BackupExportPage";
import { BulkOpsPage } from "./pages/BulkOpsPage";
import { CollectionsPage } from "./pages/CollectionsPage";
import { ConvertPage } from "./pages/ConvertPage";
import { CurationPage } from "./pages/CurationPage";
import { EntityPage } from "./pages/EntityPage";
import { FieldLocksPage } from "./pages/FieldLocksPage";
import { LibrariesPage } from "./pages/LibrariesPage";
import { MarkersPage } from "./pages/MarkersPage";
import { NavOverridePage } from "./pages/NavOverridePage";
import { OperationsPage } from "./pages/OperationsPage";
import { ProfilePage } from "./pages/ProfilePage";
import { RenamePage } from "./pages/RenamePage";
import { RestrictionsPage } from "./pages/RestrictionsPage";
import { SubscriptionsPage } from "./pages/SubscriptionsPage";
import { TagsPage } from "./pages/TagsPage";
import { TrashPage } from "./pages/TrashPage";
import { UnmatchedPage } from "./pages/UnmatchedPage";

// MANAGE (ADR-0007 §3): the promoted browser's manager surface, rendered in the
// Workbench register. Every operation is manager-gated server-side; this area is
// only an affordance — the server is the authority on every mutation. Server
// strings render as React text only (never HTML, ADR-0012 §5). The nested routes
// are disjoint manage surfaces; this index wires them together and owns the
// register.
// The left-nav, grouped into operator clusters (Library / Curation / Access).
// Grouping is visual only — a keyline between clusters — so no new domain
// strings are introduced; the labels stay the existing localized keys.
// A nav item labels itself either by a localized string key (`label`) or, for the
// powertools surfaces that have no global string key yet, a literal `text`.
type NavItem = { to: string; icon: ComponentType<LucideProps> } & (
  | { label: StringKey; text?: undefined }
  | { text: string; label?: undefined }
);
const NAV_GROUPS: NavItem[][] = [
  [
    { to: "/manage/libraries", icon: LibraryBig, label: "libraries" },
    { to: "/manage/operations", icon: ListChecks, label: "jobs" },
    { to: "/manage/convert", icon: FileCog, label: "convert" },
    { to: "/manage/rename", icon: PencilLine, label: "rename" },
    { to: "/manage/trash", icon: Trash2, label: "trashPolicyHeading" },
    { to: "/manage/backups", icon: Archive, label: "backupHeading" },
    { to: "/manage/subscriptions", icon: Rss, label: "subscriptions" },
    { to: "/manage/unmatched", icon: FileQuestion, label: "unmatchedHeading" },
  ],
  [
    { to: "/manage/collections", icon: FolderTree, label: "collections" },
    { to: "/manage/bulk", icon: ListTodo, label: "bulkOpsHeading" },
    { to: "/manage/tags", icon: Tags, label: "tagsHeading" },
    { to: "/manage/curation", icon: Sparkles, label: "curationHeading" },
    { to: "/manage/entities", icon: Shapes, label: "manageTitle" },
    { to: "/manage/artwork", icon: ImageIcon, label: "artworkCandidates" },
    { to: "/manage/markers", icon: Bookmark, label: "markersHeading" },
    { to: "/manage/field-locks", icon: Lock, label: "fieldLocksHeading" },
    { to: "/manage/navigation", icon: PanelsTopLeft, label: "navOverrideHeading" },
  ],
  [
    { to: "/manage/restrictions", icon: ShieldAlert, label: "restrictionsTitle" },
    { to: "/manage/profile", icon: UserCog, label: "profilesTitle" },
  ],
];

// The page title of the current route: the nav label of the item whose path
// the location is on or under, falling back to the area title.
function currentRouteLabel(pathname: string): { label?: StringKey; text?: string } {
  const item = NAV_GROUPS.flat().find(
    (entry) => pathname === entry.to || pathname.startsWith(`${entry.to}/`),
  );
  return item ?? { label: "manageTitle" };
}

const MANAGE_LOCK_COPY: AdminGateCopy = {
  title: "manageLockedTitle",
  lead: "manageLockedLead",
  submit: "manageUnlockSubmit",
};

// Some Manage pages read or change server settings, which the server allows
// only under an Admin Session (ADR-0007 §4) — a manager without one is refused
// with "admin session required". Which operations those are is the server's
// call, so this keeps no list: when a request the current page sent is refused
// that way, the Console's own unlock gate is shown over the page and the
// request waits. Unlocking sends it again, so the page loads its data, or
// finishes the action the manager asked for, in place and with whatever they
// had entered still there. Pages that never need an Admin Session are never
// gated, and each page gets its own lock so leaving one abandons its prompt.
function ManageAdminLock({ children }: { children: ReactNode }) {
  const { pathname } = useLocation();
  return <PageAdminLock key={pathname}>{children}</PageAdminLock>;
}

function PageAdminLock({ children }: { children: ReactNode }) {
  // null until a request is refused. A refusal that arrives while the gate
  // believes it is open (the session was ended elsewhere) changes the number,
  // which mounts the gate afresh so it reads the session again.
  const [gate, setGate] = useState<number | null>(null);
  const [unlocked, setUnlocked] = useState(false);
  const prompting = useRef(false);
  const waiting = useRef<Array<(unlocked: boolean) => void>>([]);

  const settle = useCallback((result: boolean) => {
    for (const resolve of waiting.current.splice(0)) resolve(result);
  }, []);

  // A layout effect, so the handler is in place before the page's own effects
  // send their first requests.
  useLayoutEffect(() => {
    let alive = true;
    setAdminUnlockHandler(() => {
      if (!alive) return Promise.resolve(false);
      return new Promise<boolean>((resolve) => {
        waiting.current.push(resolve);
        if (prompting.current) return;
        prompting.current = true;
        setUnlocked(false);
        setGate((current) => (current ?? 0) + 1);
      });
    });
    return () => {
      alive = false;
      setAdminUnlockHandler();
      settle(false);
    };
  }, [settle]);

  const onUnlockedChange = useCallback(
    (now: boolean) => {
      prompting.current = !now;
      setUnlocked(now);
      if (now) settle(true);
    },
    [settle],
  );

  return (
    <>
      {gate !== null && (
        <AdminGate key={gate} copy={MANAGE_LOCK_COPY}>
          <AdminUnlocked onChange={onUnlockedChange} />
        </AdminGate>
      )}
      {/* Kept mounted behind the prompt so nothing the manager entered is lost. */}
      <div className={gate !== null && !unlocked ? "hidden" : "contents"}>{children}</div>
    </>
  );
}

/** Rendered by the gate only while its Admin Session is open. */
function AdminUnlocked({ onChange }: { onChange: (unlocked: boolean) => void }) {
  useEffect(() => {
    onChange(true);
    return () => onChange(false);
  }, [onChange]);
  return null;
}

export function ManageArea() {
  const { setRegister } = useRegister();
  const location = useLocation();
  const { isManager, loading, needsEnroll, session } = useSession();
  const t = useT();
  useEffect(() => setRegister("manage"), [setRegister]);
  const routeTitle = currentRouteLabel(location.pathname);

  if (loading) {
    return (
      <AppShell>
        <p className="text-sm text-muted-foreground">{t("loading")}</p>
      </AppShell>
    );
  }

  if (needsEnroll || !isManager || session?.profile !== "adult") {
    return <Navigate to="/" replace />;
  }

  return (
    <AppShell>
      <div className="manage-area flex flex-col gap-5 md:flex-row md:gap-6">
        <nav aria-label={t("manageTitle")} className="manage-nav md:w-52 md:shrink-0">
          {NAV_GROUPS.map((group, i) => (
            <div key={i} className="manage-nav-group">
              {group.map((item) => (
                // NavLink sets aria-current="page" on the active <a>; the
                // Workbench active treatment (beam left-edge marker, raised
                // fill) keys off that attribute in manage.css — so the visual
                // state and the assistive-tech state share one source of truth.
                <NavLink key={item.to} to={item.to} className="manage-nav-link">
                  <item.icon className="manage-nav-icon" aria-hidden />
                  {item.text ?? t(item.label)}
                </NavLink>
              ))}
            </div>
          ))}
        </nav>

        <div className="min-w-0 grow">
          {/* The route's one h1; each ManageSection card title below it is an h2. */}
          <h1 className="sr-only">{routeTitle.text ?? t(routeTitle.label ?? "manageTitle")}</h1>
          <ManageAdminLock>
            <Routes>
              <Route index element={<Navigate to="/manage/libraries" replace />} />
              <Route path="libraries" element={<LibrariesPage />} />
              <Route path="operations" element={<OperationsPage />} />
              <Route path="convert" element={<ConvertPage />} />
              <Route path="rename" element={<RenamePage />} />
              <Route path="trash" element={<TrashPage />} />
              <Route path="backups" element={<BackupExportPage />} />
              <Route path="subscriptions" element={<SubscriptionsPage />} />
              <Route path="collections" element={<CollectionsPage />} />
              <Route path="bulk" element={<BulkOpsPage />} />
              <Route path="tags" element={<TagsPage />} />
              <Route path="curation" element={<CurationPage />} />
              <Route path="entities" element={<EntityPage />} />
              <Route path="artwork" element={<ArtworkPage />} />
              <Route path="markers" element={<MarkersPage />} />
              <Route path="field-locks" element={<FieldLocksPage />} />
              <Route path="navigation" element={<NavOverridePage />} />
              <Route path="unmatched" element={<UnmatchedPage />} />
              <Route path="restrictions" element={<RestrictionsPage />} />
              <Route path="profile" element={<ProfilePage />} />
              <Route path="*" element={<Navigate to="/manage/libraries" replace />} />
            </Routes>
          </ManageAdminLock>
        </div>
      </div>
    </AppShell>
  );
}
