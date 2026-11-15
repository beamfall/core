import "@testing-library/jest-dom/vitest";
import { cleanup, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ApiError, fetchSetupStatus } from "@/lib/api";
import { setSignInSecurityMode } from "@/lib/graphql";
import { I18nProvider } from "@/lib/i18n";
import { SignInSecurity } from "./SignInSecurity";

vi.mock("@/lib/graphql", () => ({
  setSignInSecurityMode: vi.fn(),
}));

vi.mock("@/lib/api", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/lib/api")>()),
  fetchSetupStatus: vi.fn(),
}));

beforeEach(() => {
  vi.mocked(setSignInSecurityMode).mockReset();
  vi.mocked(fetchSetupStatus)
    .mockReset()
    .mockResolvedValue({ required: false, tokenRequired: true, securityMode: "home" });
});

afterEach(() => {
  cleanup();
});

// Every wait in the strict-mode flow awaits one specific state with the same
// explicit ceiling: under full parallel load a render can take well over the
// one-second default (CORE-1116).
const LOADED = { timeout: 10_000 };

async function switchToStrict(password: string) {
  render(
    <I18nProvider>
      <SignInSecurity />
    </I18nProvider>,
  );
  const strict = await screen.findByRole("radio", { name: /Strict/ }, LOADED);
  await vi.waitFor(() => expect(strict).toBeEnabled(), LOADED);
  await userEvent.click(strict);
  if (password) await userEvent.type(screen.getByLabelText("Admin password"), password);
}

// Home to Strict asks for confirmation first (CORE-1608).
async function submitStrict() {
  await userEvent.click(screen.getByRole("button", { name: "Change sign-in security" }));
  await userEvent.click(await screen.findByRole("button", { name: "Switch to Strict" }, LOADED));
}

describe("SignInSecurity", () => {
  it("shows the current mode from the setup probe", async () => {
    vi.mocked(fetchSetupStatus).mockResolvedValue({
      required: false,
      tokenRequired: true,
      securityMode: "strict",
    });
    render(
      <I18nProvider>
        <SignInSecurity />
      </I18nProvider>,
    );

    await vi.waitFor(() => expect(screen.getByRole("radio", { name: /Strict/ })).toBeChecked());
  });

  it("switches the mode with the admin password and clears it", async () => {
    vi.mocked(setSignInSecurityMode).mockResolvedValue("strict");

    await switchToStrict("correct horse");
    await submitStrict();

    expect(setSignInSecurityMode).toHaveBeenCalledWith("strict", "correct horse");
    expect(await screen.findByRole("status")).toHaveTextContent("Sign-in security changed.");
    expect(screen.getByLabelText("Admin password")).toHaveValue("");
  });

  it("confirms the recovery path before switching Home to Strict", async () => {
    vi.mocked(setSignInSecurityMode).mockResolvedValue("strict");

    await switchToStrict("correct horse");
    await userEvent.click(screen.getByRole("button", { name: "Change sign-in security" }));
    const dialog = await screen.findByRole("alertdialog");
    expect(dialog).toHaveTextContent("beamfall pairing-code");
    expect(setSignInSecurityMode).not.toHaveBeenCalled();

    await userEvent.click(screen.getByRole("button", { name: "Cancel" }));
    expect(setSignInSecurityMode).not.toHaveBeenCalled();
    expect(screen.getByLabelText("Admin password")).toHaveValue("correct horse");

    await submitStrict();
    expect(setSignInSecurityMode).toHaveBeenCalledWith("strict", "correct horse");
  });

  it("switches Strict back to Home without a confirmation", async () => {
    vi.mocked(fetchSetupStatus).mockResolvedValue({
      required: false,
      tokenRequired: true,
      securityMode: "strict",
    });
    vi.mocked(setSignInSecurityMode).mockResolvedValue("home");
    render(
      <I18nProvider>
        <SignInSecurity />
      </I18nProvider>,
    );
    const home = await screen.findByRole("radio", { name: /Home/ });
    await vi.waitFor(() => expect(home).toBeEnabled());
    await userEvent.click(home);
    await userEvent.type(screen.getByLabelText("Admin password"), "correct horse");
    await userEvent.click(screen.getByRole("button", { name: "Change sign-in security" }));

    expect(setSignInSecurityMode).toHaveBeenCalledWith("home", "correct horse");
    expect(screen.queryByRole("alertdialog")).toBeNull();
  });

  it("keeps the button disabled until the admin password is entered", async () => {
    await switchToStrict("");

    expect(screen.getByRole("button", { name: "Change sign-in security" })).toBeDisabled();
    expect(setSignInSecurityMode).not.toHaveBeenCalled();
  });

  it("reports a wrong password without changing the mode", async () => {
    vi.mocked(setSignInSecurityMode).mockRejectedValue(
      new ApiError("POST /api/web/signin-security failed", 401),
    );

    await switchToStrict("wrong");
    await submitStrict();

    expect(await screen.findByRole("alert")).toHaveTextContent(
      "The admin password was not accepted.",
    );
    expect(screen.getByLabelText("Admin password")).toHaveValue("");
  });

  it("reports throttling and a non-manager refusal", async () => {
    vi.mocked(setSignInSecurityMode)
      .mockRejectedValueOnce(new ApiError("POST /api/web/signin-security failed", 429))
      .mockRejectedValueOnce(new ApiError("POST /api/web/signin-security failed", 403));

    await switchToStrict("wrong");
    await submitStrict();
    expect(await screen.findByRole("alert", {}, LOADED)).toHaveTextContent("Too many attempts.");
    // The refusal moves focus to the alert on the next frame; typing before
    // that frame lands loses keystrokes to the alert under load.
    await vi.waitFor(() => expect(screen.getByRole("alert")).toHaveFocus(), LOADED);

    await userEvent.type(screen.getByLabelText("Admin password"), "again");
    await submitStrict();
    await vi.waitFor(() => expect(setSignInSecurityMode).toHaveBeenCalledTimes(2), LOADED);
    await vi.waitFor(
      () =>
        expect(screen.getByRole("alert")).toHaveTextContent(
          "Only a Manager device can change sign-in security.",
        ),
      LOADED,
    );
    // The test budget must outlast its own waits above.
  }, 60_000);

  it("shows an error with a retry and offers no save when the probe fails", async () => {
    vi.mocked(fetchSetupStatus)
      .mockResolvedValueOnce({ required: false, tokenRequired: true, securityMode: null })
      .mockResolvedValueOnce({ required: false, tokenRequired: true, securityMode: "strict" });
    render(
      <I18nProvider>
        <SignInSecurity />
      </I18nProvider>,
    );

    expect(await screen.findByRole("alert")).toHaveTextContent("Could not reach the server.");
    expect(screen.getByRole("radio", { name: /Strict/ })).toBeDisabled();
    expect(screen.getByRole("radio", { name: /Home/ })).toBeDisabled();
    await userEvent.type(screen.getByLabelText("Admin password"), "correct horse");
    expect(screen.getByRole("button", { name: "Change sign-in security" })).toBeDisabled();

    await userEvent.click(screen.getByRole("button", { name: "Try again" }));
    await vi.waitFor(() => expect(screen.getByRole("radio", { name: /Strict/ })).toBeChecked());
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
    expect(fetchSetupStatus).toHaveBeenCalledTimes(2);
    expect(setSignInSecurityMode).not.toHaveBeenCalled();
  });
});
