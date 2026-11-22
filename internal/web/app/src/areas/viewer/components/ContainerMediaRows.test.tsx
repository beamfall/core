import "@testing-library/jest-dom/vitest";
import { cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import { MemoryRouter, Route, Routes, useLocation, useParams } from "react-router";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { fetchClientPrefs, setClientPref } from "@/lib/graphql";
import type { SourceItem } from "@/lib/graphql/types";
import { I18nProvider } from "@/lib/i18n";
import { containerPrefKey, resetSpoilerGuardForTest } from "../lib/spoilerGuard";
import { ContainerMediaRows } from "./ContainerMediaRows";

const identity = JSON.stringify(["viewer", "p1", "adult", false]);
vi.mock("@/lib/session", () => ({ useViewerIdentity: () => identity }));
vi.mock("../player/preload", () => ({ preloadPlayer: vi.fn() }));
vi.mock("@/lib/graphql", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/lib/graphql")>()),
  fetchClientPrefs: vi.fn(),
  setClientPref: vi.fn(),
}));

function Probe({ label }: { label: string }) {
  const id = useParams().id;
  const location = useLocation();
  return (
    <output aria-label={label}>
      {id} {JSON.stringify(location.state)}
    </output>
  );
}

function renderRows(items: SourceItem[], containerID?: string) {
  return render(
    <I18nProvider>
      <MemoryRouter initialEntries={["/entity/s1"]}>
        <Routes>
          <Route
            path="/entity/:id"
            element={<ContainerMediaRows items={items} label="Rows" containerID={containerID} />}
          />
          <Route path="/media/:id" element={<Probe label="detail" />} />
          <Route path="/watch/:id" element={<Probe label="player" />} />
        </Routes>
      </MemoryRouter>
    </I18nProvider>,
  );
}

const items: SourceItem[] = [
  {
    id: "m1",
    kind: "media",
    title: "Countdown",
    subtitle: { en: "Show · S1 · E1 · Countdown" },
    summary: "The crew counts down.",
    durationSeconds: 2700,
    artworkSlots: { thumb: "/art/m1" },
    engagement: { completed: true },
  },
  {
    id: "m2",
    kind: "media",
    title: "Liftoff",
    subtitle: { en: "Show · S1 · E2 · Liftoff" },
    engagement: { positionSeconds: 600, durationSeconds: 3000 },
  },
  { id: "m3", kind: "media", title: "Locked", locked: true },
  { id: "e9", kind: "entity", title: "Not a row" },
];

beforeEach(() => {
  vi.mocked(fetchClientPrefs).mockReset().mockResolvedValue([]);
  vi.mocked(setClientPref)
    .mockReset()
    .mockResolvedValue(undefined as never);
});

afterEach(() => {
  cleanup();
  resetSpoilerGuardForTest();
});

describe("ContainerMediaRows (CORE-2510)", () => {
  it("shows the ordinal and runtime, the title once and the synopsis", () => {
    renderRows(items);
    const list = screen.getByRole("list", { name: "Rows" });
    const rows = within(list).getAllByRole("listitem");
    expect(rows).toHaveLength(3);
    expect(within(rows[0]).getByText(/^E1 · 45 min/)).toBeInTheDocument();
    expect(within(rows[0]).getAllByText("Countdown")).toHaveLength(1);
    expect(within(rows[0]).getByText("The crew counts down.")).toHaveClass("bf-row-summary");
    // The class the app-wide poster size sizes (CORE-2893).
    expect(rows[0].querySelector(".bf-row-link > .bf-row-thumb")).not.toBeNull();
    expect(within(rows[0]).getByText("Watched")).toBeInTheDocument();
    expect(within(rows[1]).getByText("E2 · 50 min")).toBeInTheDocument();
    expect(within(rows[1]).getByRole("progressbar", { hidden: true })).toHaveAttribute(
      "aria-valuenow",
      "20",
    );
    // Sparse data degrades to the title alone.
    expect(rows[2]).toHaveTextContent(/^Locked$/);
    expect(screen.queryByText("Not a row")).toBeNull();
  });

  it("opens the item's detail page from the row", () => {
    renderRows(items);
    const link = screen.getByRole("link", { name: /^Countdown/ });
    expect(link).toHaveAttribute("href", "/media/m1");
    fireEvent.click(link);
    const detail = screen.getByRole("status", { name: "detail" }).textContent ?? "";
    expect(detail.startsWith("m1 ")).toBe(true);
    const state = JSON.parse(detail.slice(3));
    expect(state.mediaPreview.mediaID).toBe("m1");
    expect(state.queue.map((item: { id: string }) => item.id)).toContain("m2");
    expect(state.queueIdentity).toBe(identity);
  });

  it("names a partly played row's progress for assistive technology", () => {
    renderRows(items);
    expect(screen.getByRole("link", { name: /^Liftoff/ })).toHaveAccessibleName(/20%/);
  });

  it("plays from a separate button with the container's Media as the queue", () => {
    renderRows(items);
    expect(screen.queryByRole("button", { name: "Play Locked" })).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "Play Liftoff" }));
    const player = screen.getByRole("status", { name: "player" }).textContent ?? "";
    expect(player.startsWith("m2 ")).toBe(true);
    const state = JSON.parse(player.slice(3));
    expect(state.title).toBe("Liftoff");
    expect(state.queue.map((item: { id: string }) => item.id)).toContain("m1");
    expect(state.queueIdentity).toBe(identity);
  });

  it("renders nothing without Media", () => {
    const { container } = renderRows([{ id: "e9", kind: "entity", title: "Not a row" }]);
    expect(container.querySelector(".bf-rows")).toBeNull();
  });
});

