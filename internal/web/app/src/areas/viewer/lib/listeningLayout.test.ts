import { describe, expect, it } from "vitest";
import type { EntityInfo, Layout, Section, SourceItem } from "@/lib/graphql/types";
import { entityLinkState, readEntityPreview } from "./entityPreview";
import { entityPeople, layoutListsOnlyMedia, listensOverLayout, shuffled } from "./listeningLayout";

const section = (kind: string): Section =>
  ({ id: kind, kind: "list", title: { en: "Rows" }, source: { kind } }) as unknown as Section;
const layout = (...kinds: string[]): Layout =>
  ({ id: "detail", role: "detail", entityKind: "record", sections: kinds.map(section) }) as Layout;
const container = { id: "e1", kind: "record", title: "One", archetype: "container" } as EntityInfo;
const audio: SourceItem[] = [
  { id: "a", kind: "media", title: "A", mediaClass: "audio" },
  { id: "b", kind: "media", title: "B", mediaClass: "audio" },
];

describe("listening layout over a detail Layout (CORE-2362)", () => {
  it("treats a Layout as Media-only only when every Section lists the Entity's Media", () => {
    expect(layoutListsOnlyMedia(layout("entity-media"))).toBe(true);
    expect(layoutListsOnlyMedia(layout("entity-media", "entity-media"))).toBe(true);
    expect(layoutListsOnlyMedia(layout("entity-media", "info"))).toBe(false);
    expect(layoutListsOnlyMedia(layout())).toBe(false);
  });

  it("lists an audio container or person the listening way", () => {
    expect(listensOverLayout(layout("entity-media"), container, audio)).toBe(true);
    const person = { ...container, archetype: "person" };
    expect(listensOverLayout(layout("entity-media"), person, audio)).toBe(true);
  });

  it("keeps the Layout when anything known rules the listening layout out", () => {
    const only = layout("entity-media");
    expect(listensOverLayout(null, container, audio)).toBe(false);
    expect(listensOverLayout(layout("entity-media", "info"), container, audio)).toBe(false);
    expect(listensOverLayout(only, { ...container, archetype: "work" }, audio)).toBe(false);
    const mixed = [...audio, { id: "v", kind: "media", title: "V", mediaClass: "video" }];
    expect(listensOverLayout(only, null, mixed)).toBe(false);
    expect(listensOverLayout(only, null, null, "video")).toBe(false);
    // Media that do not report a class are not known to be audio.
    expect(listensOverLayout(only, container, [{ id: "u", kind: "media", title: "U" }])).toBe(
      false,
    );
    expect(listensOverLayout(only, container, [])).toBe(false);
  });

  it("waits while the Entity or its Media are unknown", () => {
    const only = layout("entity-media");
    expect(listensOverLayout(only, null, audio)).toBeNull();
    expect(listensOverLayout(only, container, null)).toBeNull();
    expect(listensOverLayout(only, null, null, "audio")).toBeNull();
  });

  it("names related person Entities in order, each once", () => {
    const entity = {
      ...container,
      relatedEntities: [
        { kind: "maker", archetype: "person", items: [audio[0], audio[1]] },
        { kind: "label", archetype: "container", items: [{ id: "c", kind: "entity", title: "C" }] },
        { kind: "guest", archetype: "person", items: [audio[1]] },
      ],
    } as EntityInfo;
    expect(entityPeople(entity).map((item) => item.id)).toEqual(["a", "b"]);
    expect(entityPeople(null)).toEqual([]);
  });

  it("shuffles a copy and keeps every item", () => {
    const items = [1, 2, 3, 4];
    // Always picking the first remaining slot rotates the list.
    expect(shuffled(items, () => 0)).toEqual([2, 3, 4, 1]);
    expect(items).toEqual([1, 2, 3, 4]);
    expect([...shuffled(items)].sort()).toEqual(items);
  });

  it("carries the card's Media class into the Entity page's preview", () => {
    const state = entityLinkState(
      { id: "e1", title: "One", kind: "record", mediaClass: "audio" },
      "v",
    );
    expect(readEntityPreview(state, "v", "e1")).toEqual({
      title: "One",
      kind: "record",
      mediaClass: "audio",
    });
  });
});
