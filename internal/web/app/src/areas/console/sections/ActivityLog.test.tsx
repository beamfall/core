import "@testing-library/jest-dom/vitest";
import { cleanup, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { fetchDevices, fetchLibraries, fetchProfilesAdmin } from "@/lib/graphql";
import { type ActivityRecord, fetchActivityRecords } from "@/lib/graphql/activity";
import { fetchMediaRows } from "@/lib/graphql/browse";
import { I18nProvider } from "@/lib/i18n";
import { ActivityLog } from "./ActivityLog";

vi.mock("@/lib/graphql", () => ({
  fetchDevices: vi.fn(),
  fetchLibraries: vi.fn(),
  fetchProfilesAdmin: vi.fn(),
}));
// A page of three: the three fixture entries fill it, so an older page may exist.
vi.mock("@/lib/graphql/activity", () => ({ ACTIVITY_PAGE_SIZE: 3, fetchActivityRecords: vi.fn() }));
vi.mock("@/lib/graphql/browse", () => ({ fetchMediaRows: vi.fn() }));

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
  vi.mocked(fetchActivityRecords).mockReset();
  vi.mocked(fetchActivityRecords).mockResolvedValue(entries);
  vi.mocked(fetchMediaRows).mockReset();
  vi.mocked(fetchMediaRows).mockResolvedValue({});
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
    expect(screen.getByText("Showing 1 of 3 loaded events")).toBeInTheDocument();

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

  it("names a media item when its title is visible to the viewer", async () => {
    vi.mocked(fetchMediaRows).mockResolvedValue({
      "m-1": { id: "m-1", title: "Night Train", favorited: false },
    });
    renderLog();

    expect(await screen.findByText("Alex moved Night Train to Trash")).toBeInTheDocument();
    expect(vi.mocked(fetchMediaRows).mock.calls[0][0]).toEqual(["m-1"]);
  });

  it("loads events older than the first page on request", async () => {
    const older: ActivityRecord[] = [
      { id: "0", occurredAt: "2026-11-10T09:00:00Z", action: "trash.purge", profileID: "p-alex" },
    ];
    vi.mocked(fetchActivityRecords).mockImplementation(async (_signal, offset = 0) =>
      offset === 0 ? entries : older,
    );
    const user = userEvent.setup();
    renderLog();

    await user.click(await screen.findByRole("button", { name: "Load older events" }));

    expect(await screen.findByText("Alex permanently emptied Trash")).toBeInTheDocument();
    expect(vi.mocked(fetchActivityRecords).mock.calls.at(-1)?.[1]).toBe(entries.length);
    expect(screen.getByText("Showing 4 of 4 loaded events")).toBeInTheDocument();
    // The short page was the last one, so nothing older remains to load.
    expect(screen.queryByRole("button", { name: "Load older events" })).not.toBeInTheDocument();
  });

  it("offers no older events when the first page is the whole log", async () => {
    vi.mocked(fetchActivityRecords).mockResolvedValue(entries.slice(0, 2));
    renderLog();

    expect(await screen.findByText("Alex moved an item to Trash")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Load older events" })).not.toBeInTheDocument();
  });

  it("says so when older events cannot be loaded, and keeps the loaded ones", async () => {
    vi.mocked(fetchActivityRecords).mockImplementation(async (_signal, offset = 0) => {
      if (offset === 0) return entries;
      throw new Error("offline");
    });
    const user = userEvent.setup();
    renderLog();

    await user.click(await screen.findByRole("button", { name: "Load older events" }));

    expect(await screen.findByRole("alert")).toHaveTextContent("Could not load older events.");
    expect(screen.getByText("Alex moved an item to Trash")).toBeInTheDocument();
  });

  it("writes the page in the viewer's language", async () => {
    render(
      <I18nProvider initialLocale="fr">
        <ActivityLog />
      </I18nProvider>,
    );

    expect(await screen.findByRole("group", { name: "Catégorie" })).toBeInTheDocument();
    expect(screen.queryByText("Alex moved an item to Trash")).not.toBeInTheDocument();
    expect(screen.getByText("3 événements affichés sur 3 chargés")).toBeInTheDocument();
  });
});
