import { describe, expect, it } from "vitest";
import type { Section, SourceItem } from "@/lib/graphql/types";
import {
  ambiguousAudioContainers,
  foldNewShelf,
  groupByContainer,
  groupsNewByContainer,
  groupsShelfByContainer,
  NEW_SHELF_CARDS,
  NEW_SHELF_FETCH,
  withContainerPoster,
} from "./containerGroups";

function episode(id: string, show?: string): SourceItem {
  return {
    id,
    kind: "media",
    title: id,
    mediaClass: "video",
    container: show
      ? { id: `show-${show}`, title: show, artwork: { poster: `/art/${show}` } }
      : undefined,
  };
}

const label = (count: number) => `${count} new`;

describe("groupByContainer (CORE-2524)", () => {
  it("folds siblings into one container card at the first sibling's place", () => {
    const grouped = groupByContainer(
      [
        episode("a1", "Alpha"),
        episode("film"),
        episode("a2", "Alpha"),
        episode("b1", "Beta"),
        episode("a3", "Alpha"),
      ],
      label,
    );
    expect(grouped.map((item) => item.id)).toEqual(["show-Alpha", "film", "show-Beta"]);
    expect(grouped[0]).toMatchObject({
      kind: "entity",
      title: "Alpha",
      subtitle: "3 new",
      artwork: { poster: "/art/Alpha" },
    });
  });

  it("leaves a lone non-video item and items without a container as they are", () => {
    const track = { ...episode("t1", "Album"), mediaClass: "audio" };
    const items = [track, episode("film")];
    expect(groupByContainer(items, label)).toEqual(items);
  });

  it("folds a lone episode into its show and leaves items without a container", () => {
    const grouped = groupByContainer([episode("b1", "Beta"), episode("film")], label);
    expect(grouped.map((item) => item.id)).toEqual(["show-Beta", "film"]);
    expect(grouped[0]).toMatchObject({
      kind: "entity",
      subtitle: "1 new",
      artwork: { poster: "/art/Beta" },
    });
  });
});

describe("withContainerPoster", () => {
  it("draws an episode with its show's poster", () => {
    const item = { ...episode("b1", "Beta"), artworkSlots: { poster: "/still", thumb: "/t" } };
    expect(withContainerPoster(item).artworkSlots).toEqual({ poster: "/art/Beta", thumb: "/t" });
  });

  it("leaves an item without a container poster as it is", () => {
    const film = episode("film");
    expect(withContainerPoster(film)).toBe(film);
  });
});

describe("groupsNewByContainer", () => {
  const section = (kind: string, source: string) =>
    ({
      id: "s",
      kind,
      title: "",
      artworkSlot: "poster",
      source: { kind: source },
    }) as Section;

  it("applies to a recently-added shelf only", () => {
    expect(groupsNewByContainer(section("shelf", "recently-added"))).toBe(true);
    expect(groupsNewByContainer(section("grid", "recently-added"))).toBe(false);
    expect(groupsNewByContainer(section("shelf", "continue-watching"))).toBe(false);
  });
});

describe("foldNewShelf (CORE-2823)", () => {
  it("refills the shelf when one show dominates the newest items", () => {
    // A season of 40 episodes lands ahead of 12 films and 3 other shows: the
    // first 20 items alone would fold to one card.
    const items = [
      ...Array.from({ length: 40 }, (_, i) => episode(`s${i}`, "Saga")),
      ...Array.from({ length: 12 }, (_, i) => episode(`film${i}`)),
      ...["Alpha", "Beta", "Gamma"].flatMap((show) => [
        episode(`${show}-1`, show),
        episode(`${show}-2`, show),
      ]),
    ];
    expect(items.length).toBeLessThanOrEqual(NEW_SHELF_FETCH);
    expect(foldNewShelf(items.slice(0, NEW_SHELF_CARDS), label)).toHaveLength(1);

    const shelf = foldNewShelf(items, label);
    expect(shelf).toHaveLength(16);
    expect(shelf[0]).toMatchObject({ id: "show-Saga", subtitle: "40 new" });
    expect(shelf.filter((item) => item.kind === "media")).toHaveLength(12);
    expect(shelf.filter((item) => item.kind === "entity")).toHaveLength(4);
  });

  it("stops at the shelf's card count", () => {
    const films = Array.from({ length: 50 }, (_, i) => episode(`film${i}`));
    expect(foldNewShelf(films, label)).toHaveLength(NEW_SHELF_CARDS);
  });

  it("counts only unfinished items, and names no count when every one is finished", () => {
    const done = (item: SourceItem): SourceItem => ({ ...item, engagement: { completed: true } });
    const [partly, allDone] = foldNewShelf(
      [
        done(episode("a1", "Alpha")),
        episode("a2", "Alpha"),
        episode("a3", "Alpha"),
        done(episode("b1", "Beta")),
        done(episode("b2", "Beta")),
      ],
      label,
    );
    expect(partly).toMatchObject({ id: "show-Alpha", subtitle: "2 new" });
    expect(allDone).toMatchObject({ id: "show-Beta" });
    expect(allDone.subtitle).toBeUndefined();
  });

  it("folds single-in-window tracks of multi-track audio albums when probed, keeping true singles", () => {
    const track = (id: string, albumID?: string, albumTitle?: string): SourceItem => ({
      id,
      kind: "media",
      title: id,
      mediaClass: "audio",
      container: albumID ? { id: albumID, title: albumTitle ?? albumID } : undefined,
    });
    const items = [
      track("album-track-1", "album-multi", "Revolver"),
      track("single-track-1", "album-single", "vampire"),
      track("loose-track", undefined),
    ];
    expect(ambiguousAudioContainers(items)).toEqual(["album-multi", "album-single"]);

    const grouped = groupByContainer(items, (count) => `${count} new`, {
      multiItemContainers: new Set(["album-multi"]),
    });
    expect(grouped).toHaveLength(3);
    expect(grouped[0]).toMatchObject({
      id: "album-multi",
      kind: "entity",
      title: "Revolver",
      subtitle: "1 new",
      mediaClass: "audio",
    });
    expect(grouped[1]).toMatchObject({ id: "single-track-1", kind: "media" });
    expect(grouped[2]).toMatchObject({ id: "loose-track", kind: "media" });

    // Empty countLabel (e.g. recently-played / most-played shelves) leaves subtitle undefined.
    const played = groupByContainer(items, () => "", {
      multiItemContainers: new Set(["album-multi"]),
    });
    expect(played[0].subtitle).toBeUndefined();
  });
});

describe("groupsShelfByContainer", () => {
  it("matches recently-added, recently-played, and most-played shelves only", () => {
    const mk = (kind: Section["kind"], sourceKind: string): Section => ({
      id: `${kind}-${sourceKind}`,
      kind,
      title: { en: sourceKind },
      artworkSlot: "poster",
      source: { kind: sourceKind },
    });
    expect(groupsShelfByContainer(mk("shelf", "recently-added"))).toBe(true);
    expect(groupsShelfByContainer(mk("shelf", "recently-played"))).toBe(true);
    expect(groupsShelfByContainer(mk("shelf", "most-played"))).toBe(true);
    expect(groupsShelfByContainer(mk("grid", "recently-played"))).toBe(false);
    expect(groupsShelfByContainer(mk("shelf", "continue-watching"))).toBe(false);
    expect(groupsShelfByContainer(mk("shelf", "entity-items"))).toBe(false);
  });
});
