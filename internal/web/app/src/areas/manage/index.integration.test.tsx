import "@testing-library/jest-dom/vitest";
import { cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes } from "react-router";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { NavigationManifest, SessionInfo } from "@/lib/graphql/types";
import { I18nProvider } from "@/lib/i18n";
import { RegisterProvider } from "@/lib/register";
import { SessionProvider, useSession } from "@/lib/session";
import { type Locale, STRINGS } from "@/strings";
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

function ManageHarness({ page = "libraries", locale = "en" }: { page?: string; locale?: Locale }) {
  return (
    <MemoryRouter initialEntries={[`/manage/${page}`]}>
      <I18nProvider initialLocale={locale}>
        <RegisterProvider>
          <SessionProvider>
            <RefreshProbe />
            <Routes>
              <Route path="/manage/*" element={<ManageArea />} />
            </Routes>
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

function installProfileServer(switchRefusal?: string) {
  const profiles = [
    { id: session.profileID, name: "Alex", kind: "adult" },
    { id: "profile-sam", name: "Sam", kind: "adult" },
  ];
  const pins = new Map<string, string>();
  if (switchRefusal) pins.set("profile-sam", "2468");
  let currentID = session.profileID;
  const attempts: Record<string, unknown>[] = [];

  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input);
      if (path === "/api/compat/handshake") {
        return jsonResponse({ decision: "compatible", messageClass: "compat-compatible" });
      }
      if (path === "/api/manifest") return jsonResponse({ ...manifest, profileID: currentID });
      if (path !== "/api/graphql") throw new Error(`Unexpected request: ${path}`);

      const { query, variables } = JSON.parse(String(init?.body)) as {
        query: string;
        variables: Record<string, unknown>;
      };
      if (query.startsWith("query CurrentSession")) {
        return jsonResponse({
          data: { currentSession: { ...session, profileID: currentID, subtitleLanguages: ["en"] } },
        });
      }
      if (query.startsWith("query Profiles")) {
        const roster = query.includes("entryPinSet")
          ? profiles.map((profile) => ({ ...profile, entryPinSet: pins.has(profile.id) }))
          : profiles;
        return jsonResponse({ data: { profiles: roster } });
      }
      if (query.startsWith("query AdminSession")) {
        return jsonResponse({ data: { adminSession: { active: true } } });
      }
      if (query.startsWith("query SetupState")) {
        return jsonResponse({ data: { setupState: { adminPasswordSet: true } } });
      }
      if (query.startsWith("query CapabilityStates")) {
        return jsonResponse({ data: { capabilityStates: [] } });
      }
      if (query.startsWith("query Notifications")) {
        return jsonResponse({ data: { notifications: [] } });
      }
      if (query.startsWith("mutation UpdateProfile") && variables.profileID === "profile-sam") {
        pins.set("profile-sam", String(variables.entryPIN));
        return jsonResponse({ data: { updateProfile: { id: "profile-sam" } } });
      }
      if (query.startsWith("mutation SwitchProfile")) {
        attempts.push(variables);
        const pin = pins.get(String(variables.profileID));
        if (switchRefusal || (pin && variables.pin !== pin)) {
          return jsonResponse({ errors: [{ message: switchRefusal ?? "profile PIN required" }] });
        }
        currentID = String(variables.profileID);
        return jsonResponse({ data: { switchProfile: { profileID: currentID, revealed: false } } });
      }
      throw new Error(`Unexpected GraphQL request: ${query}`);
    }),
  );
  return attempts;
}

