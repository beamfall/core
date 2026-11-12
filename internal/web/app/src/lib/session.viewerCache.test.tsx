import "@testing-library/jest-dom/vitest";
import { act, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { ApiError, resetIdentityEpoch } from "@/lib/api";
import { isBlocked, performHandshake, subscribeCompatibilityBlock } from "@/lib/compat";
import {
  fetchCurrentSession,
  fetchManifest,
  fetchProfiles,
  relock as relockMutation,
  reveal as revealMutation,
  switchProfile as switchProfileMutation,
} from "@/lib/graphql";
import type { NavigationManifest, SessionInfo } from "@/lib/graphql/types";
import { SessionProvider, useSession, useViewerIdentity } from "./session";
import {
  clearViewerCache,
  readViewerCache,
  viewerCacheSize,
  viewerIdentityKey,
  writeViewerCache,
} from "./viewerCache";

// The identity-scoped viewer data cache (viewerCache.ts) must never outlive
// the identity it was filled under: SessionProvider clears it at every epoch
// transition and authorization rejection, and the cache scope it hands out is
// null whenever no identity is bound.

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
  isCompatibilityRefusal: vi.fn(() => false),
  performHandshake: vi.fn(),
  rebindCompatibility: vi.fn(),
  subscribeCompatibilityBlock: vi.fn(),
}));

const revealed: SessionInfo = {
  deviceRole: "viewer",
  profileID: "p1",
  profile: "adult",
  revealed: true,
};
const p2: SessionInfo = {
  deviceRole: "viewer",
  profileID: "p2",
  profile: "managed",
  revealed: false,
};

function manifest(session: SessionInfo): NavigationManifest {
  return {
    profileID: session.profileID,
    revealed: session.revealed,
    generatedAt: "2026-11-10T00:00:00Z",
    navigation: [],
    layouts: [],
    libraries: [],
    theme: { accentColor: "#336699" },
  };
}

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((r) => (resolve = r));
  return { promise, resolve };
}

function never<T>(): Promise<T> {
  return new Promise<T>(() => {});
}

function Probe() {
  const session = useSession();
  const identity = useViewerIdentity();
  return (
    <>
      <button type="button" onClick={() => void session.switchProfile("p2").catch(() => {})}>
        Switch
      </button>
      <button type="button" onClick={() => void session.reveal("1234").catch(() => {})}>
        Reveal
      </button>
      <button type="button" onClick={() => void session.relock().catch(() => {})}>
        Relock
      </button>
      <button type="button" onClick={() => void session.refresh()}>
        Refresh
      </button>
      <span data-testid="identity">{identity ?? "none"}</span>
      <span data-testid="manifest-profile">{session.manifest?.profileID ?? "none"}</span>
    </>
  );
}

function renderProbe() {
  return render(
    <SessionProvider>
      <Probe />
    </SessionProvider>,
  );
}

async function boundTo(session: SessionInfo) {
  const identity = viewerIdentityKey(session) as string;
  await waitFor(() => expect(screen.getByTestId("identity")).toHaveTextContent(identity));
  writeViewerCache(identity, "section:cached", ["item"]);
  return identity;
}

beforeEach(() => {
  clearViewerCache();
  vi.mocked(isBlocked).mockReturnValue(false);
  vi.mocked(performHandshake).mockResolvedValue({
    decision: "compatible",
    messageClass: "compat-compatible",
  });
  vi.mocked(subscribeCompatibilityBlock)
    .mockReset()
    .mockReturnValue(() => {});
  vi.mocked(fetchCurrentSession).mockReset().mockResolvedValue(revealed);
  vi.mocked(fetchManifest).mockReset().mockResolvedValue(manifest(revealed));
  vi.mocked(fetchProfiles).mockReset().mockResolvedValue([]);
  vi.mocked(switchProfileMutation).mockReset();
  vi.mocked(revealMutation).mockReset();
  vi.mocked(relockMutation).mockReset();
});

