import "@testing-library/jest-dom/vitest";
import { act, cleanup, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ApiError, fetchSetupStatus } from "@/lib/api";
import { adminSignIn, enroll } from "@/lib/graphql";
import { I18nProvider } from "@/lib/i18n";
import { EnrollPage } from "./EnrollPage";

vi.mock("@/lib/graphql", () => ({
  enroll: vi.fn(),
  adminSignIn: vi.fn(),
}));

vi.mock("@/lib/api", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/lib/api")>()),
  fetchSetupStatus: vi.fn(),
}));

const assign = vi.fn();

beforeEach(() => {
  vi.mocked(enroll).mockReset();
  vi.mocked(adminSignIn).mockReset();
  vi.mocked(fetchSetupStatus)
    .mockReset()
    .mockResolvedValue({ required: false, tokenRequired: true, securityMode: "home" });
  assign.mockReset();
  Object.defineProperty(window, "location", {
    configurable: true,
    value: { ...window.location, assign },
  });
});

afterEach(() => {
  cleanup();
});

async function submit(code: string) {
  render(
    <I18nProvider>
      <EnrollPage />
    </I18nProvider>,
  );
  await userEvent.type(screen.getByLabelText("Pairing code"), code);
  await userEvent.click(screen.getByRole("button", { name: "Connect" }));
}

