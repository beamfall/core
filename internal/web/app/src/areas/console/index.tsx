import {
  Activity,
  Boxes,
  Clock,
  Database,
  FolderOpen,
  Gauge,
  HardDrive,
  History,
  Home,
  KeyRound,
  Layers,
  Lock,
  type LucideProps,
  Network,
  Plug,
  Rocket,
  ServerCog,
  ShieldCheck,
  Stethoscope,
  Users,
} from "lucide-react";
import { type ComponentType, type ReactElement, useEffect } from "react";
import { Link, Navigate, NavLink, Route, Routes, useLocation } from "react-router";
import { EmptyState, StatusChip } from "@/components/beamfall/workbench";
import { AppShell } from "@/components/chrome/AppShell";
import { SectionMenu } from "@/components/chrome/SectionMenu";
import { Button } from "@/components/ui/button";
import { AdvancedSettingsSwitch, useAdvancedSettings } from "@/lib/advancedSettings";
import { isBeamfallAppStoreHost } from "@/lib/beamfallHost";
import { fetchSetupState } from "@/lib/graphql";
import { useT } from "@/lib/i18n";
import { useRegister } from "@/lib/register";
import { useSession } from "@/lib/session";
import { cn } from "@/lib/utils";
import type { StringKey } from "@/strings";
import { BackupExportPage } from "../manage/pages/BackupExportPage";
import { AdminGate } from "./AdminGate";
import { OwnerTasks } from "./OwnerTasks";
import { Section } from "./Section";
import { AccessAdmin, ScheduleAdmin } from "./sections/AccessAdmin";
import { ActivityLog } from "./sections/ActivityLog";
import { ApiCredentials } from "./sections/ApiCredentials";
import { Capabilities } from "./sections/Capabilities";
import { Config } from "./sections/Config";
import { HostGraphs, LiveSessionCount, LiveSessions } from "./sections/Dashboard";
import { Health } from "./sections/Health";
import { Libraries } from "./sections/Libraries";
import { UpdateAwareness } from "./sections/Operations";
import { OperationsControls } from "./sections/OperationsControls";
import { PlayHistory } from "./sections/PlayHistory";
import { Plugins } from "./sections/Plugins";
import { MigrationWizard, RemoteAccess } from "./sections/RemoteAccess";
import { Retention } from "./sections/Retention";
import { SetupChecklist } from "./sections/SetupChecklist";
import { useAsync } from "./useAsync";
import "./console.css";

// The Console section nav (ADR-0007 §4 enumeration), grouped into operator
// clusters: getting the server set up, the content plane, who can see it, what
// it's doing right now, and how it's reached + automated. Each entry is a route
// under /console; `path === ""` is the index. Labels reuse existing keys.
interface NavItem {
  path: string;
  label: StringKey;
  icon: ComponentType<LucideProps>;
  element: ReactElement;
  placement: string;
  // Hidden from the rail in the Normal view unless it is the current page (CORE-1117).
  advanced?: boolean;
}

interface NavGroup {
  label: StringKey;
  items: NavItem[];
}

export const CONSOLE_IA_PLACEMENTS = [
  "0080:Overview",
  "0064:Library/Libraries",
  "0069:Library/Identification",
  "0070:Library/Plugins",
  "0071:Library/Layout & Navigation",
  "0074:Monitor/Live Sessions",
  "0007:Monitor/Play History",
  "0075:Monitor/Operations",
  "0076:Monitor/Operations",
  "0073:Monitor/Activity & Logs",
  "0042:Monitor/Library Health",
  "0061:Monitor/Library Health",
  "0086:Access/Profiles & Devices",
  "0081:Access/Profiles & Devices",
  "0087:Access/Schedules & Policy",
  "0012:Access/Remote Access",
  "0018:Access/Remote Access",
  "0025:Access/Remote Access",
  "0013:Access/API Keys",
  "0040:Access/API Keys",
  "0077:Maintain/Backup & Export",
  "0078:Maintain/Trash & Retention",
  "0051:Maintain/Trash & Retention",
  "0037:Maintain/Migration",
  "0079:Maintain/Updates",
  "0013:Maintain/Server Config",
] as const;

