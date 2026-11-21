import { afterEach, expect, it, vi } from "vitest";
import { setAdminUnlockHandler } from "@/lib/api";
import { fetchHomeLibraries, fetchSourceItems } from "./viewer";

afterEach(() => {
  setAdminUnlockHandler(undefined);
  vi.unstubAllGlobals();
});

it("reads Home visibility from the scoped libraries selection", async () => {
  const libraries = [{ id: "quiet", title: "Quiet Library", showOnHome: true }];
  const request = vi.fn(async (_url: string, init?: RequestInit) => {
    expect(JSON.parse(init?.body as string).query).toContain("libraries { id title showOnHome }");
    return new Response(JSON.stringify({ data: { libraries } }));
  });
  vi.stubGlobal("fetch", request);

  await expect(fetchHomeLibraries()).resolves.toEqual(libraries);
  expect(request).toHaveBeenCalledOnce();
});

it("handles a visibility read refusal without prompting for Admin unlock", async () => {
  const unlock = vi.fn(async () => true);
  setAdminUnlockHandler(unlock);
  vi.stubGlobal(
    "fetch",
    vi.fn(
      async () => new Response(JSON.stringify({ errors: [{ message: "admin session required" }] })),
    ),
  );

  await expect(fetchHomeLibraries()).resolves.toBeNull();
  expect(unlock).not.toHaveBeenCalled();
});

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
