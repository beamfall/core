import "@testing-library/jest-dom/vitest";
import { cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { NavigationManifest, SessionInfo } from "@/lib/graphql/types";
import { I18nProvider } from "@/lib/i18n";
import { RegisterProvider } from "@/lib/register";
import { SessionProvider, useSession } from "@/lib/session";
import { ManageArea } from ".";

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

function jsonResponse(body: unknown, status = 200): Response {
  return {
    ok: status >= 200 && status < 300,
    status,
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

function installProductionTransport(authStatus: 401 | 403): HeldTransport[] {
  const held: HeldTransport[] = [];
  let sessionRequests = 0;

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
        sessionRequests++;
        return Promise.resolve(
          sessionRequests === 1
            ? jsonResponse({ data: { currentSession: session } })
            : jsonResponse({}, authStatus),
        );
      }
      if (query.includes("Profiles")) {
        return Promise.resolve(jsonResponse({ data: { profiles: [] } }));
      }
      if (query.includes("Notifications")) {
        return Promise.resolve(jsonResponse({ data: { notifications: [] } }));
      }
      for (const operation of ["Libraries", "Plugins"]) {
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

function RefreshProbe() {
  const { needsEnroll, refresh } = useSession();
  return (
    <>
      <button type="button" onClick={() => void refresh()}>
        Refresh identity
      </button>
      <output data-testid="needs-enroll">{String(needsEnroll)}</output>
    </>
  );
}

function ManageHarness() {
  return (
    <MemoryRouter initialEntries={["/libraries"]}>
      <I18nProvider initialLocale="en">
        <RegisterProvider>
          <SessionProvider>
            <RefreshProbe />
            <ManageArea />
          </SessionProvider>
        </RegisterProvider>
      </I18nProvider>
    </MemoryRouter>
  );
}

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
  document.documentElement.removeAttribute("data-register");
  document.documentElement.removeAttribute("data-theme");
});

describe("Manage production identity boundary", () => {
  it.each([401, 403] as const)(
    "aborts deferred Manage transports when identity refresh returns %s",
    async (status) => {
      const held = installProductionTransport(status);
      const user = userEvent.setup();
      render(<ManageHarness />);

      await waitFor(() => expect(held).toHaveLength(2));
      expect(held.map((request) => request.operation).sort()).toEqual(["Libraries", "Plugins"]);
      expect(new Set(held.map((request) => request.signal)).size).toBe(held.length);
      expect(held.every((request) => !request.signal.aborted)).toBe(true);
      expect(screen.getByRole("navigation", { name: "Manage" })).toBeInTheDocument();

      await user.click(screen.getByRole("button", { name: "Refresh identity" }));

      await waitFor(() => expect(screen.getByTestId("needs-enroll")).toHaveTextContent("true"));
      expect(screen.queryByRole("navigation", { name: "Manage" })).not.toBeInTheDocument();
      expect(held.every((request) => request.signal.aborted)).toBe(true);
      expect(held.every((request) => request.aborted.mock.calls.length === 1)).toBe(true);
    },
  );
});