describe("EnrollPage", () => {
  it("says in plain words where a pairing code comes from", () => {
    render(
      <I18nProvider>
        <EnrollPage />
      </I18nProvider>,
    );

    const help = screen.getByRole("region", { name: "Where do I get a pairing code?" });
    expect(help).toHaveTextContent("go to Profiles & access and choose Add a device.");
    expect(help).toHaveTextContent("Ask the person who runs it");
  });

  it("redirects to the app root and reports the connection when the rebind succeeds", async () => {
    vi.mocked(enroll).mockResolvedValue("bound");

    await submit("ABCD-1234");

    expect(enroll).toHaveBeenCalledWith("ABCD-1234", "Web Browser");
    expect(assign).toHaveBeenCalledWith("/");
    expect(screen.getByRole("status")).toHaveTextContent("Connected.");
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("still redirects after a 201 whose rebind rejected, with a handshake notice", async () => {
    vi.mocked(enroll).mockResolvedValue("rebind-failed");

    await submit("ABCD-1234");

    expect(assign).toHaveBeenCalledWith("/");
    expect(screen.getByRole("status")).toHaveTextContent("server handshake did not complete");
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("reports an invalid code without redirecting", async () => {
    vi.mocked(enroll).mockRejectedValue(new ApiError("POST /api/web/enroll failed", 401));

    await submit("WRONG-CODE");

    expect(assign).not.toHaveBeenCalled();
    expect(screen.getByRole("alert")).toHaveTextContent("That pairing code is not valid.");
    expect(screen.getByRole("button", { name: "Connect" })).toBeEnabled();
  });

  it("tells a rejected code it may be used or expired, and selects it for retyping", async () => {
    vi.mocked(enroll).mockRejectedValue(new ApiError("POST /api/web/enroll failed", 401));

    await submit("WRONG-CODE");

    expect(screen.getByText(/expires 10 minutes after it's made/)).toBeInTheDocument();
    const field = screen.getByLabelText("Pairing code") as HTMLInputElement;
    await act(() => new Promise((resolve) => requestAnimationFrame(resolve)));
    expect(field).toHaveFocus();
    expect(field.selectionEnd! - field.selectionStart!).toBe("WRONG-CODE".length);
  });

  it("uppercases a pasted code and drops its spaces", async () => {
    vi.mocked(enroll).mockResolvedValue("bound");
    render(
      <I18nProvider>
        <EnrollPage />
      </I18nProvider>,
    );
    const field = screen.getByLabelText("Pairing code");
    await userEvent.click(field);
    await userEvent.paste(" abcd 1234\n");
    expect(field).toHaveValue("ABCD1234");
    await userEvent.click(screen.getByRole("button", { name: "Connect" }));

    expect(enroll).toHaveBeenCalledWith("ABCD1234", "Web Browser");
  });

  it("asks for a code instead of submitting an empty one", async () => {
    render(
      <I18nProvider>
        <EnrollPage />
      </I18nProvider>,
    );
    await userEvent.click(screen.getByRole("button", { name: "Connect" }));

    expect(enroll).not.toHaveBeenCalled();
    expect(screen.getByRole("alert")).toHaveTextContent("Enter the pairing code to continue.");
  });

  it("separates a network failure and throttling from a wrong code", async () => {
    vi.mocked(enroll).mockRejectedValueOnce(new TypeError("Failed to fetch"));
    await submit("ABCD1234");
    expect(screen.getByRole("alert")).toHaveTextContent("Can't reach the server.");
    cleanup();

    vi.mocked(enroll).mockRejectedValueOnce(new ApiError("POST /api/web/enroll failed", 429));
    await submit("ABCD1234");
    expect(screen.getByRole("alert")).toHaveTextContent("Too many attempts.");
  });

  async function signInWithPassword(password: string) {
    render(
      <I18nProvider>
        <EnrollPage />
      </I18nProvider>,
    );
    await userEvent.type(await screen.findByLabelText("Admin password"), password);
    await userEvent.click(screen.getByRole("button", { name: "Sign in" }));
  }

  it("signs in with the admin password and redirects", async () => {
    vi.mocked(adminSignIn).mockResolvedValue("bound");

    await signInWithPassword("correct horse");

    expect(adminSignIn).toHaveBeenCalledWith("correct horse", "Web Browser");
    expect(enroll).not.toHaveBeenCalled();
    expect(assign).toHaveBeenCalledWith("/");
    expect(screen.getByRole("status")).toHaveTextContent("Connected.");
  });

  it("reports a refused admin sign-in without saying why and clears the password", async () => {
    vi.mocked(adminSignIn).mockRejectedValue(
      new ApiError("POST /api/web/admin-signin failed", 401),
    );

    await signInWithPassword("wrong");

    expect(assign).not.toHaveBeenCalled();
    expect(screen.getByRole("alert")).toHaveTextContent("Sign-in failed.");
    expect(screen.getByLabelText("Admin password")).toHaveValue("");
  });

  it("reports throttled admin sign-in as too many attempts", async () => {
    vi.mocked(adminSignIn).mockRejectedValue(
      new ApiError("POST /api/web/admin-signin failed", 429),
    );

    await signInWithPassword("wrong");

    expect(screen.getByRole("alert")).toHaveTextContent("Too many attempts.");
  });

  it("hides the admin-password form when the server is in Strict mode", async () => {
    vi.mocked(fetchSetupStatus).mockResolvedValue({
      required: false,
      tokenRequired: true,
      securityMode: "strict",
    });
    render(
      <I18nProvider>
        <EnrollPage />
      </I18nProvider>,
    );

    await vi.waitFor(() => expect(fetchSetupStatus).toHaveBeenCalled());
    // Let the settled probe commit its state before asserting the form stays hidden.
    await act(async () => {});
    expect(screen.queryByLabelText("Admin password")).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Sign in" })).not.toBeInTheDocument();
  });

  it("hides the admin-password form while the mode is unknown after a failed probe", async () => {
    vi.mocked(fetchSetupStatus).mockResolvedValue({
      required: false,
      tokenRequired: true,
      securityMode: null,
    });
    render(
      <I18nProvider>
        <EnrollPage />
      </I18nProvider>,
    );

    await vi.waitFor(() => expect(fetchSetupStatus).toHaveBeenCalled());
    await act(async () => {});
    expect(screen.queryByLabelText("Admin password")).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Sign in" })).not.toBeInTheDocument();
  });
});