describe("SessionProvider viewer cache scope", () => {
  it("binds no cache scope until the whole session, manifest and profiles chain settles", async () => {
    const manifestRequest = deferred<NavigationManifest>();
    vi.mocked(fetchManifest).mockReturnValueOnce(manifestRequest.promise);
    renderProbe();

    await waitFor(() => expect(fetchManifest).toHaveBeenCalledTimes(1));
    expect(screen.getByTestId("identity")).toHaveTextContent("none");

    await act(async () => manifestRequest.resolve(manifest(revealed)));
    expect(screen.getByTestId("identity")).toHaveTextContent(viewerIdentityKey(revealed) as string);
    expect(screen.getByTestId("manifest-profile")).toHaveTextContent("p1");
  });

  it.each([
    ["Switch", () => vi.mocked(switchProfileMutation).mockReturnValue(never())],
    ["Reveal", () => vi.mocked(revealMutation).mockReturnValue(never())],
    ["Relock", () => vi.mocked(relockMutation).mockReturnValue(never())],
    ["Refresh", () => vi.mocked(fetchCurrentSession).mockReturnValue(never())],
  ] as const)("clears the cache and unbinds the scope as soon as %s begins", async (name, hold) => {
    const user = userEvent.setup();
    renderProbe();
    const identity = await boundTo(revealed);
    hold();

    await user.click(screen.getByRole("button", { name }));

    expect(viewerCacheSize()).toBe(0);
    expect(readViewerCache(identity, "section:cached")).toBeUndefined();
    expect(screen.getByTestId("identity")).toHaveTextContent("none");
    expect(screen.getByTestId("manifest-profile")).toHaveTextContent("none");
  });

  it("hands the next identity a different, empty scope after a Profile switch", async () => {
    const user = userEvent.setup();
    renderProbe();
    const before = await boundTo(revealed);
    vi.mocked(switchProfileMutation).mockResolvedValue({ profileID: "p2", revealed: false });
    vi.mocked(fetchCurrentSession).mockResolvedValue(p2);
    vi.mocked(fetchManifest).mockResolvedValue(manifest(p2));

    await user.click(screen.getByRole("button", { name: "Switch" }));

    const after = viewerIdentityKey(p2) as string;
    await waitFor(() => expect(screen.getByTestId("identity")).toHaveTextContent(after));
    expect(after).not.toBe(before);
    expect(viewerCacheSize()).toBe(0);
    expect(screen.getByTestId("manifest-profile")).toHaveTextContent("p2");
  });

  it("clears the cache when a Reveal-terminal response resets the identity epoch", async () => {
    renderProbe();
    await boundTo(revealed);
    vi.mocked(fetchCurrentSession).mockReturnValue(never());

    act(() => resetIdentityEpoch());

    expect(viewerCacheSize()).toBe(0);
    expect(screen.getByTestId("identity")).toHaveTextContent("none");
  });

  it("clears the cache when the refreshed manifest is rejected as unauthorized", async () => {
    const user = userEvent.setup();
    renderProbe();
    await boundTo(revealed);
    vi.mocked(fetchManifest).mockRejectedValueOnce(new ApiError("denied", 401));
    const sessionRequest = deferred<SessionInfo>();
    vi.mocked(fetchCurrentSession).mockReturnValueOnce(sessionRequest.promise);

    await user.click(screen.getByRole("button", { name: "Refresh" }));
    // Something written under the departing identity mid-epoch is still dropped.
    writeViewerCache(viewerIdentityKey(revealed), "section:late", ["late"]);
    await act(async () => sessionRequest.resolve(revealed));

    await waitFor(() => expect(viewerCacheSize()).toBe(0));
    expect(screen.getByTestId("identity")).toHaveTextContent("none");
  });

  it("clears the cache on a compatibility block", async () => {
    let onBlock: ((result: { messageClass: string }) => void) | undefined;
    vi.mocked(subscribeCompatibilityBlock).mockImplementation((listener) => {
      onBlock = listener as typeof onBlock;
      return () => {};
    });
    renderProbe();
    await boundTo(revealed);

    act(() => onBlock?.({ messageClass: "compat-client-too-old" }));

    expect(viewerCacheSize()).toBe(0);
    expect(screen.getByTestId("identity")).toHaveTextContent("none");
  });

  it("clears the cache when the session spine unmounts", async () => {
    const view = renderProbe();
    await boundTo(revealed);

    view.unmount();

    expect(viewerCacheSize()).toBe(0);
  });
});
