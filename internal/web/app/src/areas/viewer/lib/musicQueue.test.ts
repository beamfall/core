// @vitest-environment jsdom
import { act, render, waitFor } from "@testing-library/react";
import { createElement } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import * as api from "@/lib/api";
import { ApiError } from "@/lib/api";
import {
  beginQueueAdvance,
  convertCatalogAndApplyEdit,
  editServerQueue,
  enqueueQueueEdit,
  hasPendingQueueEdits,
  isProfileMismatchError,
  type MusicQueueView,
  publishMusicQueueSnapshot,
  QUEUE_BATCH_SIZE,
  QueueIdentityChanged,
  readMusicQueueSnapshot,
  resetQueueCoordinatorForTest,
  useMusicQueue,
  waitForPendingQueueEdits,
} from "./musicQueue";

const mockSessionState = vi.hoisted(() => ({
  revealed: true,
  session: { profileID: "profile-a", revealed: true },
}));

vi.mock("@/lib/session", () => ({
  useSession: () => mockSessionState,
  useViewerIdentity: () => "viewer-1",
}));

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (reason?: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

class InMemoryServerQueue {
  entries: { id: string; mediaID: string }[] = [];
  currentEntryID: string | null = null;
  profileID = "profile-a";
  private seq = 0;

  rowFor(mediaID: string) {
    return { id: mediaID, title: `Track ${mediaID}`, mediaClass: "audio" };
  }

  snapshot() {
    const seen = new Set<string>();
    const rows: { id: string; title: string; mediaClass: string }[] = [];
    for (const entry of this.entries) {
      if (seen.has(entry.mediaID)) continue;
      seen.add(entry.mediaID);
      rows.push(this.rowFor(entry.mediaID));
    }
    return {
      currentSession: { profileID: this.profileID, revealed: true },
      sourceItems: rows,
      playQueue: {
        currentEntryID: this.currentEntryID,
        entries: [...this.entries],
      },
    };
  }

  seed(ids: readonly string[]) {
    this.entries = ids.map((id) => ({ id: `e-${++this.seq}`, mediaID: id }));
    this.currentEntryID = this.entries[0]?.id ?? null;
  }

  addAt(mediaID: string, position?: number) {
    const duplicateCount = this.entries.filter((e) => e.mediaID === mediaID).length;
    if (duplicateCount === 1 || position !== undefined) {
      this.entries = this.entries.filter((e) => e.mediaID !== mediaID);
    }
    const next = { id: `e-${++this.seq}`, mediaID };
    if (position === undefined || position >= this.entries.length) {
      this.entries.push(next);
    } else {
      this.entries.splice(Math.max(0, position), 0, next);
    }
    if (!this.currentEntryID) this.currentEntryID = this.entries[0]?.id ?? null;
  }

  addMany(mediaIDs: readonly string[]) {
    for (const mediaID of mediaIDs) {
      this.addAt(mediaID);
    }
  }
}

describe("musicQueue CORE-2845 server edits, conversion, and serialization", () => {
  afterEach(() => {
    mockSessionState.revealed = true;
    mockSessionState.session = { profileID: "profile-a", revealed: true };
    resetQueueCoordinatorForTest();
    vi.restoreAllMocks();
  });

  it("handles Play next position calculation, duplicate tail moves, and active track guard", async () => {
    const queue = new InMemoryServerQueue();
    queue.seed(["a", "b", "c", "b"]);
    queue.currentEntryID = queue.entries[2]!.id; // playing "c" (dedup index 2)

    const graphql = vi.spyOn(api, "graphql").mockImplementation(async (query, variables) => {
      const vars = (variables ?? {}) as Record<string, unknown>;
      if (query.startsWith("query MusicQueue")) {
        const offset = typeof vars.offset === "number" ? vars.offset : 0;
        const limit = typeof vars.limit === "number" ? vars.limit : 200;
        const full = queue.snapshot();
        return { ...full, sourceItems: full.sourceItems.slice(offset, offset + limit) };
      }
      if (query.startsWith("mutation QueueAddAt")) {
        queue.addAt(
          String(vars.mediaID),
          typeof vars.position === "number" ? vars.position : undefined,
        );
        return { queueAdd: { id: vars.mediaID } };
      }
      return {};
    });

    await expect(
      editServerQueue({
        mediaID: "c",
        placement: "next",
        expectedProfileID: "profile-a",
        currentPlayingMediaID: "c",
      }),
    ).rejects.toThrow("Cannot requeue");

    // Moving "a" (before "c" at index 2) after "c": position is currentIndex (2).
    const afterNext = await editServerQueue({
      mediaID: "a",
      placement: "next",
      expectedProfileID: "profile-a",
      currentPlayingMediaID: "c",
    });
    expect(graphql).toHaveBeenCalledWith(expect.stringContaining("mutation QueueAddAt"), {
      mediaID: "a",
      position: 2,
      expectedProfileID: "profile-a",
    });
    expect(afterNext.rows.map((r) => r.id)).toEqual(["b", "c", "a"]);

    // Duplicate entry tail move for "b" passes explicit tail position so store removes duplicates.
    const afterTail = await editServerQueue({
      mediaID: "b",
      placement: "end",
      expectedProfileID: "profile-a",
      currentPlayingMediaID: "c",
    });
    expect(graphql).toHaveBeenCalledWith(expect.stringContaining("mutation QueueAddAt"), {
      mediaID: "b",
      position: 3,
      expectedProfileID: "profile-a",
    });
    expect(afterTail.rows.map((r) => r.id)).toEqual(["c", "a", "b"]);
  });

  it("drops edits on PROFILE_MISMATCH without applying mismatched confirmation", async () => {
    const queue = new InMemoryServerQueue();
    queue.seed(["a", "b"]);
    queue.profileID = "profile-b";
    vi.spyOn(api, "graphql").mockImplementation(async () => queue.snapshot());

    const applyConfirmed = vi.fn();
    let caught: unknown;
    try {
      await editServerQueue({
        mediaID: "b",
        placement: "end",
        expectedProfileID: "profile-a",
        currentPlayingMediaID: "a",
        applyConfirmed,
      });
    } catch (err) {
      caught = err;
    }
    expect(isProfileMismatchError(caught)).toBe(true);
    expect(applyConfirmed).not.toHaveBeenCalled();
  });

  it("recovers from a lost mutation response by rereading server state once without write replay", async () => {
    const queue = new InMemoryServerQueue();
    queue.seed(["a", "b"]);
    let mutationCalls = 0;
    let rereadCalls = 0;
    vi.spyOn(api, "graphql").mockImplementation(async (query, variables) => {
      const vars = (variables ?? {}) as Record<string, unknown>;
      if (query.startsWith("query MusicQueue")) {
        rereadCalls++;
        return queue.snapshot();
      }
      if (query.startsWith("mutation QueueAddAt")) {
        mutationCalls++;
        // Server committed the write before the response was lost.
        queue.addAt(String(vars.mediaID), vars.position as number | undefined);
        throw new Error("connection reset");
      }
      return {};
    });

    const applyConfirmed = vi.fn();
    await expect(
      editServerQueue({
        mediaID: "c",
        placement: "end",
        expectedProfileID: "profile-a",
        currentPlayingMediaID: "a",
        applyConfirmed,
      }),
    ).rejects.toThrow("connection reset");

    expect(mutationCalls).toBe(1);
    expect(rereadCalls).toBe(2);
    expect(applyConfirmed).toHaveBeenCalledTimes(1);
    expect(applyConfirmed.mock.calls[0]?.[0]?.rows.map((r: { id: string }) => r.id)).toEqual([
      "a",
      "b",
      "c",
    ]);
  });

  it("chunks >500 items into 500-item batches, keeps non-empty existing server rows, and scales to 8,000 tracks", async () => {
    const queue = new InMemoryServerQueue();
    const graphql = vi.spyOn(api, "graphql").mockImplementation(async (query, variables) => {
      const vars = (variables ?? {}) as Record<string, unknown>;
      if (query.startsWith("query MusicQueue")) {
        const offset = typeof vars.offset === "number" ? vars.offset : 0;
        const limit = typeof vars.limit === "number" ? vars.limit : 200;
        const full = queue.snapshot();
        return { ...full, sourceItems: full.sourceItems.slice(offset, offset + limit) };
      }
      if (query.startsWith("mutation QueueAddManyEnd")) {
        queue.addMany(vars.mediaIDs as string[]);
        return {
          queueAddMany: {
            currentEntryID: queue.currentEntryID,
            entries: queue.entries,
          },
        };
      }
      if (query.startsWith("mutation QueueAddAt")) {
        queue.addAt(vars.mediaID as string, vars.position as number | undefined);
        return { queueAdd: { id: vars.mediaID } };
      }
      return {};
    });

    // 1. Pre-existing server queue preserves existing server rows in order + missing catalog tracks.
    queue.seed(["s1", "s2"]);
    const onConverted = vi.fn();
    await convertCatalogAndApplyEdit({
      resolveCatalogIDs: async () => ["c0", "c1", "s2", "c2", "c3"],
      currentMediaID: "c1",
      editMediaID: "c2",
      placement: "next",
      expectedProfileID: "profile-a",
      onConverted,
    });
    expect(onConverted).toHaveBeenCalledTimes(1);
    expect(onConverted.mock.calls[0]?.[0]?.rows.map((r: { id: string }) => r.id)).toEqual([
      "c1",
      "c2",
      "s1",
      "s2",
      "c3",
      "c0",
    ]);

    // 2. Empty queue with 8,000 tracks batches in QUEUE_BATCH_SIZE (500) chunks efficiently.
    queue.entries = [];
    queue.currentEntryID = null;
    graphql.mockClear();
    const ids = Array.from({ length: 8000 }, (_, i) => `t-${i}`);
    const started = performance.now();
    const res = await convertCatalogAndApplyEdit({
      resolveCatalogIDs: async () => ids,
      currentMediaID: "t-0",
      editMediaID: "t-extra",
      placement: "end",
      expectedProfileID: "profile-a",
      onConverted: vi.fn(),
    });
    const elapsedMs = performance.now() - started;
    expect(elapsedMs).toBeLessThan(1000);

    const addManyCalls = graphql.mock.calls.filter(([q]) =>
      q.startsWith("mutation QueueAddManyEnd"),
    );
    expect(addManyCalls).toHaveLength(Math.ceil(8001 / QUEUE_BATCH_SIZE));
    expect((addManyCalls[0]?.[1] as { mediaIDs: string[] }).mediaIDs).toHaveLength(
      QUEUE_BATCH_SIZE,
    );
    expect(res.rows).toHaveLength(8001);
    expect(res.rows.at(-1)?.id).toBe("t-extra");
  });

  it("handles partial chunked import failure by rereading committed server rows once without write replay", async () => {
    const queue = new InMemoryServerQueue();
    let batchCount = 0;
    const graphql = vi.spyOn(api, "graphql").mockImplementation(async (query, variables) => {
      const vars = (variables ?? {}) as Record<string, unknown>;
      if (query.startsWith("query MusicQueue")) {
        const offset = typeof vars.offset === "number" ? vars.offset : 0;
        const limit = typeof vars.limit === "number" ? vars.limit : 200;
        const full = queue.snapshot();
        return { ...full, sourceItems: full.sourceItems.slice(offset, offset + limit) };
      }
      if (query.startsWith("mutation QueueAddManyEnd")) {
        batchCount++;
        if (batchCount === 2) throw new Error("chunk 2 failed");
        queue.addMany(vars.mediaIDs as string[]);
        return { queueAddMany: { currentEntryID: queue.currentEntryID, entries: queue.entries } };
      }
      return {};
    });

    const ids = Array.from({ length: 700 }, (_, i) => `k-${i}`);
    const applyConfirmed = vi.fn();
    const onConverted = vi.fn();
    await expect(
      convertCatalogAndApplyEdit({
        resolveCatalogIDs: async () => ids,
        currentMediaID: "k-0",
        editMediaID: "k-new",
        placement: "end",
        expectedProfileID: "profile-a",
        applyConfirmed,
        onConverted,
      }),
    ).rejects.toThrow("chunk 2 failed");

    // Batch 1 (500 rows) committed and was confirmed by reread without replaying batch 2.
    expect(batchCount).toBe(2);
    expect(
      graphql.mock.calls.filter(([q]) => q.startsWith("query MusicQueue")).length,
    ).toBeGreaterThanOrEqual(2);
    expect(applyConfirmed).toHaveBeenCalledTimes(1);
    expect(applyConfirmed.mock.calls[0]?.[0]?.rows).toHaveLength(500);
    expect(onConverted).toHaveBeenCalledTimes(1);
  });

  it("serializes edits and advances in strict order and marks ApiError PROFILE_MISMATCH", async () => {
    expect(
      isProfileMismatchError(
        new ApiError("Profile changed", 409, {
          errors: [{ message: "mismatch", extensions: { reason: "PROFILE_MISMATCH" } }],
        }),
      ),
    ).toBe(true);
    expect(
      isProfileMismatchError(new ApiError("Profile changed", 409, { reason: "PROFILE_MISMATCH" })),
    ).toBe(true);

    const log: string[] = [];
    const holdEdit = deferred<void>();
    const editPromise = enqueueQueueEdit(async () => {
      log.push("edit:start");
      await holdEdit.promise;
      log.push("edit:end");
    });

    expect(hasPendingQueueEdits()).toBe(true);
    const waiter = waitForPendingQueueEdits().then(() => log.push("advance:resumed"));
    holdEdit.resolve();
    await Promise.all([editPromise, waiter]);
    expect(log).toEqual(["edit:start", "edit:end", "advance:resumed"]);

    // Edits also wait behind an active advance.
    const endAdvance = beginQueueAdvance();
    const holdAfterAdvance: string[] = [];
    const queuedAfterAdvance = enqueueQueueEdit(async () => {
      holdAfterAdvance.push("edit:ran");
    });
    await Promise.resolve();
    expect(holdAfterAdvance).toEqual([]);
    endAdvance();
    await queuedAfterAdvance;
    expect(holdAfterAdvance).toEqual(["edit:ran"]);
  });

  it("readMusicQueueSnapshot throws QueueProfileMismatchError when session switches", async () => {
    const queue = new InMemoryServerQueue();
    queue.seed(["a"]);
    queue.profileID = "profile-other";
    vi.spyOn(api, "graphql").mockImplementation(async () => queue.snapshot());

    await expect(readMusicQueueSnapshot({ expectedProfileID: "profile-a" })).rejects.toSatisfy(
      isProfileMismatchError,
    );
  });

  it("does not duplicate an imported catalog track when Add to queue runs against a non-empty queue", async () => {
    const queue = new InMemoryServerQueue();
    queue.seed(["x"]);
    vi.spyOn(api, "graphql").mockImplementation(async (query, variables) => {
      const vars = (variables ?? {}) as Record<string, unknown>;
      if (query.startsWith("query MusicQueue")) {
        return queue.snapshot();
      }
      if (query.startsWith("mutation QueueAddAt")) {
        queue.addAt(
          String(vars.mediaID),
          typeof vars.position === "number" ? vars.position : undefined,
        );
        return { queueAdd: { id: vars.mediaID } };
      }
      if (query.startsWith("mutation QueueAddManyEnd")) {
        queue.addMany(vars.mediaIDs as string[]);
        return { queueAddMany: { currentEntryID: queue.currentEntryID, entries: queue.entries } };
      }
      return {};
    });

    const onConverted = vi.fn();
    const res = await convertCatalogAndApplyEdit({
      resolveCatalogIDs: async () => ["a", "b", "c"],
      currentMediaID: "a",
      editMediaID: "b",
      placement: "end",
      expectedProfileID: "profile-a",
      onConverted,
    });

    expect(res.rows.map((r) => r.id)).toEqual(["a", "x", "c", "b"]);
    expect(res.entries.map((e) => e.mediaID)).toEqual(["a", "x", "c", "b"]);
    expect(onConverted).toHaveBeenCalledTimes(1);
  });

  it("prevents an older in-flight refresh from overwriting confirmed snapshots", async () => {
    const staleRefresh = deferred<{
      currentSession: { profileID: string; revealed: boolean };
      sourceItems: { id: string; title: string; mediaClass: string }[];
      playQueue: { currentEntryID: string; entries: { id: string; mediaID: string }[] };
    }>();
    vi.spyOn(api, "graphql").mockImplementation(async (query) => {
      if (query.startsWith("query MusicQueue")) {
        return staleRefresh.promise;
      }
      return {};
    });

    let latestView: MusicQueueView | undefined;
    let applySnapshot!: ReturnType<typeof useMusicQueue>["applyConfirmedSnapshot"];
    let capturedRefresh!: ReturnType<typeof useMusicQueue>["refresh"];
    function ProbeWithRefresh() {
      const q = useMusicQueue();
      latestView = q.view;
      applySnapshot = q.applyConfirmedSnapshot;
      capturedRefresh = q.refresh;
      return null;
    }
    const { unmount } = render(createElement(ProbeWithRefresh));

    let refreshPromise!: Promise<unknown>;
    act(() => {
      refreshPromise = capturedRefresh(true);
    });

    const confirmed = {
      rows: [
        { id: "a", title: "Track a", mediaClass: "audio" },
        { id: "confirmed-new", title: "Confirmed New", mediaClass: "audio" },
      ],
      currentEntryID: "e-1",
      entries: [
        { id: "e-1", mediaID: "a" },
        { id: "e-2", mediaID: "confirmed-new" },
      ],
      entryMediaIDs: ["a", "confirmed-new"],
    };

    // 1. Direct applyConfirmedSnapshot retires the older in-flight refresh.
    act(() => {
      applySnapshot(confirmed);
    });
    expect(latestView?.rows?.map((r) => r.id)).toEqual(["a", "confirmed-new"]);

    await act(async () => {
      staleRefresh.resolve({
        currentSession: { profileID: "profile-a", revealed: true },
        sourceItems: [{ id: "stale-only", title: "Stale", mediaClass: "audio" }],
        playQueue: {
          currentEntryID: "e-stale",
          entries: [{ id: "e-stale", mediaID: "stale-only" }],
        },
      });
      await refreshPromise;
    });
    expect(latestView?.rows?.map((r) => r.id)).toEqual(["a", "confirmed-new"]);

    // 2. publishMusicQueueSnapshot via external listener also retires a newer in-flight refresh.
    const secondStale = deferred<{
      currentSession: { profileID: string; revealed: boolean };
      sourceItems: { id: string; title: string; mediaClass: string }[];
      playQueue: { currentEntryID: string; entries: { id: string; mediaID: string }[] };
    }>();
    vi.spyOn(api, "graphql").mockImplementationOnce(async () => secondStale.promise);

    let refresh2!: Promise<unknown>;
    act(() => {
      refresh2 = capturedRefresh(true);
    });
    const pushed = {
      ...confirmed,
      rows: [...confirmed.rows, { id: "pushed-3", title: "Pushed", mediaClass: "audio" }],
    };
    act(() => {
      publishMusicQueueSnapshot("profile-a", pushed);
    });
    await act(async () => {
      secondStale.resolve({
        currentSession: { profileID: "profile-a", revealed: true },
        sourceItems: [{ id: "stale-2", title: "Stale 2", mediaClass: "audio" }],
        playQueue: { currentEntryID: "e-stale", entries: [{ id: "e-stale", mediaID: "stale-2" }] },
      });
      await refresh2;
    });
    await waitFor(() =>
      expect(latestView?.rows?.map((r) => r.id)).toEqual(["a", "confirmed-new", "pushed-3"]),
    );
    unmount();
  });

  it("refuses and invalidates when Reveal changes on the same Profile during pagination, edits, or published snapshots", async () => {
    const queue = new InMemoryServerQueue();
    queue.seed(Array.from({ length: 205 }, (_, i) => `t-${i}`));

    // 1. Paginated read fails cleanly when Reveal flips mid-pagination (same profileID).
    vi.spyOn(api, "graphql").mockImplementation(async (_query, variables) => {
      const vars = (variables ?? {}) as Record<string, unknown>;
      const offset = typeof vars.offset === "number" ? vars.offset : 0;
      const snap = queue.snapshot();
      return {
        ...snap,
        currentSession: { profileID: "profile-a", revealed: offset === 0 },
        sourceItems: snap.sourceItems.slice(offset, offset + 200),
      };
    });
    await expect(
      readMusicQueueSnapshot({
        expectedIdentity: { profileID: "profile-a", revealed: true },
      }),
    ).rejects.toBeInstanceOf(QueueIdentityChanged);

    // 2. editServerQueue rejects QueueIdentityChanged, notifies onIdentityChanged, and does not apply stale snapshot.
    const small = new InMemoryServerQueue();
    small.seed(["a", "b"]);
    let postMutation = false;
    vi.spyOn(api, "graphql").mockImplementation(async (query, variables) => {
      const vars = (variables ?? {}) as Record<string, unknown>;
      if (query.startsWith("query MusicQueue")) {
        return {
          ...small.snapshot(),
          currentSession: { profileID: "profile-a", revealed: !postMutation },
        };
      }
      if (query.startsWith("mutation QueueAddAt")) {
        postMutation = true;
        small.addAt(
          String(vars.mediaID),
          typeof vars.position === "number" ? vars.position : undefined,
        );
        return { queueAdd: { id: vars.mediaID } };
      }
      return {};
    });

    const onIdentityChanged = vi.fn();
    const applyConfirmed = vi.fn();
    await expect(
      editServerQueue({
        mediaID: "c",
        placement: "end",
        expectedProfileID: "profile-a",
        expectedIdentity: { profileID: "profile-a", revealed: true },
        applyConfirmed,
        onIdentityChanged,
      }),
    ).rejects.toBeInstanceOf(QueueIdentityChanged);
    expect(onIdentityChanged).toHaveBeenCalledOnce();
    expect(applyConfirmed).not.toHaveBeenCalled();

    // 3. useMusicQueue invalidates on applyConfirmedSnapshot or publishMusicQueueSnapshot with mismatched Reveal.
    let latestView: MusicQueueView | undefined;
    let applySnapshot!: ReturnType<typeof useMusicQueue>["applyConfirmedSnapshot"];
    function Probe() {
      const q = useMusicQueue();
      latestView = q.view;
      applySnapshot = q.applyConfirmedSnapshot;
      return null;
    }
    const view = render(createElement(Probe));

    const validSnap = {
      rows: [{ id: "a", title: "Revealed track", mediaClass: "audio" }],
      currentEntryID: "e-1",
      entries: [{ id: "e-1", mediaID: "a" }],
      entryMediaIDs: ["a"],
      session: {
        profileID: "profile-a",
        revealed: true,
        deviceRole: "viewer",
        profile: "adult",
      },
    };
    const scopedValidSnap = {
      rows: [{ id: "a", title: "Revealed track", mediaClass: "audio" }],
      currentEntryID: "e-1",
      entries: [{ id: "e-1", mediaID: "a" }],
      entryMediaIDs: ["a"],
      session: { profileID: "profile-a", revealed: true } as unknown as typeof validSnap.session,
    };
    act(() => applySnapshot(scopedValidSnap));
    expect(latestView?.rows?.map((r) => r.id)).toEqual(["a"]);

    act(() =>
      publishMusicQueueSnapshot(
        { profileID: "profile-a", revealed: false },
        {
          ...validSnap,
          rows: [{ id: "secret", title: "Should Stay Hidden", mediaClass: "audio" }],
          session: {
            profileID: "profile-a",
            revealed: false,
            deviceRole: "viewer",
            profile: "adult",
          },
        },
      ),
    );
    expect(latestView?.invalid).toBe(true);
    expect(latestView?.rows).toBeUndefined();
    view.unmount();

    // Also verify applyConfirmedSnapshot directly invalidates mismatched session.
    const view2 = render(createElement(Probe));
    act(() => applySnapshot(scopedValidSnap));
    expect(latestView?.rows?.map((r) => r.id)).toEqual(["a"]);
    act(() =>
      applySnapshot({
        ...validSnap,
        rows: [{ id: "secret-2", title: "Relocked Secret", mediaClass: "audio" }],
        session: {
          profileID: "profile-a",
          revealed: false,
          deviceRole: "viewer",
          profile: "adult",
        },
      }),
    );
    expect(latestView?.invalid).toBe(true);
    expect(latestView?.rows).toBeUndefined();
    view2.unmount();
  });

  it("aborts and cancels multi-batch catalog conversion before later QueueAddManyEnd batches execute", async () => {
    const queue = new InMemoryServerQueue();
    const total = QUEUE_BATCH_SIZE + 50;
    const catalog = Array.from({ length: total }, (_, i) => `track-${i}`);

    // 1. AbortController aborting during batch 1 forwards signal and stops before batch 2.
    const abortController = new AbortController();
    let batchesSent = 0;
    const graphql = vi.spyOn(api, "graphql").mockImplementation(async (query, variables) => {
      const vars = (variables ?? {}) as Record<string, unknown>;
      if (query.startsWith("query MusicQueue")) {
        return queue.snapshot();
      }
      if (query.startsWith("mutation QueueAddManyEnd")) {
        batchesSent++;
        if (batchesSent === 1) abortController.abort();
        return {
          queueAddMany: {
            currentEntryID: null,
            entries: (vars.mediaIDs as string[]).map((id, i) => ({ id: `b-${i}`, mediaID: id })),
          },
        };
      }
      return {};
    });

    const onConvertedAborted = vi.fn();
    await expect(
      convertCatalogAndApplyEdit({
        resolveCatalogIDs: async () => catalog,
        currentMediaID: "track-0",
        editMediaID: "track-new",
        placement: "end",
        expectedProfileID: "profile-a",
        onConverted: onConvertedAborted,
        signal: abortController.signal,
      }),
    ).rejects.toThrow();
    expect(batchesSent).toBe(1);
    expect(onConvertedAborted).not.toHaveBeenCalled();
    expect(graphql).toHaveBeenCalledWith(
      expect.stringContaining("mutation QueueAddManyEnd"),
      expect.anything(),
      abortController.signal,
    );

    // 2. isStillCurrent flipping false during batch 1 also prevents batch 2 and onConverted.
    let stillCurrent = true;
    let supersededBatches = 0;
    vi.spyOn(api, "graphql").mockImplementation(async (query, variables) => {
      const vars = (variables ?? {}) as Record<string, unknown>;
      if (query.startsWith("query MusicQueue")) {
        return queue.snapshot();
      }
      if (query.startsWith("mutation QueueAddManyEnd")) {
        supersededBatches++;
        stillCurrent = false;
        return {
          queueAddMany: {
            currentEntryID: null,
            entries: (vars.mediaIDs as string[]).map((id, i) => ({ id: `s-${i}`, mediaID: id })),
          },
        };
      }
      return {};
    });

    const onConvertedSuperseded = vi.fn();
    await expect(
      convertCatalogAndApplyEdit({
        resolveCatalogIDs: async () => catalog,
        currentMediaID: "track-0",
        editMediaID: "track-new",
        placement: "end",
        expectedProfileID: "profile-a",
        isStillCurrent: () => stillCurrent,
        onConverted: onConvertedSuperseded,
      }),
    ).rejects.toThrow("Queue conversion superseded");
    expect(supersededBatches).toBe(1);
    expect(onConvertedSuperseded).not.toHaveBeenCalled();

    // 3. Non-empty queue conversion cancels before later QueueAddAt / QueueAddManyEnd writes, forwards signal,
    //    and role/profile/error-reread identity drift refuses safely.
    const nonEmptyQueue = new InMemoryServerQueue();
    nonEmptyQueue.seed(["other"]);
    const nonEmptyAbort = new AbortController();
    const writes: string[] = [];
    vi.spyOn(api, "graphql").mockImplementation(async (query, variables, signal) => {
      if (query.startsWith("query MusicQueue")) {
        return nonEmptyQueue.snapshot();
      }
      if (query.startsWith("mutation QueueAddAt")) {
        writes.push(`QueueAddAt:${(variables as Record<string, unknown>).mediaID}`);
        expect(signal).toBe(nonEmptyAbort.signal);
        nonEmptyAbort.abort();
        return { queueAdd: { id: "track-0" } };
      }
      if (query.startsWith("mutation QueueAddManyEnd")) {
        writes.push("QueueAddManyEnd");
        return {};
      }
      return {};
    });

    await expect(
      convertCatalogAndApplyEdit({
        resolveCatalogIDs: async () => ["track-0", "track-1"],
        currentMediaID: "track-0",
        editMediaID: "track-1",
        placement: "next",
        expectedProfileID: "profile-a",
        onConverted: vi.fn(),
        signal: nonEmptyAbort.signal,
      }),
    ).rejects.toThrow();
    expect(writes).toEqual(["QueueAddAt:track-0"]);

    // 4. Role/profile mismatch (same profileID & revealed, different deviceRole/profile) + error-reread drift.
    await expect(
      (async () => {
        vi.spyOn(api, "graphql").mockResolvedValueOnce({
          ...nonEmptyQueue.snapshot(),
          currentSession: {
            profileID: "profile-a",
            revealed: true,
            deviceRole: "managed",
            profile: "managed",
          },
        });
        return readMusicQueueSnapshot({
          expectedIdentity: {
            profileID: "profile-a",
            revealed: true,
            deviceRole: "viewer",
            profile: "adult",
          },
        });
      })(),
    ).rejects.toBeInstanceOf(QueueIdentityChanged);

    let readIndex = 0;
    const onErrorRereadIdentityChanged = vi.fn();
    const applyAfterFailedWrite = vi.fn();
    vi.spyOn(api, "graphql").mockImplementation(async (query) => {
      if (query.startsWith("query MusicQueue")) {
        readIndex++;
        return {
          ...nonEmptyQueue.snapshot(),
          currentSession: {
            profileID: "profile-a",
            revealed: readIndex === 1,
          },
        };
      }
      throw new Error("write timeout");
    });
    await expect(
      editServerQueue({
        mediaID: "track-x",
        placement: "end",
        expectedProfileID: "profile-a",
        expectedIdentity: { profileID: "profile-a", revealed: true },
        applyConfirmed: applyAfterFailedWrite,
        onIdentityChanged: onErrorRereadIdentityChanged,
      }),
    ).rejects.toThrow("write timeout");
    expect(onErrorRereadIdentityChanged).toHaveBeenCalledOnce();
    expect(applyAfterFailedWrite).not.toHaveBeenCalled();
  });
});
