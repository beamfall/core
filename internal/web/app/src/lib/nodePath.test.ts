import { describe, expect, it } from "vitest";
import { findNodeBySlug, nodePath, nodeSlug } from "./nodePath";

describe("node addresses (CORE-2322)", () => {
  it("collapses a repeated leading word", () => {
    expect(nodeSlug("library-library-films")).toBe("library-films");
    expect(nodePath("library-library-films", "/all")).toBe("/n/library-films/all");
  });

  it("leaves other IDs alone", () => {
    expect(nodeSlug("home")).toBe("home");
    expect(nodeSlug("library-films")).toBe("library-films");
    expect(nodeSlug("library-library-")).toBe("library-library-");
  });

  it("finds a node by its raw ID or its slug, the raw ID first", () => {
    const nodes = [{ id: "library-library-films" }, { id: "home" }];
    expect(findNodeBySlug(nodes, "library-films")).toBe(nodes[0]);
    expect(findNodeBySlug(nodes, "library-library-films")).toBe(nodes[0]);
    expect(findNodeBySlug(nodes, "home")).toBe(nodes[1]);
    expect(findNodeBySlug(nodes, "missing")).toBeUndefined();
    expect(findNodeBySlug(nodes, undefined)).toBeUndefined();
  });
});
