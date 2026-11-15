import "@testing-library/jest-dom/vitest";
import { cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ApiError, fetchSetupStatus } from "@/lib/api";
import {
  enroll,
  type JoinRequest,
  pollJoinRequest,
  regenerateJoinRequest,
  requestToJoin,
} from "@/lib/graphql";
import { I18nProvider } from "@/lib/i18n";
import { EnrollPage } from "./EnrollPage";

vi.mock("@/lib/graphql", () => ({
  enroll: vi.fn(),
  adminSignIn: vi.fn(),
  requestToJoin: vi.fn(),
  pollJoinRequest: vi.fn(),
  regenerateJoinRequest: vi.fn(),
}));

vi.mock("@/lib/api", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/lib/api")>()),
  fetchSetupStatus: vi.fn(),
}));

const assign = vi.fn();
const HANDLE = "request-handle-1";
const NEXT_HANDLE = "request-handle-2";

function ticket(overrides: Partial<JoinRequest> = {}): JoinRequest {
  return {
    code: "K7M2QX4D",
    requestSecret: HANDLE,
    expiresAt: new Date(Date.now() + 600_000).toISOString(),
    // A few milliseconds keeps the poll loop on real timers.
    pollIntervalSeconds: 0.005,
    ...overrides,
  };
}

beforeEach(() => {
  vi.mocked(enroll).mockReset();
  vi.mocked(requestToJoin).mockReset().mockResolvedValue(ticket());
  vi.mocked(pollJoinRequest).mockReset().mockResolvedValue({ status: "pending" });
  vi.mocked(regenerateJoinRequest).mockReset();
  vi.mocked(fetchSetupStatus)
    .mockReset()
    .mockResolvedValue({ required: false, tokenRequired: true, securityMode: "strict" });
  assign.mockReset();
  Object.defineProperty(window, "location", {
    configurable: true,
    value: { ...window.location, assign },
  });
});

afterEach(() => {
  cleanup();
});

function renderPage() {
  return render(
    <I18nProvider>
      <EnrollPage />
    </I18nProvider>,
  );
}

async function ask() {
  renderPage();
  await userEvent.click(screen.getByRole("button", { name: "Ask to join" }));
}

