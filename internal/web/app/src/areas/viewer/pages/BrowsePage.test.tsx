import "@testing-library/jest-dom/vitest";
import { cleanup, render, screen } from "@testing-library/react";
import { MemoryRouter, Route, Routes, useLocation } from "react-router";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { NavigationManifest } from "@/lib/graphql/types";
import { I18nProvider } from "@/lib/i18n";
import { BrowsePage } from "./BrowsePage";

// The manifest the server really sends: Home, the layout-less "ambient" entry,
// then one node per Library.
const manifest = {
  libraries: [{ id: "lib-a", title: "Space Archive", pluginID: null, previewClips: false }],
  navigation: [
    { id: "home", title: "Home", view: "layout", layoutID: "layout-home" },
    { id: "ambient", title: "Ambient", view: "ambient", layoutID: "" },
    {
      id: "library-lib-a",
      title: "Space Archive",
      view: "layout",
      layoutID: "layout-home",
      libraryID: "lib-a",
    },
  ],
  layouts: [{ id: "layout-home", sections: [] }],
} as unknown as NavigationManifest;

const mockSession = vi.fn();
vi.mock("@/lib/session", () => ({
  useSession: () => mockSession(),
}));
vi.mock("@/components/chrome/AppShell", () => ({
  AppShell: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
}));
vi.mock("../components/LayoutView", () => ({
  LayoutView: ({ navigationID, libraryID }: { navigationID: string; libraryID: string }) => (
    <div data-testid="layout" data-navigation={navigationID} data-library={libraryID} />
  ),
}));
vi.mock("../components/RevealControls", () => ({ RevealControls: () => null }));
vi.mock("../components/ViewerActions", () => ({ ViewerActions: () => null }));

function Path() {
  return <span data-testid="path">{useLocation().pathname}</span>;
}

function renderAt(path: string, session: object = { manifest, loading: false }) {
  mockSession.mockReturnValue(session);
  return render(
    <I18nProvider>
      <MemoryRouter initialEntries={[path]}>
        <Path />
        <Routes>
          <Route path="/" element={<BrowsePage home />} />
          <Route path="/n/:nodeId" element={<BrowsePage />} />
        </Routes>
      </MemoryRouter>
    </I18nProvider>,
  );
}

afterEach(() => {
  cleanup();
  mockSession.mockReset();
});

describe("BrowsePage node resolution", () => {
  it("renders a library node under its own heading and scope", () => {
    renderAt("/n/library-lib-a");

    expect(screen.getByTestId("path")).toHaveTextContent("/n/library-lib-a");
    expect(screen.getByRole("heading", { level: 1 })).toHaveTextContent("Space Archive");
    expect(screen.getByTestId("layout")).toHaveAttribute("data-library", "lib-a");
  });

  it("sends the layout-less ambient node to Home instead of drawing Home at its address", () => {
    renderAt("/n/ambient");

    expect(screen.getByTestId("path")).toHaveTextContent(/^\/$/);
    expect(screen.getByRole("heading", { level: 1 })).toHaveTextContent("Home");
    expect(screen.getByTestId("layout")).toHaveAttribute("data-navigation", "home");
  });

  it("sends an unknown or no-longer-visible node to Home", () => {
    renderAt("/n/library-gone");

    expect(screen.getByTestId("path")).toHaveTextContent(/^\/$/);
  });

  it("stays put while there is no navigation to resolve against", () => {
    renderAt("/n/library-lib-a", { manifest: null, loading: false });

    expect(screen.getByTestId("path")).toHaveTextContent("/n/library-lib-a");
  });
});
