import "@testing-library/jest-dom/vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  addCollectionMedia,
  fetchCollections,
  fetchItemState,
  fetchTagAssignments,
} from "@/lib/graphql";
import { I18nProvider } from "@/lib/i18n";
import { MediaItemCardMenu } from "./MediaItemCardMenu";

vi.mock("@/lib/graphql", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/lib/graphql")>()),
  addCollectionMedia: vi.fn(),
  fetchCollections: vi.fn(),
  fetchItemState: vi.fn(),
  fetchTagAssignments: vi.fn(),
}));

vi.mock("@/lib/session", () => ({
  useViewerIdentity: () => "viewer-1",
  useSession: () => ({ manifest: { libraries: [{ id: "lib-1" }] }, isManager: true }),
}));

beforeEach(() => {
  vi.mocked(fetchItemState).mockReset().mockResolvedValue({
    id: "t1",
    kind: "media",
    watched: false,
    watchlisted: false,
    rating: 0,
  });
  vi.mocked(fetchCollections)
    .mockReset()
    .mockResolvedValue([
      {
        id: "p1",
        libraryID: "lib-1",
        name: "Test List",
        sortOrder: 0,
        memberCount: 0,
        members: [],
      },
    ]);
  vi.mocked(fetchTagAssignments).mockReset().mockResolvedValue({
    targetType: "media",
    targetID: "t1",
    libraryID: "lib-1",
    tags: [],
  });
  vi.mocked(addCollectionMedia)
    .mockReset()
    .mockResolvedValue(undefined as never);
});

afterEach(cleanup);

function renderMenu(isAudio: boolean) {
  render(
    <I18nProvider initialLocale="en">
      <MediaItemCardMenu
        title="Track One"
        primaryLabel="Play"
        isMedia
        isAudio={isAudio}
        href="https://example.test/watch/t1"
        target={{ mediaID: "t1" }}
        onPrimary={() => {}}
      />
    </I18nProvider>,
  );
  fireEvent.click(screen.getByRole("button", { name: "More actions for Track One" }));
}

describe("MediaItemCardMenu wording", () => {
  it("uses listening wording for audio Media", async () => {
    renderMenu(true);
    expect(await screen.findByRole("menuitem", { name: "Mark played" })).toBeInTheDocument();
    expect(screen.queryByRole("menuitem", { name: "Mark watched" })).not.toBeInTheDocument();
  });

  it("keeps watching wording for other Media", async () => {
    renderMenu(false);
    expect(await screen.findByRole("menuitem", { name: "Mark watched" })).toBeInTheDocument();
    expect(screen.queryByRole("menuitem", { name: "Mark played" })).not.toBeInTheDocument();
  });

  it("adds the Media to a playlist from the menu", async () => {
    renderMenu(true);
    fireEvent.click(await screen.findByRole("menuitem", { name: "Add to playlist" }));
    fireEvent.click(await screen.findByRole("button", { name: /Test List/ }));
    await vi.waitFor(() => expect(addCollectionMedia).toHaveBeenCalledWith("p1", ["t1"]));
    expect(await screen.findByRole("status")).toHaveTextContent("Test List");
  });
});