describe("Manage profile settings in the workspace", () => {
  it("switches without a PIN until one is set for the same adult profile", async () => {
    const attempts = installProfileServer();
    const user = userEvent.setup();
    render(<ManageHarness page="profile" />);

    await user.click(await screen.findByRole("button", { name: "Sam" }));
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    await waitFor(() =>
      expect(screen.getByRole("button", { name: /^Sam/ })).toHaveAttribute("aria-current", "true"),
    );
    await user.click(screen.getByRole("button", { name: "Alex" }));
    await waitFor(() =>
      expect(screen.getByRole("button", { name: /^Alex/ })).toHaveAttribute("aria-current", "true"),
    );
    const pinFreeSwitches = [{ profileID: "profile-sam" }, { profileID: session.profileID }];
    expect(attempts).toEqual(pinFreeSwitches);

    await user.click(await screen.findByRole("button", { name: "Open settings" }));
    await user.selectOptions(await screen.findByLabelText("Profile"), "profile-sam");
    await user.type(screen.getByLabelText("New PIN"), "2468");
    await user.type(screen.getByLabelText("Confirm PIN"), "2468");
    await user.click(screen.getByRole("button", { name: "Set PIN" }));
    expect(await screen.findByText("PIN saved.")).toBeVisible();

    const sam = screen.getByRole("button", { name: "Sam" });
    await user.click(sam);
    expect(await screen.findByRole("dialog", { name: "Hi, Sam" })).toBeVisible();
    expect(screen.getByLabelText("PIN", { exact: true })).toHaveValue("");
    expect(screen.getByRole("button", { name: "Continue" })).toBeDisabled();
    await user.click(screen.getByRole("button", { name: "Cancel" }));
    expect(attempts).toEqual(pinFreeSwitches);
    await user.click(sam);
    await user.type(screen.getByLabelText("PIN", { exact: true }), "2468");
    await user.click(screen.getByRole("button", { name: "Continue" }));

    await waitFor(() =>
      expect(screen.getByRole("button", { name: /^Sam/ })).toHaveAttribute("aria-current", "true"),
    );
    expect(attempts).toEqual([...pinFreeSwitches, { profileID: "profile-sam", pin: "2468" }]);
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });

  it.each([
    ["profile PIN required", "fr", "whosWatchingPinWrong"],
    ["profile PIN attempts throttled", "de", "whosWatchingPinThrottled"],
    ["server refusal", "es", "couldNotSwitchProfile"],
  ] as const)(
    "keeps the localized switch error visible after session reload for %s",
    async (refusal, locale, reason) => {
      const attempts = installProfileServer(refusal);
      const user = userEvent.setup();
      const copy = STRINGS[locale];
      render(<ManageHarness page="profile" locale={locale} />);

      const sam = await screen.findByRole("button", { name: "Sam" });
      await user.click(sam);
      await user.type(screen.getByLabelText(copy.pin, { exact: true }), "0000");
      await user.click(screen.getByRole("button", { name: copy.whosWatchingContinue }));

      expect(await screen.findByRole("alert")).toHaveTextContent(copy[reason]);
      expect(screen.getByRole("button", { name: "Sam" })).not.toBe(sam);
      expect(attempts).toEqual([{ profileID: "profile-sam", pin: "0000" }]);
      await user.click(screen.getByRole("button", { name: "Sam" }));
      expect(screen.getByLabelText(copy.pin, { exact: true })).toHaveValue("");
    },
  );

  it.each([
    ["Profile PIN", /^Profile PIN /, "combobox", "Profile"],
    [
      "subtitle languages",
      /^Preferred subtitle languages for your current profile /,
      "textbox",
      STRINGS.en.manageProfileSubtitleLanguages,
    ],
  ] as const)(
    "finds %s, focuses the opener, and focuses the control when opened",
    async (query, result, role, field) => {
      installProfileServer();
      const user = userEvent.setup();
      render(<ManageHarness page="profile" />);

      const search = await screen.findByRole("searchbox", { name: "Search settings" });
      await user.type(search, query);
      const link = screen.getByRole("link", { name: result });
      expect(link).toHaveAttribute("href", "/manage/profile");
      await user.click(link);
      const opener = screen.getByRole("button", { name: "Open settings" });
      await waitFor(() => expect(opener).toHaveFocus(), { timeout: 3000 });
      await user.click(opener);

      const control = await screen.findByRole(role, { name: field });
      expect(control).toBeVisible();
      await user.type(search, query);
      await user.click(screen.getByRole("link", { name: result }));
      await waitFor(() => expect(control).toHaveFocus());
    },
  );
});
