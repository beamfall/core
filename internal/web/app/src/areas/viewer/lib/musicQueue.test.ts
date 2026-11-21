import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import * as api from "@/lib/api";
import { editMusicQueue, type MusicQueueRow, registerMusicQueuePlayer } from "./musicQueue";

const scope = { profileID: "profile-a", revealed: false, deviceRole: "viewer", profile: "adult" };
const track = (id: string): MusicQueueRow => ({ id, title: id, mediaClass: "audio" });

describe("server Play Queue edits", () => {
  let rows: MusicQueueRow[];
  let reads: number;
  const writes: { mediaID: string; position?: number; expectedProfileID: string }[] = [];
  let afterWrite: (id: string) => void;
  let dispose: (() => void) | undefined;

  beforeEach(() => {
    rows = [];
    reads = 0;
    writes.length = 0;
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

  function player(id: string) {
    const receive = vi.fn<(rows: MusicQueueRow[] | null) => void>();
    const invalidateIdentity = vi.fn();
    dispose = registerMusicQueuePlayer({
      id,
      scope,
      server: { canEdit: () => true, invalidateIdentity, receive },
    });
    return { receive, invalidateIdentity };
  }

  it("moves an existing track immediately after the cursor and then to the tail", async () => {
    rows = ["target", "head", "playing", "after"].map(track);
    const { receive } = player("playing");
    expect(await editMusicQueue(scope, "target", "next")).toBe("confirmed");
    expect(rows.map((row) => row.id)).toEqual(["head", "playing", "target", "after"]);
    expect(await editMusicQueue(scope, "target", "tail")).toBe("confirmed");
    expect(rows.map((row) => row.id)).toEqual(["head", "playing", "after", "target"]);
    expect(reads).toBe(4);
    expect(receive).toHaveBeenLastCalledWith(rows);
    expect(await editMusicQueue(scope, "playing", "tail")).toBe("dropped");
    expect(writes).toHaveLength(2);
  });

  it("can populate an empty queue while idle and put the next track at the head", async () => {
    expect(await editMusicQueue(scope, "first", "tail")).toBe("confirmed");
    expect(await editMusicQueue(scope, "next", "next")).toBe("confirmed");
    expect(rows.map((row) => row.id)).toEqual(["next", "first"]);
    expect(writes).toEqual([
      { mediaID: "first", expectedProfileID: scope.profileID },
      { mediaID: "next", position: 0, expectedProfileID: scope.profileID },
    ]);
    expect(reads).toBe(4);
  });

  it("drops a locked target before writing", async () => {
    rows = [track("playing"), { ...track("target"), locked: true }];
    const { receive } = player("playing");
    expect(await editMusicQueue(scope, "target", "tail")).toBe("dropped");
    expect(writes).toHaveLength(0);
    expect(receive).toHaveBeenCalledWith(rows);
  });

  it("drops an edit if the listing loses the target's eligibility during preflight", async () => {
    const isTargetUnlocked = vi.fn().mockReturnValueOnce(true).mockReturnValue(false);
    expect(await editMusicQueue(scope, "target", "next", isTargetUnlocked)).toBe("dropped");
    expect(reads).toBe(1);
    expect(writes).toHaveLength(0);
  });

  it.each(["next", "tail"] as const)(
    "drops %s when another device removed the cursor",
    async (edit) => {
      rows = [track("other")];
      const { receive } = player("playing");
      expect(await editMusicQueue(scope, "target", edit)).toBe("dropped");
      expect(writes).toHaveLength(0);
      expect(receive).toHaveBeenCalledWith(rows);
    },
  );

  it("confirms a committed placement with a lost response and does not repeat the write", async () => {
    rows = [track("playing"), track("old")];
    const { receive } = player("playing");
    afterWrite = () => {
      throw new Error("response lost");
    };
    expect(await editMusicQueue(scope, "target", "next")).toBe("confirmed");
    expect(writes).toHaveLength(1);
    expect(reads).toBe(2);
    expect(receive).toHaveBeenLastCalledWith([track("playing"), track("target"), track("old")]);
  });

  it("drops a PROFILE_MISMATCH without adopting or retrying the edit", async () => {
    rows = [track("playing")];
    const { receive, invalidateIdentity } = player("playing");
    vi.mocked(api.postJSON).mockResolvedValueOnce({
      errors: [{ message: "foreign profile", extensions: { reason: "PROFILE_MISMATCH" } }],
    });
    expect(await editMusicQueue(scope, "target", "tail")).toBe("dropped");
    expect(api.postJSON).toHaveBeenCalledTimes(1);
    expect(reads).toBe(1);
    expect(receive).toHaveBeenCalledExactlyOnceWith(rows);
    expect(invalidateIdentity).toHaveBeenCalledOnce();
  });
});
