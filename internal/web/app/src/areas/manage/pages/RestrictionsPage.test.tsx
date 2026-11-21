import "@testing-library/jest-dom/vitest";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { setRevealPin } from "@/lib/graphql";
import type { SessionInfo } from "@/lib/graphql/types";
import { I18nProvider } from "@/lib/i18n";
import { type SessionState, useSession } from "@/lib/session";
import type { Locale } from "@/strings";
import { RestrictionsPage } from "./RestrictionsPage";

vi.mock("@/lib/session", () => ({ useSession: vi.fn() }));
vi.mock("@/lib/graphql", () => ({ setRevealPin: vi.fn() }));

const reveal = vi.fn();
const relock = vi.fn();
const refreshSession = vi.fn();

function withSession(revealPinSet: boolean | undefined, revealed: boolean) {
  vi.mocked(useSession).mockReturnValue({
    session: { revealed, revealPinSet } as SessionInfo,
    revealed,
    reveal,
    relock,
    refreshSession,
  } as unknown as SessionState);
}

function renderPage(initialLocale?: Locale) {
  return render(
    <I18nProvider initialLocale={initialLocale}>
      <MemoryRouter>
        <RestrictionsPage />
      </MemoryRouter>
    </I18nProvider>,
  );
}

describe("Manage Restrictions setup (CORE-2463)", () => {
  beforeEach(() => {
    vi.resetAllMocks();
    refreshSession.mockResolvedValue(undefined);
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
    expect(refreshSession).toHaveBeenCalled();
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

  it("warns about TV-incompatible digits without blocking setup", async () => {
    const user = userEvent.setup();
    withSession(false, false);
    renderPage();
    const warning = /cannot be entered with a TV remote/;
    const pin = screen.getByLabelText("New PIN");

    expect(screen.queryByText(warning)).not.toBeInTheDocument();
    await user.type(pin, "2468");
    expect(screen.queryByText(warning)).not.toBeInTheDocument();
    await user.type(pin, "0");
    expect(screen.getByText(warning)).toBeInTheDocument();
    await user.type(screen.getByLabelText("Confirm PIN"), "24680");
    await user.click(screen.getByRole("button", { name: "Set PIN" }));
    expect(setRevealPin).toHaveBeenCalledWith("24680");
  });

  it("localizes the TV warning and direction setup instructions", async () => {
    const user = userEvent.setup();
    withSession(false, false);
    renderPage("fr");

    const pin = screen.getByLabelText("Nouveau PIN");
    await user.type(pin, "1");
    expect(pin).toHaveAccessibleDescription(/Ce PIN ne peut pas être saisi avec une télécommande/);
    await user.click(screen.getByRole("button", { name: "Directions" }));
    const directions = screen.getByRole("group", { name: "Nouveau PIN" });
    expect(directions).toHaveAccessibleDescription(/Saisissez au moins 6 directions/);
    expect(within(directions).getByRole("button", { name: "Haut" })).toHaveFocus();
  });

  it("saves six arrows as keypad digits and accepts the same PIN typed as digits", async () => {
    const user = userEvent.setup();
    let storedPin: string | undefined;
    vi.mocked(setRevealPin).mockImplementation(async (pin) => {
      storedPin = pin;
      return { revealed: false };
    });
    reveal.mockImplementation(async (pin) => pin === storedPin);
    withSession(false, false);
    const { unmount } = renderPage();

    expect(screen.getByText(/Swipes are easier/)).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Directions" }));
    expect(screen.getByText(/Use at least 6 steps/)).toBeInTheDocument();
    const pin = within(screen.getByRole("group", { name: "New PIN" }));
    const confirm = within(screen.getByRole("group", { name: "Confirm PIN" }));
    const save = screen.getByRole("button", { name: "Set PIN" });

    await user.keyboard("{ArrowUp}{ArrowLeft}{ArrowRight}{ArrowDown}{ArrowUp}");
    for (const direction of ["Up", "Left", "Right", "Down", "Up"]) {
      await user.click(confirm.getByRole("button", { name: direction }));
    }
    expect(pin.getByText("↑←→↓↑")).toBeInTheDocument();
    expect(save).toBeDisabled();
    expect(setRevealPin).not.toHaveBeenCalled();

    await user.click(pin.getByRole("button", { name: "Left" }));
    expect(save).toBeDisabled();
    await user.click(confirm.getByRole("button", { name: "Right" }));
    await user.click(save);
    expect(screen.getByRole("alert")).toHaveTextContent("The two PINs don't match.");
    expect(setRevealPin).not.toHaveBeenCalled();

    await user.click(confirm.getByRole("button", { name: "Remove last step" }));
    await user.click(confirm.getByRole("button", { name: "Left" }));
    expect(pin.getByText("↑←→↓↑←")).toBeInTheDocument();
    expect(confirm.getByText("↑←→↓↑←")).toBeInTheDocument();
    await user.click(save);
    expect(setRevealPin).toHaveBeenCalledWith("246824");
    expect(await screen.findByRole("status")).toHaveTextContent("PIN saved.");
    expect(screen.queryByText("↑←→↓↑←")).not.toBeInTheDocument();

    unmount();
    withSession(true, false);
    renderPage();
    await user.type(screen.getByLabelText("PIN"), "246824");
    await user.click(screen.getByRole("button", { name: "Reveal" }));
    expect(reveal).toHaveBeenCalledWith(storedPin);
    await expect(reveal.mock.results[0].value).resolves.toBe(true);
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
