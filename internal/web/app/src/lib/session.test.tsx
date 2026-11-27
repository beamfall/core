import "@testing-library/jest-dom/vitest";
import { act, render, renderHook, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  ApiError,
  getExpectedProfile,
  getIdentityEpochSignal,
  noteProfileRefusal,
} from "@/lib/api";
import {
  advanceCompatibilityEpoch,
  isBlocked,
  isCompatibilityRefusal,
  performHandshake,
  rebindCompatibility,
  subscribeCompatibilityBlock,
} from "@/lib/compat";
import {
  fetchCurrentSession,
  fetchManifest,
  fetchProfiles,
  relock as relockMutation,
  renewSession,
  reveal as revealMutation,
  switchProfile as switchProfileMutation,
} from "@/lib/graphql";
import type { NavigationManifest, ProfileSummary, SessionInfo } from "@/lib/graphql/types";
import { requestManifestRefresh, requestSessionRefresh } from "./manifestRefresh";
import { SessionProvider, useSession } from "./session";

const postUnmountWrites = vi.hoisted(() => ({ armed: false, values: [] as unknown[] }));

vi.mock("react", async (importOriginal) => {
  const react = await importOriginal<typeof import("react")>();
  return {
    ...react,
    useState<T>(initial: T | (() => T)) {
      const [value, setValue] = react.useState(initial);
      const trackedSetValue: typeof setValue = (next) => {
        if (postUnmountWrites.armed) postUnmountWrites.values.push(next);
        setValue(next);
      };
      return [value, trackedSetValue] as const;
    },
  };
});

vi.mock("@/lib/graphql", () => ({
  fetchCurrentSession: vi.fn(),
  fetchManifest: vi.fn(),
  fetchProfiles: vi.fn(),
  reveal: vi.fn(),
  relock: vi.fn(),
  renewSession: vi.fn(),
  switchProfile: vi.fn(),
}));
vi.mock("@/lib/compat", () => ({
  advanceCompatibilityEpoch: vi.fn(),
  isBlocked: vi.fn(),
  isCompatibilityRefusal: vi.fn(),
  performHandshake: vi.fn(),
  rebindCompatibility: vi.fn(),
  subscribeCompatibilityBlock: vi.fn(),
}));

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (error: unknown) => void;
  const promise = new Promise<T>((r, j) => {
    resolve = r;
    reject = j;
  });
  return { promise, reject, resolve };
}

const p1Session: SessionInfo = {
  deviceRole: "viewer",
  profileID: "p1",
  profile: "adult",
  revealed: true,
};
const p2Session: SessionInfo = {
  deviceRole: "viewer",
  profileID: "p2",
  profile: "managed",
  revealed: false,
};
const managerP1Session: SessionInfo = {
  ...p1Session,
  deviceRole: "manager",
  revealed: false,
};
const viewerP1Session: SessionInfo = {
  ...managerP1Session,
  deviceRole: "viewer",
};
const expiredP1Session: SessionInfo = { ...p1Session, revealed: false };
const relockedP1Session: SessionInfo = { ...p1Session, revealed: false };
const unmountedViewerSession: SessionInfo = {
  ...p1Session,
  profileID: "unmounted-viewer-profile",
};

function manifestFixture(
  epoch: string,
  session: SessionInfo,
  generatedAt: string,
): NavigationManifest {
  return {
    profileID: session.profileID,
    revealed: session.revealed,
    generatedAt,
    navigation: [
      { id: `${epoch}-home`, title: `${epoch} Home`, view: "layout", layoutID: `${epoch}-root` },
    ],
    layouts: [],
    libraries: [],
    theme: { accentColor: "#336699" },
  };
}

function rosterFixture(epoch: string, session: SessionInfo): ProfileSummary[] {
  return [
    {
      id: session.profileID,
      name: `${epoch} Current Profile`,
      kind: session.profile,
    },
    {
      id: `${epoch}-sibling`,
      name: `${epoch} Sibling Profile`,
      kind: "managed",
    },
  ];
}

const p1Manifest = manifestFixture("p1-revealed-epoch", p1Session, "2026-06-24T00:00:00Z");
const p1Roster = rosterFixture("p1-revealed-epoch", p1Session);
const p2Manifest = manifestFixture("p2-current-epoch", p2Session, "2026-06-24T00:01:00Z");
const p2Roster = rosterFixture("p2-current-epoch", p2Session);
const managerP1Manifest = manifestFixture(
  "p1-manager-epoch",
  managerP1Session,
  "2026-06-24T00:01:10Z",
);
const managerP1Roster = rosterFixture("p1-manager-epoch", managerP1Session);
const viewerP1Manifest = manifestFixture(
  "p1-viewer-return-epoch",
  viewerP1Session,
  "2026-06-24T00:01:20Z",
);
const viewerP1Roster = rosterFixture("p1-viewer-return-epoch", viewerP1Session);
const expiredP1Manifest = manifestFixture(
  "p1-expired-epoch",
  expiredP1Session,
  "2026-06-24T00:02:00Z",
);
const expiredP1Roster = rosterFixture("p1-expired-epoch", expiredP1Session);
const relockedP1Manifest = manifestFixture(
  "p1-relocked-epoch",
  relockedP1Session,
  "2026-06-24T00:03:00Z",
);
const relockedP1Roster = rosterFixture("p1-relocked-epoch", relockedP1Session);
const unmountedViewerManifest = manifestFixture(
  "unmounted-viewer-epoch",
  unmountedViewerSession,
  "2026-06-24T00:04:00Z",
);
const unmountedViewerRoster = rosterFixture("unmounted-viewer-epoch", unmountedViewerSession);

function Probe() {
  const session = useSession();
  return (
    <>
      <button type="button" onClick={() => void session.switchProfile("p2").catch(() => undefined)}>
        Switch
      </button>
      <button type="button" onClick={() => void session.relock().catch(() => undefined)}>
        Relock
      </button>
      <button type="button" onClick={() => void session.refresh()}>
        Refresh
      </button>
      <button type="button" onClick={() => void session.refreshSession()}>
        Refresh session
      </button>
      <button type="button" onClick={() => void session.reveal("1234").catch(() => {})}>
        Reveal
      </button>
      <span data-testid="profile">{session.session?.profileID ?? "none"}</span>
      <span data-testid="revealed">{String(session.revealed)}</span>
      <span data-testid="manager">{String(session.isManager)}</span>
      <span data-testid="enroll">{String(session.needsEnroll)}</span>
      <span data-testid="manifest">{session.manifest?.navigation[0]?.id ?? "none"}</span>
      <span data-testid="profiles">{session.profiles.length}</span>
      <span data-testid="roster">
        {session.profiles.map((profile) => `${profile.id}:${profile.name}`).join("|") || "none"}
      </span>
      <span data-testid="loading">{String(session.loading)}</span>
      <span data-testid="error">{session.error ?? "none"}</span>
    </>
  );
}

