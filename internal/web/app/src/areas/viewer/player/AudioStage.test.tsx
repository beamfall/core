import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
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
    const img = container.querySelector("img");
    expect(img).toBeTruthy();
    fireEvent.error(img!);
    expect(container.querySelector("img")).toBeNull();
    expect(container.querySelector(".bf-audio-artwork")).toBeTruthy();
  });
});
