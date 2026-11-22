import "@testing-library/jest-dom/vitest";
import { act, cleanup, render, screen, waitFor } from "@testing-library/react";
import { useState } from "react";
import { MemoryRouter } from "react-router";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { fetchEntity, fetchMediaForEntity } from "@/lib/graphql";
import { fetchMediaRows } from "@/lib/graphql/browse";
import type { GenericMediaInfo, SourceItem } from "@/lib/graphql/types";
import { I18nProvider } from "@/lib/i18n";
import { expectNoAxeViolations } from "@/test/axe";
import { NextInSequenceCard } from "./NextInSequenceCard";
import { useSequenceFinish } from "./useSequenceFinish";

vi.mock("@/lib/graphql", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/lib/graphql")>()),
  fetchEntity: vi.fn(),
  fetchMediaForEntity: vi.fn(),
}));
vi.mock("@/lib/graphql/browse", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/lib/graphql/browse")>()),
  fetchMediaRows: vi.fn(),
}));

const credit = (sequence: string) => ({
  entityID: "s1",
  title: "The Long Road",
  kind: "series",
  archetype: "container",
  relation: "contains",
  attributes: { sequence },
});
const book = (id: string, sequence: string, over: Partial<GenericMediaInfo> = {}) =>
  ({
    id,
    title: id,
    mediaClass: "audio",
    credits: [credit(sequence)],
    ...over,
  }) as GenericMediaInfo;
const item = (id: string): SourceItem => ({
  id,
  kind: "media",
  title: `Title ${id}`,
  artworkSlots: { poster: `/art/${id}.jpg` },
});

function Harness({
  media,
  video,
  enabled = true,
}: {
  media: GenericMediaInfo | null;
  video: HTMLVideoElement;
  enabled?: boolean;
}) {
  const next = useSequenceFinish({ video, media, mediaID: media?.id ?? "", enabled });
  const [dismissed, setDismissed] = useState(false);
  if (!next || dismissed) return <p>ended plainly</p>;
  return <NextInSequenceCard next={next} identity={null} onDismiss={() => setDismissed(true)} />;
}

function renderHarness(media: GenericMediaInfo | null, enabled = true) {
  const video = document.createElement("video");
  render(
    <I18nProvider>
      <MemoryRouter>
        <Harness media={media} video={video} enabled={enabled} />
      </MemoryRouter>
    </I18nProvider>,
  );
  return video;
}

const fire = (video: HTMLVideoElement, type: string) =>
  act(() => {
    video.dispatchEvent(new Event(type));
  });

beforeEach(() => {
  vi.mocked(fetchEntity).mockReset().mockResolvedValue(null);
  vi.mocked(fetchMediaForEntity)
    .mockReset()
    .mockResolvedValue([item("b4"), item("b45"), item("b5")]);
  vi.mocked(fetchMediaRows)
    .mockReset()
    .mockImplementation(async (ids) =>
      Object.fromEntries(ids.map((id) => [id, book(id, id === "b45" ? "4.5" : "5")])),
    );
});
afterEach(cleanup);

