import { act, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { MediaCredit } from "@/lib/graphql/types";
import { AudioStage } from "./AudioStage";

const credit = (title: string, archetype: string): MediaCredit => ({
  entityID: title,
  title,
  kind: "k",
  archetype,
  relation: "r",
});

describe("AudioStage (CORE-1143)", () => {
  it("shows the title, queue position and credits grouped by archetype", () => {
    render(
      <AudioStage
        title="Song"
        artworkURL="/art.jpg"
        queuePosition="2 of 9"
        credits={[credit("Ann", "person"), credit("Bo", "person"), credit("Album", "container")]}
      />,
    );
    expect(screen.getByText("Song")).toBeTruthy();
    expect(screen.getByText("2 of 9")).toBeTruthy();
    expect(screen.getByText("Ann, Bo")).toBeTruthy();
    expect(screen.getByText("Album")).toBeTruthy();
  });

  it("falls back quietly when the artwork fails to load", () => {
    const { container } = render(<AudioStage title="Song" artworkURL="/bad.jpg" credits={[]} />);
    const img = container.querySelector(".bf-audio-artwork img");
    expect(img).toBeTruthy();
    fireEvent.error(img!);
    expect(container.querySelector(".bf-audio-artwork img")).toBeNull();
    expect(container.querySelector(".bf-audio-artwork")).toBeTruthy();
  });

  it("reserves the visualizer slot and leaves it empty without a player (CORE-1309)", () => {
    const { container } = render(<AudioStage title="Song" artworkURL="" credits={[]} />);
    const slot = container.querySelector("[data-visualizer-slot]");
    expect(slot).toBeTruthy();
    expect(slot?.getAttribute("aria-hidden")).toBe("true");
    expect(slot?.childElementCount).toBe(0);
    expect(container.querySelector("canvas")).toBeNull();
  });
});

describe("AudioStage track change (CORE-2218)", () => {
  afterEach(() => vi.useRealTimers());

  const art = (c: HTMLElement) =>
    [...c.querySelectorAll<HTMLImageElement>(".bf-audio-artwork img")].map((i) => [
      i.getAttribute("src"),
      i.dataset.state,
    ]);

  it("keeps the old artwork under the new one until it loads, then drops it", () => {
    vi.useFakeTimers();
    const { container, rerender } = render(
      <AudioStage title="A" trackKey="a" artworkURL="/a.jpg" credits={[]} />,
    );
    expect(art(container)).toEqual([["/a.jpg", "pending"]]);
    fireEvent.load(container.querySelector(".bf-audio-artwork img")!);
    expect(art(container)).toEqual([["/a.jpg", "in"]]);

    rerender(<AudioStage title="B" trackKey="b" artworkURL="/b.jpg" credits={[]} />);
    expect(art(container)).toEqual([
      ["/a.jpg", "under"],
      ["/b.jpg", "pending"],
    ]);
    act(() => vi.advanceTimersByTime(5000));
    expect(art(container)).toHaveLength(2);

    fireEvent.load(container.querySelectorAll(".bf-audio-artwork img")[1]!);
    expect(art(container)).toEqual([
      ["/a.jpg", "out"],
      ["/b.jpg", "in"],
    ]);
    act(() => vi.advanceTimersByTime(700));
    expect(art(container)).toEqual([["/b.jpg", "in"]]);
  });

  it("fades the old artwork out when the next item has none", () => {
    vi.useFakeTimers();
    const { container, rerender } = render(
      <AudioStage title="A" trackKey="a" artworkURL="/a.jpg" credits={[]} />,
    );
    fireEvent.load(container.querySelector(".bf-audio-artwork img")!);
    rerender(<AudioStage title="B" trackKey="b" artworkURL="" credits={[]} />);
    expect(art(container)).toEqual([["/a.jpg", "out"]]);
    act(() => vi.advanceTimersByTime(700));
    expect(art(container)).toEqual([]);
  });

  it("lifts the old words away while the new ones come in", () => {
    vi.useFakeTimers();
    const { container, rerender } = render(
      <AudioStage title="Old song" trackKey="a" artworkURL="" credits={[]} />,
    );
    rerender(
      <AudioStage
        title="New song"
        trackKey="b"
        artworkURL=""
        credits={[credit("Ann", "person")]}
      />,
    );
    const layers = [...container.querySelectorAll<HTMLElement>(".bf-audio-meta-layer")];
    expect(
      layers.map((l) => [l.textContent, l.dataset.state, l.getAttribute("aria-hidden")]),
    ).toEqual([
      ["Old song", "out", "true"],
      ["New songAnn", "in", null],
    ]);
    act(() => vi.advanceTimersByTime(320));
    expect(container.querySelectorAll(".bf-audio-meta-layer")).toHaveLength(1);
    expect(screen.getByText("New song")).toBeTruthy();
  });

  it("updates the words in place when the same item's credits arrive", () => {
    const { container, rerender } = render(
      <AudioStage title="Song" trackKey="a" artworkURL="" credits={[]} />,
    );
    rerender(
      <AudioStage title="Song" trackKey="a" artworkURL="" credits={[credit("Ann", "person")]} />,
    );
    expect(container.querySelectorAll(".bf-audio-meta-layer")).toHaveLength(1);
    expect(screen.getByText("Ann")).toBeTruthy();
  });
});