function Overview() {
  const t = useT();
  const { data, loading } = useAsync(fetchSetupState);
  const needsLibrary = !loading && data && !data.hasLibrary;
  const needsOwner = !loading && data && !data.hasOwnerProfile;
  const queue = [
    needsLibrary && {
      to: "/console/libraries",
      label: "consoleOverviewNeedsLibrary" as const,
    },
    needsOwner && {
      to: "/console/access",
      label: "consoleOverviewNeedsOwner" as const,
    },
  ].filter(Boolean) as { to: string; label: StringKey }[];

  const libraryState = needsLibrary ? "setupChecklistNext" : "consoleOverviewReady";

  return (
    <div className="space-y-10">
      <Section heading="consoleOverviewHeading" lead="consoleOverviewLead">
        <div className="console-status-strip">
          {(
            [
              ["consoleOverviewServer", "consoleOverviewReady"],
              ["libraries", libraryState],
              ["jobs", "consoleOverviewReady"],
            ] as const
          ).map(([label, state]) => (
            <div className="console-status-cell" key={label}>
              <span className="console-status-label">{t(label)}</span>
              <span className="console-status-value">
                <span
                  className="console-status-dot"
                  data-tone={state === "setupChecklistNext" ? "amber" : "green"}
                  aria-hidden
                />
                {t(state)}
              </span>
            </div>
          ))}
          <Link className="console-status-cell" to="/console/sessions">
            <span className="console-status-label">{t("liveSessionsHeading")}</span>
            <LiveSessionCount />
          </Link>
        </div>
      </Section>

      <Section heading="consoleNeedsAttentionHeading" lead="consoleNeedsAttentionLead">
        {queue.length === 0 ? (
          <EmptyState message="consoleNeedsAttentionEmpty" />
        ) : (
          <div className="console-card">
            {queue.map((row) => (
              <Link className="console-triage-row" key={row.label} to={row.to}>
                <span>{t(row.label)}</span>
                <StatusChip tone="amber">{t("setupChecklistNext")}</StatusChip>
              </Link>
            ))}
          </div>
        )}
      </Section>

      <OwnerTasks />

      <HostGraphs />

      <SetupChecklist />
    </div>
  );
}

