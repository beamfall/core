import "@testing-library/jest-dom/vitest";
import { act, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ApiError } from "@/lib/api";
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
  reveal as revealMutation,
  switchProfile as switchProfileMutation,
} from "@/lib/graphql";
import type { NavigationManifest, ProfileSummary, SessionInfo } from "@/lib/graphql/types";
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
});
