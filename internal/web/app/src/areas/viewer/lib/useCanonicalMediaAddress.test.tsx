import { act, render, screen } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router";
import { afterEach, describe, expect, it, vi } from "vitest";
import { ApiError } from "@/lib/api";
import type { GenericMediaInfo } from "@/lib/graphql/types";
import { clearViewerCache, readViewerCache, writeViewerCache } from "@/lib/viewerCache";
import { fetchMedia, mediaDetailCacheKey } from "./mediaDetail";
import { useCanonicalMediaAddress, useMediaAddress } from "./useCanonicalMediaAddress";

const session = vi.hoisted(() => ({ revealed: false }));
vi.mock("@/lib/session", () => ({
  useViewerIdentity: () => "viewer",
  useSession: () => ({
    revealed: session.revealed,
    manifest: {
      libraries: [{ id: "library-music", title: "Music", pluginID: "music", previewClips: false }],
    },
  }),
}));
vi.mock("./mediaDetail", async (importOriginal) => ({
  ...(await importOriginal<typeof import("./mediaDetail")>()),
  fetchMedia: vi.fn(),
}));

const ID = "media-sha256-a371bff46385f7ded";
const media: GenericMediaInfo = {
  id: ID,
  title: "38 Planes",
  mediaClass: "audio",
  libraryID: "library-music",
  credits: [{ entityID: "p", title: "Company", kind: "k", archetype: "person", relation: "r" }],
} as GenericMediaInfo;

function Harness({ route, enabled }: { route: "watch" | "media"; enabled?: boolean }) {
  useCanonicalMediaAddress(route, media, enabled);
  return null;
}

function WatchAddress({ cachedDetail = false }: { cachedDetail?: boolean }) {
  const { id } = useMediaAddress({ cachedDetail });
  return <div>{id === "" ? "unavailable" : id}</div>;
}

describe("legacy watch bookmark resolution", () => {
  const bookmark = "/watch/media-sample1-a371bff46385f7ded";
  const sampledID = "media-sample1-a371bff46385f7ded";

  afterEach(() => {
    clearViewerCache();
    vi.mocked(fetchMedia).mockReset();
    vi.useRealTimers();
  });

  it("uses cached sampled detail until the server confirms a miss", async () => {
    const key = mediaDetailCacheKey(sampledID);
    writeViewerCache("viewer", key, { media: { ...media, id: sampledID }, fields: [] });
    let confirmMiss: (value: null) => void = () => {};
    vi.mocked(fetchMedia).mockReturnValue(
      new Promise((resolve) => {
        confirmMiss = resolve;
      }),
    );
    render(
      <MemoryRouter initialEntries={[bookmark]}>
        <Routes>
          <Route path="/watch/*" element={<WatchAddress cachedDetail />} />
        </Routes>
      </MemoryRouter>,
    );
    expect(screen.getByText(sampledID)).toBeDefined();
    await act(async () => confirmMiss(null));
    expect(screen.getByText(ID)).toBeDefined();
    expect(readViewerCache("viewer", key)).toBeUndefined();
  });

  it.each([null, { ...media, id: sampledID }])(
    "resolves the literal bookmark when the sampled lookup returns %j",
    async (sample) => {
      vi.mocked(fetchMedia).mockResolvedValueOnce(sample);
      render(
        <MemoryRouter initialEntries={[bookmark]}>
          <Routes>
            <Route path="/watch/*" element={<WatchAddress />} />
          </Routes>
        </MemoryRouter>,
      );
      const expectedID = sample ? sampledID : ID;
      expect((await screen.findByText(expectedID)).textContent).toBe(expectedID);
      expect(fetchMedia).toHaveBeenLastCalledWith(sampledID, expect.any(AbortSignal));
    },
  );

  it("keeps the address unresolved after an error and tries the legacy ID after a confirmed miss", async () => {
    vi.useFakeTimers();
    vi.mocked(fetchMedia)
      .mockClear()
      .mockRejectedValueOnce(new TypeError("network unavailable"))
      .mockResolvedValueOnce(null);
    render(
      <MemoryRouter initialEntries={[bookmark]}>
        <Routes>
          <Route path="/watch/*" element={<WatchAddress />} />
        </Routes>
      </MemoryRouter>,
    );

    await act(() => vi.advanceTimersByTimeAsync(500));
    expect(screen.queryByText(sampledID)).toBeNull();
    expect(screen.queryByText(ID)).toBeNull();
    expect(fetchMedia).toHaveBeenCalledTimes(1);

    await act(() => vi.advanceTimersByTimeAsync(1_000));
    expect(screen.getByText(ID).textContent).toBe(ID);
    expect(fetchMedia).toHaveBeenCalledTimes(2);
    expect(fetchMedia).toHaveBeenLastCalledWith(sampledID, expect.any(AbortSignal));
  });

  it.each([401, 403, 410])("shows unavailable without retrying an HTTP %s", async (status) => {
    vi.useFakeTimers();
    vi.mocked(fetchMedia).mockRejectedValue(new ApiError("lookup denied", status));
    render(
      <MemoryRouter initialEntries={[bookmark]}>
        <Routes>
          <Route path="/watch/*" element={<WatchAddress />} />
        </Routes>
      </MemoryRouter>,
    );

    await act(() => vi.advanceTimersByTimeAsync(31_000));
    expect(screen.getByText("unavailable")).toBeDefined();
    expect(fetchMedia).toHaveBeenCalledExactlyOnceWith(sampledID, expect.any(AbortSignal));
  });
});

function arriveAt(path: string, route: "watch" | "media" = "watch", enabled = true) {
  window.history.replaceState({ idx: 3 }, "", path);
  render(
    <MemoryRouter>
      <Harness route={route} enabled={enabled} />
    </MemoryRouter>,
  );
}

describe("useCanonicalMediaAddress (CORE-2329)", () => {
  afterEach(() => {
    session.revealed = false;
    window.history.replaceState(null, "", "/");
  });

  it("replaces a bare-ID or stale address with the readable one in place", () => {
    const length = window.history.length;
    arriveAt(`/watch/${ID}?t=5#x`);
    expect(window.location.pathname).toBe("/watch/music/company-38-planes-a371bff46385f7ded");
    expect(window.location.search).toBe("?t=5");
    expect(window.location.hash).toBe("#x");
    expect(window.history.state).toEqual({ idx: 3 });
    expect(window.history.length).toBe(length);
  });

  it("fixes an old slug on the detail route", () => {
    arriveAt("/media/old/renamed-a371bff46385f7ded", "media");
    expect(window.location.pathname).toBe("/media/music/company-38-planes-a371bff46385f7ded");
  });

  it("keeps the bare ID in a Revealed session", () => {
    session.revealed = true;
    arriveAt("/watch/music/company-38-planes-a371bff46385f7ded");
    expect(window.location.pathname).toBe(`/watch/${ID}`);
  });

  it("leaves another page's address alone (docked player or other item)", () => {
    arriveAt("/browse/library-music");
    expect(window.location.pathname).toBe("/browse/library-music");
    arriveAt("/watch/media-sha256-00000000000000000");
    expect(window.location.pathname).toBe("/watch/media-sha256-00000000000000000");
    arriveAt(`/watch/${ID}`, "watch", false);
    expect(window.location.pathname).toBe(`/watch/${ID}`);
  });
});
