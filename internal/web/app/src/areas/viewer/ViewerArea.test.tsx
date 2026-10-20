import "@testing-library/jest-dom/vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { SessionInfo } from "@/lib/graphql/types";
import { I18nProvider } from "@/lib/i18n";
import { ViewerArea } from "./index";

const mocks = vi.hoisted(() => ({
  setRegister: vi.fn(),
  routeMounts: 0,
  routeUnmounts: 0,
  scopedFetches: [] as string[],
  sessionState: {
    error: null as string | null,
    loading: false,
    needsEnroll: false,
    refresh: vi.fn(),
    session: {
      deviceRole: "viewer",
      profileID: "p1",
      profile: "adult",
      revealed: false,
    } as SessionInfo | null,
  },
}));

vi.mock("@/lib/api", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/lib/api")>()),
  fetchSetupRequired: vi.fn().mockResolvedValue(false),
}));

vi.mock("@/lib/register", () => ({
  useRegister: () => ({ setRegister: mocks.setRegister }),
}));

vi.mock("@/lib/session", () => ({
  useSession: () => mocks.sessionState,
}));

vi.mock("./pages/BrowsePage", async () => {
  const React = await import("react");
  return {
    BrowsePage: () => {
      const [value, setValue] = React.useState("fresh");
      React.useEffect(() => {
        mocks.routeMounts++;
        mocks.scopedFetches.push(mocks.sessionState.session?.profileID ?? "no-profile");
        return () => {
          mocks.routeUnmounts++;
        };
      }, []);
      return (
        <input
          aria-label="profile-bound-state"
          value={value}
          onChange={(event) => setValue(event.currentTarget.value)}
        />
      );
    },
  };
});

vi.mock("./pages/EntityPage", () => ({ EntityPage: () => null }));
vi.mock("./pages/SeeAllPage", () => ({ SeeAllPage: () => null }));
vi.mock("./pages/SearchPage", () => ({ SearchPage: () => null }));
vi.mock("./pages/MyDataPage", () => ({ MyDataPage: () => null }));
vi.mock("./pages/NotificationsPage", () => ({ NotificationsPage: () => null }));
vi.mock("./pages/ProfilePickerPage", () => ({ ProfilePickerPage: () => null }));
vi.mock("./pages/ProfileSettingsPage", () => ({ ProfileSettingsPage: () => null }));

describe("ViewerArea Profile scope", () => {
  beforeEach(() => {
    mocks.routeMounts = 0;
    mocks.routeUnmounts = 0;
    mocks.scopedFetches.length = 0;
    mocks.sessionState = {
      error: null,
      loading: false,
      needsEnroll: false,
      refresh: vi.fn(),
      session: {
        deviceRole: "viewer",
        profileID: "p1",
        profile: "adult",
        revealed: false,
      },
    };
  });

  it("remounts profile-bound route state when the selected Profile changes", async () => {
    const user = userEvent.setup();
    const view = render(
      <MemoryRouter initialEntries={["/"]}>
        <ViewerArea />
      </MemoryRouter>,
    );

    const input = screen.getByLabelText("profile-bound-state");
    await user.clear(input);
    await user.type(input, "previous profile state");
    expect(input).toHaveValue("previous profile state");

    mocks.sessionState = {
      ...mocks.sessionState,
      session: { ...(mocks.sessionState.session as SessionInfo), profileID: "p2" },
    };
    view.rerender(
      <MemoryRouter initialEntries={["/"]}>
        <ViewerArea />
      </MemoryRouter>,
    );

    expect(screen.getByLabelText("profile-bound-state")).toHaveValue("fresh");
  });

  it("unmounts Viewer routes throughout an identity transition before remounting fresh state", async () => {
    const user = userEvent.setup();
    const view = render(
      <MemoryRouter initialEntries={["/"]}>
        <ViewerArea />
      </MemoryRouter>,
    );

    await user.type(screen.getByLabelText("profile-bound-state"), "departed");
    mocks.sessionState = { ...mocks.sessionState, session: null };
    view.rerender(
      <MemoryRouter initialEntries={["/"]}>
        <ViewerArea />
      </MemoryRouter>,
    );

    expect(screen.queryByLabelText("profile-bound-state")).not.toBeInTheDocument();
    expect(mocks.scopedFetches).toEqual(["p1"]);

    mocks.sessionState = {
      ...mocks.sessionState,
      session: {
        deviceRole: "manager",
        profileID: "p1",
        profile: "adult",
        revealed: false,
      },
    };
    view.rerender(
      <MemoryRouter initialEntries={["/"]}>
        <ViewerArea />
      </MemoryRouter>,
    );

    expect(screen.getByLabelText("profile-bound-state")).toHaveValue("fresh");
    expect(mocks.scopedFetches).toEqual(["p1", "p1"]);
    expect(mocks.routeUnmounts).toBe(1);
  });

  it("keeps Viewer-local state invalidated after a generic session rejection", async () => {
    const user = userEvent.setup();
    const view = render(
      <MemoryRouter initialEntries={["/"]}>
        <ViewerArea />
      </MemoryRouter>,
    );

    await user.type(screen.getByLabelText("profile-bound-state"), "departed");
    mocks.sessionState = { ...mocks.sessionState, session: null };
    view.rerender(
      <MemoryRouter initialEntries={["/"]}>
        <ViewerArea />
      </MemoryRouter>,
    );
    view.rerender(
      <MemoryRouter initialEntries={["/"]}>
        <ViewerArea />
      </MemoryRouter>,
    );

    expect(screen.queryByLabelText("profile-bound-state")).not.toBeInTheDocument();
    expect(mocks.routeMounts).toBe(1);
    expect(mocks.routeUnmounts).toBe(1);
    expect(mocks.scopedFetches).toEqual(["p1"]);
  });

  it("renders a retryable Viewer error when the current session rejects", async () => {
    const user = userEvent.setup();
    mocks.sessionState = {
      ...mocks.sessionState,
      error: "current session failed",
      session: null,
    };
    render(
      <I18nProvider initialLocale="en">
        <MemoryRouter initialEntries={["/"]}>
          <ViewerArea />
        </MemoryRouter>
      </I18nProvider>,
    );

    expect(screen.getByRole("alert")).toHaveTextContent("Could not reach the server.");
    await user.click(screen.getByRole("button", { name: "Retry" }));
    expect(mocks.sessionState.refresh).toHaveBeenCalledOnce();
  });

  it("does not mount a no-profile route between overlapping identity epochs", () => {
    const view = render(
      <MemoryRouter initialEntries={["/"]}>
        <ViewerArea />
      </MemoryRouter>,
    );

    mocks.sessionState = { ...mocks.sessionState, session: null };
    view.rerender(
      <MemoryRouter initialEntries={["/"]}>
        <ViewerArea />
      </MemoryRouter>,
    );
    view.rerender(
      <MemoryRouter initialEntries={["/"]}>
        <ViewerArea />
      </MemoryRouter>,
    );

    expect(mocks.scopedFetches).toEqual(["p1"]);
    expect(mocks.routeMounts).toBe(1);

    mocks.sessionState = {
      ...mocks.sessionState,
      session: {
        deviceRole: "viewer",
        profileID: "p2",
        profile: "managed",
        revealed: false,
      },
    };
    view.rerender(
      <MemoryRouter initialEntries={["/"]}>
        <ViewerArea />
      </MemoryRouter>,
    );

    expect(mocks.scopedFetches).toEqual(["p1", "p2"]);
    expect(mocks.routeMounts).toBe(2);
  });
});
