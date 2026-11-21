import { describe, expect, it } from "vitest";
import type { SourceItem } from "@/lib/graphql/types";
import { cardHeading, cardSubline, repeatedTitles, showsYear } from "./cardTitles";

function item(id: string, title: string, year?: number, summary?: string): SourceItem {
  return { id, kind: "media", title, year, summary } as SourceItem;
}

describe("card titles (CORE-2320)", () => {
  it("finds titles repeated in a list, ignoring case and spacing", () => {
    const repeated = repeatedTitles([
      item("a", "Dune", 1984),
      item("b", "dune ", 2021),
      item("c", "Arrival", 2016),
    ]);
    expect([...repeated]).toEqual(["dune"]);
  });

  it("shows the year only on a repeated title that has one", () => {
    const items = [item("a", "Dune", 1984), item("b", "Dune"), item("c", "Arrival", 2016)];
    const repeated = repeatedTitles(items);
    expect(items.map((entry) => showsYear(entry, repeated))).toEqual([true, false, false]);
  });

  it("puts the year before the summary and never doubles it", () => {
    expect(cardSubline(item("a", "Dune", 2021, "Part one"), true)).toBe("2021 · Part one");
    expect(cardSubline(item("a", "Dune", 2021), true)).toBe("2021");
    expect(cardSubline(item("a", "Dune", 2021, "2021"), true)).toBe("2021");
    expect(cardSubline(item("a", "Dune", 2021, "Part one"), false)).toBe("Part one");
  });

  it("names an audio card's byline after the year and before the summary (CORE-2362)", () => {
    const track = {
      ...item("t", "Bohemian Rhapsody", 1975, "Side one"),
      byline: ["Queen"],
    };
    expect(cardSubline(track, false)).toBe("Queen · Side one");
    expect(cardSubline(track, true)).toBe("1975 · Queen · Side one");
    expect(cardSubline({ ...track, summary: "Queen" }, false)).toBe("Queen");
    expect(cardSubline({ ...track, byline: ["A", "B"], summary: "" }, false)).toBe("A, B");
  });

  it("puts the plugin subtitle first, in the display locale (CORE-2386)", () => {
    const episode = {
      ...item("e", "Reunion", 2024, "Reunion"),
      subtitle: { en: "S5 · E3", fr: "S5 · É3" },
    };
    expect(cardSubline(episode, false)).toBe("S5 · E3 · Reunion");
    expect(cardSubline(episode, true, "fr")).toBe("S5 · É3 · 2024 · Reunion");
    expect(cardSubline({ ...episode, summary: "S5 · E3" }, false)).toBe("S5 · E3");
    expect(cardSubline({ ...episode, subtitle: undefined }, false)).toBe("Reunion");
  });
});

describe("cardHeading for audio (CORE-2824)", () => {
  const track = {
    ...item("t", "Lantern"),
    mediaClass: "audio",
    container: { id: "album", title: "Paper Moons" },
    byline: ["Nightglass"],
  } as SourceItem;

  it("heads a track by its own title and leads with the album", () => {
    const { heading, lead } = cardHeading(track, true);
    expect(heading).toBe("Lantern");
    expect(lead).toBe("Paper Moons");
    expect(cardSubline(track, false, "en", lead)).toBe("Paper Moons · Nightglass");
  });

  it("keeps a track's subtitle as its lead when it has one", () => {
    expect(cardHeading({ ...track, subtitle: "Track 1" }, true)).toEqual({
      heading: "Lantern",
      lead: "Track 1",
    });
  });

  it("leaves video items headed by their container", () => {
    expect(cardHeading({ ...track, mediaClass: "video" }, true).heading).toBe("Paper Moons");
  });
});

describe("cardHeading (CORE-2524)", () => {
  const episode = {
    ...item("e", "S01E02 · Earthfall"),
    subtitle: "S1 · E2 · Earthfall",
    container: { id: "show", title: "The Expanse" },
  } as SourceItem;

  it("heads an item a container holds with the container and leads with its subtitle", () => {
    const { heading, lead } = cardHeading(episode, true);
    expect(heading).toBe("The Expanse");
    expect(lead).toBe("S1 · E2 · Earthfall");
    expect(cardSubline(episode, false, "en", lead)).toBe("S1 · E2 · Earthfall");
  });

  it("leads with the item's own title when it has no subtitle", () => {
    expect(cardHeading({ ...episode, subtitle: undefined }, true)).toEqual({
      heading: "The Expanse",
      lead: "S01E02 · Earthfall",
    });
  });

  it("keeps the item's own title inside the container's page or without a container", () => {
    expect(cardHeading(episode, false)).toEqual({
      heading: "S01E02 · Earthfall",
      lead: "S1 · E2 · Earthfall",
    });
    expect(cardHeading({ ...episode, container: undefined }, true).heading).toBe(
      "S01E02 · Earthfall",
    );
    expect(cardHeading({ ...episode, container: { id: "show", title: "  " } }, true).heading).toBe(
      "S01E02 · Earthfall",
    );
  });
});
