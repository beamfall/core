import { Blocks, Camera } from "lucide-react";
import { describe, expect, it } from "vitest";
import type { StringKey } from "@/strings";
import { en } from "@/strings/catalog";
import { libraryTypeOptions, pluginIcon } from "./libraryTypes";

const t = (key: StringKey) => en[key];

describe("libraryTypeOptions (CORE-1156)", () => {
  it("gives one type per plugin, labelled by the server, and the Film & TV presets", () => {
    const options = libraryTypeOptions(
      [
        { id: "filmtv", displayName: "Film & TV" },
        { id: "photos", displayName: { en: "Photos", fr: "Photos" } },
        { id: "comics", displayName: "Comics" },
      ],
      t,
      "en",
    );
    expect(options.map((o) => [o.key, o.pluginID, o.familyScope, o.label, o.pluginLabel])).toEqual([
      ["filmtv/films", "filmtv", "films", "Films", "Film & TV"],
      ["filmtv/tvshows", "filmtv", "tvshows", "TV Shows", "Film & TV"],
      ["filmtv/mixed", "filmtv", "mixed", "Mixed", "Film & TV"],
      ["photos", "photos", null, "Photos", "Photos"],
      ["comics", "comics", null, "Comics", "Comics"],
    ]);
    expect(options[3].icon).toBe(Camera);
    expect(options[4].icon).toBe(Blocks);
  });

  it("prefers family scopes the server publishes over the presets", () => {
    const options = libraryTypeOptions(
      [
        {
          id: "filmtv",
          displayName: "Film & TV",
          familyScopes: [{ value: "films", displayName: "Movies" }],
        },
      ],
      t,
      "en",
    );
    expect(options.map((o) => [o.key, o.label])).toEqual([["filmtv/films", "Movies"]]);
  });

  it("returns no types without plugins", () => {
    expect(libraryTypeOptions([], t, "en")).toEqual([]);
    expect(pluginIcon("unknown")).toBe(Blocks);
  });
});
