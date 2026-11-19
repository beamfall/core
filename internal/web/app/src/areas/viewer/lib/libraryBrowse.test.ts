import { afterEach, describe, expect, it, vi } from "vitest";
import {
  browseFollowsEngagement,
  browseQuery,
  browseStateParams,
  DEFAULT_BROWSE_STATE,
  LIBRARY_MAX_QUERY,
  libraryItemsVariables,
  libraryViewClass,
  parseBrowseState,
  readLibraryViewMode,
  writeLibraryViewMode,
} from "./libraryBrowse";

afterEach(() => {
  vi.restoreAllMocks();
  window.localStorage.clear();
});

describe("library view mode (CORE-2386)", () => {
  it("remembers the layout per library", () => {
    writeLibraryViewMode("tv", "thumbnail");
    writeLibraryViewMode("films", "list");
    expect(readLibraryViewMode("tv")).toBe("thumbnail");
    expect(readLibraryViewMode("films")).toBe("list");
    expect(readLibraryViewMode("music")).toBe("poster");
  });

  it("reads the earlier grid choice, or anything unknown, as posters", () => {
    window.localStorage.setItem("beamfall.libraryView.tv", "grid");
    expect(readLibraryViewMode("tv")).toBe("poster");
    window.localStorage.setItem("beamfall.libraryView.tv", "mosaic");
    expect(readLibraryViewMode("tv")).toBe("poster");
  });

  it("falls back to posters when storage is blocked", () => {
    vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => {
      throw new Error("blocked");
    });
    vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
      throw new Error("blocked");
    });
    expect(() => writeLibraryViewMode("tv", "list")).not.toThrow();
    expect(readLibraryViewMode("tv")).toBe("poster");
  });

  it("draws each layout with its own list class", () => {
    expect(libraryViewClass("poster")).toBe("bf-grid");
    expect(libraryViewClass("thumbnail")).toBe("bf-grid bf-grid-wide");
    expect(libraryViewClass("list")).toBe("bf-stack bf-rows");
    expect(libraryViewClass("table")).toBe("bf-stack bf-rows bf-table-rows");
  });

  it("remembers the table view (CORE-2443)", () => {
    writeLibraryViewMode("films", "table");
    expect(readLibraryViewMode("films")).toBe("table");
  });
});

describe("library browse sorts and year range (CORE-2443)", () => {
  const read = (query: string) => parseBrowseState(new URLSearchParams(query));

  it("restarts a plays-sorted read on an engagement change, like the other watching sorts", () => {
    for (const query of [
      "sort=plays",
      "sort=last-watched",
      "sort=rating",
      "watch=unwatched",
      "rated=8",
    ]) {
      expect(browseFollowsEngagement(read(query))).toBe(true);
    }
    for (const query of [
      "",
      "sort=year",
      "sort=release-date",
      "sort=audience-score",
      "from=1990",
    ]) {
      expect(browseFollowsEngagement(read(query))).toBe(false);
    }
  });

  it("reads the release-date, audience-score and plays sorts", () => {
    for (const sort of ["release-date", "audience-score", "plays"] as const) {
      expect(read(`sort=${sort}`).sort).toBe(sort);
    }
    expect(read("sort=critic").sort).toBe("title");
  });

  it("reads a year range in order and drops a year out of range", () => {
    expect(read("from=1990&to=1999")).toMatchObject({ yearFrom: 1990, yearTo: 1999 });
    expect(read("from=1999&to=1990")).toMatchObject({ yearFrom: 1990, yearTo: 1999 });
    expect(read("from=1990")).toMatchObject({ yearFrom: 1990, yearTo: 0 });
    expect(read("from=12&to=99999")).toMatchObject({ yearFrom: 0, yearTo: 0 });
    expect(read("from=19x0&to=1700")).toMatchObject({ yearFrom: 0, yearTo: 0 });
  });

  it("lets a decade win over a range in the same URL", () => {
    expect(read("decade=1980&from=1990&to=1999")).toMatchObject({
      decade: 1980,
      yearFrom: 0,
      yearTo: 0,
    });
  });

  it("writes the range to the URL and sends it as the year bounds", () => {
    const state = { ...DEFAULT_BROWSE_STATE, yearFrom: 1990, yearTo: 2004 };
    expect(browseStateParams(state).toString()).toBe("from=1990&to=2004");
    expect(libraryItemsVariables({ libraryID: "l", state, limit: 10, offset: 0 })).toMatchObject({
      yearFrom: 1990,
      yearTo: 2004,
    });
    const openEnded = { ...DEFAULT_BROWSE_STATE, yearTo: 1979 };
    const vars = libraryItemsVariables({ libraryID: "l", state: openEnded, limit: 10, offset: 0 });
    expect(vars.yearTo).toBe(1979);
    expect(vars).not.toHaveProperty("yearFrom");
  });
});

describe("library browse search (CORE-2568)", () => {
  const read = (query: string) => parseBrowseState(new URLSearchParams(query));

  it("reads the search from q, trimmed, and writes it back first", () => {
    const state = read("sort=year&q=%20%20blue%20moon%20");
    expect(state.query).toBe("blue moon");
    expect(browseStateParams(state).toString()).toBe("q=blue+moon&sort=year");
    expect(read("q=%20%20").query).toBe("");
    expect(browseStateParams(read("q=")).toString()).toBe("");
  });

  it("caps the search at the server's limit in characters, not bytes", () => {
    const long = "é".repeat(LIBRARY_MAX_QUERY + 20);
    expect(Array.from(browseQuery(long))).toHaveLength(LIBRARY_MAX_QUERY);
    expect(browseQuery(undefined)).toBe("");
  });

  it("sends the search as the query argument only when there is one", () => {
    const vars = (state = DEFAULT_BROWSE_STATE) =>
      libraryItemsVariables({ libraryID: "l", state, limit: 10, offset: 0 });
    expect(vars({ ...DEFAULT_BROWSE_STATE, query: "moon" })).toMatchObject({ query: "moon" });
    expect(vars()).not.toHaveProperty("query");
  });
});
