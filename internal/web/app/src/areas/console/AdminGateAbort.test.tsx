import "@testing-library/jest-dom/vitest";
import { act, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
// Mock only the admin-session/auth surface (the "gate request"); the Console
// fetcher below (fetchRemoteAccess, from console.ts) and the transport layer
// stay real so the underlying fetch()'s AbortSignal is directly observable —
// this is the "common fetcher path", not a specially signal-aware operation.
import { endAdminSession, fetchAdminSession, verifyAdminSession } from "@/lib/graphql/auth";
import { fetchRemoteAccess } from "@/lib/graphql/console";
import { I18nProvider } from "@/lib/i18n";
import { useSession } from "@/lib/session";
import { AdminGate } from "./AdminGate";
import { useAsync } from "./useAsync";

vi.mock("@/lib/graphql/auth", () => ({
  fetchAdminSession: vi.fn(),
  verifyAdminSession: vi.fn(),
  endAdminSession: vi.fn(),
}));
vi.mock("@/lib/session", () => ({ useSession: vi.fn() }));

function setSession(profile: "adult" | "managed", profileID = "p1") {
  vi.mocked(useSession).mockReturnValue({
    session: { deviceRole: "manager", profileID, profile, revealed: false },
  } as unknown as ReturnType<typeof useSession>);
}

let capturedSignal: AbortSignal | undefined;
let fetchMock: ReturnType<typeof vi.fn>;
let resolveFetch: ((response: Response) => void) | undefined;
let abortFired = false;

beforeEach(() => {
  vi.mocked(fetchAdminSession).mockReset();
  vi.mocked(verifyAdminSession).mockReset();
  vi.mocked(endAdminSession).mockReset();
  vi.mocked(fetchAdminSession).mockResolvedValue({
    active: true,
    expiresAt: "2999-01-01T00:00:00Z",
  });
  vi.mocked(endAdminSession).mockResolvedValue({ active: false });
  setSession("adult");
  delete window.beamfallHost;

  capturedSignal = undefined;
  resolveFetch = undefined;
  abortFired = false;
  // Deliberately let the transport resolve after abort so this covers both
  // halves of the contract: expiry fires the request signal, and a response
  // which loses that race cannot land in the departed Console screen.
  fetchMock = vi.fn((_url: string, init?: RequestInit) => {
    capturedSignal = init?.signal ?? undefined;
    return new Promise<Response>((resolve) => {
      resolveFetch = resolve;
      init?.signal?.addEventListener("abort", () => {
        abortFired = true;
      });
    });
  });
  vi.stubGlobal("fetch", fetchMock);
});

afterEach(() => {
  delete window.beamfallHost;
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

function ConsoleScreen() {
  const { data } = useAsync(fetchRemoteAccess);
  return <div>{data ? "LATE RESPONSE LANDED" : "CONSOLE SCREEN CONTENT"}</div>;
}

function renderGate() {
  render(
    <I18nProvider>
      <AdminGate>
        <ConsoleScreen />
      </AdminGate>
    </I18nProvider>,
  );
}

async function flushEffects(): Promise<void> {
  await act(async () => {
    for (let index = 0; index < 10; index++) await Promise.resolve();
  });
}

describe("AdminGate expiry cancels in-flight Console transport", () => {
  it("aborts a common (non-specially-signal-aware) Console fetcher's transport request when the Admin Session expires", async () => {
    vi.useFakeTimers();
    vi.mocked(fetchAdminSession).mockResolvedValue({
      active: true,
      expiresAt: new Date(Date.now() + 1_000).toISOString(),
    });
    renderGate();
    await flushEffects();

    expect(screen.getByText("CONSOLE SCREEN CONTENT")).toBeInTheDocument();
    expect(fetchMock).toHaveBeenCalled();
    expect(capturedSignal).toBeDefined();
    expect(capturedSignal?.aborted).toBe(false);

    act(() => vi.advanceTimersByTime(1_000));

    expect(screen.queryByText("CONSOLE SCREEN CONTENT")).toBeNull();
    expect(screen.getByText("Admin session expired")).toBeInTheDocument();
    expect(abortFired).toBe(true);
    expect(capturedSignal?.aborted).toBe(true);

    await act(async () => {
      resolveFetch?.(
        new Response(
          JSON.stringify({
            data: { remoteAccess: { plainHTTPPublicWarning: false, endpoints: [] } },
          }),
          { status: 200, headers: { "Content-Type": "application/json" } },
        ),
      );
      await Promise.resolve();
    });

    expect(screen.queryByText("LATE RESPONSE LANDED")).toBeNull();
    expect(screen.getByText("Admin session expired")).toBeInTheDocument();
  });
});
