import { describe, expect, it } from "vitest";
import type { MediaCredit, SourceItem } from "@/lib/graphql/types";
import {
  castGroups,
  castHasDetail,
  creditPart,
  creditsWithJob,
  filmographyOrder,
  nameInitial,
} from "./people";

const person = (entityID: string, attributes?: Record<string, string>): MediaCredit => ({
  entityID,
  title: `Name ${entityID}`,
  kind: "person",
  archetype: "person",
  relation: "credited-in",
  attributes,
});

describe("castGroups", () => {
  it("keeps person credits in server order, once each, grouped by department", () => {
    const groups = castGroups([
      person("a", { department: "Acting" }),
      { ...person("box"), archetype: "container" },
      person("b", { department: "Directing" }),
      person("c", { department: "Acting" }),
      person("a", { department: "Directing" }),
    ]);
    expect(groups.map((g) => [g.department, g.credits.map((c) => c.entityID)])).toEqual([
      ["Acting", ["a", "c"]],
      ["Directing", ["b"]],
    ]);
  });

  it("puts credits without a department in one group", () => {
    expect(castGroups([person("a"), person("b")])).toEqual([
      { department: "", credits: [person("a"), person("b")] },
    ]);
    expect(castGroups(undefined)).toEqual([]);
  });

  it("lists cast before crew departments, a merged credit under each of its departments", () => {
    const groups = castGroups([
      person("lead", { character: "John Nash" }),
      person("ron", { character: "Cameo", job: "Director", department: "Directing" }),
      person("akiva", { job: "Screenplay, Producer", department: "Writing, Production" }),
    ]);
    expect(
      groups.map((g) => [g.department, g.credits.map((c) => [c.entityID, creditPart(c)])]),
    ).toEqual([
      [
        "",
        [
          ["lead", "John Nash"],
          ["ron", "Cameo"],
        ],
      ],
      ["Directing", [["ron", "Director"]]],
      ["Writing", [["akiva", "Screenplay, Producer"]]],
      ["Production", [["akiva", "Screenplay, Producer"]]],
    ]);
  });

  it("keeps a billed credit without a character in the cast, and the cast first", () => {
    const groups = castGroups([
      person("dp", { job: "Director of Photography", department: "Camera" }),
      person("lead", { billing: "0", job: "Director", department: "Directing" }),
      person("second", { billing: "1", character: "" }),
    ]);
    expect(groups.map((g) => [g.department, g.credits.map((c) => c.entityID)])).toEqual([
      ["", ["lead", "second"]],
      ["Camera", ["dp"]],
      ["Directing", ["lead"]],
    ]);
    expect(groups[2].credits[0].attributes).toEqual({ job: "Director", department: "Directing" });
  });
});

describe("creditsWithJob", () => {
  it("returns people holding any of the jobs in credit order, once each", () => {
    const credits = [
      person("a", { job: "Producer" }),
      person("b", { job: "Writer, Director" }),
      person("c", { job: "Screenplay" }),
      person("b", { job: "Screenplay" }),
      { ...person("box", { job: "Writer" }), archetype: "container" },
    ];
    expect(creditsWithJob(credits, ["Writer", "Screenplay"]).map((c) => c.entityID)).toEqual([
      "b",
      "c",
    ]);
    expect(creditsWithJob(credits, ["Director"]).map((c) => c.entityID)).toEqual(["b"]);
    expect(creditsWithJob(undefined, ["Director"])).toEqual([]);
  });
});

describe("creditPart", () => {
  it("prefers the character, then the role, then the job", () => {
    expect(creditPart(person("a", { character: " Hero ", role: "Lead" }))).toBe("Hero");
    expect(creditPart(person("a", { character: " ", role: "Lead" }))).toBe("Lead");
    expect(creditPart(person("a", { job: "Director" }))).toBe("Director");
    expect(creditPart(person("a"))).toBe("");
  });
});

describe("castHasDetail", () => {
  it("is true when a person credit carries a headshot or a part", () => {
    expect(castHasDetail([person("a"), { ...person("b"), artworkSlots: { thumb: "/t" } }])).toBe(
      true,
    );
    expect(castHasDetail([person("a", { job: "Narrator" })])).toBe(true);
  });

  it("is false for names only, or detail on a credit that is not a person", () => {
    expect(castHasDetail([person("a"), person("b", { department: "Sound" })])).toBe(false);
    expect(
      castHasDetail([{ ...person("g", { role: "x" }), archetype: "category" }, person("a")]),
    ).toBe(false);
    expect(castHasDetail(null)).toBe(false);
  });
});

describe("nameInitial", () => {
  it("returns the first letter, uppercased", () => {
    expect(nameInitial(" émile")).toBe("É");
    expect(nameInitial("")).toBe("");
  });
});

describe("filmographyOrder", () => {
  it("sorts newest year first, undated last, and keeps ties in server order", () => {
    const items: SourceItem[] = [
      { id: "old", kind: "media", title: "Old", year: 1999 },
      { id: "undated", kind: "media", title: "Undated" },
      { id: "new", kind: "media", title: "New", year: 2015 },
      { id: "new-2", kind: "media", title: "New 2", year: 2015 },
    ];
    expect(filmographyOrder(items).map((item) => item.id)).toEqual([
      "new",
      "new-2",
      "old",
      "undated",
    ]);
  });
});
