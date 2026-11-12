import "@testing-library/jest-dom/vitest";
import { cleanup, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { fetchDevices, fetchLibraries, fetchProfilesAdmin } from "@/lib/graphql";
import { type ActivityRecord, fetchActivityRecords } from "@/lib/graphql/activity";
import { I18nProvider } from "@/lib/i18n";
import { ActivityLog } from "./ActivityLog";

vi.mock("@/lib/graphql", () => ({
  fetchDevices: vi.fn(),
  fetchLibraries: vi.fn(),
  fetchProfilesAdmin: vi.fn(),
}));
vi.mock("@/lib/graphql/activity", () => ({ fetchActivityRecords: vi.fn() }));

const entries: ActivityRecord[] = [
  {
    id: "3",
    occurredAt: "2026-11-12T03:23:23Z",
    action: "media.trash",
    profileID: "p-alex",
    deviceID: "d-tv",
    targetType: "media",
    targetID: "m-1",
    libraryID: "lib-films",
  },
  { id: "2", occurredAt: "2026-11-12T03:00:00Z", action: "admin.login", profileID: "p-alex" },
  { id: "1", occurredAt: "2026-11-11T09:00:00Z", action: "widget.frobnicate_all" },
];

function renderLog() {
  return render(
    <I18nProvider>
      <ActivityLog />
    </I18nProvider>,
  );
}

beforeEach(() => {
  vi.mocked(fetchActivityRecords).mockResolvedValue(entries);
  vi.mocked(fetchProfilesAdmin).mockResolvedValue([
    { id: "p-alex", name: "Alex", kind: "owner", libraryIDs: [], disabled: false },
  ]);
  vi.mocked(fetchDevices).mockResolvedValue([
    { deviceID: "d-tv", name: "Living Room TV", role: "viewer", revoked: false, current: false },
  ]);
  vi.mocked(fetchLibraries).mockResolvedValue([]);
});

afterEach(cleanup);

describe("Console Activity log", () => {
  it("shows each event as a sentence, never as a raw key", async () => {
    renderLog();

    expect(await screen.findByText("Alex moved an item to Trash")).toBeInTheDocument();
    expect(screen.getByText("Alex unlocked the Console")).toBeInTheDocument();
    expect(screen.getByText("Widget frobnicate all")).toBeInTheDocument();
    expect(screen.queryByText("media.trash")).not.toBeInTheDocument();
    expect(screen.queryByText("widget.frobnicate_all")).not.toBeInTheDocument();
  });

  it("shows relative and local time, with the exact UTC time on hover", async () => {
    renderLog();

    const row = (await screen.findByText("Alex moved an item to Trash")).closest("tr")!;
    const time = row.querySelector("time")!;
    expect(time).toHaveAttribute("datetime", "2026-11-12T03:23:23Z");
    expect(time).toHaveAttribute("title", "2026-11-12 03:23:23 UTC");
    expect(time.textContent).not.toContain("T03:23:23Z");
    expect(time.textContent).toMatch(/2026/);
  });

  it("reveals the raw key and identifiers on demand", async () => {
    const user = userEvent.setup();
    renderLog();

    await user.click(
      await screen.findByRole("button", { name: "Show details: Alex moved an item to Trash" }),
    );

    expect(screen.getByText("media.trash")).toBeInTheDocument();
    expect(screen.getByText("media: m-1")).toBeInTheDocument();
    expect(screen.getByText("Living Room TV (d-tv)")).toBeInTheDocument();

    await user.click(
      screen.getByRole("button", { name: "Hide details: Alex moved an item to Trash" }),
    );
    expect(screen.queryByText("media.trash")).not.toBeInTheDocument();
  });

  it("filters by category", async () => {
    const user = userEvent.setup();
    renderLog();

    const filters = within(await screen.findByRole("group", { name: "Category" }));
    await user.click(filters.getByRole("button", { name: /Security/ }));

    expect(screen.getByText("Alex unlocked the Console")).toBeInTheDocument();
    expect(screen.queryByText("Alex moved an item to Trash")).not.toBeInTheDocument();
    expect(screen.getByText("Showing 1 of the latest 3 events")).toBeInTheDocument();

    await user.click(filters.getByRole("button", { name: "All" }));
    expect(screen.getByText("Alex moved an item to Trash")).toBeInTheDocument();
  });

  it("searches sentences and raw keys, and says when nothing matches", async () => {
    const user = userEvent.setup();
    renderLog();

    const search = await screen.findByRole("searchbox", { name: "Search activity" });
    await user.type(search, "admin.login");
    expect(screen.getByText("Alex unlocked the Console")).toBeInTheDocument();
    expect(screen.queryByText("Alex moved an item to Trash")).not.toBeInTheDocument();

    await user.clear(search);
    await user.type(search, "no such thing");
    expect(screen.getByText("No matches.")).toBeInTheDocument();
  });

  it("still reads as sentences when names cannot be loaded", async () => {
    vi.mocked(fetchProfilesAdmin).mockRejectedValue(new Error("forbidden"));
    renderLog();

    expect(
      await screen.findByText("A profile that no longer exists moved an item to Trash"),
    ).toBeInTheDocument();
  });
});
