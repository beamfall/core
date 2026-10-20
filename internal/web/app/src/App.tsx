import { lazy, type ReactNode, Suspense } from "react";
import { Navigate, Route, Routes, useLocation } from "react-router";
import { AreaErrorBoundary } from "@/components/chrome/ErrorBoundary";
import { Skeleton } from "@/components/ui/skeleton";

// ADR-0007 — one web app, three areas: Viewer (root), Manage, Console (Admin).
// Each area owns its register and its own nested routes; this is the only file
// that wires the areas together, so per-area work stays in disjoint dirs.
//
// Code-splitting: every area is split into its own chunk via React.lazy so the
// initial/index chunk stays lean. The Viewer is still Viewer-first — its chunk
// is the only area without a route guard in front of it, so it's the immediate
// next request after the entry. Crucially, lazying the Viewer also moves its
// static player dependency (hls.js, ~150 KB via usePlayer) out of the entry
// chunk: the entry no longer modulepreloads hls.js, so it's fetched alongside
// the Viewer chunk rather than on the very first paint. The heavier Manage /
// Console areas and the auth pages stay behind their own route guards. The area
// roots are named exports, so each dynamic import re-maps the named export to
// the default the lazy() contract wants — without touching the area files.
const ViewerArea = lazy(() => import("@/areas/viewer").then((m) => ({ default: m.ViewerArea })));
const ManageArea = lazy(() => import("@/areas/manage").then((m) => ({ default: m.ManageArea })));
const ConsoleArea = lazy(() => import("@/areas/console").then((m) => ({ default: m.ConsoleArea })));
const EnrollPage = lazy(() => import("@/areas/enroll").then((m) => ({ default: m.EnrollPage })));
// The first-run setup wizard (WEB-1, ADR-0007 §2). Always renders at /setup;
// the session spine routes a fresh server here (see areas/viewer).
const SetupWizard = lazy(() => import("@/areas/setup").then((m) => ({ default: m.SetupWizard })));
const PromotePage = lazy(() => import("@/areas/enroll").then((m) => ({ default: m.PromotePage })));

// Minimal, register-neutral fallback shown only while a lazy area chunk loads.
// Kept deliberately spare (a single neutral spinner) so the brief flash while
// the Viewer chunk is fetched reads as a load indicator, not an area's chrome.
function RouteFallback() {
  return (
    <div className="flex min-h-screen items-center justify-center">
      <Skeleton className="h-12 w-12 rounded-full" />
    </div>
  );
}

// Every area mounts inside its own error boundary (the root boundary lives in
// main.tsx around the router): a failed lazy chunk — the first load after a
// server upgrade, before the new worker has taken over — renders that area's
// reload panel instead of unmounting the whole root. The boundary resets when
// the route path changes, so a failed area never pins navigation.
function Area({ children }: { children: ReactNode }) {
  const { pathname } = useLocation();
  return <AreaErrorBoundary resetKey={pathname}>{children}</AreaErrorBoundary>;
}

export function App() {
  return (
    <Suspense fallback={<RouteFallback />}>
      <Routes>
        <Route
          path="/setup"
          element={
            <Area>
              <SetupWizard />
            </Area>
          }
        />
        <Route
          path="/enroll"
          element={
            <Area>
              <EnrollPage />
            </Area>
          }
        />
        <Route
          path="/promote"
          element={
            <Area>
              <PromotePage />
            </Area>
          }
        />
        <Route
          path="/manage/*"
          element={
            <Area>
              <ManageArea />
            </Area>
          }
        />
        <Route
          path="/console/*"
          element={
            <Area>
              <ConsoleArea />
            </Area>
          }
        />
        <Route
          path="/*"
          element={
            <Area>
              <ViewerArea />
            </Area>
          }
        />
        <Route path="*" element={<Navigate to="/" replace />} />
      </Routes>
    </Suspense>
  );
}
