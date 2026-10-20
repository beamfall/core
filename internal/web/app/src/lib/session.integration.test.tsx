import "@testing-library/jest-dom/vitest";
import { cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router";
import { afterEach, describe, expect, it, vi } from "vitest";
import { PromotePage } from "@/areas/enroll/PromotePage";
import { usePlayer } from "@/areas/viewer/player/usePlayer";
import { getIdentityEpochSignal } from "@/lib/api";
import { fetchPlaybackMarkers } from "@/lib/graphql";
import type { NavigationManifest, SessionInfo } from "@/lib/graphql/types";
import { I18nProvider } from "@/lib/i18n";
import { SessionProvider, useSession } from "./session";

const session: SessionInfo = {
  deviceRole: "viewer",
  profileID: "profile-alpha",
  profile: "adult",
  revealed: true,
};

const manifest = {
  profileID: "profile-alpha",
  revealed: true,
  generatedAt: "2026-08-01T00:00:00Z",
  navigation: [],
  layouts: [],
  libraries: [],
  theme: { accentColor: "#336699" },
} satisfies NavigationManifest;

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

function productionResponse(path: string, init?: RequestInit): Promise<Response> {
  if (path === "/api/compat/handshake") {
    return Promise.resolve(
      jsonResponse({ decision: "compatible", messageClass: "compat-compatible" }),
    );
  }
  if (path === "/api/manifest") return Promise.resolve(jsonResponse(manifest));
  if (path === "/api/playback-info") {
    return Promise.resolve(
      jsonResponse({
        decision: "direct-play",
        streams: [{ kind: "direct-play", url: "/api/media/direct/token" }],
        assets: {
          trickplay: "/api/assets/trickplay.json",
          expiresAt: "2999-01-01T00:00:00Z",
        },
      }),
    );
  }
  if (path !== "/api/graphql") throw new Error(`Unexpected production request: ${path}`);

  const query = queryFrom(init);
  if (query.includes("CurrentSession")) {
    return Promise.resolve(jsonResponse({ data: { currentSession: session } }));
  }
  if (query.includes("Profiles")) {
    return Promise.resolve(jsonResponse({ data: { profiles: [] } }));
  }
  if (query.includes("ClientPrefs")) {
    return Promise.resolve(jsonResponse({ data: { clientPrefs: [] } }));
  }
  if (query.includes("PlaybackMarkers")) {
    return Promise.resolve(jsonResponse({ data: { markers: [] } }));
  }
  throw new Error(`Unexpected production GraphQL request: ${query}`);
}

function pendingUntilAbort(signal: AbortSignal | null | undefined): Promise<Response> {
  return new Promise((_resolve, reject) => {
    signal?.addEventListener(
      "abort",
      () => reject(signal.reason ?? new DOMException("Aborted", "AbortError")),
      { once: true },
    );
  });
}

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (error: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, reject, resolve };
}

function GraphQLChildProbe() {
  const current = useSession();
  return (
    <>
      <button
        type="button"
        onClick={() => void fetchPlaybackMarkers("media-alpha").catch(() => {})}
      >
        Load markers
      </button>
      <button type="button" onClick={() => void current.refresh()}>
        Refresh
      </button>
      <output data-testid="profile">{current.session?.profileID ?? "none"}</output>
      <output data-testid="role">{current.session?.deviceRole ?? "none"}</output>
      <output data-testid="revealed">{String(current.revealed)}</output>
      <output data-testid="manifest">{current.manifest?.profileID ?? "none"}</output>
      <output data-testid="profiles">{current.profiles.length}</output>
      <output data-testid="loading">{String(current.loading)}</output>
      <output data-testid="error">{current.error ?? "none"}</output>
    </>
  );
}

const playerMessages = {
  couldNotReachServer: "server unreachable",
  playbackUnavailable: "playback unavailable",
  transcodedPlaybackUnsupported: "transcoded unsupported",
  playbackUnsupportedCodec: "unsupported codec",
  playbackFailed: "playback failed",
  terminalUnavailable: "terminal unavailable",
};