describe("useSequenceFinish and NextInSequenceCard (CORE-3013)", () => {
  it("offers the next book with its place only once playback ends, as a link", async () => {
    const video = renderHarness(book("b4", "4"));
    await waitFor(() => expect(fetchMediaForEntity).toHaveBeenCalledWith("s1", expect.anything()));
    expect(screen.getByText("ended plainly")).toBeInTheDocument();
    expect(fetchMediaRows).not.toHaveBeenCalled();

    fire(video, "ended");
    const link = await screen.findByRole("link", { name: /Title b45/ });
    expect(link).toHaveAttribute("href", "/media/b45");
    expect(await screen.findByText("The Long Road · No. 4.5")).toBeInTheDocument();
    expect(screen.getByRole("region", { name: "Next in series" })).toBeInTheDocument();
    expect(screen.getByRole("region").querySelector("img")).toHaveAttribute("src", "/art/b45.jpg");
    // An offer only: nothing in the card plays or counts down.
    expect(screen.queryByRole("button", { name: /play/i })).toBeNull();
    await expectNoAxeViolations(document.body);
  });

  it("clears the offer when playback resumes or seeks", async () => {
    const video = renderHarness(book("b4", "4"));
    await waitFor(() => expect(fetchMediaForEntity).toHaveBeenCalled());
    fire(video, "ended");
    await screen.findByRole("region", { name: "Next in series" });
    fire(video, "seeking");
    expect(screen.getByText("ended plainly")).toBeInTheDocument();
    fire(video, "ended");
    await screen.findByRole("region", { name: "Next in series" });
    fire(video, "play");
    expect(screen.getByText("ended plainly")).toBeInTheDocument();
  });

  it("shows the plain ended state for the last book", async () => {
    const video = renderHarness(book("b5", "5"));
    await waitFor(() => expect(fetchMediaForEntity).toHaveBeenCalled());
    fire(video, "ended");
    await act(async () => {});
    expect(screen.getByText("ended plainly")).toBeInTheDocument();
  });

  it("reads nothing for audio outside a sequence, and stays off while disabled", async () => {
    const plain = book("t1", "", { credits: [] });
    const video = renderHarness(plain);
    fire(video, "ended");
    await act(async () => {});
    expect(fetchMediaForEntity).not.toHaveBeenCalled();
    expect(screen.getByText("ended plainly")).toBeInTheDocument();
    cleanup();

    const queued = renderHarness(book("b4", "4"), false);
    await waitFor(() => expect(fetchMediaForEntity).toHaveBeenCalled());
    fire(queued, "ended");
    await act(async () => {});
    expect(screen.getByText("ended plainly")).toBeInTheDocument();
  });

  it("keeps the offer without a place when the next book's read fails", async () => {
    vi.mocked(fetchMediaRows).mockRejectedValue(new Error("offline"));
    const video = renderHarness(book("b4", "4"));
    await waitFor(() => expect(fetchMediaForEntity).toHaveBeenCalled());
    fire(video, "ended");
    await screen.findByRole("link", { name: /Title b45/ });
    expect(screen.getByText("The Long Road")).toBeInTheDocument();
  });

  it("offers the series' next even when a smaller container is the nearest", async () => {
    vi.mocked(fetchMediaForEntity).mockImplementation(async (id) =>
      id === "c1" ? [item("b4"), item("x9")] : [item("b3"), item("b4"), item("b45"), item("b5")],
    );
    const collection = {
      ...credit(""),
      entityID: "c1",
      title: "Shelf",
      kind: "collection",
      attributes: {},
    };
    const video = renderHarness(book("b4", "4", { credits: [collection, credit("4")] }));
    await waitFor(() => expect(fetchMediaForEntity).toHaveBeenCalledWith("s1", expect.anything()));
    fire(video, "ended");
    const link = await screen.findByRole("link", { name: /Title b45/ });
    expect(link).toHaveAttribute("href", "/media/b45");
  });

  it("does not bring a stale offer back after playing again while disabled", async () => {
    const video = document.createElement("video");
    const tree = (enabled: boolean) => (
      <I18nProvider>
        <MemoryRouter>
          <Harness media={book("b4", "4")} video={video} enabled={enabled} />
        </MemoryRouter>
      </I18nProvider>
    );
    const { rerender } = render(tree(true));
    await waitFor(() => expect(fetchMediaForEntity).toHaveBeenCalled());
    fire(video, "ended");
    await screen.findByRole("region", { name: "Next in series" });
    rerender(tree(false));
    // Played again while the listeners were off: the element is no longer ended.
    fire(video, "play");
    rerender(tree(true));
    await act(async () => {});
    expect(screen.getByText("ended plainly")).toBeInTheDocument();
  });
});
