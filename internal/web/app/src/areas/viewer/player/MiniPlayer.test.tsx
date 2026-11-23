import "@testing-library/jest-dom/vitest";
import { render, screen, waitFor } from "@testing-library/react";
import { createRef } from "react";
import { MemoryRouter } from "react-router";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { MediaCredit } from "@/lib/graphql/types";
import { I18nProvider } from "@/lib/i18n";
import { MiniPlayer } from "./MiniPlayer";
import { loadWaveform, resetWaveformCache, type WaveformInput } from "./useWaveform";

beforeEach(() => resetWaveformCache());
afterEach(() => vi.restoreAllMocks());

function mockWaveformFetch(byURL: Record<string, number[]>) {
  return vi.spyOn(globalThis, "fetch").mockImplementation(async (input) => {
    const points = byURL[String(input)];
    if (!points) return new Response("missing", { status: 404 });
    return new Response(JSON.stringify({ version: 1, points }), { status: 200 });
  });
}

function Mini({
  mediaID,
  waveform,
}: {
  mediaID: string;
  waveform?: Omit<WaveformInput, "mediaID">;
}) {
  const videoRef = createRef<HTMLVideoElement>();
  return (
    <I18nProvider initialLocale="en">
      <video ref={videoRef} />
      <MiniPlayer
        videoRef={videoRef}
        title="Long Weekend"
        credits={[]}
        artworkURL=""
        mediaID={mediaID}
        songScale={1}
        onOpen={() => {}}
        onStop={() => {}}
        waveform={waveform}
      />
    </I18nProvider>
  );
}

describe("MiniPlayer waveform strip (CORE-2404)", () => {
  it("shows peaks the full player already cached without fetching again", async () => {
    const fetchMock = mockWaveformFetch({ "/w/a": [0.2, 0.8] });
    await loadWaveform("a", "/w/a");
    render(<Mini mediaID="a" waveform={{ infoMediaID: "a", url: "/w/a" }} />);

    const bar = screen.getByRole("progressbar", { name: "Playback position" });
    expect(bar).toHaveClass("has-waveform");
    expect(screen.getByTestId("scrubber-waveform")).not.toHaveClass("is-placeholder");
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });

  it("falls back to the plain strip when the waveform cannot load", async () => {
    mockWaveformFetch({});
    render(<Mini mediaID="a" waveform={{ infoMediaID: "a", url: "/w/a" }} />);
    await waitFor(() => expect(screen.queryByTestId("scrubber-waveform")).not.toBeInTheDocument());
    expect(screen.getByRole("progressbar", { name: "Playback position" })).not.toHaveClass(
      "has-waveform",
    );
  });

  it("shows the prefetched next track's peaks at once after advancing while docked", async () => {
    mockWaveformFetch({ "/w/a": [0.2, 0.5], "/w/b": [0.7, 0.3] });
    const waveform = { infoMediaID: "a", url: "/w/a", nextMediaID: "b", nextURL: "/w/b" };
    const view = render(<Mini mediaID="a" waveform={waveform} />);
    await waitFor(() =>
      expect(screen.getByTestId("scrubber-waveform")).not.toHaveClass("is-placeholder"),
    );
    await waitFor(() => expect(globalThis.fetch).toHaveBeenCalledTimes(2));

    view.rerender(<Mini mediaID="b" waveform={{}} />);
    expect(screen.getByTestId("scrubber-waveform")).not.toHaveClass("is-placeholder");
  });

  it("keeps the plain strip without a waveform", () => {
    render(<Mini mediaID="v" />);
    expect(screen.queryByTestId("scrubber-waveform")).not.toBeInTheDocument();
  });
});

describe("MiniPlayer artist and album links (CORE-3349)", () => {
  const credits: MediaCredit[] = [
    {
      entityID: "p1",
      title: "Bon Iver",
      kind: "artist",
      archetype: "person",
      relation: "performer",
    },
    { entityID: "p2", title: "Guest", kind: "artist", archetype: "person", relation: "performer" },
    {
      entityID: "c1",
      title: "22, A Million",
      kind: "album",
      archetype: "container",
      relation: "in",
    },
  ];

  function Linked({ credits }: { credits: MediaCredit[] }) {
    const videoRef = createRef<HTMLVideoElement>();
    return (
      <MemoryRouter>
        <I18nProvider initialLocale="en">
          <video ref={videoRef} />
          <MiniPlayer
            videoRef={videoRef}
            title="10 dEAThbREasT"
            credits={credits}
            artworkURL="/art.jpg"
            mediaID="m"
            songScale={1}
            onOpen={() => {}}
            onStop={() => {}}
          />
        </I18nProvider>
      </MemoryRouter>
    );
  }

  it("links every credited person and the album, and the artwork to the album", () => {
    const { container } = render(<Linked credits={credits} />);
    expect(screen.getByRole("link", { name: "Bon Iver" })).toHaveAttribute("href", "/entity/p1");
    expect(screen.getByRole("link", { name: "Guest" })).toHaveAttribute("href", "/entity/p2");
    const albumLinks = screen.getAllByRole("link", { name: "22, A Million" });
    expect(albumLinks).toHaveLength(2);
    for (const link of albumLinks) expect(link).toHaveAttribute("href", "/entity/c1");
    const artwork = container.querySelector("a.bf-mini-artwork");
    expect(albumLinks).toContain(artwork);
    expect(artwork).toHaveAttribute("tabindex", "-1");
    expect(artwork).not.toHaveAttribute("aria-hidden");
  });

  it("keeps names plain when a credit names no Entity", () => {
    const { container } = render(
      <Linked credits={credits.map((credit) => ({ ...credit, entityID: "" }))} />,
    );
    expect(screen.queryByRole("link")).not.toBeInTheDocument();
    expect(screen.getByText("Bon Iver")).toBeInTheDocument();
    expect(container.querySelector("a.bf-mini-artwork")).toBeNull();
  });
});
