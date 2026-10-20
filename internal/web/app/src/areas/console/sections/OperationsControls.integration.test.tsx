import "@testing-library/jest-dom/vitest";
import { act, cleanup, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { NavigationManifest, SessionInfo } from "@/lib/graphql/types";
import { I18nProvider } from "@/lib/i18n";
import { SessionProvider, useSession } from "@/lib/session";
import { AdminGate } from "../AdminGate";
import { OperationsControls } from "./OperationsControls";

const session: SessionInfo = {
  deviceRole: "manager",
  profileID: "profile-alpha",
  profile: "adult",
  revealed: false,
};

const manifest = {
  profileID: "profile-alpha",
  revealed: false,
  generatedAt: "2026-08-02T00:00:00Z",
  navigation: [],
  layouts: [],
  libraries: [],
  theme: { accentColor: "#336699" },
} satisfies NavigationManifest;

interface HeldTransport {
  operation: string;
  signal: AbortSignal;
  aborted: ReturnType<typeof vi.fn>;
}

function jsonResponse(body: unknown): Response {
  return {
    ok: true,
    status: 200,
    json: async () => body,
  } as Response;
}

function queryFrom(init?: RequestInit): string {
  return JSON.parse(String(init?.body)).query as string;
}

function holdUntilAbort(operation: string, signal: AbortSignal): HeldTransport & Promise<Response> {
  const aborted = vi.fn();
  signal.addEventListener("abort", aborted, { once: true });
  const pending = new Promise<Response>((_resolve, reject) => {
    signal.addEventListener(
      "abort",
      () => reject(signal.reason ?? new DOMException("Aborted", "AbortError")),
      { once: true },
    );
  });
  return Object.assign(pending, { operation, signal, aborted });
}

function installProductionTransport(expiresAt: string): HeldTransport[] {
  const held: HeldTransport[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input);
      if (path === "/api/compat/handshake") {
        return Promise.resolve(
          jsonResponse({ decision: "compatible", messageClass: "compat-compatible" }),
        );
      }
      if (path === "/api/manifest") return Promise.resolve(jsonResponse(manifest));
      if (path !== "/api/graphql") throw new Error(`Unexpected production request: ${path}`);

      const query = queryFrom(init);
      if (query.includes("CurrentSession")) {
        return Promise.resolve(jsonResponse({ data: { currentSession: session } }));
      }
      if (query.includes("Profiles")) {
        return Promise.resolve(jsonResponse({ data: { profiles: [] } }));
      }
      if (query.includes("AdminSession")) {
        return Promise.resolve(
          jsonResponse({ data: { adminSession: { active: true, expiresAt } } }),
        );
      }
      for (const operation of ["OperationsGovernor", "ConsoleConfig", "Jobs"]) {
        if (!query.includes(operation)) continue;
        const signal = init?.signal as AbortSignal;
        const pending = holdUntilAbort(operation, signal);
        held.push(pending);
        return pending;
      }
      throw new Error(`Unexpected production GraphQL request: ${query}`);
    }),
  );
  return held;
}

function ConsoleBoundary({ childrenMounted }: { childrenMounted: boolean }) {
  const { session: currentSession } = useSession();
  if (!currentSession) return null;
  return <AdminGate>{childrenMounted ? <OperationsControls /> : null}</AdminGate>;
}

function ConsoleHarness({ childrenMounted = true }: { childrenMounted?: boolean }) {
  return (
    <I18nProvider initialLocale="en">
      <SessionProvider>
        <ConsoleBoundary childrenMounted={childrenMounted} />
      </SessionProvider>
    </I18nProvider>
  );
}

async function flushEffects(): Promise<void> {
  await act(async () => {
    for (let index = 0; index < 10; index++) await Promise.resolve();
  });
}

beforeEach(() => {
  class EventSourceStub {
    onerror: ((event: Event) => void) | null = null;
    onmessage: ((event: MessageEvent) => void) | null = null;
    onopen: ((event: Event) => void) | null = null;
    close() {}
  }
  vi.stubGlobal("EventSource", EventSourceStub);
});

afterEach(() => {
  cleanup();
  vi.useRealTimers();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

describe("OperationsControls production transport lifecycle", () => {
  it("aborts real GraphQL transport when the Admin Session expires", async () => {
    vi.useFakeTimers();
    const held = installProductionTransport(new Date(Date.now() + 1_000).toISOString());
    render(<ConsoleHarness />);
    await flushEffects();

    expect(held.map((request) => request.operation).sort()).toEqual([
      "ConsoleConfig",
      "Jobs",
      "OperationsGovernor",
    ]);
    expect(held.every((request) => !request.signal.aborted)).toBe(true);

    act(() => vi.advanceTimersByTime(1_000));

    expect(screen.getByText("Admin session expired")).toBeInTheDocument();
    expect(held.every((request) => request.signal.aborted)).toBe(true);
    expect(held.every((request) => request.aborted.mock.calls.length === 1)).toBe(true);
  });

  it("aborts real GraphQL transport when the Console child unmounts", async () => {
    const held = installProductionTransport("2999-01-01T00:00:00Z");
    const view = render(<ConsoleHarness />);
    await waitFor(() => expect(held).toHaveLength(3));
    expect(held.every((request) => !request.signal.aborted)).toBe(true);

    view.rerender(<ConsoleHarness childrenMounted={false} />);

    await waitFor(() => expect(held.every((request) => request.signal.aborted)).toBe(true));
    expect(held.every((request) => request.aborted.mock.calls.length === 1)).toBe(true);
  });
});
