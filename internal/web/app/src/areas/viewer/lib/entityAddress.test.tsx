import { cleanup, render } from "@testing-library/react";
import { MemoryRouter } from "react-router";
import { afterEach, describe, expect, it, vi } from "vitest";
import { entityIdFromAddress, entityPath, useCanonicalEntityAddress } from "./entityAddress";

const session = vi.hoisted(() => ({ revealed: false }));
vi.mock("@/lib/session", () => ({ useSession: () => ({ revealed: session.revealed }) }));

const HEX = "e2e3918f0227c0cefec7197c2f220a17";
const ID = `entity-${HEX}`;

describe("Entity addresses (CORE-2511)", () => {
  it("reads the Entity ID from a bare, readable or stale segment", () => {
    expect(entityIdFromAddress(ID)).toBe(ID);
    expect(entityIdFromAddress(`ted-lasso-season-3-${HEX}`)).toBe(ID);
    expect(entityIdFromAddress(`old-name-${HEX}`)).toBe(ID);
    expect(entityIdFromAddress(HEX)).toBe(ID);
    expect(entityIdFromAddress("e1")).toBe("e1");
    expect(entityIdFromAddress("a%20b")).toBe("a b");
    expect(entityIdFromAddress("%E0%A4%A")).toBe("%E0%A4%A");
    expect(entityIdFromAddress(undefined)).toBe("");
  });

  it("builds the readable address from the parent's title and the Entity's own", () => {
    expect(entityPath(ID, { title: "Season 3", parent: "Ted Lasso" })).toBe(
      `/entity/ted-lasso-season-3-${HEX}`,
    );
    expect(entityPath(ID, { title: "Ted Lasso" })).toBe(`/entity/ted-lasso-${HEX}`);
    expect(entityPath(ID)).toBe(`/entity/${ID}`);
    expect(entityIdFromAddress(entityPath(ID, { title: "Ted Lasso" }).slice(8))).toBe(ID);
  });

  it("keeps the bare-ID address for an ID that is not content-derived", () => {
    expect(entityPath("p1", { title: "Someone" })).toBe("/entity/p1");
    expect(entityPath("a/b", { title: "x" })).toBe("/entity/a%2Fb");
  });
});

function Harness({ parent, enabled = true }: { parent?: string; enabled?: boolean }) {
  useCanonicalEntityAddress({ id: ID, title: "Season 3" }, parent, enabled);
  return null;
}

function arriveAt(path: string, parent?: string, enabled = true) {
  window.history.replaceState({ idx: 2 }, "", path);
  render(
    <MemoryRouter>
      <Harness parent={parent} enabled={enabled} />
    </MemoryRouter>,
  );
}

describe("useCanonicalEntityAddress (CORE-2511)", () => {
  afterEach(() => {
    cleanup();
    session.revealed = false;
    window.history.replaceState(null, "", "/");
  });

  it("rewrites an old bare-ID address to the readable one in place", () => {
    const length = window.history.length;
    arriveAt(`/entity/${ID}?x=1#h`, "Ted Lasso");
    expect(window.location.pathname).toBe(`/entity/ted-lasso-season-3-${HEX}`);
    expect(window.location.search).toBe("?x=1");
    expect(window.location.hash).toBe("#h");
    expect(window.history.state).toEqual({ idx: 2 });
    expect(window.history.length).toBe(length);
  });

  it("waits until the parent is settled", () => {
    arriveAt(`/entity/${ID}`, undefined, false);
    expect(window.location.pathname).toBe(`/entity/${ID}`);
  });

  it("keeps a Revealed session on the bare address", () => {
    session.revealed = true;
    arriveAt(`/entity/ted-lasso-season-3-${HEX}`, "Ted Lasso");
    expect(window.location.pathname).toBe(`/entity/${ID}`);
  });

  it("leaves another page's address alone", () => {
    arriveAt("/entity/entity-00000000000000000000000000000000", "Ted Lasso");
    expect(window.location.pathname).toBe("/entity/entity-00000000000000000000000000000000");
  });
});