describe("ContainerMediaRows spoiler guard (CORE-3057)", () => {
  const episodes: SourceItem[] = [
    {
      id: "u1",
      kind: "media",
      mediaClass: "video",
      title: "Twist",
      summary: "Everyone was a ghost.",
      artworkSlots: { thumb: "/art/u1" },
    },
    { ...items[0], mediaClass: "video" },
  ];

  it("hides an unplayed row's still and summary, and keeps its title", async () => {
    renderRows(episodes, "s1");
    const rows = within(screen.getByRole("list", { name: "Rows" })).getAllByRole("listitem");
    await screen.findByRole("button", { name: "Show spoilers" });
    expect(within(rows[0]).getAllByText("Twist").length).toBeGreaterThan(0);
    expect(within(rows[0]).getByTestId("spoiler-still")).toBeInTheDocument();
    expect(rows[0].querySelector('img[src*="/art/u1"]')).toBeNull();
    expect(within(rows[0]).queryByText("Everyone was a ghost.")).toBeNull();
    expect(within(rows[0]).getByText("Description hidden until you watch")).toBeInTheDocument();
    // A played row is untouched and has no reveal control.
    expect(within(rows[1]).getByText("The crew counts down.")).toBeInTheDocument();
    expect(within(rows[1]).queryByRole("button", { name: /Show details/ })).toBeNull();
  });

  it("reveals one row for the session from its labelled toggle", async () => {
    renderRows(episodes, "s1");
    const reveal = screen.getByRole("button", { name: "Show details for Twist" });
    expect(reveal).toHaveAttribute("aria-pressed", "false");
    fireEvent.click(reveal);
    expect(reveal).toHaveAttribute("aria-pressed", "true");
    expect(screen.getByText("Everyone was a ghost.")).toBeInTheDocument();
    expect(screen.queryByTestId("spoiler-still")).toBeNull();
    expect(setClientPref).not.toHaveBeenCalled();
  });

  it("reveals a row from its placeholder without opening it", async () => {
    renderRows(episodes, "s1");
    fireEvent.click(screen.getByTestId("spoiler-still"));
    expect(screen.queryByRole("status", { name: "detail" })).toBeNull();
    expect(screen.getByText("Everyone was a ghost.")).toBeInTheDocument();
  });

  it("stores Show spoilers for the container", async () => {
    renderRows(episodes, "s1");
    const toggle = await screen.findByRole("button", { name: "Show spoilers" });
    expect(toggle).toHaveAttribute("aria-pressed", "false");
    fireEvent.click(toggle);
    expect(setClientPref).toHaveBeenCalledWith(containerPrefKey("s1"), "on");
    expect(toggle).toHaveAttribute("aria-pressed", "true");
    expect(screen.getByText("Everyone was a ghost.")).toBeInTheDocument();
    // The per-row toggle leaves with the container reveal.
    expect(screen.queryByRole("button", { name: "Show details for Twist" })).toBeNull();
  });

  it("shows everything when the Profile turned the guard off", async () => {
    vi.mocked(fetchClientPrefs).mockResolvedValue([
      { key: "profile.show-unplayed-details", value: "on" },
    ]);
    renderRows(episodes, "s1");
    expect(await screen.findByText("Everyone was a ghost.")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Show spoilers" })).toBeNull();
  });

  it("does not guard without a container", () => {
    renderRows(episodes);
    expect(screen.getByText("Everyone was a ghost.")).toBeInTheDocument();
    expect(fetchClientPrefs).not.toHaveBeenCalled();
  });
});