describe("Ask to join", () => {
  it("requests as a web viewer, displays the code and binds the browser on approval", async () => {
    vi.mocked(pollJoinRequest)
      .mockResolvedValueOnce({ status: "pending" })
      .mockResolvedValue({ status: "approved", rebind: "bound" });

    await ask();

    expect(requestToJoin).toHaveBeenCalledWith("Web Browser", expect.any(AbortSignal));
    expect(await screen.findByText("K7M2QX4D")).toBeInTheDocument();
    expect(
      screen.getByText("On the server, open the Console and approve this code."),
    ).toBeVisible();
    await waitFor(() => expect(assign).toHaveBeenCalledWith("/"));
    expect(pollJoinRequest).toHaveBeenCalledWith(HANDLE, expect.any(AbortSignal));
    expect(screen.getByRole("status")).toHaveTextContent("Connected.");
    // The secret is a credential: never rendered, never stored.
    expect(document.body).not.toHaveTextContent(HANDLE);
    expect(JSON.stringify({ ...window.localStorage })).not.toContain(HANDLE);
    expect(JSON.stringify({ ...window.sessionStorage })).not.toContain(HANDLE);
  });

  it("says the request was declined", async () => {
    vi.mocked(pollJoinRequest).mockResolvedValue({ status: "denied" });

    await ask();

    expect(await screen.findByRole("alert")).toHaveTextContent("declined this request");
    expect(assign).not.toHaveBeenCalled();
    expect(screen.getByRole("button", { name: "Ask to join" })).toBeEnabled();
  });

  it("says the code expired once expiresAt has passed, without polling", async () => {
    vi.mocked(requestToJoin).mockResolvedValue(
      ticket({ expiresAt: new Date(Date.now() - 1000).toISOString() }),
    );

    await ask();

    expect(await screen.findByRole("alert")).toHaveTextContent("This code expired.");
    expect(pollJoinRequest).not.toHaveBeenCalled();
  });

  it("says the code expired when the poll answers 401", async () => {
    vi.mocked(pollJoinRequest).mockRejectedValue(new ApiError("poll failed", 401));

    await ask();

    expect(await screen.findByRole("alert")).toHaveTextContent("This code expired.");
  });

  it("says when requests are throttled", async () => {
    vi.mocked(requestToJoin).mockRejectedValue(new ApiError("request failed", 429));

    await ask();

    expect(await screen.findByRole("alert")).toHaveTextContent("Too many requests");
  });

  it("swaps in a new code and polls with the new secret", async () => {
    let resolvePoll: (value: { status: "pending" }) => void = () => {};
    vi.mocked(pollJoinRequest).mockImplementation(
      () => new Promise((resolve) => (resolvePoll = resolve)),
    );
    vi.mocked(regenerateJoinRequest).mockResolvedValue(
      ticket({ code: "ZZ22YY33", requestSecret: NEXT_HANDLE }),
    );

    await ask();
    await screen.findByText("K7M2QX4D");
    await waitFor(() => expect(pollJoinRequest).toHaveBeenCalledTimes(1));
    await userEvent.click(screen.getByRole("button", { name: "New code" }));

    expect(regenerateJoinRequest).toHaveBeenCalledWith(HANDLE, expect.any(AbortSignal));
    expect(await screen.findByText("ZZ22YY33")).toBeInTheDocument();
    // The poll that was in flight for the old code was aborted and its answer is ignored.
    expect(vi.mocked(pollJoinRequest).mock.calls[0][1]?.aborted).toBe(true);
    resolvePoll({ status: "pending" });
    await waitFor(() =>
      expect(pollJoinRequest).toHaveBeenLastCalledWith(NEXT_HANDLE, expect.any(AbortSignal)),
    );
  });

  it("hides the button on a server without device pairing and keeps the code form", async () => {
    vi.mocked(requestToJoin).mockRejectedValue(new ApiError("request failed", 404));

    await ask();

    await waitFor(() => expect(screen.queryByRole("button", { name: "Ask to join" })).toBeNull());
    expect(screen.queryByRole("alert")).toBeNull();
    expect(screen.getByLabelText("Pairing code")).toBeEnabled();
    expect(screen.getByRole("button", { name: "Connect" })).toBeInTheDocument();
  });

  it("stops polling when the page unmounts", async () => {
    const view = renderPage();
    await userEvent.click(screen.getByRole("button", { name: "Ask to join" }));
    await waitFor(() => expect(pollJoinRequest).toHaveBeenCalled());

    view.unmount();
    const calls = vi.mocked(pollJoinRequest).mock.calls.length;
    await new Promise((resolve) => setTimeout(resolve, 50));

    expect(pollJoinRequest).toHaveBeenCalledTimes(calls);
    expect(vi.mocked(pollJoinRequest).mock.calls.at(-1)?.[1]?.aborted).toBe(true);
  });

  it("cancels a pending ask when a typed code is submitted", async () => {
    vi.mocked(enroll).mockReturnValue(new Promise(() => {}));

    await ask();
    await screen.findByText("K7M2QX4D");
    await userEvent.type(screen.getByLabelText("Pairing code"), "ABCD1234");
    await userEvent.click(screen.getByRole("button", { name: "Connect" }));

    expect(screen.queryByText("K7M2QX4D")).toBeNull();
    const calls = vi.mocked(pollJoinRequest).mock.calls.length;
    await new Promise((resolve) => setTimeout(resolve, 50));
    expect(pollJoinRequest).toHaveBeenCalledTimes(calls);
    // The typed attempt owns the page until it settles.
    expect(screen.getByRole("button", { name: "Ask to join" })).toBeDisabled();
  });

  it("Cancel returns to the button and stops polling", async () => {
    await ask();
    await screen.findByText("K7M2QX4D");
    await userEvent.click(screen.getByRole("button", { name: "Cancel" }));

    expect(screen.getByRole("button", { name: "Ask to join" })).toBeEnabled();
    const calls = vi.mocked(pollJoinRequest).mock.calls.length;
    await new Promise((resolve) => setTimeout(resolve, 50));
    expect(pollJoinRequest).toHaveBeenCalledTimes(calls);
  });
});