beforeEach(() => {
  postUnmountWrites.armed = false;
  postUnmountWrites.values.length = 0;
  vi.mocked(fetchCurrentSession).mockReset();
  vi.mocked(fetchManifest).mockReset();
  vi.mocked(fetchProfiles).mockReset();
  vi.mocked(relockMutation).mockReset();
  vi.mocked(revealMutation).mockReset();
  vi.mocked(switchProfileMutation).mockReset();
  vi.mocked(isBlocked).mockReset();
  vi.mocked(isCompatibilityRefusal).mockReset();
  vi.mocked(advanceCompatibilityEpoch).mockReset();
  vi.mocked(performHandshake).mockReset();
  vi.mocked(rebindCompatibility).mockReset();
  vi.mocked(subscribeCompatibilityBlock).mockReset();
  vi.mocked(renewSession).mockReset();
  vi.mocked(renewSession).mockResolvedValue(false);
  vi.mocked(isBlocked).mockReturnValue(false);
  vi.mocked(isCompatibilityRefusal).mockReturnValue(false);
  vi.mocked(performHandshake).mockResolvedValue({
    decision: "compatible",
    messageClass: "compat-compatible",
  });
  vi.mocked(rebindCompatibility).mockResolvedValue({
    decision: "compatible",
    messageClass: "compat-compatible",
  });
  vi.mocked(subscribeCompatibilityBlock).mockReturnValue(() => {});
  vi.mocked(fetchManifest).mockResolvedValue(p1Manifest);
  vi.mocked(fetchProfiles).mockResolvedValue(p1Roster);
  vi.mocked(switchProfileMutation).mockResolvedValue({
    profileID: "p2",
    revealed: false,
  });
  vi.mocked(revealMutation).mockResolvedValue({ revealed: true });
  vi.mocked(relockMutation).mockResolvedValue({ revealed: false });
  delete window.beamfallHost;
});

afterEach(() => {
  delete window.beamfallHost;
});

describe("SessionProvider Profile switching", () => {
  it("handshakes before normal API use and stops on a terminal decision", async () => {
    vi.mocked(performHandshake).mockResolvedValueOnce({
      decision: "client-too-old",
      messageClass: "compat-client-too-old",
    });
    vi.mocked(isBlocked).mockReturnValue(true);

    render(
      <SessionProvider>
        <Probe />
      </SessionProvider>,
    );

    await waitFor(() =>
      expect(screen.getByTestId("error")).toHaveTextContent("compat-client-too-old"),
    );
    expect(fetchCurrentSession).not.toHaveBeenCalled();
  });

  it("clears loaded state when a later handshake becomes terminal", async () => {
    let notify: Parameters<typeof subscribeCompatibilityBlock>[0] | undefined;
    vi.mocked(subscribeCompatibilityBlock).mockImplementation((listener) => {
      notify = listener;
      return () => {};
    });
    vi.mocked(fetchCurrentSession).mockResolvedValueOnce(p1Session);

    render(
      <SessionProvider>
        <Probe />
      </SessionProvider>,
    );
    await waitFor(() => expect(screen.getByTestId("profile")).toHaveTextContent("p1"));

    act(() => {
      notify?.({
        decision: "client-too-old",
        messageClass: "compat-client-too-old",
      });
    });

    expect(screen.getByTestId("profile")).toHaveTextContent("none");
    expect(screen.getByTestId("manifest")).toHaveTextContent("none");
    expect(screen.getByTestId("error")).toHaveTextContent("compat-client-too-old");
  });

  it("ignores stale pre-switch loads after switching Profiles", async () => {
    const firstLoad = deferred<SessionInfo>();
    vi.mocked(fetchCurrentSession)
      .mockReturnValueOnce(firstLoad.promise)
      .mockResolvedValueOnce(p2Session);
    vi.mocked(fetchManifest).mockResolvedValue(p2Manifest);
    vi.mocked(fetchProfiles).mockResolvedValue(p2Roster);

    const user = userEvent.setup();
    render(
      <SessionProvider>
        <Probe />
      </SessionProvider>,
    );

    await user.click(screen.getByRole("button", { name: "Switch" }));
    await waitFor(() => expect(screen.getByTestId("profile")).toHaveTextContent("p2"));
    expect(screen.getByTestId("manifest")).toHaveTextContent("p2-current-epoch-home");
    expect(screen.getByTestId("roster")).toHaveTextContent("p2-current-epoch");
    expect(performHandshake).toHaveBeenCalledTimes(2);

    await act(async () => {
      firstLoad.resolve(p1Session);
      await firstLoad.promise;
    });

    expect(screen.getByTestId("profile")).toHaveTextContent("p2");
    expect(fetchManifest).toHaveBeenCalledTimes(1);
  });

  it.each([
    [
      "Profile switch",
      "Switch",
      () =>
        vi.mocked(switchProfileMutation).mockImplementation(async (_profileID, _signal) => {
          throw new Error("offline");
        }),
    ],
    [
      "Relock",
      "Relock",
      () =>
        vi.mocked(relockMutation).mockImplementation(async (_signal) => {
          throw new Error("offline");
        }),
    ],
  ] as const)(
    "clears prior rendered state when %s rebinding fails",
    async (_name, button, rejectAfterCommit) => {
      vi.mocked(fetchCurrentSession)
        .mockResolvedValueOnce(p1Session)
        .mockRejectedValueOnce(new Error("offline"));
      rejectAfterCommit();
      const user = userEvent.setup();

      render(
        <SessionProvider>
          <Probe />
        </SessionProvider>,
      );
      await waitFor(() => expect(screen.getByTestId("profile")).toHaveTextContent("p1"));
      expect(screen.getByTestId("manifest")).toHaveTextContent("home");
      expect(screen.getByTestId("revealed")).toHaveTextContent("true");

      await user.click(screen.getByRole("button", { name: button }));

      await waitFor(() => expect(screen.getByTestId("profile")).toHaveTextContent("none"));
      expect(screen.getByTestId("manifest")).toHaveTextContent("none");
      expect(screen.getByTestId("revealed")).toHaveTextContent("false");
    },
  );

  it("rebinds once and retries a safe session read after a compatibility refusal", async () => {
    const refusal = new ApiError("compatibility decision required", 403, {
      decision: "client-too-old",
      messageClass: "compat-client-too-old",
    });
    vi.mocked(isCompatibilityRefusal).mockImplementation((error) => error === refusal);
    vi.mocked(fetchCurrentSession).mockRejectedValueOnce(refusal).mockResolvedValueOnce(p1Session);

    render(
      <SessionProvider>
        <Probe />
      </SessionProvider>,
    );

    await waitFor(() => expect(screen.getByTestId("profile")).toHaveTextContent("p1"));
    expect(rebindCompatibility).toHaveBeenCalledOnce();
    expect(fetchCurrentSession).toHaveBeenCalledTimes(2);
    expect(screen.getByTestId("enroll")).toHaveTextContent("false");
  });

  it("consumes Apple host handoff messages but uses the server session", async () => {
    const takeInitialMessages = vi.fn(() => [
      { name: "session.handoff", payload: { sessionID: "native-session" } },
      {
        name: "state.profileRevealMode",
        payload: { profileID: "p2", revealed: false, mode: "viewer" },
      },
    ]);
    Object.defineProperty(window, "beamfallHost", {
      configurable: true,
      value: {
        kind: "apple-manager",
        platform: "macos",
        clientClass: "apple-manager-macos-direct",
        takeInitialMessages,
      },
    });
    vi.mocked(fetchCurrentSession).mockResolvedValueOnce(p1Session);

    render(
      <SessionProvider>
        <Probe />
      </SessionProvider>,
    );

    await waitFor(() => expect(screen.getByTestId("profile")).toHaveTextContent("p1"));
    expect(takeInitialMessages).toHaveBeenCalledTimes(1);
    expect(screen.getByTestId("enroll")).toHaveTextContent("false");
    expect(screen.getByTestId("revealed")).toHaveTextContent("true");
  });

  it("does not turn host payloads into local manager or Reveal authority", async () => {
    Object.defineProperty(window, "beamfallHost", {
      configurable: true,
      value: {
        kind: "apple-manager",
        platform: "ipad",
        clientClass: "ipad-manager-app-store",
        takeInitialMessages: () => [
          { name: "session.handoff", payload: { sessionID: "native-session" } },
          {
            name: "state.profileRevealMode",
            payload: { profileID: "p1", revealed: true, mode: "manager" },
          },
        ],
      },
    });
    vi.mocked(fetchCurrentSession).mockResolvedValueOnce(p2Session);
    vi.mocked(fetchManifest).mockResolvedValue(p2Manifest);
    vi.mocked(fetchProfiles).mockResolvedValue(p2Roster);

    render(
      <SessionProvider>
        <Probe />
      </SessionProvider>,
    );

    await waitFor(() => expect(screen.getByTestId("profile")).toHaveTextContent("p2"));
    expect(screen.getByTestId("manager")).toHaveTextContent("false");
    expect(screen.getByTestId("revealed")).toHaveTextContent("false");
  });
});

