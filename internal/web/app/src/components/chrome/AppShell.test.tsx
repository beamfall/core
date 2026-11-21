import "@testing-library/jest-dom/vitest";
import { cleanup, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { I18nProvider } from "@/lib/i18n";
import { expectNoAxeViolations } from "@/test/axe";
import { AppShell } from "./AppShell";

vi.mock("@/lib/graphql", () => ({
  fetchNotifications: vi.fn().mockResolvedValue([]),
  // The activity link reads jobs only with an active Admin Session (CORE-2318).
  fetchAdminSession: vi.fn().mockResolvedValue({ active: true }),
  fetchJobs: vi
    .fn()
    .mockResolvedValue([
      { id: "j1", kind: "scan", status: "running", progress: { current: 0, total: 0, unit: "" } },
    ]),
}));

// The signed-in sidebar reads the Profile's sidebar choice (CORE-2435).
vi.mock("@/lib/graphql/viewer", () => ({
  fetchClientPrefs: vi.fn().mockResolvedValue([]),
  setClientPref: vi.fn().mockResolvedValue({ key: "web.sidebar-libraries" }),
}));

vi.mock("@/lib/register", () => ({
  useRegister: () => ({ register: "viewer" }),
}));

const sessionState = vi.hoisted(() => ({
  isManager: false,
  signedIn: false,
  profileKind: "adult",
  manifest: null as unknown,
}));

vi.mock("@/lib/session", () => ({
  useSession: () => ({
    session: sessionState.signedIn
      ? {
          deviceRole: sessionState.isManager ? "manager" : "viewer",
          profileID: "p1",
          profile: sessionState.profileKind,
          revealed: false,
        }
      : null,
    profiles: [{ id: "p1", name: "Owner", kind: "adult" }],
    manifest: sessionState.manifest,
    isManager: sessionState.isManager,
    revealed: false,
    needsEnroll: false,
  }),
}));

function renderShell(path = "/") {
  return render(
    <I18nProvider>
      <MemoryRouter initialEntries={[path]}>
        <AppShell>
          <div>Surface content</div>
        </AppShell>
      </MemoryRouter>
    </I18nProvider>,
  );
}

beforeEach(() => {
  sessionState.isManager = false;
  sessionState.signedIn = false;
  sessionState.profileKind = "adult";
  sessionState.manifest = null;
  delete window.beamfallHost;
});

afterEach(() => {
  cleanup();
  delete window.beamfallHost;
});

describe("AppShell host chrome suppression", () => {
  it("renders normal chrome in an ordinary browser", () => {
    renderShell();
    expect(screen.getByLabelText("Beamfall")).toBeInTheDocument();
    expect(screen.getByText("Surface content")).toBeInTheDocument();
    expect(screen.queryByRole("link", { name: /corresponding source/i })).toBeNull();
  });

  it("links the Console from the header and Manage from the account menu for managers", async () => {
    const user = userEvent.setup();
    sessionState.isManager = true;
    sessionState.signedIn = true;
    renderShell();
    expect(screen.getByRole("link", { name: "Console" })).toHaveAttribute("href", "/console");
    expect(screen.queryByRole("link", { name: "Manage libraries" })).toBeNull();

    await user.click(screen.getByRole("button", { name: "Account menu for Owner" }));

    expect(screen.getByRole("link", { name: "Manage libraries" })).toHaveAttribute(
      "href",
      "/manage",
    );
    expect(screen.getByRole("link", { name: "My Data" })).toHaveAttribute("href", "/mydata");
    expect(screen.getByRole("link", { name: "Profile settings" })).toHaveAttribute(
      "href",
      "/settings",
    );
    expect(screen.getByRole("button", { name: "Sign out" })).toHaveAccessibleDescription(
      /forget this browser/,
    );

    await user.keyboard("{Escape}");
    expect(screen.queryByRole("link", { name: "Manage libraries" })).toBeNull();
    expect(screen.getByRole("button", { name: "Account menu for Owner" })).toHaveFocus();
  });

  it("links the Console for an adult Profile on a viewer-role device", async () => {
    // The server opens an Admin Session for the adult owner on any Device Role,
    // so the way in cannot depend on the device being a Manager.
    const user = userEvent.setup();
    sessionState.signedIn = true;
    renderShell();
    expect(screen.getByRole("link", { name: "Console" })).toHaveAttribute("href", "/console");

    await user.click(screen.getByRole("button", { name: "Account menu for Owner" }));

    expect(screen.getByRole("link", { name: "Server console" })).toHaveAttribute(
      "href",
      "/console",
    );
    expect(screen.queryByRole("link", { name: "Manage libraries" })).toBeNull();
    expect(screen.queryByText("MANAGER")).toBeNull();
  });

  it("offers no Console link to a managed Profile, even on a manager device", async () => {
    const user = userEvent.setup();
    sessionState.signedIn = true;
    sessionState.isManager = true;
    sessionState.profileKind = "managed";
    renderShell();
    expect(screen.queryByRole("link", { name: "Console" })).toBeNull();

    await user.click(screen.getByRole("button", { name: "Account menu for Owner" }));

    expect(screen.queryByRole("link", { name: "Console" })).toBeNull();
    expect(screen.getByRole("link", { name: "Manage libraries" })).toHaveAttribute(
      "href",
      "/manage",
    );
  });

  it("shows running jobs in the header activity link for managers", async () => {
    sessionState.isManager = true;
    renderShell();
    expect(await screen.findByRole("link", { name: "1 job running" })).toHaveAttribute(
      "href",
      "/console/operations",
    );
  });

  it("suppresses app chrome inside the Apple Manager host", () => {
    Object.defineProperty(window, "beamfallHost", {
      configurable: true,
      value: {
        kind: "apple-manager",
        platform: "macos",
        clientClass: "apple-manager-macos-direct",
      },
    });

    renderShell();
    expect(screen.queryByLabelText("Beamfall")).toBeNull();
    expect(screen.getByText("Surface content")).toBeInTheDocument();
  });

  it("suppresses app chrome inside the iPad Manager host", () => {
    Object.defineProperty(window, "beamfallHost", {
      configurable: true,
      value: {
        kind: "apple-manager",
        platform: "ipad",
        clientClass: "ipad-manager-app-store",
      },
    });

    renderShell();
    expect(screen.queryByLabelText("Beamfall")).toBeNull();
    expect(screen.getByText("Surface content")).toBeInTheDocument();
  });
});

describe("AppShell library sidebar", () => {
  beforeEach(() => {
    sessionState.manifest = {
      libraries: [{ id: "lib-a", title: "Space Archive", pluginID: null, previewClips: false }],
      navigation: [
        { id: "home", title: "Home", view: "layout", layoutID: "home" },
        { id: "ambient", title: "Ambient", view: "ambient", layoutID: "ambient" },
        {
          id: "space",
          title: "Space Archive",
          view: "layout",
          layoutID: "space",
          libraryID: "lib-a",
        },
      ],
    };
  });

  it("lists home and each library in the sidebar, outside the header", () => {
    renderShell();
    const nav = screen.getByRole("navigation", { name: "Navigation" });
    expect(nav.closest("header")).toBeNull();
    expect(screen.getByRole("link", { name: "Home" })).toHaveAttribute("aria-current", "page");
    // The web client renders only layout views; an Ambient link would land on Home.
    expect(screen.queryByRole("link", { name: "Ambient" })).not.toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Space Archive" })).toHaveAttribute("href", "/n/space");
    expect(screen.getByRole("heading", { name: "Libraries" })).toBeInTheDocument();
  });

  it("collapses the sidebar to an icon rail from the header menu button", async () => {
    renderShell();
    const toggle = screen.getByRole("button", { name: "Navigation" });
    const expanded = toggle.getAttribute("aria-expanded");
    await userEvent.click(toggle);
    expect(toggle).toHaveAttribute("aria-expanded", expanded === "true" ? "false" : "true");
    expect(document.getElementById(toggle.getAttribute("aria-controls") ?? "")).toHaveClass(
      expanded === "true" ? "is-collapsed" : "bf-sidebar",
    );
  });

  it("puts Playlists one click away for a signed-in Profile on desktop (CORE-2827)", () => {
    sessionState.signedIn = true;
    renderShell("/favorites");
    const nav = screen.getByRole("navigation", { name: "Navigation" });
    expect(within(nav).getByRole("link", { name: "Playlists" })).toHaveAttribute(
      "href",
      "/playlists",
    );
    expect(within(nav).getByRole("link", { name: "Favorites" })).toHaveAttribute(
      "aria-current",
      "page",
    );
    // The tab bar is phone-only.
    expect(screen.queryByRole("navigation", { name: "Main sections" })).toBeNull();
  });

  describe("resizable width (CORE-2734)", () => {
    beforeEach(() => {
      window.localStorage.setItem("beamfall.sidebarCollapsed", "0");
    });

    afterEach(() => {
      window.localStorage.clear();
    });

    function sidebarOf(separator: HTMLElement) {
      return document.getElementById(separator.getAttribute("aria-controls") ?? "");
    }

    it("resizes by keyboard within the limits and remembers the width", async () => {
      const user = userEvent.setup();
      renderShell();
      const separator = screen.getByRole("separator", { name: "Resize sidebar" });
      expect(separator).toHaveAttribute("aria-valuenow", "200");
      expect(separator).toHaveAttribute("aria-valuemin", "168");
      expect(separator).toHaveAttribute("aria-valuemax", "360");
      expect(sidebarOf(separator)).toHaveClass("bf-sidebar");

      separator.focus();
      await user.keyboard("{ArrowRight}{ArrowRight}");
      expect(separator).toHaveAttribute("aria-valuenow", "232");
      expect(sidebarOf(separator)?.style.getPropertyValue("--bf-sidebar-width")).toBe("232px");
      expect(window.localStorage.getItem("beamfall.sidebarWidth")).toBe("232");

      await user.keyboard("{End}{ArrowRight}");
      expect(separator).toHaveAttribute("aria-valuenow", "360");
      await user.keyboard("{Home}{ArrowLeft}");
      expect(separator).toHaveAttribute("aria-valuenow", "168");

      cleanup();
      renderShell();
      expect(screen.getByRole("separator", { name: "Resize sidebar" })).toHaveAttribute(
        "aria-valuenow",
        "168",
      );
    });

    it("resets to the default on double-click", async () => {
      window.localStorage.setItem("beamfall.sidebarWidth", "300");
      renderShell();
      const separator = screen.getByRole("separator", { name: "Resize sidebar" });
      expect(separator).toHaveAttribute("aria-valuenow", "300");
      await userEvent.dblClick(separator);
      expect(separator).toHaveAttribute("aria-valuenow", "200");
      expect(window.localStorage.getItem("beamfall.sidebarWidth")).toBeNull();
    });

    it("offers no handle on the collapsed icon rail", async () => {
      renderShell();
      await userEvent.click(screen.getByRole("button", { name: "Navigation" }));
      expect(screen.queryByRole("separator", { name: "Resize sidebar" })).not.toBeInTheDocument();
    });
  });

  describe("phone drawer (CORE-1166)", () => {
    const originalMatchMedia = window.matchMedia;

    beforeEach(() => {
      window.matchMedia = ((query: string) => ({
        matches: query === "(max-width: 760px)",
        media: query,
        addEventListener: () => {},
        removeEventListener: () => {},
      })) as unknown as typeof window.matchMedia;
    });

    afterEach(() => {
      window.matchMedia = originalMatchMedia;
    });

    it("keeps the drawer out of the page until the menu button opens it", () => {
      renderShell();
      const toggle = screen.getByRole("button", { name: "Navigation" });
      expect(toggle).toHaveAttribute("aria-expanded", "false");
      expect(toggle).toHaveAttribute("aria-haspopup", "dialog");
      expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
      expect(screen.queryByRole("link", { name: "Space Archive" })).not.toBeInTheDocument();
      expect(screen.queryByRole("separator", { name: "Resize sidebar" })).not.toBeInTheDocument();
    });

    it("moves focus into the drawer on open and traps it there", async () => {
      const user = userEvent.setup();
      renderShell();
      await user.click(screen.getByRole("button", { name: "Navigation" }));

      const drawer = await screen.findByRole("dialog", { name: "Navigation" });
      expect(drawer).toHaveClass("bf-sidebar", "is-drawer");
      await waitFor(() => expect(drawer).toContainElement(document.activeElement as HTMLElement));

      for (let i = 0; i < 6; i++) {
        await user.tab();
        await waitFor(() => expect(drawer).toContainElement(document.activeElement as HTMLElement));
      }
    });

    it("closes on Escape and returns focus to the menu button", async () => {
      const user = userEvent.setup();
      renderShell();
      const toggle = screen.getByRole("button", { name: "Navigation" });
      await user.click(toggle);
      await screen.findByRole("dialog", { name: "Navigation" });

      await user.keyboard("{Escape}");

      await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
      await waitFor(() => expect(toggle).toHaveFocus());
      expect(toggle).toHaveAttribute("aria-expanded", "false");
    });

    it("offers Home, Search and Libraries in a bottom tab bar (CORE-2827)", async () => {
      const user = userEvent.setup();
      const { container } = renderShell();
      const tabs = screen.getByRole("navigation", { name: "Main sections" });
      expect(container.querySelector(".bf-shell")).toHaveClass("has-tabbar");
      const home = within(tabs).getByRole("link", { name: "Home" });
      expect(home).toHaveAttribute("href", "/");
      expect(home).toHaveAttribute("aria-current", "page");
      const search = within(tabs).getByRole("link", { name: "Search" });
      expect(search).toHaveAttribute("href", "/search");
      expect(search).not.toHaveAttribute("aria-current");
      // Your stuff belongs to a signed-in Profile.
      expect(within(tabs).queryByRole("button", { name: "Your stuff" })).toBeNull();
      await expectNoAxeViolations(tabs);

      const librariesTab = within(tabs).getByRole("button", { name: "Libraries" });
      expect(librariesTab).toHaveAttribute("aria-haspopup", "dialog");
      await user.click(librariesTab);
      const drawer = await screen.findByRole("dialog", { name: "Navigation" });
      expect(librariesTab).toHaveAttribute("aria-expanded", "true");
      const library = within(drawer).getByRole("link", { name: "Space Archive" });
      await waitFor(() => expect(library).toHaveFocus());

      await user.keyboard("{Escape}");
      await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
      await waitFor(() => expect(librariesTab).toHaveFocus());
    });

    it("opens the drawer at Your stuff for a signed-in Profile (CORE-2827)", async () => {
      sessionState.signedIn = true;
      const user = userEvent.setup();
      renderShell("/search");
      const tabs = screen.getByRole("navigation", { name: "Main sections" });
      expect(within(tabs).getByRole("link", { name: "Search" })).toHaveAttribute(
        "aria-current",
        "page",
      );
      await user.click(within(tabs).getByRole("button", { name: "Your stuff" }));
      const drawer = await screen.findByRole("dialog", { name: "Navigation" });
      const playlists = within(drawer).getByRole("link", { name: "Playlists" });
      await waitFor(() => expect(playlists).toHaveFocus());
    });

    it("closes when a destination is chosen", async () => {
      const user = userEvent.setup();
      renderShell();
      await user.click(screen.getByRole("button", { name: "Navigation" }));
      await screen.findByRole("dialog", { name: "Navigation" });

      await user.click(screen.getByRole("link", { name: "Space Archive" }));

      await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    });
  });
});
