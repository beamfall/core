import { beforeEach, describe, expect, it, vi } from "vitest";
import { fetchPlaybackInfo } from "./playback";

const postJSON = vi.hoisted(() => vi.fn());
vi.mock("@/lib/api", async (original) => ({
  ...(await original<typeof import("@/lib/api")>()),
  postJSON,
}));
vi.mock("./capabilityProfile", () => ({ probeCapabilityProfile: async () => ({}) }));
const queue = vi.hoisted(() => ({ next: "", title: "" }));
vi.mock("./playQueue", () => ({
  playQueueHandle: () => null,
  nextInPlayQueue: async () => queue.next,
  playQueueTitle: () => queue.title,
}));

describe("fetchPlaybackInfo", () => {
  beforeEach(() => {
    postJSON.mockReset();
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
});
