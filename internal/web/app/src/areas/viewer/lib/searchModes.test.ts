import { describe, expect, it } from "vitest";
import type { ClientPref, SemanticCapabilityInfo } from "@/lib/graphql/types";
import { availableModes, recommendAvailable, semanticAvailable } from "./searchModes";

const cap = (pluginActive: boolean): SemanticCapabilityInfo => ({
  libraryID: "lib-1",
  pluginActive,
  state: pluginActive ? "ready" : "empty",
});

describe("semanticAvailable", () => {
  it("is true when any visible Library has the plugin active", () => {
    expect(semanticAvailable([cap(false), cap(true)])).toBe(true);
  });
  it("is false when no Library has the plugin active (or the list is empty)", () => {
    expect(semanticAvailable([cap(false)])).toBe(false);
    expect(semanticAvailable([])).toBe(false);
  });
});

describe("recommendAvailable", () => {
  it("is true only when recommend.opt-in is on", () => {
    const on: ClientPref[] = [{ key: "recommend.opt-in", value: "on" }];
    const off: ClientPref[] = [{ key: "recommend.opt-in", value: "" }];
    expect(recommendAvailable(on)).toBe(true);
    expect(recommendAvailable(off)).toBe(false);
    expect(recommendAvailable([])).toBe(false);
  });
});

describe("availableModes", () => {
  it("offers keyword-only (toggle hidden) when nothing else is available", () => {
    expect(availableModes([cap(false)], [])).toEqual(["keyword"]);
  });
  it("appends semantic when the plugin is active", () => {
    expect(availableModes([cap(true)], [])).toEqual(["keyword", "semantic"]);
  });
  it("appends recommend when opted in, in stable order", () => {
    expect(availableModes([cap(true)], [{ key: "recommend.opt-in", value: "on" }])).toEqual([
      "keyword",
      "semantic",
      "recommend",
    ]);
  });
});
