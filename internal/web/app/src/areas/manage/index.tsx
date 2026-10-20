import { useEffect } from "react";
import { Navigate, NavLink, Route, Routes } from "react-router";
import { AppShell } from "@/components/chrome/AppShell";
import { useT } from "@/lib/i18n";
import { useRegister } from "@/lib/register";
import { useSession } from "@/lib/session";
import type { StringKey } from "@/strings";
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
type NavItem =
  | { to: string; label: StringKey; text?: undefined }
  | { to: string; text: string; label?: undefined };
const NAV_GROUPS: NavItem[][] = [
  [
    { to: "/manage/libraries", label: "libraries" },
    { to: "/manage/operations", label: "jobs" },
    { to: "/manage/convert", label: "convert" },
    { to: "/manage/rename", label: "rename" },
    { to: "/manage/trash", label: "trashPolicyHeading" },
    { to: "/manage/backups", label: "backupHeading" },
    { to: "/manage/subscriptions", label: "subscriptions" },
    { to: "/manage/unmatched", label: "unmatchedHeading" },
  ],
  [
    { to: "/manage/collections", label: "collections" },
    { to: "/manage/bulk", label: "bulkOpsHeading" },
    { to: "/manage/tags", label: "tagsHeading" },
    { to: "/manage/curation", label: "curationHeading" },
    { to: "/manage/entities", label: "manageTitle" },
    { to: "/manage/artwork", label: "artworkCandidates" },
    { to: "/manage/markers", label: "markersHeading" },
    { to: "/manage/field-locks", label: "fieldLocksHeading" },
    { to: "/manage/navigation", label: "navOverrideHeading" },
  ],
  [
    { to: "/manage/restrictions", label: "restrictionsTitle" },
    { to: "/manage/profile", label: "profilesTitle" },
  ],
];

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
                  {item.text ?? t(item.label)}
                </NavLink>
              ))}
            </div>
          ))}
        </nav>

        <div className="min-w-0 grow">
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
        </div>
      </div>
    </AppShell>
  );
}
