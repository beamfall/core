import type { ReactNode } from "react";
import "@testing-library/jest-dom/vitest";
import { cleanup, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  fetchCurrentSession,
  fetchManifest,
  fetchProfiles,
  reveal as revealMutation,
  switchProfile as switchProfileMutation,
} from "@/lib/graphql";
import type { SessionInfo } from "@/lib/graphql/types";
import { I18nProvider } from "@/lib/i18n";
import { SessionProvider, useSession } from "@/lib/session";
import { ProfilePickerPage } from "../pages/ProfilePickerPage";
import { RevealControls } from "./RevealControls";

// Integration: the real SessionProvider clears the bound session for the
// identity epoch a Reveal or Profile switch opens, which unmounts the control
// (as ViewerArea does) while the mutation runs. Its failure must still show.

vi.mock("@/lib/graphql", () => ({
  fetchCurrentSession: vi.fn(),
  fetchManifest: vi.fn(),
  fetchProfiles: vi.fn(),
  promote: vi.fn(),
  relock: vi.fn(),
  renewSession: vi.fn().mockResolvedValue(false),
  reveal: vi.fn(),
  switchProfile: vi.fn(),
}));
vi.mock("@/lib/compat", () => ({
  advanceCompatibilityEpoch: vi.fn(),
  isBlocked: vi.fn(() => false),
  isCompatibilityRefusal: vi.fn(() => false),
  performHandshake: vi.fn().mockResolvedValue({ messageClass: "compat-ok" }),
  rebindCompatibility: vi.fn().mockResolvedValue({ messageClass: "compat-ok" }),
  subscribeCompatibilityBlock: vi.fn(() => () => {}),
}));
vi.mock("../lib/renderFocus", () => ({ prepareRenderFocusRestore: vi.fn() }));
vi.mock("@/components/chrome/AppShell", () => ({
  AppShell: ({ children }: { children: ReactNode }) => <>{children}</>,
}));

const session: SessionInfo = {
  deviceRole: "viewer",
  profileID: "p1",
  profile: "adult",
  revealed: false,
};

// Mirrors ViewerArea: no Viewer route stays mounted without a bound session.
function Gate({ children }: { children: ReactNode }) {
  const { session: bound } = useSession();
  return bound ? <>{children}</> : <p>pending</p>;
}

function renderWithSession(children: ReactNode) {
  render(
    <I18nProvider>
      <MemoryRouter>
        <SessionProvider>
          <Gate>{children}</Gate>
        </SessionProvider>
      </MemoryRouter>
    </I18nProvider>,
  );
}

beforeEach(() => {
  vi.mocked(fetchCurrentSession).mockResolvedValue(session);
  vi.mocked(fetchManifest).mockResolvedValue({
    profileID: "p1",
    revealed: false,
    generatedAt: "2026-11-11T00:00:00Z",
    navigation: [],
    layouts: [],
    libraries: [],
    theme: { accentColor: "#336699" },
  });
  vi.mocked(fetchProfiles).mockResolvedValue([
    { id: "p1", name: "Adult", kind: "adult" },
    { id: "p2", name: "Kid", kind: "managed" },
  ]);
});

afterEach(() => {
  cleanup();
  vi.mocked(revealMutation).mockReset();
  vi.mocked(switchProfileMutation).mockReset();
});

describe("identity-epoch failures survive the remount", () => {
  it("shows the wrong-PIN error when Reveal resolves false", async () => {
    vi.mocked(revealMutation).mockResolvedValue({ revealed: false });
    const user = userEvent.setup();
    renderWithSession(<RevealControls />);

    await user.click(await screen.findByRole("button", { name: /restrictions/i }));
    await user.type(screen.getByLabelText(/pin/i), "0000");
    await user.click(screen.getByRole("button", { name: /^reveal$/i }));

    expect(await screen.findByText("That PIN is not right.")).toBeInTheDocument();
    expect(screen.getByRole("dialog")).toBeInTheDocument();
    expect(revealMutation).toHaveBeenCalledOnce();
  });

  it("shows the switch error when a Profile switch fails", async () => {
    vi.mocked(switchProfileMutation).mockRejectedValue(new Error("offline"));
    const user = userEvent.setup();
    renderWithSession(<ProfilePickerPage />);

    await user.click(await screen.findByRole("button", { name: /kid/i }));

    expect(await screen.findByText("Could not switch the profile.")).toBeInTheDocument();
  });
});