const GROUPS: NavGroup[] = [
  {
    label: "consoleOverviewHeading",
    items: [
      {
        path: "",
        label: "consoleOverviewHeading",
        icon: Home,
        element: <Overview />,
        placement: "Overview",
      },
    ],
  },
  {
    label: "consoleMonitorGroup",
    items: [
      {
        path: "operations",
        label: "consoleOperationsDestination",
        icon: Gauge,
        element: <OperationsControls />,
        placement: "Monitor/Operations",
      },
      {
        path: "sessions",
        label: "liveSessionsHeading",
        icon: Users,
        element: <LiveSessions />,
        placement: "Monitor/Live Sessions",
      },
      {
        path: "history",
        label: "playHistoryHeading",
        icon: History,
        element: <PlayHistory />,
        placement: "Monitor/Play History",
      },
      {
        path: "health",
        label: "libraryHealthHeading",
        icon: Stethoscope,
        element: <Health />,
        placement: "Monitor/Library Health",
      },
      {
        path: "activity",
        advanced: true,
        label: "activityLogHeading",
        icon: Activity,
        element: <ActivityLog />,
        placement: "Monitor/Activity & Logs",
      },
    ],
  },
  {
    label: "consoleLibraryGroup",
    items: [
      {
        path: "libraries",
        label: "libraries",
        icon: Boxes,
        element: <Libraries />,
        placement: "Library/Libraries",
      },
      {
        path: "plugins",
        label: "consoleConfigPlugins",
        icon: Plug,
        element: <Plugins />,
        placement: "Library/Plugins",
      },
      {
        path: "identification",
        advanced: true,
        label: "capabilitiesHeading",
        icon: Layers,
        element: <Capabilities />,
        placement: "Library/Identification",
      },
      {
        path: "layout",
        advanced: true,
        label: "consoleLayoutNavigation",
        icon: FolderOpen,
        element: <Capabilities />,
        placement: "Library/Layout & Navigation",
      },
    ],
  },
  {
    label: "consoleAccessRailGroup",
    items: [
      {
        path: "access",
        label: "consoleAccessGroup",
        icon: ShieldCheck,
        element: <AccessAdmin />,
        placement: "Access/Profiles & Devices",
      },
      {
        path: "schedules",
        label: "schedules",
        icon: Clock,
        element: <ScheduleAdmin />,
        placement: "Access/Schedules & Policy",
      },
      {
        path: "remote",
        label: "remoteAccessHeading",
        icon: Network,
        element: <RemoteAccess />,
        placement: "Access/Remote Access",
      },
      {
        path: "api",
        advanced: true,
        label: "apiTokens",
        icon: KeyRound,
        element: <ApiCredentials />,
        placement: "Access/API Keys",
      },
    ],
  },
  {
    label: "consoleMaintainGroup",
    items: [
      {
        path: "backups",
        label: "backupHeading",
        icon: Database,
        element: <BackupExportPage />,
        placement: "Maintain/Backup & Export",
      },
      {
        path: "retention",
        advanced: true,
        label: "retentionRulesTitle",
        icon: Clock,
        element: <Retention />,
        placement: "Maintain/Trash & Retention",
      },
      {
        path: "migrate",
        label: "jobKindMigration",
        icon: Rocket,
        element: <MigrationWizard />,
        placement: "Maintain/Migration",
      },
      {
        path: "updates",
        label: "consoleUpdates",
        icon: HardDrive,
        element: <UpdateAwareness />,
        placement: "Maintain/Updates",
      },
      {
        path: "config",
        label: "consoleConfigHeading",
        icon: ServerCog,
        element: <Config />,
        placement: "Maintain/Server Config",
      },
    ],
  },
];

const ROUTE_ALIASES = [
  { path: "capabilities", to: "/console/identification" },
  { path: "operations-controls", to: "/console/operations" },
  { path: "playback-cache", to: "/console/operations" },
];

const APP_STORE_FIREWALL_PATHS = new Set(["identification", "layout", "capabilities", "migrate"]);

function groupsForHost(appStoreHost: boolean): NavGroup[] {
  if (!appStoreHost) return GROUPS;
  return GROUPS.map((group) => ({
    ...group,
    items: group.items
      .filter((item) => !APP_STORE_FIREWALL_PATHS.has(item.path))
      .map((item) =>
        item.path === "remote"
          ? {
              ...item,
              element: <RemoteAccess suppressDispatchSurfaces />,
            }
          : item,
      ),
  })).filter((group) => group.items.length > 0);
}

// The foot of the rail, visible at every width: the one Normal/Advanced switch
// (CORE-1117), then the active-session status: the Console is "unlocked" as
// visible STATE (design system: admin is visible state, never a hidden mode),
// with the lock action.
function SessionBar({ onLock }: { onLock: () => void }) {
  const t = useT();
  return (
    <div className="console-session-bar">
      <AdvancedSettingsSwitch className="console-advanced-switch" />
      <span className="console-session-lead">
        <span className="console-session-dot" aria-hidden />
        {t("consoleActiveLead")}
      </span>
      <Button variant="ghost" size="sm" onClick={onLock} className="-ml-2">
        <Lock className="console-nav-icon" aria-hidden />
        {t("adminLock")}
      </Button>
    </div>
  );
}

