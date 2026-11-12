import { beforeEach, describe, expect, it, vi } from "vitest";
import { fetchPlaybackInfo } from "./playback";

const postJSON = vi.hoisted(() =>
  vi.fn(async (_path: string, _body: object): Promise<object> => ({})),
);
vi.mock("@/lib/api", async (original) => ({
  ...(await original<typeof import("@/lib/api")>()),
  postJSON,
}));
vi.mock("./capabilityProfile", () => ({ probeCapabilityProfile: async () => ({}) }));
const queue = vi.hoisted(() => ({ next: "", title: "" }));
const nextInPlayQueue = vi.hoisted(() => vi.fn(async () => queue.next));
const leavePlayQueue = vi.hoisted(() => vi.fn());
vi.mock("./playQueue", () => ({
  playQueueHandle: () => "queue",
  nextInPlayQueue,
  leavePlayQueue,
  playQueueTitle: () => queue.title,
}));

describe("fetchPlaybackInfo", () => {
  beforeEach(() => {
    postJSON.mockReset();
    postJSON.mockResolvedValue({});
    nextInPlayQueue.mockClear();
    leavePlayQueue.mockClear();
    queue.next = "";
    queue.title = "";
  });

  it("names the queue's next item, which the server issues without a title", async () => {
    queue.next = "b";
    queue.title = "Title b";
    postJSON.mockResolvedValue({ next: { mediaID: "b" } });

    const info = await fetchPlaybackInfo("a");

    expect(postJSON).toHaveBeenCalledWith(
      "/api/playback-info",
      expect.objectContaining({ mediaID: "a", nextMediaID: "b" }),
    );
    expect(info.next).toEqual({ mediaID: "b", title: "Title b" });
  });

  it("keeps the server's own title and its own next pick as issued", async () => {
    queue.title = "Title b";
    postJSON.mockResolvedValue({ next: { mediaID: "z", title: "Server title" } });
    expect((await fetchPlaybackInfo("a")).next).toEqual({ mediaID: "z", title: "Server title" });

    // The queue is finished: the server's next pick is not a queue item.
    postJSON.mockResolvedValue({ next: { mediaID: "z" } });
    expect((await fetchPlaybackInfo("a")).next).toEqual({ mediaID: "z" });
  });

  it("declares a card queue's next item and leaves the Play all queue (CORE-1143)", async () => {
    queue.next = "play-all-next";
    queue.title = "Play all title";
    postJSON.mockResolvedValue({ next: { mediaID: "card-next" } });

    const info = await fetchPlaybackInfo("media-a", "card-next");

    expect(leavePlayQueue).toHaveBeenCalledWith("queue");
    expect(nextInPlayQueue).not.toHaveBeenCalled();
    expect(postJSON).toHaveBeenCalledWith(
      "/api/playback-info",
      expect.objectContaining({ mediaID: "media-a", nextMediaID: "card-next" }),
    );
    // The Play all queue's title is not borrowed for the card queue's item.
    expect(info.next).toEqual({ mediaID: "card-next" });
  });

  it("does not fall back to the Play all queue after a card queue's last item (CORE-1143)", async () => {
    queue.next = "play-all-next";

    await fetchPlaybackInfo("media-last", "");

    expect(leavePlayQueue).toHaveBeenCalledWith("queue");
    expect(nextInPlayQueue).not.toHaveBeenCalled();
    expect(postJSON.mock.calls[0][1]).not.toHaveProperty("nextMediaID");
  });

  it("omits the next item when neither queue has one (CORE-1143)", async () => {
    await fetchPlaybackInfo("media-b");
    expect(postJSON.mock.calls[0][1]).not.toHaveProperty("nextMediaID");
  });
});
