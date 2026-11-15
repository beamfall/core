import { describe, expect, it } from "vitest";
import type { GenericMediaInfo, RelatedEntityGroup, SourceItem } from "@/lib/graphql/types";
import {
  appearsOn,
  formatDuration,
  hasKnownNonAudio,
  isAudioListing,
  ownerPeople,
  playState,
  topTracks,
  trackRows,
} from "./trackList";

const media = (id: string, title: string, extra: Partial<SourceItem> = {}): SourceItem => ({
  id,
  kind: "media",
  title,
  ...extra,
});

function row(
  id: string,
  title: string,
  people: string[],
  extra: Partial<GenericMediaInfo> = {},
): GenericMediaInfo {
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
    const tracks = trackRows(items, rows, ownerPeople(people, items, rows));
    expect(
      tracks.map((track) => [track.number, track.item.title, track.artist, track.durationSeconds]),
    ).toEqual([
      [1, "Track One", undefined, 201],
      [2, "Track Two", "Test Artist, Guest Artist", 59],
      [3, "Track Three", undefined, undefined],
    ]);
  });

  it("skips non-Media items when numbering", () => {
    const mixed = [{ id: "e1", kind: "entity", title: "Other" }, ...items];
    expect(trackRows(mixed, rows, new Set(["person-a"])).map((t) => t.number)).toEqual([1, 2, 3]);
  });

  it("falls back to the people credited on every track when the container relates none", () => {
    expect([...ownerPeople(undefined, items, rows)]).toEqual(["person-a"]);
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
  const rows: Record<string, GenericMediaInfo> = {
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
    expect(topTracks(items, profileTop).map((item) => item.id)).toEqual(["t3", "t1", "t2"]);
    expect(topTracks(items, profileTop, 2).map((item) => item.id)).toEqual(["t3", "t1"]);
  });
});
