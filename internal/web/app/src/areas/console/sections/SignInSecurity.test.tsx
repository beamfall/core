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

async function switchToStrict(password: string) {
  render(
    <I18nProvider>
      <SignInSecurity />
    </I18nProvider>,
  );
  const strict = await screen.findByRole("radio", { name: /Strict/ });
  await vi.waitFor(() => expect(strict).toBeEnabled());
  await userEvent.click(strict);
  if (password) await userEvent.type(screen.getByLabelText("Admin password"), password);
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
    await userEvent.click(screen.getByRole("button", { name: "Change sign-in security" }));

    expect(setSignInSecurityMode).toHaveBeenCalledWith("strict", "correct horse");
    expect(await screen.findByRole("status")).toHaveTextContent("Sign-in security changed.");
    expect(screen.getByLabelText("Admin password")).toHaveValue("");
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
    await userEvent.click(screen.getByRole("button", { name: "Change sign-in security" }));

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
    await userEvent.click(screen.getByRole("button", { name: "Change sign-in security" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("Too many attempts.");

    await userEvent.type(screen.getByLabelText("Admin password"), "again");
    await userEvent.click(screen.getByRole("button", { name: "Change sign-in security" }));
    // Generous waits: under full-suite load the second refusal can take over
    // the default second to render.
    await vi.waitFor(() => expect(setSignInSecurityMode).toHaveBeenCalledTimes(2), {
      timeout: 5_000,
    });
    await vi.waitFor(
      () =>
        expect(screen.getByRole("alert")).toHaveTextContent(
          "Only a Manager device can change sign-in security.",
        ),
      { timeout: 5_000 },
    );
  });

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
