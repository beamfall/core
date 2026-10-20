import "@testing-library/jest-dom/vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import {
  fetchEntity,
  fetchEntityRelations,
  fetchMediaFields,
  fetchMediaFiles,
  fetchMediaRelations,
  linkMediaRelation,
  upsertEntityRelation,
} from "@/lib/graphql";
import { I18nProvider } from "@/lib/i18n";
import { CurationPage } from "./CurationPage";

// Radix Select drives PointerEvents that jsdom does not implement; stub the
// capture probes so the dropdown opens under the test renderer.
beforeEach(() => {
  if (!Element.prototype.hasPointerCapture) {
    Element.prototype.hasPointerCapture = () => false;
    Element.prototype.setPointerCapture = () => {};
    Element.prototype.releasePointerCapture = () => {};
  }
  if (!Element.prototype.scrollIntoView) {
    Element.prototype.scrollIntoView = () => {};
  }
});

vi.mock("@/lib/graphql", () => ({
  fetchMediaFields: vi.fn(),
  fetchMediaFiles: vi.fn(),
  fetchMediaRelations: vi.fn(),
  fetchEntity: vi.fn(),
  fetchEntityRelations: vi.fn(),
  upsertEntityRelation: vi.fn(),
  linkMediaRelation: vi.fn(),
}));

beforeEach(() => {
  vi.mocked(fetchMediaFields)
    .mockReset()
    .mockResolvedValue([
      { name: "title", type: "string", value: "The Film" },
      { name: "year", type: "int", value: "2024" },
    ]);
  vi.mocked(fetchMediaFiles)
    .mockReset()
    .mockResolvedValue([
      {
        mediaID: "media-1",
        path: "/media/part-1.mkv",
        contentFingerprint: "fp1",
        versionLabel: "1080p",
        partOrdinal: 1,
      },
      {
        mediaID: "media-1",
        path: "/media/part-2.mkv",
        contentFingerprint: "fp2",
        versionLabel: "1080p",
        partOrdinal: 2,
      },
    ]);
  vi.mocked(fetchMediaRelations).mockReset().mockResolvedValue([]);
  vi.mocked(fetchEntity)
    .mockReset()
    .mockResolvedValue({
      id: "entity-1",
      kind: "container",
      title: "The Collection",
      fields: [{ name: "title", type: "string", value: "The Collection" }],
    });
  vi.mocked(fetchEntityRelations)
    .mockReset()
    .mockResolvedValue([
      {
        relation: "contains",
        targetKind: "media",
        media: { id: "media-a", title: "Part One", favorited: false },
        attributes: {},
        sortOrder: 0,
      },
      {
        relation: "contains",
        targetKind: "media",
        media: { id: "media-b", title: "Part Two", favorited: false },
        attributes: {},
        sortOrder: 1,
      },
    ]);
  vi.mocked(upsertEntityRelation).mockReset().mockResolvedValue({
    relation: "contains",
    sourceID: "entity-1",
    targetID: "media-a",
  });
  vi.mocked(linkMediaRelation).mockReset().mockResolvedValue({
    relation: "extra-of",
    sourceID: "media-1",
    targetID: "media-x",
  });
});

function renderPage() {
  return render(
    <I18nProvider>
      <CurationPage />
    </I18nProvider>,
  );
}

async function loadMedia(user: ReturnType<typeof userEvent.setup>) {
  await user.type(screen.getByLabelText("Subject ID"), "media-1");
  await user.click(screen.getByRole("button", { name: "Load" }));
}

describe("CurationPage", () => {
  it("renders the empty state before a subject is loaded", () => {
    renderPage();
    expect(screen.getByText("Enter a subject ID to load its curation detail.")).toBeVisible();
  });

  it("loads a Media subject's fields and backing files", async () => {
    const user = userEvent.setup();
    renderPage();
    await loadMedia(user);

    expect(await screen.findAllByText("The Film")).not.toHaveLength(0);
    expect(screen.getAllByText("1080p")).toHaveLength(2);
    expect(screen.getByText("/media/part-1.mkv")).toBeVisible();
    expect(screen.getByText("/media/part-2.mkv")).toBeVisible();
    expect(fetchMediaFiles).toHaveBeenCalledWith("media-1");
    // Media subjects have no editable relation rows.
    expect(screen.getByText("Relations are edited on an Entity subject.")).toBeVisible();
  });

  it("links an extra through linkMediaRelation", async () => {
    const user = userEvent.setup();
    renderPage();
    await loadMedia(user);
    await screen.findByText("/media/part-1.mkv");

    await user.type(screen.getByLabelText("Extra Media ID"), "media-x");
    await user.click(screen.getByRole("button", { name: "Link extra" }));

    expect(linkMediaRelation).toHaveBeenCalledWith("media-1", "extra-of", "media-x");
  });

  it("loads an Entity subject's ordered relations and reorders them", async () => {
    const user = userEvent.setup();
    renderPage();

    await user.click(screen.getByLabelText("Subject type"));
    await user.click(await screen.findByRole("option", { name: "Entity" }));
    await user.type(screen.getByLabelText("Subject ID"), "entity-1");
    await user.click(screen.getByRole("button", { name: "Load" }));

    expect(await screen.findByText("Part One")).toBeVisible();
    expect(screen.getByText("Part Two")).toBeVisible();

    // Move the first row down: re-upserts both neighbours with swapped order.
    await user.click(screen.getAllByRole("button", { name: "Move down" })[0]);
    await waitFor(() => expect(upsertEntityRelation).toHaveBeenCalledTimes(2));
    expect(vi.mocked(upsertEntityRelation).mock.calls[0][0]).toMatchObject({
      fromEntityID: "entity-1",
      relation: "contains",
      toMediaID: "media-a",
      sortOrder: 1,
    });
  });

  it("surfaces an error when the load fails", async () => {
    const user = userEvent.setup();
    vi.mocked(fetchMediaFields).mockRejectedValue(new Error("boom"));
    renderPage();
    await loadMedia(user);

    expect(await screen.findByText("Could not load curation detail.")).toBeVisible();
  });
});