// The Normal view leaves out Advanced destinations, except the page the operator
// is on, and any group that is then empty. Every route stays registered.
function visibleNavGroups(groups: NavGroup[], advanced: boolean, pathname: string): NavGroup[] {
  if (advanced) return groups;
  return groups
    .map((group) => ({
      ...group,
      items: group.items.filter((item) => !item.advanced || pathname === `/console/${item.path}`),
    }))
    .filter((group) => group.items.length > 0);
}

function ConsoleNav({ groups }: { groups: NavGroup[] }) {
  const t = useT();
  const { pathname } = useLocation();
  const [advanced] = useAdvancedSettings();
  return (
    <div className="console-nav">
      {visibleNavGroups(groups, advanced, pathname).map((group) => (
        <div className="console-nav-group" key={group.label}>
          <p className="console-nav-group-label">{t(group.label)}</p>
          {group.items.map((item) => {
            const Icon = item.icon;
            return (
              <NavLink
                key={item.path}
                to={item.path ? `/console/${item.path}` : "/console"}
                end={item.path === ""}
                className={({ isActive }) => cn("console-nav-link", isActive && "is-active")}
              >
                <Icon className="console-nav-icon" aria-hidden />
                {t(item.label)}
              </NavLink>
            );
          })}
        </div>
      ))}
    </div>
  );
}

// CONSOLE (Admin, ADR-0007 §4): the operator surface — onboarding wizard,
// Libraries, Plugins, server/transcode config, Profiles & access, Operations
// (jobs + live sessions), Library Health, remote access/ingest/migration, API
// credentials, and the activity log. Admin-session gated; Workbench register.
//
// Any enrolled adult Profile may reach the Console AdminGate; the server-side
// Admin Session check enforces owner/admin eligibility. MANAGER status is
// intentionally orthogonal to Console administration (ADR-0060 §8).
export function ConsoleArea() {
  const { setRegister } = useRegister();
  const { loading, needsEnroll } = useSession();
  const t = useT();
  const location = useLocation();
  const groups = groupsForHost(isBeamfallAppStoreHost());
  const allItems = groups.flatMap((g) => g.items);
  const routeTitle = allItems.find((item) =>
    item.path === ""
      ? /^\/console\/?$/.test(location.pathname)
      : location.pathname === `/console/${item.path}`,
  )?.label;

  useEffect(() => setRegister("manage"), [setRegister]);

  if (loading) {
    return (
      <AppShell>
        <p className="text-sm text-muted-foreground">{t("loading")}</p>
      </AppShell>
    );
  }

  // An unenrolled browser is a true enrollment state, not a Console-authority
  // lock — route it to the enroll/setup hand-off. A non-adult (managed) Profile,
  // however, must NOT be silently bounced (ADR-0085 §30): it falls through to
  // the AdminGate, which renders the explicit "not adult" locked reason.
  if (needsEnroll) {
    return <Navigate to="/" replace />;
  }

  return (
    <AppShell>
      <div className="console-area">
        {/* The route's one h1 (also while the AdminGate is locked); each Section title is an h2. */}
        <h1 className="sr-only">{t(routeTitle ?? "consoleHeading")}</h1>
        <AdminGate>
          {(lock) => (
            <div className="console-layout">
              <SectionMenu
                label={t("consoleHeading")}
                className="console-rail"
                current={t(routeTitle ?? "consoleHeading")}
                footer={<SessionBar onLock={lock} />}
              >
                <ConsoleNav groups={groups} />
              </SectionMenu>
              <div className="console-content">
                <Routes>
                  {allItems.map((item) => (
                    <Route key={item.path} path={item.path} element={item.element} />
                  ))}
                  {ROUTE_ALIASES.map((route) => (
                    <Route
                      key={route.path}
                      path={route.path}
                      element={<Navigate to={route.to} replace />}
                    />
                  ))}
                  <Route path="*" element={<Navigate to="/console" replace />} />
                </Routes>
              </div>
            </div>
          )}
        </AdminGate>
      </div>
    </AppShell>
  );
}
