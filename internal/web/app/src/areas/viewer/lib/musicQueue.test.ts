import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import * as api from "@/lib/api";
import { editMusicQueue, type MusicQueueRow, registerMusicQueuePlayer } from "./musicQueue";
import { catalogQueueSource, catalogQueueState } from "./watchQueue";

const scope = { profileID: "profile-a", revealed: false, deviceRole: "viewer", profile: "adult" };
const track = (id: string): MusicQueueRow => ({ id, title: id, mediaClass: "audio" });

describe("server Play Queue edits", () => {
  let rows: MusicQueueRow[];
  let reads: number;
  const writes: { mediaID: string; position?: number; expectedProfileID: string }[] = [];
  let beforeWrite: (id: string) => void;
  let afterWrite: (id: string) => void;
  let dispose: (() => void) | undefined;

  beforeEach(() => {
    rows = [];
    reads = 0;
    writes.length = 0;
    beforeWrite = () => {};
    afterWrite = () => {};
    vi.spyOn(api, "graphql").mockImplementation(async (_, variables) => {
      reads++;
      const offset = Number(variables?.offset);
      return {
        currentSession: scope,
        sourceItems: rows.slice(offset, offset + Number(variables?.limit)),
      };
    });
    vi.spyOn(api, "postJSON").mockImplementation(async (_, request) => {
      const { query, variables } = request as {
        query: string;
        variables: (typeof writes)[number];
      };
      expect(query).toContain(
        "queueAdd(mediaID: $mediaID, position: $position, expectedProfileID: $expectedProfileID)",
      );
      expect(variables.expectedProfileID).toBe(scope.profileID);
      writes.push(variables);
      beforeWrite(variables.mediaID);
      rows = rows.filter((row) => row.id !== variables.mediaID);
      rows.splice(variables.position ?? rows.length, 0, track(variables.mediaID));
      afterWrite(variables.mediaID);
      return { data: { queueAdd: rows } };
    });
  });

  afterEach(() => {
    dispose?.();
    dispose = undefined;
    vi.restoreAllMocks();
  });

  function player(id: string, readCatalog?: () => Promise<string[]>) {
    const receive = vi.fn<(rows: MusicQueueRow[] | null) => void>();
    const cursor = {};
    dispose = registerMusicQueuePlayer({
      id,
      scope,
      cursor,
      readCatalog,
      beginConversion: () => {
        dispose?.();
        dispose = registerMusicQueuePlayer({ id, scope, cursor, receive });
      },
      receive,
    });
    return receive;
  }

  it("moves an existing track immediately after the cursor and then to the tail", async () => {
    rows = ["target", "head", "playing", "after"].map(track);
    const receive = player("playing");
    expect(await editMusicQueue(scope, "target", "next")).toBe("confirmed");
    expect(rows.map((row) => row.id)).toEqual(["head", "playing", "target", "after"]);
    expect(await editMusicQueue(scope, "target", "tail")).toBe("confirmed");
    expect(rows.map((row) => row.id)).toEqual(["head", "playing", "after", "target"]);
    expect(reads).toBe(4);
    expect(receive).toHaveBeenLastCalledWith(rows);
    expect(await editMusicQueue(scope, "playing", "tail")).toBe("dropped");
    expect(writes).toHaveLength(2);
  });

  it("imports a complete album outside the navigation window only once", async () => {
    const album = Array.from({ length: 530 }, (_, index) => track("album-" + (index + 1)));
    const current = album[240].id;
    const state = catalogQueueState("viewer-1", album, current);
    expect(state.queue).toHaveLength(500);
    expect(state.queue?.some((row) => row.id === album[529].id)).toBe(false);
    rows = [track("old-1"), track(current), track(album[3].id), track("old-2")];
    expect(state.queueSource).toEqual(expect.any(String));
    const source = vi.fn(async () => {
      const key = state.queueSource;
      const full = catalogQueueSource(typeof key === "string" ? key : null, undefined);
      if (!full) throw new Error("Missing catalog source");
      return full.map((row) => row.id);
    });
    const receive = player(current, source);

    expect(await editMusicQueue(scope, "new", "next")).toBe("confirmed");
    const kept = [current, "new", "old-1", album[3].id, "old-2"];
    expect(rows.map((row) => row.id)).toEqual([
      ...kept,
      ...album.map((row) => row.id).filter((id) => !kept.includes(id)),
    ]);
    expect(receive).toHaveBeenLastCalledWith(rows);
    expect(await editMusicQueue(scope, album[529].id, "tail")).toBe("confirmed");
    expect(source).toHaveBeenCalledTimes(1);
    expect(reads).toBeGreaterThan(4);
  });

  it("adopts a partial import from a reread and never resumes the writes", async () => {
    rows = [track("old")];
    const source = vi.fn(async () => ["played", "playing", "failed", "unattempted"]);
    const receive = player("playing", source);
    beforeWrite = (id) => {
      if (id === "failed") throw new Error("connection closed");
    };
    expect(await editMusicQueue(scope, "new", "next")).toBe("failed");
    expect(writes.map((write) => write.mediaID)).toEqual(["playing", "played", "failed"]);
    expect(receive).toHaveBeenLastCalledWith([track("playing"), track("old"), track("played")]);
    expect(await editMusicQueue(scope, "later", "tail")).toBe("confirmed");
    expect(source).toHaveBeenCalledTimes(1);
    expect(writes.map((write) => write.mediaID)).toEqual(["playing", "played", "failed", "later"]);
    expect(reads).toBe(4);
  });

  it("confirms a committed placement with a lost response and does not repeat the write", async () => {
    rows = [track("playing"), track("old")];
    const receive = player("playing");
    afterWrite = () => {
      throw new Error("response lost");
    };
    expect(await editMusicQueue(scope, "target", "next")).toBe("confirmed");
    expect(writes).toHaveLength(1);
    expect(reads).toBe(2);
    expect(receive).toHaveBeenLastCalledWith([track("playing"), track("target"), track("old")]);
  });

  it("drops a PROFILE_MISMATCH without adopting or retrying the edit", async () => {
    const receive = player("playing", async () => ["catalog"]);
    vi.mocked(api.postJSON).mockResolvedValueOnce({
      errors: [{ message: "foreign profile", extensions: { reason: "PROFILE_MISMATCH" } }],
    });
    expect(await editMusicQueue(scope, "target", "tail")).toBe("dropped");
    expect(api.postJSON).toHaveBeenCalledTimes(1);
    expect(reads).toBe(1);
    expect(receive).not.toHaveBeenCalled();
  });
});
