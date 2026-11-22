import { describe, expect, it } from "vitest";
import type { MediaRow } from "@/lib/graphql/browse";
import type { RelatedEntityGroup, SourceItem } from "@/lib/graphql/types";
import {
  albumTrackRows,
  appearsOn,
  formatDuration,
  hasKnownNonAudio,
  isAudioListing,
  LISTED_BYLINE_LIMIT,
  listedRow,
  listedTrackRows,
  ownerPeople,
  playState,
  topTracks,
  trackGroups,
} from "./trackList";

const media = (id: string, title: string, extra: Partial<SourceItem> = {}): SourceItem => ({
  id,
  kind: "media",
  title,
  ...extra,
});

function row(id: string, title: string, people: string[], extra: Partial<MediaRow> = {}): MediaRow {
  return {
    id,
    title,
    favorited: false,
    mediaClass: "audio",
    credits: people.map((person) => ({
      entityID: person,
      title: person === "person-a" ? "Test Artist" : "Guest Artist",
      kind: "performer",
      archetype: "person",
      relation: "performed-by",
    })),
    ...extra,
  };
}

const people: RelatedEntityGroup[] = [
  {
    kind: "performer",
    archetype: "person",
    items: [{ id: "person-a", kind: "entity", title: "Test Artist" }],
  },
];

