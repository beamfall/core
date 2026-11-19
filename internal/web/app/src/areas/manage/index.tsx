import {
  Archive,
  Bookmark,
  FileCog,
  FilePen,
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
import { matchRoutes, Navigate, NavLink, Route, Routes, useLocation } from "react-router";
import { IconHeading } from "@/components/beamfall/workbench";
import { AppShell } from "@/components/chrome/AppShell";
import { SectionMenu } from "@/components/chrome/SectionMenu";
import { RevealSetting, SettingsMenuIntro } from "@/components/chrome/SettingsMenuIntro";
import { AdvancedSettingsSwitch, useAdvancedSettings } from "@/lib/advancedSettings";
import { setAdminUnlockHandler } from "@/lib/api";
import { useT } from "@/lib/i18n";
import { manageWorkflow, useManageCapabilities } from "@/lib/manageCapabilities";
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
import { ItemMetadataPage } from "./pages/ItemMetadataPage";
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
// powertools surfaces that have no global string key yet, a literal `text`. Its
// icon, label and one-line `lead` also make the page's heading (CORE-1125).
// `advanced` items are expert destinations the Normal view leaves out of the nav
// (CORE-1117); their routes stay registered, so a link or bookmark still works.
// `hidden` items are never in the nav: an old route kept so a bookmark still
// opens, whose page now lives in the Console (CORE-2445).
type NavItem = {
  to: string;
  icon: ComponentType<LucideProps>;
  lead: StringKey;
  advanced?: true;
  hidden?: true;
} & ({ label: StringKey; text?: undefined } | { text: string; label?: undefined });
// Exported so the test can check the settings search's MENU_PAGES against it.
export const MANAGE_NAV_GROUPS: NavItem[][] = [
  [
    { to: "/manage/libraries", icon: LibraryBig, label: "libraries", lead: "manageLibrariesLead" },
    { to: "/manage/operations", icon: ListChecks, label: "jobs", lead: "manageJobsLead" },
    {
      to: "/manage/convert",
      icon: FileCog,
      label: "convert",
      lead: "convertLead",
      advanced: true,
    },
    {
      to: "/manage/rename",
      icon: PencilLine,
      label: "rename",
      lead: "renameLead",
      advanced: true,
    },
    { to: "/manage/trash", icon: Trash2, label: "navTrash", lead: "manageTrashLead" },
    {
      to: "/manage/backups",
      icon: Archive,
      label: "backupHeading",
      lead: "manageBackupsLead",
      hidden: true,
    },
    {
      to: "/manage/subscriptions",
      icon: Rss,
      label: "subscriptions",
      lead: "manageSubscriptionsLead",
    },
    {
      to: "/manage/unmatched",
      icon: FileQuestion,
      label: "unmatchedHeading",
      lead: "unmatchedLead",
    },
  ],
  [
    {
      to: "/manage/collections",
      icon: FolderTree,
      label: "collections",
      lead: "manageCollectionsLead",
    },
    {
      to: "/manage/bulk",
      icon: ListTodo,
      label: "bulkOpsHeading",
      lead: "bulkOpsLead",
      advanced: true,
    },
    { to: "/manage/tags", icon: Tags, label: "tagsHeading", lead: "tagsLead" },
    {
      to: "/manage/curation",
      icon: Sparkles,
      label: "curationHeading",
      lead: "curationLead",
      advanced: true,
    },
    {
      to: "/manage/entities",
      icon: Shapes,
      label: "manageEntitiesTitle",
      lead: "manageEntitiesLead",
    },
    { to: "/manage/items", icon: FilePen, label: "itemMetadataHeading", lead: "itemMetadataLead" },
    {
      to: "/manage/artwork",
      icon: ImageIcon,
      label: "artworkCandidates",
      lead: "manageArtworkLead",
    },
    {
      to: "/manage/markers",
      icon: Bookmark,
      label: "markersHeading",
      lead: "markersLead",
      advanced: true,
    },
    {
      to: "/manage/field-locks",
      icon: Lock,
      label: "fieldLocksHeading",
      lead: "fieldLocksLead",
      advanced: true,
    },
    {
      to: "/manage/navigation",
      icon: PanelsTopLeft,
      label: "navOverrideHeading",
      lead: "navOverrideLead",
      advanced: true,
    },
  ],
  [
    {
      to: "/manage/restrictions",
      icon: ShieldAlert,
      label: "navUnlockRestricted",
      lead: "restrictionsLead",
    },
    { to: "/manage/profile", icon: UserCog, label: "navSwitchProfile", lead: "profilesLead" },
  ],
];

// The nav item of the current route: the one whose path the location is on or
// under, falling back to the area itself.
const AREA_ROUTE: NavItem = {
  to: "/manage",
  icon: LibraryBig,
  label: "manageTitle",
  lead: "manageLead",
};
const MANAGE_NAV_ROUTES = MANAGE_NAV_GROUPS.flat().map((item) => ({
  path: `${item.to}/*`,
  item,
}));

function currentRoute(pathname: string): NavItem {
  // Use the same path decoding and case matching as the rendered Routes.
  return matchRoutes(MANAGE_NAV_ROUTES, pathname)?.[0]?.route.item ?? AREA_ROUTE;
}

// The Normal nav drops Advanced items, except the page being viewed, so a deep
// link never lands on a page missing from its own nav, and drops any group left
// empty. Advanced shows every group whole, less the hidden items.
function visibleNavGroups(
  advanced: boolean,
  current: NavItem,
  enabled: (id: string | undefined) => boolean,
): NavItem[][] {
  return MANAGE_NAV_GROUPS.map((group) =>
    group.filter(
      (item) =>
        !item.hidden &&
        enabled(manageWorkflow(item.to)) &&
        (advanced || !item.advanced || current === item),
    ),
  ).filter((group) => group.length > 0);
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
type ManageAdminLockProps = { children: ReactNode; onUnlock: () => void };

function ManageAdminLock(props: ManageAdminLockProps) {
  const { pathname } = useLocation();
  return <PageAdminLock key={pathname} {...props} />;
}

function PageAdminLock({ children, onUnlock }: ManageAdminLockProps) {
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
      if (now) {
        settle(true);
        onUnlock();
      }
    },
    [onUnlock, settle],
  );

  const locked = gate !== null && !unlocked;
  return (
    <>
      {gate !== null && (
        <AdminGate key={gate} copy={MANAGE_LOCK_COPY}>
          <AdminUnlocked onChange={onUnlockedChange} />
        </AdminGate>
      )}
      {/* Kept mounted behind the prompt so nothing the manager entered is lost.
          Inert while locked, so nothing behind the prompt takes focus and a
          settings search waits to reveal its setting until the page shows. */}
      <div className={locked ? "hidden" : "contents"} inert={locked}>
        {children}
      </div>
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
  const { isManager, loading, needsEnroll, session } = useSession();
  const t = useT();
  useEffect(() => setRegister("manage"), [setRegister]);

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

  return <ManageWorkspace key={session.profileID} />;
}

function ManageWorkspace() {
  const { pathname } = useLocation();
  const { session } = useSession();
  const t = useT();
  const [advanced] = useAdvancedSettings();
  const [unlockCount, setUnlockCount] = useState(0);
  const onUnlock = useCallback(() => setUnlockCount((count) => count + 1), []);
  const {
    loading: capabilitiesLoading,
    enabled,
    visibleManagePage,
  } = useManageCapabilities(unlockCount);
  const route = currentRoute(pathname);
  const capability = manageWorkflow(route.to);
  const routeTitle = route.text ?? t(route.label);
  const routeLead =
    route.to === "/manage/restrictions" && session?.revealPinSet === false
      ? "restrictionsSetupSubtitle"
      : route.lead;
  if (capability && capabilitiesLoading) {
    return (
      <AppShell>
        <p className="text-sm text-muted-foreground">{t("loading")}</p>
      </AppShell>
    );
  }
  if (!enabled(capability)) return <Navigate to="/manage/libraries" replace />;

  return (
    <AppShell>
      <div className="manage-area flex flex-col gap-5 md:flex-row md:gap-6">
        <SectionMenu
          label={t("manageTitle")}
          className="md:w-52 md:shrink-0"
          current={routeTitle}
          footer={<AdvancedSettingsSwitch className="manage-advanced-switch" />}
        >
          <SettingsMenuIntro area="manage" visibleManagePage={visibleManagePage} />
          <div className="manage-nav">
            {visibleNavGroups(advanced, route, enabled).map((group, i) => (
              <div key={i} className="manage-nav-group">
                {group.map((item) => (
                  // NavLink sets aria-current="page" on the active <a>; the
                  // Workbench active treatment (raised fill) keys off that attribute in manage.css — so the visual
                  // state and the assistive-tech state share one source of truth.
                  <NavLink key={item.to} to={item.to} className="manage-nav-link">
                    <item.icon className="manage-nav-icon" aria-hidden />
                    {item.text ?? t(item.label)}
                  </NavLink>
                ))}
              </div>
            ))}
          </div>
        </SectionMenu>

        <div className="min-w-0 grow">
          {/* The route's one h1 and its lead; each ManageSection card title below is an h2. */}
          <IconHeading icon={route.icon} title={routeTitle} lead={t(routeLead)} />
          <ManageAdminLock onUnlock={onUnlock}>
            <RevealSetting />
            <Routes>
              <Route index element={<Navigate to="/manage/libraries" replace />} />
              <Route
                path="libraries"
                element={
                  <LibrariesPage renameEnabled={!capabilitiesLoading && enabled("rename")} />
                }
              />
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
              <Route path="items" element={<ItemMetadataPage />} />
              <Route path="items/:mediaID" element={<ItemMetadataPage />} />
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