function PlayerHarness() {
  const { videoRef } = usePlayer("media-alpha", playerMessages);
  return <video data-testid="player" ref={videoRef} />;
}

// The real Reveal-terminal path: a mounted player alongside the session probe,
// so a terminal playback response can be observed clearing the SPINE's state,
// not merely the player's own.
function TerminalPlayerProbe() {
  const current = useSession();
  return (
    <>
      <button
        type="button"
        onClick={() => void fetchPlaybackMarkers("media-alpha").catch(() => {})}
      >
        Load markers
      </button>
      <output data-testid="revealed">{String(current.revealed)}</output>
      <output data-testid="manifest">{current.manifest?.profileID ?? "none"}</output>
      <output data-testid="manifest-revealed">{String(current.manifest?.revealed ?? false)}</output>
      {current.session ? <PlayerHarness /> : null}
    </>
  );
}

function PlayerChildProbe() {
  const current = useSession();
  return (
    <>
      <button type="button" onClick={() => void current.refresh()}>
        Refresh
      </button>
      {current.session ? <PlayerHarness /> : null}
    </>
  );
}

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

describe("SessionProvider production identity wiring", () => {
  it("starts a fresh handshake when a pending identity epoch is refreshed or remounted", async () => {
    const handshakes: Array<{
      request: ReturnType<typeof deferred<Response>>;
      signal: AbortSignal | undefined;
    }> = [];
    vi.stubGlobal(
      "fetch",
      vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
        const path = String(input);
        if (path === "/api/compat/handshake") {
          const request = deferred<Response>();
          handshakes.push({ request, signal: init?.signal as AbortSignal | undefined });
          return request.promise;
        }
        return productionResponse(path, init);
      }),
    );

    const user = userEvent.setup();
    const firstMount = render(
      <SessionProvider>
        <GraphQLChildProbe />
      </SessionProvider>,
    );
    await waitFor(() => expect(handshakes).toHaveLength(1));
    handshakes[0]?.signal?.addEventListener(
      "abort",
      () => handshakes[0]?.request.reject(new DOMException("Aborted", "AbortError")),
      { once: true },
    );

    await user.click(screen.getByRole("button", { name: "Refresh" }));
    await waitFor(() => expect(handshakes).toHaveLength(2));
    expect(handshakes[0]?.signal?.aborted).toBe(true);

    handshakes[1]?.request.resolve(
      jsonResponse({ decision: "compatible", messageClass: "compat-compatible" }),
    );
    await waitFor(() => expect(screen.getByTestId("profile")).toHaveTextContent("profile-alpha"));

    await user.click(screen.getByRole("button", { name: "Refresh" }));
    await waitFor(() => expect(handshakes).toHaveLength(3));
    firstMount.unmount();
    expect(handshakes[2]?.signal?.aborted).toBe(true);

    handshakes[2]?.request.resolve(
      jsonResponse({
        decision: "client-too-old",
        messageClass: "compat-client-too-old",
      }),
    );
    await new Promise((resolve) => setTimeout(resolve, 0));
    expect(handshakes).toHaveLength(3);

    render(
      <SessionProvider>
        <GraphQLChildProbe />
      </SessionProvider>,
    );
    await waitFor(() => expect(handshakes).toHaveLength(4));
    handshakes[3]?.request.resolve(
      jsonResponse({ decision: "compatible", messageClass: "compat-compatible" }),
    );
    await waitFor(() => expect(screen.getByTestId("profile")).toHaveTextContent("profile-alpha"));

    await waitFor(() => expect(screen.getByTestId("error")).toHaveTextContent("none"));
    expect(screen.getByTestId("profile")).toHaveTextContent("profile-alpha");
  });

  it("clears and aborts scoped work before a production device-role mutation", async () => {
    const promotion = deferred<Response>();
    let serverSession = session;
    let markerSignal: AbortSignal | undefined;
    let promoteSignal: AbortSignal | undefined;
    let markerAbortedBeforeMutation = false;
    vi.stubGlobal(
      "fetch",
      vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
        const path = String(input);
        if (path === "/api/web/promote") {
          promoteSignal = init?.signal as AbortSignal | undefined;
          markerAbortedBeforeMutation = markerSignal?.aborted === true;
          return promotion.promise;
        }
        if (path === "/api/graphql") {
          const query = queryFrom(init);
          if (query.includes("CurrentSession")) {
            return Promise.resolve(jsonResponse({ data: { currentSession: serverSession } }));
          }
          if (query.includes("Profiles")) {
            return Promise.resolve(
              jsonResponse({
                data: { profiles: [{ id: "profile-alpha", name: "Alpha", kind: "adult" }] },
              }),
            );
          }
          if (query.includes("PlaybackMarkers")) {
            markerSignal = init?.signal as AbortSignal | undefined;
            return pendingUntilAbort(markerSignal);
          }
        }
        return productionResponse(path, init);
      }),
    );

    const user = userEvent.setup();
    render(
      <I18nProvider initialLocale="en">
        <SessionProvider>
          <MemoryRouter>
            <GraphQLChildProbe />
            <PromotePage />
          </MemoryRouter>
        </SessionProvider>
      </I18nProvider>,
    );
    await waitFor(() => expect(screen.getByTestId("profile")).toHaveTextContent("profile-alpha"));
    expect(screen.getByTestId("manifest")).toHaveTextContent("profile-alpha");
    expect(screen.getByTestId("profiles")).toHaveTextContent("1");

    await user.click(screen.getByRole("button", { name: "Load markers" }));
    expect(markerSignal?.aborted).toBe(false);
    await user.type(screen.getByLabelText("Pairing code"), "manager-code");
    await user.click(screen.getByRole("button", { name: "Promote" }));
    await waitFor(() => expect(promoteSignal).toBeInstanceOf(AbortSignal));

    expect(markerAbortedBeforeMutation).toBe(true);
    expect(markerSignal?.aborted).toBe(true);
    expect(promoteSignal).not.toBe(markerSignal);
    expect(getIdentityEpochSignal()).toBeInstanceOf(AbortSignal);
    expect(getIdentityEpochSignal()).not.toBe(promoteSignal);
    expect(getIdentityEpochSignal()?.aborted).toBe(false);
    expect(promoteSignal?.aborted).toBe(false);
    expect(screen.getByTestId("profile")).toHaveTextContent("none");
    expect(screen.getByTestId("role")).toHaveTextContent("none");
    expect(screen.getByTestId("manifest")).toHaveTextContent("none");
    expect(screen.getByTestId("profiles")).toHaveTextContent("0");
    expect(screen.getByTestId("loading")).toHaveTextContent("true");
    expect(screen.getByTestId("error")).toHaveTextContent("none");

    serverSession = { ...session, deviceRole: "manager" };
    promotion.resolve(jsonResponse({}));
    await waitFor(() => expect(screen.getByTestId("role")).toHaveTextContent("manager"));
  });

  it("wires SessionProvider's production identity epoch into a child production GraphQL request", async () => {
    let sessionSignal: AbortSignal | undefined;
    let childSignal: AbortSignal | undefined;
    vi.stubGlobal(
      "fetch",
      vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
        const path = String(input);
        if (path === "/api/graphql" && queryFrom(init).includes("CurrentSession")) {
          sessionSignal ??= init?.signal as AbortSignal;
        }
        if (path === "/api/graphql" && queryFrom(init).includes("PlaybackMarkers")) {
          childSignal = init?.signal as AbortSignal | undefined;
          return pendingUntilAbort(childSignal);
        }
        return productionResponse(path, init);
      }),
    );

    const user = userEvent.setup();
    render(
      <SessionProvider>
        <GraphQLChildProbe />
      </SessionProvider>,
    );
    await waitFor(() => expect(screen.getByTestId("profile")).toHaveTextContent("profile-alpha"));

    await user.click(screen.getByRole("button", { name: "Load markers" }));
    expect(childSignal).toBeInstanceOf(AbortSignal);
    expect(childSignal).not.toBe(sessionSignal);
    expect(sessionSignal?.aborted).toBe(false);
    expect(childSignal?.aborted).toBe(false);

    await user.click(screen.getByRole("button", { name: "Refresh" }));
    await waitFor(() => expect(childSignal?.aborted).toBe(true));
    expect(sessionSignal?.aborted).toBe(true);
  });

  it("refreshes an expired Reveal through production wiring and aborts prior scoped work", async () => {
    let serverSession = session;
    let markerSignal: AbortSignal | undefined;
    vi.stubGlobal(
      "fetch",
      vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
        const path = String(input);
        if (path === "/api/graphql") {
          const query = queryFrom(init);
          if (query.includes("CurrentSession")) {
            return Promise.resolve(jsonResponse({ data: { currentSession: serverSession } }));
          }
          if (query.includes("PlaybackMarkers")) {
            markerSignal = init?.signal as AbortSignal | undefined;
            return pendingUntilAbort(markerSignal);
          }
        }
        if (path === "/api/manifest") {
          return Promise.resolve(jsonResponse({ ...manifest, revealed: serverSession.revealed }));
        }
        return productionResponse(path, init);
      }),
    );

    const user = userEvent.setup();
    render(
      <SessionProvider>
        <GraphQLChildProbe />
      </SessionProvider>,
    );
    await waitFor(() => expect(screen.getByTestId("revealed")).toHaveTextContent("true"));

    await user.click(screen.getByRole("button", { name: "Load markers" }));
    expect(markerSignal).toBeInstanceOf(AbortSignal);
    expect(markerSignal?.aborted).toBe(false);

    serverSession = { ...session, revealed: false };
    await user.click(screen.getByRole("button", { name: "Refresh" }));

    await waitFor(() => expect(markerSignal?.aborted).toBe(true));
    await waitFor(() => expect(screen.getByTestId("revealed")).toHaveTextContent("false"));
    expect(getIdentityEpochSignal()).not.toBe(markerSignal);
    expect(getIdentityEpochSignal()?.aborted).toBe(false);
  });

  // Distinct from the "expired Reveal" case above, which drives the transition
  // by clicking Refresh. Here the REAL Reveal-terminal response drives it: a
  // 403 on playback-info, the exact wire outcome usePlayer used to absorb into
  // player-local terminal state while the revealed session, manifest, and
  // in-flight scoped fetches all stayed live behind the terminal card.
  it("resets the identity epoch when playback returns a Reveal-terminal response", async () => {
    const playback = deferred<Response>();
    let serverSession = session;
    let markerSignal: AbortSignal | undefined;
    let manifestReads = 0;
    let sessionReads = 0;
    vi.spyOn(HTMLMediaElement.prototype, "canPlayType").mockReturnValue("");
    vi.spyOn(HTMLMediaElement.prototype, "load").mockImplementation(() => {});
    vi.stubGlobal(
      "fetch",
      vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
        const path = String(input);
        if (path === "/api/playback-info") return playback.promise;
        if (path === "/api/manifest") {
          manifestReads += 1;
          return Promise.resolve(jsonResponse({ ...manifest, revealed: serverSession.revealed }));
        }
        if (path === "/api/graphql") {
          const query = queryFrom(init);
          if (query.includes("CurrentSession")) {
            sessionReads += 1;
            return Promise.resolve(jsonResponse({ data: { currentSession: serverSession } }));
          }
          if (query.includes("PlaybackMarkers")) {
            markerSignal = init?.signal as AbortSignal | undefined;
            return pendingUntilAbort(markerSignal);
          }
        }
        return productionResponse(path, init);
      }),
    );

    const user = userEvent.setup();
    render(
      <SessionProvider>
        <TerminalPlayerProbe />
      </SessionProvider>,
    );
    await waitFor(() => expect(screen.getByTestId("revealed")).toHaveTextContent("true"));
    expect(screen.getByTestId("manifest")).toHaveTextContent("profile-alpha");
    expect(screen.getByTestId("manifest-revealed")).toHaveTextContent("true");
    const manifestReadsBeforeTerminal = manifestReads;
    const readsBeforeTerminal = sessionReads;

    await user.click(screen.getByRole("button", { name: "Load markers" }));
    expect(markerSignal).toBeInstanceOf(AbortSignal);
    expect(markerSignal?.aborted).toBe(false);

    // The server has relocked: playback answers 403 and the next session read
    // reports the reveal gone.
    serverSession = { ...session, revealed: false };
    playback.resolve({ ok: false, status: 403, json: async () => ({}) } as Response);

    // The revealed epoch's in-flight scoped fetch is aborted, the spine re-reads
    // identity, and the revealed state is gone — none of which the player-local
    // terminal state could have done.
    await waitFor(() => expect(markerSignal?.aborted).toBe(true));
    await waitFor(() => expect(screen.getByTestId("revealed")).toHaveTextContent("false"));
    await waitFor(() => expect(screen.getByTestId("manifest-revealed")).toHaveTextContent("false"));
    expect(manifestReads).toBeGreaterThan(manifestReadsBeforeTerminal);
    expect(sessionReads).toBeGreaterThan(readsBeforeTerminal);
    expect(getIdentityEpochSignal()).not.toBe(markerSignal);
    expect(getIdentityEpochSignal()?.aborted).toBe(false);

    // Self-terminating: with no reveal in force, a remounted player observing
    // the same terminal status does not drive another epoch reload.
    const readsAfterTerminal = sessionReads;
    await new Promise((resolve) => setTimeout(resolve, 20));
    expect(sessionReads).toBe(readsAfterTerminal);
  });

  it("passes SessionProvider's production identity epoch to usePlayer's raw trickplay fetch", async () => {
    let sessionSignal: AbortSignal | undefined;
    let trickplaySignal: AbortSignal | undefined;
    let trickplayObserved = false;
    vi.spyOn(HTMLMediaElement.prototype, "canPlayType").mockReturnValue("");
    vi.spyOn(HTMLMediaElement.prototype, "load").mockImplementation(() => {});
    vi.stubGlobal(
      "fetch",
      vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
        const path = String(input);
        if (path === "/api/graphql" && queryFrom(init).includes("CurrentSession")) {
          sessionSignal ??= init?.signal as AbortSignal;
        }
        if (path === "/api/assets/trickplay.json") {
          const signal = init?.signal as AbortSignal | undefined;
          trickplaySignal ??= signal;
          trickplayObserved = true;
          return pendingUntilAbort(signal);
        }
        return productionResponse(path, init);
      }),
    );

    const user = userEvent.setup();
    render(
      <SessionProvider>
        <PlayerChildProbe />
      </SessionProvider>,
    );

    await waitFor(() => expect(trickplayObserved).toBe(true));
    expect(trickplaySignal).toBeInstanceOf(AbortSignal);
    expect(trickplaySignal).toBe(getIdentityEpochSignal());
    expect(trickplaySignal).not.toBe(sessionSignal);
    expect(sessionSignal?.aborted).toBe(false);
    expect(trickplaySignal?.aborted).toBe(false);

    await user.click(screen.getByRole("button", { name: "Refresh" }));
    await waitFor(() => expect(trickplaySignal?.aborted).toBe(true));
    expect(sessionSignal?.aborted).toBe(true);
  });
});