describe("track list rules", () => {
  const items = [media("t1", "Track One"), media("t2", "Track Two"), media("t3", "Track Three")];
  const rows = {
    t1: row("t1", "Track One", ["person-a"], { durationSeconds: 201 }),
    t2: row("t2", "Track Two", ["person-a", "person-b"], { durationSeconds: 59 }),
    t3: row("t3", "Track Three", ["person-a"]),
  };

  it("numbers tracks in server order with runtime, naming only credits that differ", () => {
    const tracks = albumTrackRows(items, rows, ownerPeople(people, items, rows));
    expect(
      tracks.map((track) => [track.number, track.item.title, track.artist, track.durationSeconds]),
    ).toEqual([
      [1, "Track One", undefined, 201],
      [2, "Track Two", "Test Artist, Guest Artist", 59],
      [3, "Track Three", undefined, undefined],
    ]);
  });

  it("groups by stored sequence and numbers each group by its stored index", () => {
    const discs = [
      media("d2a", "Track Four"),
      media("d1b", "Track Two"),
      media("d1a", "Track One"),
      media("d2b", "Track Five"),
    ];
    const positioned = {
      d1a: row("d1a", "Track One", ["person-a"], { sequence: 1, index: 1 }),
      d1b: row("d1b", "Track Two", ["person-a"], { sequence: 1, index: 2 }),
      d2a: row("d2a", "Track Four", ["person-a"], { sequence: 2, index: 1 }),
      d2b: row("d2b", "Track Five", ["person-a"], { sequence: 2, index: 2 }),
    };
    const tracks = albumTrackRows(discs, positioned, new Set(["person-a"]));
    expect(tracks.map((track) => [track.group, track.number, track.item.title])).toEqual([
      [1, 1, "Track One"],
      [1, 2, "Track Two"],
      [2, 1, "Track Four"],
      [2, 2, "Track Five"],
    ]);
    expect(
      trackGroups(tracks).map((group) => [group.group, group.rows.map((r) => r.item.id)]),
    ).toEqual([
      [1, ["d1a", "d1b"]],
      [2, ["d2a", "d2b"]],
    ]);
  });

  it("keeps server order and position numbers when a group's stored index repeats", () => {
    const repeated = {
      t1: row("t1", "Track One", ["person-a"], { sequence: 1, index: 4 }),
      t2: row("t2", "Track Two", ["person-a"], { sequence: 1, index: 4 }),
      t3: row("t3", "Track Three", ["person-a"], { sequence: 2 }),
    };
    expect(
      albumTrackRows(items, repeated, new Set(["person-a"])).map((t) => [t.group, t.number]),
    ).toEqual([
      [1, 1],
      [1, 2],
      [2, 1],
    ]);
  });

  it("ignores nonpositive listed positions and falls back to the per-Media row", () => {
    const listed = [
      media("t1", "Track One", { sequence: 1, index: 1 }),
      media("t2", "Track Two", { sequence: -1, index: -1 }),
    ];
    const positioned = {
      t1: row("t1", "Track One", ["person-a"]),
      t2: row("t2", "Track Two", ["person-a"], { sequence: 1, index: 2 }),
    };
    expect(
      albumTrackRows(listed, positioned, new Set(["person-a"])).map((t) => [
        t.item.id,
        t.group,
        t.number,
      ]),
    ).toEqual([
      ["t1", undefined, 1],
      ["t2", undefined, 2],
    ]);
  });

  it("does not group unless every track has a sequence and they differ", () => {
    const partial = {
      t1: row("t1", "Track One", ["person-a"], { sequence: 1, index: 3 }),
      t2: row("t2", "Track Two", ["person-a"], { sequence: 2, index: 1 }),
      t3: row("t3", "Track Three", ["person-a"], { index: 2 }),
    };
    const ungrouped = albumTrackRows(items, partial, new Set(["person-a"]));
    expect(ungrouped.map((t) => [t.group, t.number, t.item.id])).toEqual([
      [undefined, 1, "t2"],
      [undefined, 2, "t3"],
      [undefined, 3, "t1"],
    ]);
    expect(trackGroups(ungrouped)).toHaveLength(1);
    const single = {
      t1: row("t1", "Track One", ["person-a"], { sequence: 1 }),
      t2: row("t2", "Track Two", ["person-a"], { sequence: 1 }),
      t3: row("t3", "Track Three", ["person-a"], { sequence: 1 }),
    };
    expect(albumTrackRows(items, single, new Set()).every((t) => t.group === undefined)).toBe(true);
  });

  it("skips non-Media items when numbering", () => {
    const mixed = [{ id: "e1", kind: "entity", title: "Other" }, ...items];
    expect(albumTrackRows(mixed, rows, new Set(["person-a"])).map((t) => t.number)).toEqual([
      1, 2, 3,
    ]);
  });

  it("falls back to the people credited on every track when the container relates none", () => {
    expect([...ownerPeople(undefined, items, rows)]).toEqual(["Test Artist"]);
  });

  it("is an audio listing only when every Media item is audio", () => {
    expect(isAudioListing(items, rows)).toBe(true);
    expect(
      isAudioListing(items, { ...rows, t3: row("t3", "Track Three", [], { mediaClass: "video" }) }),
    ).toBe(false);
    expect(isAudioListing([], rows)).toBe(false);
    // A class the listing row already reports wins without a row read.
    expect(
      isAudioListing([media("x", "Clip", { mediaClass: "audio" } as Partial<SourceItem>)], {}),
    ).toBe(true);
    expect(
      hasKnownNonAudio([media("x", "Clip", { mediaClass: "video" } as Partial<SourceItem>)]),
    ).toBe(true);
  });

  it("reads a rolled-up container card as held non-audio Media (CORE-3058)", () => {
    const card: SourceItem = { id: "c1", kind: "entity", title: "Held" };
    const track = media("x", "Clip", { mediaClass: "audio" } as Partial<SourceItem>);
    expect(isAudioListing([track, card], {})).toBe(false);
    expect(hasKnownNonAudio([track, card])).toBe(true);
    expect(hasKnownNonAudio([track])).toBe(false);
  });

  it("formats runtimes", () => {
    expect(formatDuration(201)).toBe("3:21");
    expect(formatDuration(5)).toBe("0:05");
    expect(formatDuration(3725)).toBe("1:02:05");
    expect(formatDuration(undefined)).toBe("");
  });

  it("starts the player with the list as its queue", () => {
    const queue = [
      { id: "t1", title: "Track One", mediaClass: "audio" },
      { id: "t2", title: "Track Two", mediaClass: "audio" },
    ];
    expect(playState("viewer-1", queue, { id: "t2", title: "Track Two" })).toEqual({
      title: "Track Two",
      queue,
      queueIdentity: "viewer-1",
    });
    expect(playState(null, queue, { id: "t2", title: "Track Two" })).toEqual({
      title: "Track Two",
    });
  });
});

