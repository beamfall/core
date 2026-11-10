import "@testing-library/jest-dom/vitest";
import { render, screen } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { SessionInfo } from "@/lib/graphql/types";
import { I18nProvider } from "@/lib/i18n";
import { ViewerArea } from "./index";

// The Viewer paints chrome and skeletons while the session spine settles, and
// still mounts no route (and so issues no route-local request) until a session
// is bound.

const mocks = vi.hoisted(() => ({
  routeMounts: 0,
  setupRequired: Promise.resolve(false) as Promise<boolean>,
  sessionState: {
    error: null as string | null,
    loading: true,
    needsEnroll: false,
    refresh: vi.fn(),
    session: null as SessionInfo | null,
    manifest: null,
    isManager: false,
    revealed: false,
  },
}));

vi.mock("@/lib/api", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/lib/api")>()),
  fetchSetupRequired: vi.fn(() => mocks.setupRequired),
}));

vi.mock("@/lib/register", () => ({
  useRegister: () => ({ register: null, setRegister: vi.fn() }),
}));

vi.mock("@/lib/session", () => ({
  useSession: () => mocks.sessionState,
  useViewerIdentity: () => null,
}));

vi.mock("./pages/BrowsePage", () => ({
  BrowsePage: () => {
    mocks.routeMounts++;
    return <p>browse route</p>;
  },
}));

function renderViewer() {
  return render(
    <I18nProvider initialLocale="en">
      <MemoryRouter initialEntries={["/"]}>
        <Routes>
          <Route path="/*" element={<ViewerArea />} />
          <Route path="/enroll" element={<p>enroll page</p>} />
        </Routes>
      </MemoryRouter>
    </I18nProvider>,
  );
}

beforeEach(() => {
  mocks.routeMounts = 0;
  mocks.setupRequired = Promise.resolve(false);
  mocks.sessionState = {
    ...mocks.sessionState,
    error: null,
    loading: true,
    needsEnroll: false,
    session: null,
  };
});

describe("ViewerArea while the session is pending", () => {
  it("renders the chrome and a busy skeleton instead of a blank screen", () => {
    const { container } = renderViewer();

    expect(screen.getByRole("link", { name: "Beamfall" })).toBeInTheDocument();
    expect(container.querySelector('[aria-busy="true"]')).not.toBeNull();
    expect(container.querySelectorAll(".bf-shimmer").length).toBeGreaterThan(0);
    // The chrome shows no session-scoped affordance before an identity is bound.
    expect(screen.queryByRole("navigation")).toBeNull();
    expect(screen.queryByText("browse route")).toBeNull();
    expect(mocks.routeMounts).toBe(0);
  });

  it("shows a neutral placeholder, not the chrome, while the enroll probe is pending", async () => {
    mocks.setupRequired = new Promise(() => {});
    mocks.sessionState = { ...mocks.sessionState, loading: false, needsEnroll: true };
    const { container } = renderViewer();

    expect(container.querySelector('[aria-busy="true"]')).not.toBeNull();
    expect(screen.queryByRole("link", { name: "Beamfall" })).toBeNull();
    expect(mocks.routeMounts).toBe(0);
  });

  it("still routes an unenrolled browser to the enroll page once the probe settles", async () => {
    mocks.sessionState = { ...mocks.sessionState, loading: false, needsEnroll: true };
    renderViewer();

    expect(await screen.findByText("enroll page")).toBeInTheDocument();
    expect(mocks.routeMounts).toBe(0);
  });
});
