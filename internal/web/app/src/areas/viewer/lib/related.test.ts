import { describe, expect, it } from "vitest";
import type { MediaCredit } from "@/lib/graphql/types";
import { moreWithLead } from "./related";

const credit = (entityID: string, archetype: string, role?: string): MediaCredit => ({
  entityID,
  title: entityID,
  kind: archetype,
  archetype,
  relation: "r",
  ...(role ? { attributes: { role } } : {}),
});

describe("moreWithLead (CORE-2444)", () => {
  it("takes the first person credit in server order, across departments", () => {
    const credits = [
      credit("g1", "category"),
      credit("b", "person", "Directing"),
      credit("a", "person", "Acting"),
    ];
    expect(moreWithLead(credits)?.entityID).toBe("b");
  });

  it("is null without a person credit", () => {
    expect(moreWithLead([credit("g1", "category")])).toBeNull();
    expect(moreWithLead(undefined)).toBeNull();
  });
});
