import "@testing-library/jest-dom/vitest";
import { act, cleanup, fireEvent, render, screen } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { I18nProvider } from "@/lib/i18n";
import type { FeedFilters } from "../components/ProfileFeed";
import { HistoryPage, playedAfterFor } from "./HistoryPage";

vi.mock("@/components/chrome/AppShell", () => ({
  AppShell: ({ children }: { children: ReactNode }) => <main>{children}</main>,
}));
vi.mock("../components/ViewerActions", () => ({ ViewerActions: () => null }));
vi.mock("@/lib/session", () => ({
  useSession: () => ({
    manifest: {
      libraries: [
        { id: "lib-music", title: "Music" },
        { id: "lib-tv", title: "TV" },
      ],
    },
  }),
}));
vi.mock("../components/ProfileFeed", () => ({
  ProfileFeed: ({
    kind,
    filters,
    empty,
  }: {
    kind: string;
    filters: FeedFilters;
    empty: string;
  }) => (
    <output data-testid={kind} data-empty={empty}>
      {JSON.stringify(filters)}
    </output>
  ),
}));

afterEach(() => {
  cleanup();
  vi.useRealTimers();
});

function filtersOf(kind: string): FeedFilters {
  return JSON.parse(screen.getByTestId(kind).textContent ?? "{}");
}

describe("playedAfterFor", () => {
  const now = new Date(2026, 10, 23, 15, 30);
  it("bounds each range from now", () => {
    expect(playedAfterFor("any", now)).toBe("");
    expect(playedAfterFor("today", now)).toBe(new Date(2026, 10, 23).toISOString());
    expect(playedAfterFor("week", now)).toBe(
      new Date(now.getTime() - 7 * 86_400_000).toISOString(),
    );
    expect(playedAfterFor("year", now)).toBe(
      new Date(now.getTime() - 365 * 86_400_000).toISOString(),
    );
  });
});

describe("HistoryPage filters (CORE-3333)", () => {
  it("narrows both feeds by search, Library and range", () => {
    vi.useFakeTimers();
    render(
      <I18nProvider initialLocale="en">
        <HistoryPage />
      </I18nProvider>,
    );
    expect(filtersOf("recently-played")).toEqual({ query: "", libraryID: "", playedAfter: "" });
    expect(screen.getByTestId("most-played")).toHaveAttribute("data-empty", "Nothing played yet.");

    fireEvent.change(screen.getByRole("searchbox"), { target: { value: "  coltrane " } });
    act(() => {
      vi.advanceTimersByTime(300);
    });
    fireEvent.click(screen.getByRole("button", { name: "Music" }));
    fireEvent.click(screen.getByRole("button", { name: "Past 7 days" }));

    for (const kind of ["recently-played", "most-played"]) {
      const filters = filtersOf(kind);
      expect(filters.query).toBe("coltrane");
      expect(filters.libraryID).toBe("lib-music");
      expect(Number.isNaN(Date.parse(filters.playedAfter))).toBe(false);
    }
    expect(screen.getByRole("button", { name: "Past 7 days" })).toHaveAttribute(
      "aria-pressed",
      "true",
    );
    expect(screen.getByTestId("recently-played")).toHaveAttribute(
      "data-empty",
      "No plays match these filters.",
    );

    fireEvent.click(screen.getByRole("button", { name: "All" }));
    fireEvent.click(screen.getByRole("button", { name: "Any time" }));
    expect(filtersOf("most-played")).toMatchObject({ libraryID: "", playedAfter: "" });
  });
});
