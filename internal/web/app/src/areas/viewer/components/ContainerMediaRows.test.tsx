import "@testing-library/jest-dom/vitest";
import { cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import { MemoryRouter, Route, Routes, useLocation, useParams } from "react-router";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { SourceItem } from "@/lib/graphql/types";
import { I18nProvider } from "@/lib/i18n";
import { ContainerMediaRows } from "./ContainerMediaRows";

const identity = JSON.stringify(["viewer", "p1", "adult", false]);
vi.mock("@/lib/session", () => ({ useViewerIdentity: () => identity }));
vi.mock("../player/preload", () => ({ preloadPlayer: vi.fn() }));

function Probe({ label }: { label: string }) {
  const id = useParams().id;
  const location = useLocation();
  return (
    <output aria-label={label}>
      {id} {JSON.stringify(location.state)}
    </output>
  );
}

function renderRows(items: SourceItem[]) {
  return render(
    <I18nProvider>
      <MemoryRouter initialEntries={["/entity/s1"]}>
        <Routes>
          <Route path="/entity/:id" element={<ContainerMediaRows items={items} label="Rows" />} />
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

afterEach(cleanup);

describe("ContainerMediaRows (CORE-2510)", () => {
  it("shows the ordinal and runtime, the title once and the synopsis", () => {
    renderRows(items);
    const list = screen.getByRole("list", { name: "Rows" });
    const rows = within(list).getAllByRole("listitem");
    expect(rows).toHaveLength(3);
    expect(within(rows[0]).getByText(/^E1 · 45 min/)).toBeInTheDocument();
    expect(within(rows[0]).getAllByText("Countdown")).toHaveLength(1);
    expect(within(rows[0]).getByText("The crew counts down.")).toHaveClass("bf-row-summary");
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
