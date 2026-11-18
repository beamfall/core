import "@testing-library/jest-dom/vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { setRevealPin } from "@/lib/graphql";
import type { SessionInfo } from "@/lib/graphql/types";
import { I18nProvider } from "@/lib/i18n";
import { type SessionState, useSession } from "@/lib/session";
import { RestrictionsPage } from "./RestrictionsPage";

vi.mock("@/lib/session", () => ({ useSession: vi.fn() }));
vi.mock("@/lib/graphql", () => ({ setRevealPin: vi.fn() }));

const reveal = vi.fn();
const relock = vi.fn();
const refresh = vi.fn();

function withSession(revealPinSet: boolean | undefined, revealed: boolean) {
  vi.mocked(useSession).mockReturnValue({
    session: { revealed, revealPinSet } as SessionInfo,
    revealed,
    reveal,
    relock,
    refresh,
  } as unknown as SessionState);
}

function renderPage() {
  return render(
    <I18nProvider>
      <MemoryRouter>
        <RestrictionsPage />
      </MemoryRouter>
    </I18nProvider>,
  );
}

describe("Manage Restrictions setup (CORE-2463)", () => {
  beforeEach(() => {
    vi.resetAllMocks();
    refresh.mockResolvedValue(undefined);
    reveal.mockResolvedValue(true);
  });

  it("explains how to set a PIN instead of asking to unlock when none is set", () => {
    withSession(false, false);
    renderPage();

    expect(screen.getByText(/No Restrictions PIN is set yet/)).toBeInTheDocument();
    expect(screen.getByText(/turn on Restricted for the Library/)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Set PIN" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Reveal" })).not.toBeInTheDocument();
  });

  it("refuses two different PINs and sends nothing", async () => {
    const user = userEvent.setup();
    withSession(false, false);
    renderPage();

    await user.type(screen.getByLabelText("New PIN"), "1234");
    await user.type(screen.getByLabelText("Confirm PIN"), "4321");
    await user.click(screen.getByRole("button", { name: "Set PIN" }));

    expect(await screen.findByText("The two PINs don't match.")).toBeInTheDocument();
    expect(setRevealPin).not.toHaveBeenCalled();
  });

  it("sets the PIN, clears the fields and reloads the session", async () => {
    const user = userEvent.setup();
    vi.mocked(setRevealPin).mockResolvedValue({ revealed: false });
    withSession(false, false);
    renderPage();

    await user.type(screen.getByLabelText("New PIN"), "1234");
    await user.type(screen.getByLabelText("Confirm PIN"), "1234");
    await user.click(screen.getByRole("button", { name: "Set PIN" }));

    await waitFor(() => expect(setRevealPin).toHaveBeenCalledWith("1234"));
    expect(await screen.findByRole("status")).toHaveTextContent("PIN saved.");
    expect(refresh).toHaveBeenCalled();
    expect(screen.getByLabelText("New PIN")).toHaveValue("");
  });

  it("says when this Profile cannot have a PIN", async () => {
    const user = userEvent.setup();
    vi.mocked(setRevealPin).mockRejectedValue(new Error("profile has no reveal path"));
    withSession(false, false);
    renderPage();

    await user.type(screen.getByLabelText("New PIN"), "1234");
    await user.type(screen.getByLabelText("Confirm PIN"), "1234");
    await user.click(screen.getByRole("button", { name: "Set PIN" }));

    expect(await screen.findByRole("alert")).toHaveTextContent(
      "This profile can't have a Restrictions PIN.",
    );
  });

  it("keeps the unlock field when a PIN is set, and when the server does not say", () => {
    for (const pinSet of [true, undefined]) {
      withSession(pinSet, false);
      const { unmount } = renderPage();
      expect(screen.getByRole("button", { name: "Reveal" })).toBeInTheDocument();
      expect(screen.queryByRole("button", { name: "Set PIN" })).not.toBeInTheDocument();
      unmount();
    }
  });

  it("changes the PIN once unlocked and points to Console Libraries", async () => {
    const user = userEvent.setup();
    vi.mocked(setRevealPin).mockResolvedValue({ revealed: true });
    withSession(true, true);
    renderPage();

    expect(screen.getByRole("link", { name: /Console › Libraries/ })).toHaveAttribute(
      "href",
      "/console/libraries",
    );
    await user.click(screen.getByRole("button", { name: "Change PIN" }));
    await user.type(screen.getByLabelText("New PIN"), "9876");
    await user.type(screen.getByLabelText("Confirm PIN"), "9876");
    await user.click(screen.getByRole("button", { name: "Change PIN" }));

    await waitFor(() => expect(setRevealPin).toHaveBeenCalledWith("9876"));
    expect(await screen.findByRole("status")).toHaveTextContent("PIN saved.");
    expect(screen.queryByLabelText("New PIN")).not.toBeInTheDocument();
  });
});
