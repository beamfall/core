import { afterEach, expect, it, vi } from "vitest";
import { fetchSourceItems } from "./viewer";

afterEach(() => vi.unstubAllGlobals());

it("requests stored card runtime and playback eligibility", async () => {
  let query = "";
  vi.stubGlobal(
    "fetch",
    vi.fn((_url: string, init?: RequestInit) => {
      query = JSON.parse(init?.body as string).query;
      return Promise.resolve({
        ok: true,
        status: 200,
        json: async () => ({ data: { sourceItems: [] } }),
      } as Response);
    }),
  );
  await fetchSourceItems({
    kind: "recently-added",
    entityKind: "",
    libraryID: "library",
    entityID: "",
    fieldFilters: null,
    entityFilter: null,
    limit: 50,
    offset: 0,
  });
  // An unplayed item has no engagement runtime; the stored card field must be selected.
  expect(query.replace(/engagement\s*\{[^}]*\}/g, "")).toMatch(/\bdurationSeconds\b/);
  expect(query).toMatch(/\blocked\b/);
});
