import { lazy, Suspense, useEffect, useState } from "react";
import { Navigate, Route, Routes } from "react-router";
import { ErrorState } from "@/components/beamfall/workbench";
import { AppShell } from "@/components/chrome/AppShell";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { fetchSetupRequired } from "@/lib/api";
import { useT } from "@/lib/i18n";
import { useRegister } from "@/lib/register";
import { useSession } from "@/lib/session";
import { SectionSkeleton } from "./components/HydratedSection";
import { BrowsePage } from "./pages/BrowsePage";
import { EntityPage } from "./pages/EntityPage";
import { MyDataPage } from "./pages/MyDataPage";
import { NotificationsPage } from "./pages/NotificationsPage";
import { ProfilePickerPage } from "./pages/ProfilePickerPage";
import { ProfileSettingsPage } from "./pages/ProfileSettingsPage";
import { SearchPage } from "./pages/SearchPage";
import { SeeAllPage } from "./pages/SeeAllPage";
import "./player/player-deferred.css";

// The player is the only viewer route that pulls in hls.js (~162 KB gzip via
// usePlayer). Lazying PlayerPage moves that static dependency out of the viewer
// area chunk and into a PlayerPage chunk, so hls.js is fetched only when the
// /watch route is actually navigated to — not on every Viewer route. PlayerPage
// is a named export, re-mapped to the default the lazy() contract wants.
const PlayerPage = lazy(() =>
  import("./pages/PlayerPage").then((m) => ({ default: m.PlayerPage })),
);

function ViewerSessionError({ onRetry }: { onRetry: () => Promise<void> }) {
  const t = useT();
  return (
    <AppShell>
      <div className="mx-auto max-w-xl space-y-3 py-12">
        <ErrorState message="couldNotReachServer" />
        <Button type="button" variant="outline" onClick={() => void onRetry()}>
          {t("jobRetryAction")}
        </Button>
      </div>
    </AppShell>
  );
}

// Painted while the session spine is still settling: the app chrome (which
// renders no session-scoped data while no session is bound) around the same
// inert section skeletons a Layout shows, so the first paint is the viewer's
// shape rather than a blank screen. No Viewer route mounts here, so no
// route-local request runs without a bound identity.
function ViewerSessionPending() {
  const t = useT();
  return (
    <AppShell>
      <div aria-busy="true" className="flex flex-col gap-8">
        <span className="sr-only">{t("loading")}</span>
        <SectionSkeleton hero />
        <SectionSkeleton hero={false} />
      </div>
    </AppShell>
  );
}

// VIEWER (root): the full client (ADR-0007) — Navigation Manifest + Layout
// rendering (the six Section renderers), profile picker, entity detail, See All,
// search, the Reveal contract, the My Data / Privacy panel (ADR-0036), and the
// player. Sets the Cinema Dark register at the area root and owns the viewer's
// nested routes. An unenrolled browser is routed to /enroll by App.tsx's
// session spine, so this area assumes a bound session.
export function ViewerArea() {
  const { setRegister } = useRegister();
  const { error, needsEnroll, refresh, session } = useSession();
  // When the browser is unenrolled (the 401 state), a FRESH server with setup
  // pending must reach the wizard, not the enroll gate (ADR-0007 §2). We probe
  // the unauthenticated GET /api/setup once needsEnroll flips, leaving the
  // session spine untouched. `null` = not yet known; `true`/`false` decide the
  // hand-off. A failed probe resolves to `false` (fetchSetupRequired), so a
  // normal enrolled server never gets stuck routed at /setup.
  const [setupRequired, setSetupRequired] = useState<boolean | null>(null);

  useEffect(() => setRegister("viewer"), [setRegister]);

  useEffect(() => {
    if (!needsEnroll) return;
    let cancelled = false;
    void fetchSetupRequired().then((required) => {
      if (!cancelled) setSetupRequired(required);
    });
    return () => {
      cancelled = true;
    };
  }, [needsEnroll]);

  // An unenrolled browser cannot browse; decide between the first-run wizard
  // (fresh server) and the enroll gate. Wait for the probe before deciding so
  // a fresh server doesn't briefly flash the enroll form.
  // The pending probe shows a neutral placeholder, not the chrome: the chrome's
  // enroll banner is exactly the flash this wait avoids.
  if (needsEnroll) {
    if (setupRequired === null) {
      return (
        <div aria-busy="true" className="flex min-h-screen items-center justify-center">
          <Skeleton className="h-12 w-12 rounded-full" />
        </div>
      );
    }
    return <Navigate to={setupRequired ? "/setup" : "/enroll"} replace />;
  }

  // Identity transitions clear SessionProvider before the departing-cookie
  // mutation runs. Do not keep (or remount) a Viewer route in that no-session
  // window: route-local effects would otherwise issue fresh requests against
  // the old cookie, and a generic session rejection could leave those results
  // mounted indefinitely.
  if (!session) {
    if (!error) return <ViewerSessionPending />;
    return <ViewerSessionError onRetry={refresh} />;
  }

  return (
    <Routes key={session.profileID}>
      <Route path="/" element={<BrowsePage home />} />
      <Route path="/n/:nodeId" element={<BrowsePage />} />
      <Route path="/entity/:entityId" element={<EntityPage />} />
      <Route path="/seeall/:sourceId" element={<SeeAllPage />} />
      <Route path="/search" element={<SearchPage />} />
      <Route path="/mydata" element={<MyDataPage />} />
      <Route path="/notifications" element={<NotificationsPage />} />
      <Route path="/profiles" element={<ProfilePickerPage />} />
      <Route path="/settings" element={<ProfileSettingsPage />} />
      <Route
        path="/watch/:mediaId"
        element={
          <Suspense fallback={<div className="min-h-screen bg-black" />}>
            <PlayerPage />
          </Suspense>
        }
      />
      <Route path="*" element={<Navigate to="/" replace />} />
    </Routes>
  );
}