describe("person page split", () => {
  const container = (id: string, title: string) => ({
    entityID: id,
    title,
    kind: "record",
    archetype: "container",
    relation: "on",
  });
  const own: RelatedEntityGroup[] = [
    {
      kind: "record",
      archetype: "container",
      items: [{ id: "album-own", kind: "entity", title: "Test Album" }],
    },
  ];
  const items = [media("t1", "Track One"), media("t2", "Track Two"), media("t3", "Track Three")];
  const rows: Record<string, MediaRow> = {
    t1: {
      id: "t1",
      title: "Track One",
      favorited: false,
      credits: [container("album-own", "Test Album")],
    },
    t2: {
      id: "t2",
      title: "Track Two",
      favorited: false,
      credits: [container("album-mix", "Test Mix")],
    },
    t3: {
      id: "t3",
      title: "Track Three",
      favorited: false,
      credits: [container("album-mix", "Test Mix")],
    },
  };

  it("lists containers credited on the person's Media but not related to it as appears-on", () => {
    expect(appearsOn(own, items, rows)).toEqual([
      { id: "album-mix", kind: "entity", title: "Test Mix" },
    ]);
    expect(appearsOn(undefined, items, rows).map((item) => item.id)).toEqual([
      "album-own",
      "album-mix",
    ]);
  });

  it("ranks top tracks by the profile feed, then server order, capped", () => {
    const profileTop = [media("other", "Elsewhere"), media("t3", "Track Three")];
    const ranking = topTracks(items, profileTop);
    expect(ranking.hasPlays).toBe(true);
    expect(ranking.tracks.map((item) => item.id)).toEqual(["t3", "t1", "t2"]);
    expect(topTracks(items, profileTop, 2).tracks.map((item) => item.id)).toEqual(["t3", "t1"]);
  });

  it("keeps server order when none of the listed tracks have known plays", () => {
    const ranking = topTracks(items, [media("other", "Elsewhere")]);
    expect(ranking.hasPlays).toBe(false);
    expect(ranking.tracks.map((item) => item.id)).toEqual(["t1", "t2", "t3"]);
  });
});

describe("rows built from the listing (CORE-2922)", () => {
  const audio = (id: string, title: string, byline: string[], extra: Partial<SourceItem> = {}) =>
    media(id, title, { mediaClass: "audio", byline, ...extra });

  it("builds an audio item's row from its listing entry", () => {
    const item = audio("t1", "Track One", ["Test Artist"], {
      durationSeconds: 201,
      sequence: 2,
      index: 7,
      container: { id: "album-own", title: "Test Album" },
    });
    expect(listedRow(item)).toEqual({
      id: "t1",
      title: "Track One",
      favorited: false,
      mediaClass: "audio",
      byline: ["Test Artist"],
      durationSeconds: 201,
      sequence: 2,
      index: 7,
      container: { id: "album-own", title: "Test Album" },
    });
  });

  it("leaves an item of unknown class, or with a byline at the server's cap, to media(id)", () => {
    expect(listedRow(media("t1", "Track One"))).toBeNull();
    expect(listedRow(media("t1", "Track One", { mediaClass: "video" }))).toBeNull();
    const capped = Array.from({ length: LISTED_BYLINE_LIMIT }, (_, n) => `Person ${n}`);
    expect(listedRow(audio("t1", "Track One", capped))).toBeNull();
    expect(listedRow(audio("t1", "Track One", capped.slice(1)))).not.toBeNull();
  });

  it("names differing people, finds the album's own and lists appears-on as a media(id) row would", () => {
    const items = [
      audio("t1", "Track One", ["Test Artist"], {
        container: { id: "album-own", title: "Test Album" },
      }),
      audio("t2", "Track Two", ["Test Artist", "Guest Artist"], {
        container: { id: "album-mix", title: "Test Mix" },
      }),
    ];
    const listed = Object.fromEntries(items.map((item) => [item.id, listedRow(item) as MediaRow]));
    const owners = ownerPeople(undefined, items, listed);
    expect([...owners]).toEqual(["Test Artist"]);
    const tracks = listedTrackRows(items, listed, owners);
    expect(tracks.map((track) => track.artist)).toEqual([undefined, "Test Artist, Guest Artist"]);
    expect(tracks.every((track) => track.credits === undefined)).toBe(true);
    expect(appearsOn(own(), items, listed)).toEqual([
      { id: "album-mix", kind: "entity", title: "Test Mix" },
    ]);
  });

  function own(): RelatedEntityGroup[] {
    return [
      {
        kind: "record",
        archetype: "container",
        items: [{ id: "album-own", kind: "entity", title: "Test Album" }],
      },
    ];
  }
});