describe("SessionProvider identity-epoch fetch lifecycle", () => {
  it("clears every scoped field across viewer-to-manager and manager-to-viewer role transitions", async () => {
    const promote = deferred<SessionInfo>();
    const demote = deferred<SessionInfo>();
    vi.mocked(fetchCurrentSession)
      .mockResolvedValueOnce(p1Session)
      .mockReturnValueOnce(promote.promise)
      .mockReturnValueOnce(demote.promise);
    vi.mocked(fetchManifest)
      .mockReset()
      .mockResolvedValueOnce(p1Manifest)
      .mockResolvedValueOnce(managerP1Manifest)
      .mockResolvedValueOnce(viewerP1Manifest);
    vi.mocked(fetchProfiles)
      .mockReset()
      .mockResolvedValueOnce(p1Roster)
      .mockResolvedValueOnce(managerP1Roster)
      .mockResolvedValueOnce(viewerP1Roster);

    const user = userEvent.setup();
    render(
      <SessionProvider>
        <Probe />
      </SessionProvider>,
    );
    await waitFor(() => expect(screen.getByTestId("profile")).toHaveTextContent("p1"));
    expect(screen.getByTestId("manager")).toHaveTextContent("false");
    expect(screen.getByTestId("manifest")).toHaveTextContent("p1-revealed-epoch-home");
    expect(screen.getByTestId("roster")).toHaveTextContent("p1-revealed-epoch");

    await user.click(screen.getByRole("button", { name: "Refresh" }));
    expect(screen.getByTestId("profile")).toHaveTextContent("none");
    expect(screen.getByTestId("manifest")).toHaveTextContent("none");
    expect(screen.getByTestId("profiles")).toHaveTextContent("0");
    expect(screen.getByTestId("manager")).toHaveTextContent("false");
    expect(screen.getByTestId("loading")).toHaveTextContent("true");

    await act(async () => {
      promote.resolve(managerP1Session);
      await promote.promise;
    });
    await waitFor(() => expect(screen.getByTestId("manager")).toHaveTextContent("true"));
    expect(screen.getByTestId("manifest")).toHaveTextContent("p1-manager-epoch-home");
    expect(screen.getByTestId("roster")).toHaveTextContent("p1-manager-epoch");

    await user.click(screen.getByRole("button", { name: "Refresh" }));
    expect(screen.getByTestId("profile")).toHaveTextContent("none");
    expect(screen.getByTestId("manifest")).toHaveTextContent("none");
    expect(screen.getByTestId("profiles")).toHaveTextContent("0");
    expect(screen.getByTestId("manager")).toHaveTextContent("false");
    expect(screen.getByTestId("loading")).toHaveTextContent("true");

    await act(async () => {
      demote.resolve(viewerP1Session);
      await demote.promise;
    });
    await waitFor(() => expect(screen.getByTestId("loading")).toHaveTextContent("false"));
    expect(screen.getByTestId("manager")).toHaveTextContent("false");
    expect(screen.getByTestId("manifest")).toHaveTextContent("p1-viewer-return-epoch-home");
    expect(screen.getByTestId("roster")).toHaveTextContent("p1-viewer-return-epoch");
  });

  it("aborts the previous epoch's in-flight fetch when switching Profiles", async () => {
    const firstLoad = deferred<SessionInfo>();
    vi.mocked(fetchCurrentSession)
      .mockReturnValueOnce(firstLoad.promise)
      .mockResolvedValueOnce(p2Session);
    vi.mocked(fetchManifest).mockResolvedValue(p2Manifest);
    vi.mocked(fetchProfiles).mockResolvedValue(p2Roster);

    const user = userEvent.setup();
    render(
      <SessionProvider>
        <Probe />
      </SessionProvider>,
    );

    await waitFor(() => expect(fetchCurrentSession).toHaveBeenCalled());
    const firstSignal = vi.mocked(fetchCurrentSession).mock.calls[0]?.[0] as AbortSignal;
    expect(firstSignal.aborted).toBe(false);

    await user.click(screen.getByRole("button", { name: "Switch" }));
    await waitFor(() => expect(firstSignal.aborted).toBe(true));

    firstLoad.resolve(p1Session);
  });

  it("ignores a stale rejection from the previous epoch after switching Profiles", async () => {
    const firstLoad = deferred<SessionInfo>();
    vi.mocked(fetchCurrentSession)
      .mockReturnValueOnce(firstLoad.promise)
      .mockResolvedValueOnce(p2Session);
    vi.mocked(fetchManifest).mockResolvedValue(p2Manifest);
    vi.mocked(fetchProfiles).mockResolvedValue(p2Roster);

    const user = userEvent.setup();
    render(
      <SessionProvider>
        <Probe />
      </SessionProvider>,
    );

    await waitFor(() => expect(fetchCurrentSession).toHaveBeenCalled());
    const firstSignal = vi.mocked(fetchCurrentSession).mock.calls[0]?.[0] as AbortSignal;
    await user.click(screen.getByRole("button", { name: "Switch" }));
    await waitFor(() => expect(screen.getByTestId("profile")).toHaveTextContent("p2"));
    expect(firstSignal.aborted).toBe(true);

    await act(async () => {
      firstLoad.reject(new Error("stale rejection"));
      await firstLoad.promise.catch(() => {});
    });

    expect(screen.getByTestId("profile")).toHaveTextContent("p2");
    expect(screen.getByTestId("error")).toHaveTextContent("none");
    expect(screen.getByTestId("loading")).toHaveTextContent("false");
  });

  it("aborts a deferred old-manifest request across a Profile epoch and rejects its late payload", async () => {
    const oldManifestRequest = deferred<NavigationManifest>();
    const departedManifest = manifestFixture(
      "departed-old-manifest-epoch",
      p1Session,
      "2026-06-24T00:05:00Z",
    );
    const departedRoster = rosterFixture("departed-old-manifest-epoch", p1Session);
    vi.mocked(fetchCurrentSession)
      .mockResolvedValueOnce(p1Session)
      .mockResolvedValueOnce(p2Session);
    vi.mocked(fetchManifest)
      .mockReset()
      .mockReturnValueOnce(oldManifestRequest.promise)
      .mockResolvedValueOnce(p2Manifest);
    vi.mocked(fetchProfiles)
      .mockReset()
      .mockResolvedValueOnce(departedRoster)
      .mockResolvedValueOnce(p2Roster);

    const user = userEvent.setup();
    render(
      <SessionProvider>
        <Probe />
      </SessionProvider>,
    );
    await waitFor(() => expect(fetchManifest).toHaveBeenCalledTimes(1));
    const oldSignal = vi.mocked(fetchManifest).mock.calls[0]?.[0] as AbortSignal;
    expect(oldSignal.aborted).toBe(false);

    await user.click(screen.getByRole("button", { name: "Switch" }));
    await waitFor(() =>
      expect(screen.getByTestId("manifest")).toHaveTextContent("p2-current-epoch-home"),
    );
    expect.soft(oldSignal.aborted).toBe(true);
    expect(screen.getByTestId("profile")).toHaveTextContent("p2");
    expect(screen.getByTestId("roster")).toHaveTextContent("p2-current-epoch");

    await act(async () => {
      oldManifestRequest.resolve(departedManifest);
      await oldManifestRequest.promise;
    });

    expect.soft(screen.getByTestId("profile")).toHaveTextContent("p2");
    expect.soft(screen.getByTestId("manifest")).toHaveTextContent("p2-current-epoch-home");
    expect(screen.getByTestId("roster")).toHaveTextContent("p2-current-epoch");
    expect(oldSignal.aborted).toBe(true);
  });

  it("aborts a deferred old-profile roster request across a Profile epoch and rejects its late payload", async () => {
    const oldProfileRequest = deferred<ProfileSummary[]>();
    const departedManifest = manifestFixture(
      "departed-old-profile-epoch",
      p1Session,
      "2026-06-24T00:06:00Z",
    );
    const departedRoster = rosterFixture("departed-old-profile-epoch", p1Session);
    vi.mocked(fetchCurrentSession)
      .mockResolvedValueOnce(p1Session)
      .mockResolvedValueOnce(p2Session);
    vi.mocked(fetchManifest)
      .mockReset()
      .mockResolvedValueOnce(departedManifest)
      .mockResolvedValueOnce(p2Manifest);
    vi.mocked(fetchProfiles)
      .mockReset()
      .mockReturnValueOnce(oldProfileRequest.promise)
      .mockResolvedValueOnce(p2Roster);

    const user = userEvent.setup();
    render(
      <SessionProvider>
        <Probe />
      </SessionProvider>,
    );
    await waitFor(() => expect(fetchProfiles).toHaveBeenCalledTimes(1));
    const oldSignal = vi.mocked(fetchProfiles).mock.calls[0]?.[0] as AbortSignal;
    expect(oldSignal.aborted).toBe(false);

    await user.click(screen.getByRole("button", { name: "Switch" }));
    await waitFor(() => expect(screen.getByTestId("roster")).toHaveTextContent("p2-current-epoch"));
    expect.soft(oldSignal.aborted).toBe(true);
    expect(screen.getByTestId("profile")).toHaveTextContent("p2");
    expect(screen.getByTestId("manifest")).toHaveTextContent("p2-current-epoch-home");

    await act(async () => {
      oldProfileRequest.resolve(departedRoster);
      await oldProfileRequest.promise;
    });

    expect.soft(screen.getByTestId("profile")).toHaveTextContent("p2");
    expect.soft(screen.getByTestId("manifest")).toHaveTextContent("p2-current-epoch-home");
    expect(screen.getByTestId("roster")).toHaveTextContent("p2-current-epoch");
    expect(oldSignal.aborted).toBe(true);
  });

  it("aborts a deferred revealed epoch when Reveal expires and keeps the expired epoch relocked", async () => {
    const preExpiryManifestRequest = deferred<NavigationManifest>();
    const preExpiryManifest = manifestFixture(
      "revealed-before-expiry-epoch",
      p1Session,
      "2026-06-24T00:07:00Z",
    );
    const preExpiryRoster = rosterFixture("revealed-before-expiry-epoch", p1Session);
    vi.mocked(fetchCurrentSession)
      .mockResolvedValueOnce(p1Session)
      .mockResolvedValueOnce(p1Session)
      .mockResolvedValueOnce(expiredP1Session);
    vi.mocked(fetchManifest)
      .mockReset()
      .mockResolvedValueOnce(p1Manifest)
      .mockReturnValueOnce(preExpiryManifestRequest.promise)
      .mockResolvedValueOnce(expiredP1Manifest);
    vi.mocked(fetchProfiles)
      .mockReset()
      .mockResolvedValueOnce(p1Roster)
      .mockResolvedValueOnce(preExpiryRoster)
      .mockResolvedValueOnce(expiredP1Roster);

    const user = userEvent.setup();
    render(
      <SessionProvider>
        <Probe />
      </SessionProvider>,
    );
    await waitFor(() =>
      expect(screen.getByTestId("manifest")).toHaveTextContent("p1-revealed-epoch-home"),
    );

    await user.click(screen.getByRole("button", { name: "Refresh" }));
    await waitFor(() => expect(fetchManifest).toHaveBeenCalledTimes(2));
    const preExpirySignal = vi.mocked(fetchManifest).mock.calls[1]?.[0] as AbortSignal;
    expect(preExpirySignal.aborted).toBe(false);

    await user.click(screen.getByRole("button", { name: "Refresh" }));
    await waitFor(() =>
      expect(screen.getByTestId("manifest")).toHaveTextContent("p1-expired-epoch-home"),
    );
    expect.soft(preExpirySignal.aborted).toBe(true);
    expect(screen.getByTestId("revealed")).toHaveTextContent("false");
    expect(screen.getByTestId("roster")).toHaveTextContent("p1-expired-epoch");

    await act(async () => {
      preExpiryManifestRequest.resolve(preExpiryManifest);
      await preExpiryManifestRequest.promise;
    });

    expect.soft(screen.getByTestId("revealed")).toHaveTextContent("false");
    expect.soft(screen.getByTestId("manifest")).toHaveTextContent("p1-expired-epoch-home");
    expect(screen.getByTestId("roster")).toHaveTextContent("p1-expired-epoch");
    expect(preExpirySignal.aborted).toBe(true);
  });

  it("aborts a deferred revealed epoch on Relock and keeps the relocked epoch", async () => {
    const preRelockProfileRequest = deferred<ProfileSummary[]>();
    const preRelockManifest = manifestFixture(
      "revealed-before-relock-epoch",
      p1Session,
      "2026-06-24T00:08:00Z",
    );
    const preRelockRoster = rosterFixture("revealed-before-relock-epoch", p1Session);
    vi.mocked(fetchCurrentSession)
      .mockResolvedValueOnce(p1Session)
      .mockResolvedValueOnce(p1Session)
      .mockResolvedValueOnce(relockedP1Session);
    vi.mocked(fetchManifest)
      .mockReset()
      .mockResolvedValueOnce(p1Manifest)
      .mockResolvedValueOnce(preRelockManifest)
      .mockResolvedValueOnce(relockedP1Manifest);
    vi.mocked(fetchProfiles)
      .mockReset()
      .mockResolvedValueOnce(p1Roster)
      .mockReturnValueOnce(preRelockProfileRequest.promise)
      .mockResolvedValueOnce(relockedP1Roster);

    const user = userEvent.setup();
    render(
      <SessionProvider>
        <Probe />
      </SessionProvider>,
    );
    await waitFor(() =>
      expect(screen.getByTestId("manifest")).toHaveTextContent("p1-revealed-epoch-home"),
    );

    await user.click(screen.getByRole("button", { name: "Refresh" }));
    await waitFor(() => expect(fetchProfiles).toHaveBeenCalledTimes(2));
    const preRelockSignal = vi.mocked(fetchProfiles).mock.calls[1]?.[0] as AbortSignal;
    expect(preRelockSignal.aborted).toBe(false);

    await user.click(screen.getByRole("button", { name: "Relock" }));
    await waitFor(() =>
      expect(screen.getByTestId("manifest")).toHaveTextContent("p1-relocked-epoch-home"),
    );
    const relockSignal = vi.mocked(relockMutation).mock.calls[0]?.[0] as AbortSignal;
    expect.soft(preRelockSignal.aborted).toBe(true);
    expect(relockSignal).toBeInstanceOf(AbortSignal);
    expect(relockSignal.aborted).toBe(false);
    expect(vi.mocked(fetchCurrentSession).mock.calls[2]?.[0]).toBe(relockSignal);
    expect(screen.getByTestId("revealed")).toHaveTextContent("false");
    expect(screen.getByTestId("roster")).toHaveTextContent("p1-relocked-epoch");

    await act(async () => {
      preRelockProfileRequest.resolve(preRelockRoster);
      await preRelockProfileRequest.promise;
    });

    expect.soft(screen.getByTestId("revealed")).toHaveTextContent("false");
    expect.soft(screen.getByTestId("manifest")).toHaveTextContent("p1-relocked-epoch-home");
    expect(screen.getByTestId("roster")).toHaveTextContent("p1-relocked-epoch");
    expect(preRelockSignal.aborted).toBe(true);
  });

  it("aborts the Viewer session epoch on unmount and makes zero post-unmount state writes", async () => {
    const pending = deferred<SessionInfo>();
    vi.mocked(fetchCurrentSession).mockReturnValueOnce(pending.promise);
    vi.mocked(fetchManifest).mockResolvedValue(unmountedViewerManifest);
    vi.mocked(fetchProfiles).mockResolvedValue(unmountedViewerRoster);

    const { unmount } = render(
      <SessionProvider>
        <Probe />
      </SessionProvider>,
    );

    await waitFor(() => expect(fetchCurrentSession).toHaveBeenCalled());
    const signal = vi.mocked(fetchCurrentSession).mock.calls[0]?.[0] as AbortSignal;
    expect(signal.aborted).toBe(false);

    unmount();

    expect.soft(signal.aborted).toBe(true);
    postUnmountWrites.armed = true;
    await act(async () => {
      pending.resolve(unmountedViewerSession);
      await pending.promise;
    });
    await act(async () => {});
    postUnmountWrites.armed = false;

    expect(
      postUnmountWrites.values,
      `zero post-unmount writes; leaked values: ${JSON.stringify(postUnmountWrites.values)}`,
    ).toEqual([]);
    expect(signal.aborted).toBe(true);
  });

  it("renews a lapsed session once and retries before asking to enroll", async () => {
    vi.mocked(fetchCurrentSession)
      .mockRejectedValueOnce(new ApiError("session expired", 401))
      .mockResolvedValueOnce(p1Session);
    vi.mocked(renewSession).mockResolvedValueOnce(true);
    render(
      <SessionProvider>
        <Probe />
      </SessionProvider>,
    );

    await waitFor(() => expect(screen.getByTestId("profile")).toHaveTextContent("p1"));
    expect(renewSession).toHaveBeenCalledTimes(1);
    expect(fetchCurrentSession).toHaveBeenCalledTimes(2);
    expect(screen.getByTestId("enroll")).toHaveTextContent("false");
  });

  it("asks to enroll when renewal is refused", async () => {
    vi.mocked(fetchCurrentSession).mockRejectedValueOnce(new ApiError("session expired", 401));
    render(
      <SessionProvider>
        <Probe />
      </SessionProvider>,
    );

    await waitFor(() => expect(screen.getByTestId("enroll")).toHaveTextContent("true"));
    expect(renewSession).toHaveBeenCalledTimes(1);
    expect(fetchCurrentSession).toHaveBeenCalledTimes(1);
  });

  it("renews once when the handshake refuses a lapsed session and retries the handshake", async () => {
    vi.mocked(performHandshake).mockRejectedValueOnce(new ApiError("session expired", 401));
    vi.mocked(renewSession).mockResolvedValueOnce(true);
    vi.mocked(fetchCurrentSession).mockResolvedValueOnce(p1Session);
    render(
      <SessionProvider>
        <Probe />
      </SessionProvider>,
    );

    await waitFor(() => expect(screen.getByTestId("profile")).toHaveTextContent("p1"));
    expect(renewSession).toHaveBeenCalledTimes(1);
    expect(performHandshake).toHaveBeenCalledTimes(2);
    expect(screen.getByTestId("enroll")).toHaveTextContent("false");
  });

  it("asks to enroll when the handshake refuses a lapsed session and renewal is refused", async () => {
    vi.mocked(performHandshake).mockRejectedValueOnce(new ApiError("session expired", 401));
    render(
      <SessionProvider>
        <Probe />
      </SessionProvider>,
    );

    await waitFor(() => expect(screen.getByTestId("enroll")).toHaveTextContent("true"));
    expect(renewSession).toHaveBeenCalledTimes(1);
    expect(performHandshake).toHaveBeenCalledTimes(1);
    expect(fetchCurrentSession).not.toHaveBeenCalled();
  });

  it("asks to enroll when the retried handshake is refused again", async () => {
    vi.mocked(performHandshake)
      .mockRejectedValueOnce(new ApiError("session expired", 401))
      .mockRejectedValueOnce(new ApiError("session expired", 401));
    vi.mocked(renewSession).mockResolvedValueOnce(true);
    render(
      <SessionProvider>
        <Probe />
      </SessionProvider>,
    );

    await waitFor(() => expect(screen.getByTestId("enroll")).toHaveTextContent("true"));
    expect(renewSession).toHaveBeenCalledTimes(1);
    expect(performHandshake).toHaveBeenCalledTimes(2);
    expect(fetchCurrentSession).not.toHaveBeenCalled();
  });

  it("does not renew on a 403", async () => {
    vi.mocked(fetchCurrentSession).mockRejectedValueOnce(new ApiError("forbidden", 403));
    render(
      <SessionProvider>
        <Probe />
      </SessionProvider>,
    );

    await waitFor(() => expect(screen.getByTestId("enroll")).toHaveTextContent("true"));
    expect(renewSession).not.toHaveBeenCalled();
  });

  it.each([401, 403])(
    "clears session, manifest, and profiles when a refresh's session fetch is rejected with %s",
    async (status) => {
      vi.mocked(fetchCurrentSession).mockResolvedValueOnce(p1Session);
      const user = userEvent.setup();
      render(
        <SessionProvider>
          <Probe />
        </SessionProvider>,
      );

      await waitFor(() => expect(screen.getByTestId("profile")).toHaveTextContent("p1"));
      expect(screen.getByTestId("manifest")).toHaveTextContent("p1-revealed-epoch-home");
      expect(screen.getByTestId("profiles")).toHaveTextContent("2");

      vi.mocked(fetchCurrentSession).mockRejectedValueOnce(new ApiError("locked out", status));
      await user.click(screen.getByRole("button", { name: "Refresh" }));

      await waitFor(() => expect(screen.getByTestId("enroll")).toHaveTextContent("true"));
      expect(screen.getByTestId("profile")).toHaveTextContent("none");
      expect(screen.getByTestId("manifest")).toHaveTextContent("none");
      expect(screen.getByTestId("profiles")).toHaveTextContent("0");
      expect(screen.getByTestId("loading")).toHaveTextContent("false");
      expect(screen.getByTestId("error")).toHaveTextContent("none");
    },
  );

  it("clears scoped state and records only the current generic session rejection", async () => {
    vi.mocked(fetchCurrentSession).mockResolvedValueOnce(p1Session);
    const user = userEvent.setup();
    render(
      <SessionProvider>
        <Probe />
      </SessionProvider>,
    );

    await waitFor(() => expect(screen.getByTestId("profile")).toHaveTextContent("p1"));
    vi.mocked(fetchCurrentSession).mockRejectedValueOnce(new Error("current load failed"));
    await user.click(screen.getByRole("button", { name: "Refresh" }));

    await waitFor(() => expect(screen.getByTestId("loading")).toHaveTextContent("false"));
    expect(screen.getByTestId("profile")).toHaveTextContent("none");
    expect(screen.getByTestId("manifest")).toHaveTextContent("none");
    expect(screen.getByTestId("profiles")).toHaveTextContent("0");
    expect(screen.getByTestId("enroll")).toHaveTextContent("false");
    expect(screen.getByTestId("error")).toHaveTextContent("current load failed");
  });

  it.each([
    ["manifest", 401],
    ["profiles", 403],
  ] as const)(
    "fails closed when the refreshed %s request is rejected with %s",
    async (target, status) => {
      vi.mocked(fetchCurrentSession).mockResolvedValue(p1Session);
      const user = userEvent.setup();
      render(
        <SessionProvider>
          <Probe />
        </SessionProvider>,
      );

      await waitFor(() => expect(screen.getByTestId("profile")).toHaveTextContent("p1"));
      if (target === "manifest") {
        vi.mocked(fetchManifest).mockRejectedValueOnce(new ApiError("manifest denied", status));
      } else {
        vi.mocked(fetchProfiles).mockRejectedValueOnce(new ApiError("profiles denied", status));
      }

      await user.click(screen.getByRole("button", { name: "Refresh" }));

      await waitFor(() => expect(screen.getByTestId("enroll")).toHaveTextContent("true"));
      expect(screen.getByTestId("profile")).toHaveTextContent("none");
      expect(screen.getByTestId("manifest")).toHaveTextContent("none");
      expect(screen.getByTestId("profiles")).toHaveTextContent("0");
      expect(screen.getByTestId("loading")).toHaveTextContent("false");
      expect(screen.getByTestId("error")).toHaveTextContent("none");
    },
  );

  it("clears and aborts the prior identity before a rejected Reveal mutation settles", async () => {
    const pending = deferred<{ revealed: boolean }>();
    vi.mocked(fetchCurrentSession).mockResolvedValue(p1Session);
    vi.mocked(revealMutation).mockReturnValueOnce(pending.promise);
    const user = userEvent.setup();
    render(
      <SessionProvider>
        <Probe />
      </SessionProvider>,
    );

    await waitFor(() => expect(screen.getByTestId("profile")).toHaveTextContent("p1"));
    const priorSignal = vi.mocked(fetchCurrentSession).mock.calls[0]?.[0] as AbortSignal;
    await user.click(screen.getByRole("button", { name: "Reveal" }));

    expect(priorSignal.aborted).toBe(true);
    expect(screen.getByTestId("profile")).toHaveTextContent("none");
    expect(screen.getByTestId("manifest")).toHaveTextContent("none");
    expect(screen.getByTestId("profiles")).toHaveTextContent("0");
    const mutationSignal = vi.mocked(revealMutation).mock.calls[0]?.[1] as AbortSignal;
    expect(mutationSignal.aborted).toBe(false);

    pending.reject(new Error("rejected"));
    await pending.promise.catch(() => {});
    await waitFor(() => expect(screen.getByTestId("profile")).toHaveTextContent("p1"));
  });

  it("drops the previous epoch's manifest when a refresh's manifest fetch fails", async () => {
    vi.mocked(fetchCurrentSession).mockResolvedValue(p1Session);
    const user = userEvent.setup();
    render(
      <SessionProvider>
        <Probe />
      </SessionProvider>,
    );

    await waitFor(() =>
      expect(screen.getByTestId("manifest")).toHaveTextContent("p1-revealed-epoch-home"),
    );

    vi.mocked(fetchManifest).mockRejectedValueOnce(new Error("manifest fetch failed"));
    await user.click(screen.getByRole("button", { name: "Refresh" }));

    await waitFor(() => expect(screen.getByTestId("loading")).toHaveTextContent("false"));
    expect(screen.getByTestId("manifest")).toHaveTextContent("none");
  });

  it("drops the previous epoch's profiles when a refresh's profiles fetch fails", async () => {
    vi.mocked(fetchCurrentSession).mockResolvedValue(p1Session);
    const user = userEvent.setup();
    render(
      <SessionProvider>
        <Probe />
      </SessionProvider>,
    );

    await waitFor(() => expect(screen.getByTestId("profiles")).toHaveTextContent("2"));

    vi.mocked(fetchProfiles).mockRejectedValueOnce(new Error("profiles fetch failed"));
    await user.click(screen.getByRole("button", { name: "Refresh" }));

    await waitFor(() => expect(screen.getByTestId("loading")).toHaveTextContent("false"));
    expect(screen.getByTestId("profiles")).toHaveTextContent("0");
  });

  it("waits for a late valid profile roster and preserves it after a partial manifest failure", async () => {
    const lateProfiles = deferred<ProfileSummary[]>();
    vi.mocked(fetchCurrentSession)
      .mockResolvedValueOnce(p1Session)
      .mockResolvedValueOnce(p2Session);
    vi.mocked(fetchManifest)
      .mockReset()
      .mockResolvedValueOnce(p1Manifest)
      .mockRejectedValueOnce(new Error("manifest temporarily unavailable"));
    vi.mocked(fetchProfiles)
      .mockReset()
      .mockResolvedValueOnce(p1Roster)
      .mockReturnValueOnce(lateProfiles.promise);

    const user = userEvent.setup();
    render(
      <SessionProvider>
        <Probe />
      </SessionProvider>,
    );
    await waitFor(() => expect(screen.getByTestId("profile")).toHaveTextContent("p1"));

    await user.click(screen.getByRole("button", { name: "Refresh" }));
    await waitFor(() => expect(fetchProfiles).toHaveBeenCalledTimes(2));
    expect(screen.getByTestId("profile")).toHaveTextContent("none");
    expect(screen.getByTestId("manifest")).toHaveTextContent("none");
    expect(screen.getByTestId("profiles")).toHaveTextContent("0");
    expect(screen.getByTestId("loading")).toHaveTextContent("true");

    await act(async () => {
      lateProfiles.resolve(p2Roster);
      await lateProfiles.promise;
    });

    await waitFor(() => expect(screen.getByTestId("loading")).toHaveTextContent("false"));
    expect(screen.getByTestId("profile")).toHaveTextContent("p2");
    expect(screen.getByTestId("manifest")).toHaveTextContent("none");
    expect(screen.getByTestId("roster")).toHaveTextContent("p2-current-epoch");
  });

  it("waits for a late valid manifest and preserves it after a partial profile failure", async () => {
    const lateManifest = deferred<NavigationManifest>();
    vi.mocked(fetchCurrentSession)
      .mockResolvedValueOnce(p1Session)
      .mockResolvedValueOnce(p2Session);
    vi.mocked(fetchManifest)
      .mockReset()
      .mockResolvedValueOnce(p1Manifest)
      .mockReturnValueOnce(lateManifest.promise);
    vi.mocked(fetchProfiles)
      .mockReset()
      .mockResolvedValueOnce(p1Roster)
      .mockRejectedValueOnce(new Error("profiles temporarily unavailable"));

    const user = userEvent.setup();
    render(
      <SessionProvider>
        <Probe />
      </SessionProvider>,
    );
    await waitFor(() => expect(screen.getByTestId("profile")).toHaveTextContent("p1"));

    await user.click(screen.getByRole("button", { name: "Refresh" }));
    await waitFor(() => expect(fetchManifest).toHaveBeenCalledTimes(2));
    expect(screen.getByTestId("profile")).toHaveTextContent("none");
    expect(screen.getByTestId("manifest")).toHaveTextContent("none");
    expect(screen.getByTestId("profiles")).toHaveTextContent("0");
    expect(screen.getByTestId("loading")).toHaveTextContent("true");

    await act(async () => {
      lateManifest.resolve(p2Manifest);
      await lateManifest.promise;
    });

    await waitFor(() => expect(screen.getByTestId("loading")).toHaveTextContent("false"));
    expect(screen.getByTestId("profile")).toHaveTextContent("p2");
    expect(screen.getByTestId("manifest")).toHaveTextContent("p2-current-epoch-home");
    expect(screen.getByTestId("profiles")).toHaveTextContent("0");
  });

  it("aborts the earlier of two overlapping refreshes and keeps only the later result", async () => {
    const firstRefresh = deferred<SessionInfo>();
    vi.mocked(fetchCurrentSession)
      .mockResolvedValueOnce(p1Session)
      .mockReturnValueOnce(firstRefresh.promise)
      .mockResolvedValueOnce(p2Session);
    vi.mocked(fetchManifest)
      .mockReset()
      .mockResolvedValueOnce(p1Manifest)
      .mockResolvedValueOnce(p2Manifest);
    vi.mocked(fetchProfiles)
      .mockReset()
      .mockResolvedValueOnce(p1Roster)
      .mockResolvedValueOnce(p2Roster);

    const user = userEvent.setup();
    render(
      <SessionProvider>
        <Probe />
      </SessionProvider>,
    );
    await waitFor(() => expect(screen.getByTestId("profile")).toHaveTextContent("p1"));

    await user.click(screen.getByRole("button", { name: "Refresh" }));
    const firstSignal = vi.mocked(fetchCurrentSession).mock.calls[1]?.[0] as AbortSignal;

    await user.click(screen.getByRole("button", { name: "Refresh" }));
    await waitFor(() => expect(screen.getByTestId("profile")).toHaveTextContent("p2"));
    expect(screen.getByTestId("manifest")).toHaveTextContent("p2-current-epoch-home");
    expect(screen.getByTestId("roster")).toHaveTextContent("p2-current-epoch");

    expect(firstSignal.aborted).toBe(true);

    await act(async () => {
      firstRefresh.resolve(p1Session);
      await firstRefresh.promise;
    });
    expect(screen.getByTestId("profile")).toHaveTextContent("p2");
    expect(screen.getByTestId("manifest")).toHaveTextContent("p2-current-epoch-home");
    expect(screen.getByTestId("roster")).toHaveTextContent("p2-current-epoch");
  });

  it("applies only the latest of two overlapping manifest re-reads", async () => {
    vi.mocked(fetchCurrentSession).mockResolvedValue(p1Session);
    render(
      <SessionProvider>
        <Probe />
      </SessionProvider>,
    );
    await waitFor(() =>
      expect(screen.getByTestId("manifest")).toHaveTextContent("p1-revealed-epoch-home"),
    );

    // Delete, then Undo: the first re-read resolves after the second.
    const first = deferred<NavigationManifest>();
    const second = deferred<NavigationManifest>();
    vi.mocked(fetchManifest).mockReturnValueOnce(first.promise).mockReturnValueOnce(second.promise);
    act(() => requestManifestRefresh());
    act(() => requestManifestRefresh());
    await act(async () => {
      second.resolve(manifestFixture("p1-second", p1Session, "2026-06-24T00:06:00Z"));
      await second.promise;
    });
    expect(screen.getByTestId("manifest")).toHaveTextContent("p1-second-home");
    await act(async () => {
      first.resolve(manifestFixture("p1-first", p1Session, "2026-06-24T00:05:00Z"));
      await first.promise;
    });
    expect(screen.getByTestId("manifest")).toHaveTextContent("p1-second-home");
  });

  it("keeps a confirmed entry PIN when an older roster refresh resolves after a failed refresh", async () => {
    vi.mocked(fetchCurrentSession).mockResolvedValue(p1Session);
    const { result } = renderHook(useSession, { wrapper: SessionProvider });
    await waitFor(() => expect(result.current.profiles).toEqual(p1Roster));

    const stale = deferred<ProfileSummary[]>();
    const refreshError = new Error("Roster refresh failed");
    vi.mocked(fetchProfiles).mockReturnValueOnce(stale.promise).mockRejectedValueOnce(refreshError);
    const olderRefresh = result.current.refreshProfiles();
    await act(async () => {
      await expect(result.current.refreshProfiles(p1Session.profileID)).rejects.toThrow(
        refreshError,
      );
    });
    expect(result.current.profiles[0]).toMatchObject({
      id: p1Session.profileID,
      entryPinSet: true,
    });

    await act(async () => {
      stale.resolve(p1Roster);
      await olderRefresh;
    });
    expect(result.current.profiles[0]).toMatchObject({
      id: p1Session.profileID,
      entryPinSet: true,
    });
    expect(result.current.profiles[1]).toEqual(p1Roster[1]);
  });

  it("re-reads the manifest inside the epoch on request without clearing session state", async () => {
    vi.mocked(fetchCurrentSession).mockResolvedValue(p1Session);
    const user = userEvent.setup();
    render(
      <SessionProvider>
        <Probe />
      </SessionProvider>,
    );
    await waitFor(() =>
      expect(screen.getByTestId("manifest")).toHaveTextContent("p1-revealed-epoch-home"),
    );

    const refreshed = deferred<NavigationManifest>();
    vi.mocked(fetchManifest).mockReturnValueOnce(refreshed.promise);
    act(() => requestManifestRefresh());
    // Nothing is cleared while the re-read is in flight.
    expect(screen.getByTestId("loading")).toHaveTextContent("false");
    expect(screen.getByTestId("profile")).toHaveTextContent("p1");
    expect(screen.getByTestId("manifest")).toHaveTextContent("p1-revealed-epoch-home");
    await act(async () => {
      refreshed.resolve(manifestFixture("p1-refreshed", p1Session, "2026-06-24T00:05:00Z"));
      await refreshed.promise;
    });
    expect(screen.getByTestId("manifest")).toHaveTextContent("p1-refreshed-home");
    expect(vi.mocked(fetchCurrentSession)).toHaveBeenCalledTimes(1);

    // A failed re-read keeps the manifest already on screen.
    vi.mocked(fetchManifest).mockRejectedValueOnce(new Error("manifest fetch failed"));
    await act(async () => requestManifestRefresh());
    expect(screen.getByTestId("manifest")).toHaveTextContent("p1-refreshed-home");

    // A re-read that outlives its epoch is aborted and its payload dropped.
    const late = deferred<NavigationManifest>();
    vi.mocked(fetchManifest).mockReturnValueOnce(late.promise).mockResolvedValue(p2Manifest);
    vi.mocked(fetchCurrentSession).mockResolvedValue(p2Session);
    act(() => requestManifestRefresh());
    const lateSignal = vi.mocked(fetchManifest).mock.lastCall?.[0] as AbortSignal;
    await user.click(screen.getByRole("button", { name: "Switch" }));
    await waitFor(() =>
      expect(screen.getByTestId("manifest")).toHaveTextContent("p2-current-epoch-home"),
    );
    expect(lateSignal.aborted).toBe(true);
    await act(async () => {
      late.resolve(manifestFixture("p1-late", p1Session, "2026-06-24T00:06:00Z"));
      await late.promise;
    });
    expect(screen.getByTestId("manifest")).toHaveTextContent("p2-current-epoch-home");
  });

  it("re-reads currentSession in place without clearing state (CORE-2463)", async () => {
    vi.mocked(fetchCurrentSession).mockResolvedValue(p1Session);
    const user = userEvent.setup();
    render(
      <SessionProvider>
        <Probe />
      </SessionProvider>,
    );
    await waitFor(() =>
      expect(screen.getByTestId("manifest")).toHaveTextContent("p1-revealed-epoch-home"),
    );
    expect(vi.mocked(fetchManifest)).toHaveBeenCalledTimes(1);

    const next = deferred<SessionInfo>();
    vi.mocked(fetchCurrentSession).mockReturnValueOnce(next.promise);
    await user.click(screen.getByRole("button", { name: "Refresh session" }));
    // Nothing is cleared while the re-read is in flight.
    expect(screen.getByTestId("loading")).toHaveTextContent("false");
    expect(screen.getByTestId("manifest")).toHaveTextContent("p1-revealed-epoch-home");
    await act(async () => {
      next.resolve({ ...p1Session, revealed: false });
      await next.promise;
    });
    expect(screen.getByTestId("revealed")).toHaveTextContent("false");
    expect(screen.getByTestId("profile")).toHaveTextContent("p1");
    expect(screen.getByTestId("manifest")).toHaveTextContent("p1-revealed-epoch-home");
    expect(vi.mocked(fetchManifest)).toHaveBeenCalledTimes(1);
  });

  it("takes the full identity reload when another tab switched Profiles during a re-read", async () => {
    vi.mocked(fetchCurrentSession).mockResolvedValue(p1Session);
    const user = userEvent.setup();
    render(
      <SessionProvider>
        <Probe />
      </SessionProvider>,
    );
    await waitFor(() =>
      expect(screen.getByTestId("manifest")).toHaveTextContent("p1-revealed-epoch-home"),
    );

    const next = deferred<SessionInfo>();
    vi.mocked(fetchCurrentSession).mockReturnValueOnce(next.promise);
    vi.mocked(fetchCurrentSession).mockResolvedValue(p2Session);
    vi.mocked(fetchManifest).mockResolvedValue(p2Manifest);
    vi.mocked(fetchProfiles).mockResolvedValue(p2Roster);
    await user.click(screen.getByRole("button", { name: "Refresh session" }));
    await act(async () => {
      next.resolve(p2Session);
      await next.promise;
    });
    await waitFor(() =>
      expect(screen.getByTestId("manifest")).toHaveTextContent("p2-current-epoch-home"),
    );
    expect(screen.getByTestId("profile")).toHaveTextContent("p2");
    expect(vi.mocked(fetchManifest)).toHaveBeenCalledTimes(2);
  });
});

