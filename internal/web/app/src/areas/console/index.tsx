import {
  Activity,
  AlertTriangle,
  Boxes,
  Clock,
  Database,
  FolderOpen,
  Gauge,
  HardDrive,
  HeartPulse,
  Home,
  KeyRound,
  Layers,
  type LucideProps,
  Network,
  Plug,
  Rocket,
  ServerCog,
  ShieldCheck,
  Stethoscope,
  Terminal,
  Users,
} from "lucide-react";
import { type ComponentType, type ReactElement, useEffect } from "react";
import { Link, Navigate, NavLink, Route, Routes } from "react-router";
import { EmptyState, StatusChip } from "@/components/beamfall/workbench";
import { AppShell } from "@/components/chrome/AppShell";
import { Button } from "@/components/ui/button";
import { isBeamfallAppStoreHost } from "@/lib/beamfallHost";
import { fetchSetupState } from "@/lib/graphql";
import { useT } from "@/lib/i18n";
import { useRegister } from "@/lib/register";
import { useSession } from "@/lib/session";
import { cn } from "@/lib/utils";
import type { StringKey } from "@/strings";
import { BackupExportPage } from "../manage/pages/BackupExportPage";
import { AdminGate } from "./AdminGate";
import { Section } from "./Section";
import { AccessAdmin } from "./sections/AccessAdmin";
import { ActivityLog } from "./sections/ActivityLog";
import { ApiCredentials } from "./sections/ApiCredentials";
import { Capabilities } from "./sections/Capabilities";
import { Config } from "./sections/Config";
import { Health } from "./sections/Health";
import { Libraries } from "./sections/Libraries";
import { Operations } from "./sections/Operations";
import { OperationsControls } from "./sections/OperationsControls";
import { Plugins } from "./sections/Plugins";
import { RemoteAccess } from "./sections/RemoteAccess";
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
}

interface NavGroup {
  label: StringKey;
  items: NavItem[];
  collapsed?: boolean;
}

export const CONSOLE_IA_PLACEMENTS = [
  "0080:Overview",
  "0064:Library/Libraries",
  "0069:Library/Identification",
  "0070:Library/Plugins",
  "0071:Library/Layout & Navigation",
  "0074:Monitor/Live Sessions",
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

  return (
    <div className="space-y-8">
      <Section heading="consoleOverviewHeading" lead="consoleOverviewLead">
        <div className="console-overview-tiles">
          {[
            ["consoleOverviewServer", "consoleOverviewReady", HeartPulse],
            ["libraries", needsLibrary ? "setupChecklistNext" : "consoleOverviewReady", Boxes],
            ["jobs", "consoleOverviewReady", Gauge],
            ["liveSessionsHeading", "none", Users],
          ].map(([label, state, Icon]) => (
            <div className="console-overview-tile" key={label as string}>
              <Icon className="console-nav-icon" aria-hidden />
              <span>{t(label as StringKey)}</span>
              <StatusChip tone={state === "setupChecklistNext" ? "amber" : "green"}>
                {t(state as StringKey)}
              </StatusChip>
            </div>
          ))}
        </div>
      </Section>

      <Section heading="consoleNeedsAttentionHeading" lead="consoleNeedsAttentionLead">
        {queue.length === 0 ? (
          <EmptyState message="consoleNeedsAttentionEmpty" icon={AlertTriangle} />
        ) : (
          <div className="console-card">
            {queue.map((row) => (
              <Link className="console-triage-row" key={row.label} to={row.to}>
                <AlertTriangle className="console-nav-icon" aria-hidden />
                <span>{t(row.label)}</span>
                <StatusChip tone="amber">{t("setupChecklistNext")}</StatusChip>
              </Link>
            ))}
          </div>
        )}
      </Section>

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
        element: <Operations />,
        placement: "Monitor/Live Sessions",
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
        label: "capabilitiesHeading",
        icon: Layers,
        element: <Capabilities />,
        placement: "Library/Identification",
      },
      {
        path: "layout",
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
        element: <AccessAdmin />,
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
        label: "apiTokens",
        icon: KeyRound,
        element: <ApiCredentials />,
        placement: "Access/API Keys",
      },
    ],
  },
  {
    label: "consoleMaintainGroup",
    collapsed: true,
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
        label: "retentionRulesTitle",
        icon: Clock,
        element: <Retention />,
        placement: "Maintain/Trash & Retention",
      },
      {
        path: "migrate",
        label: "jobKindMigration",
        icon: Rocket,
        element: <RemoteAccess />,
        placement: "Maintain/Migration",
      },
      {
        path: "updates",
        label: "consoleUpdates",
        icon: HardDrive,
        element: <OperationsControls />,
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
        item.path === "remote" || item.path === "migrate"
          ? {
              ...item,
              element: <RemoteAccess suppressDispatchSurfaces />,
            }
          : item,
      ),
  })).filter((group) => group.items.length > 0);
}

// The active-session bar shown once an Admin Session is active — the Console is
// "unlocked" as visible STATE (design system: admin is visible state, never a
// hidden mode). A live dot + the lock affordance.
function SessionBar({ onLock }: { onLock: () => void }) {
  const t = useT();
  return (
    <div className="console-session-bar">
      <span className="console-session-lead">
        <span className="console-session-dot" aria-hidden />
        <ShieldCheck className="console-nav-icon" aria-hidden />
        {t("consoleActiveLead")}
      </span>
      <Button variant="outline" size="sm" onClick={onLock}>
        <Terminal className="console-nav-icon" aria-hidden />
        {t("adminLock")}
      </Button>
    </div>
  );
}

function ConsoleNav({ groups }: { groups: NavGroup[] }) {
  const t = useT();
  return (
    <nav aria-label={t("consoleHeading")} className="console-nav shrink-0 sm:w-56">
      {groups.map((group, gi) => {
        const links = group.items.map((item) => {
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
        });
        if (group.collapsed) {
          return (
            <details className="console-nav-group" key={group.label + gi}>
              <summary className="console-nav-group-label">{t(group.label)}</summary>
              {links}
            </details>
          );
        }
        return (
          <div className="console-nav-group" key={group.label + gi}>
            <p className="console-nav-group-label">{t(group.label)}</p>
            {links}
          </div>
        );
      })}
    </nav>
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
  const groups = groupsForHost(isBeamfallAppStoreHost());
  const allItems = groups.flatMap((g) => g.items);

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
        <AdminGate sessionBar={(lock) => <SessionBar onLock={lock} />}>
          <div className="flex flex-col gap-8 sm:flex-row">
            <ConsoleNav groups={groups} />
            <div className="min-w-0 flex-1">
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
        </AdminGate>
      </div>
    </AppShell>
  );
}