describe("SessionProvider names its Profile on requests (CORE-2923)", () => {
  const mismatch = {
    errors: [{ message: "x", extensions: { code: "CONFLICT", reason: "PROFILE_MISMATCH" } }],
  };

  it("names the Profile it read on a switch, and none while the switch is loading", async () => {
    const switched = deferred<SessionInfo>();
    vi.mocked(fetchCurrentSession)
      .mockResolvedValueOnce(p1Session)
      .mockReturnValueOnce(switched.promise);
    const user = userEvent.setup();
    render(
      <SessionProvider>
        <Probe />
      </SessionProvider>,
    );
    await waitFor(() => expect(screen.getByTestId("profile")).toHaveTextContent("p1"));
    expect(getExpectedProfile()).toBe("p1");

    let namedBySwitch: string | undefined = "unset";
    vi.mocked(switchProfileMutation).mockImplementationOnce(async () => {
      namedBySwitch = getExpectedProfile();
      return p2Session;
    });
    await user.click(screen.getByRole("button", { name: "Switch" }));
    // The switch mutation still names the Profile this tab shows, so a stale
    // tab's switch is refused; the reads after it name none.
    expect(namedBySwitch).toBe("p1");
    expect(getExpectedProfile()).toBeUndefined();
    await act(async () => {
      switched.resolve(p2Session);
      await switched.promise;
    });
    await waitFor(() => expect(screen.getByTestId("profile")).toHaveTextContent("p2"));
    expect(getExpectedProfile()).toBe("p2");
  });

  it("reloads to the session's Profile when a request for the shown one is refused", async () => {
    vi.mocked(fetchCurrentSession)
      .mockResolvedValueOnce(p1Session)
      .mockResolvedValueOnce(p2Session);
    render(
      <SessionProvider>
        <Probe />
      </SessionProvider>,
    );
    await waitFor(() => expect(screen.getByTestId("profile")).toHaveTextContent("p1"));
    vi.mocked(fetchManifest).mockResolvedValue(p2Manifest);
    vi.mocked(fetchProfiles).mockResolvedValue(p2Roster);

    // Another tab switched the shared session to p2; this tab's next write is refused.
    act(() => noteProfileRefusal("p1", 409, mismatch));

    await waitFor(() => expect(screen.getByTestId("profile")).toHaveTextContent("p2"));
    expect(screen.getByTestId("manifest")).toHaveTextContent("p2-current-epoch-home");
    expect(fetchCurrentSession).toHaveBeenCalledTimes(2);
    expect(getExpectedProfile()).toBe("p2");

    // A late refusal for p1 arrives after the tab moved on: nothing reloads.
    act(() => noteProfileRefusal("p1", 409, mismatch));
    expect(fetchCurrentSession).toHaveBeenCalledTimes(2);
  });

  it("names the shown Profile on the Reveal mutation, so a stale tab's PIN is refused", async () => {
    vi.mocked(fetchCurrentSession).mockResolvedValue(p1Session);
    let namedByReveal: string | undefined = "unset";
    vi.mocked(revealMutation).mockImplementationOnce(async () => {
      namedByReveal = getExpectedProfile();
      return { revealed: true } as Awaited<ReturnType<typeof revealMutation>>;
    });
    const user = userEvent.setup();
    render(
      <SessionProvider>
        <Probe />
      </SessionProvider>,
    );
    await waitFor(() => expect(getExpectedProfile()).toBe("p1"));
    await user.click(screen.getByRole("button", { name: "Reveal" }));
    await waitFor(() => expect(revealMutation).toHaveBeenCalled());
    expect(namedByReveal).toBe("p1");
  });

  it("ignores refusals that are not a Profile mismatch", async () => {
    vi.mocked(fetchCurrentSession).mockResolvedValueOnce(p1Session);
    render(
      <SessionProvider>
        <Probe />
      </SessionProvider>,
    );
    await waitFor(() => expect(screen.getByTestId("profile")).toHaveTextContent("p1"));
    act(() => {
      noteProfileRefusal("p1", 409, { error: "conflict", code: "CONFLICT" });
      noteProfileRefusal("p1", 400, { reason: "PROFILE_MISMATCH" });
    });
    expect(fetchCurrentSession).toHaveBeenCalledTimes(1);
  });

  it("names no Profile once unmounted", async () => {
    vi.mocked(fetchCurrentSession).mockResolvedValueOnce(p1Session);
    const { unmount } = render(
      <SessionProvider>
        <Probe />
      </SessionProvider>,
    );
    await waitFor(() => expect(getExpectedProfile()).toBe("p1"));
    unmount();
    expect(getExpectedProfile()).toBeUndefined();
  });

  it("ignores stale requestSessionRefresh from a superseded epoch or Profile during switchProfile", async () => {
    const switched = deferred<SessionInfo>();
    vi.mocked(fetchCurrentSession)
      .mockResolvedValueOnce(p1Session)
      .mockResolvedValueOnce(p2Session);
    vi.mocked(switchProfileMutation).mockImplementationOnce(async () => switched.promise);
    const user = userEvent.setup();
    render(
      <SessionProvider>
        <Probe />
      </SessionProvider>,
    );
    await waitFor(() => expect(screen.getByTestId("profile")).toHaveTextContent("p1"));
    const p1Epoch = getIdentityEpochSignal();
    expect(p1Epoch).toBeInstanceOf(AbortSignal);

    let p2Epoch: AbortSignal | undefined;
    // Start switching to p2 -> aborts p1Epoch and begins p2Epoch.
    await user.click(screen.getByRole("button", { name: "Switch" }));
    p2Epoch = getIdentityEpochSignal();
    expect(p1Epoch?.aborted).toBe(true);
    expect(p2Epoch).toBeInstanceOf(AbortSignal);
    expect(p2Epoch?.aborted).toBe(false);

    // A late stream 401 refresh originating from p1 must NOT abort p2's switchProfile.
    act(() => requestSessionRefresh({ epoch: p1Epoch, profileID: "p1" }));
    expect(p2Epoch?.aborted).toBe(false);
    expect(fetchCurrentSession).toHaveBeenCalledTimes(1);

    // Finish switchProfile -> settles cleanly on p2 without interference.
    await act(async () => {
      switched.resolve(p2Session);
      await switched.promise;
    });
    await waitFor(() => expect(screen.getByTestId("profile")).toHaveTextContent("p2"));

    // A late refresh naming old profile p1 while p2 is live is also ignored.
    act(() => requestSessionRefresh({ epoch: p2Epoch, profileID: "p1" }));
    expect(fetchCurrentSession).toHaveBeenCalledTimes(2);
  });
});
