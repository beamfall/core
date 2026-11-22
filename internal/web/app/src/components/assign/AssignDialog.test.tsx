import "@testing-library/jest-dom/vitest";
import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { fetchMedia } from "@/areas/viewer/lib/mediaDetail";
import {
  fixMatch,
  fixMatchCandidates,
  fixMatchCurrent,
  fixMatchLockedFields,
  fixMatchPreview,
  fixMatchSources,
} from "@/lib/graphql";
import { I18nProvider } from "@/lib/i18n";
import { AssignDialog } from "./AssignDialog";

vi.mock("@/areas/viewer/lib/mediaDetail", () => ({ fetchMedia: vi.fn() }));
vi.mock("@/lib/graphql", () => ({
  fixMatch: vi.fn(),
  fixMatchCandidates: vi.fn(),
  fixMatchCurrent: vi.fn(),
  fixMatchLockedFields: vi.fn(),
  fixMatchPreview: vi.fn(),
  fixMatchSources: vi.fn(),
  identifyLookup: vi.fn(),
}));

const movie = { mediaID: "m1", title: "The Thing", year: 1982 };
const catalog = {
  pluginID: "filmtv",
  scraperID: "catalog",
  label: "Catalog",
  acceptsPastedID: true,
  supportsChildren: false,
  isDefault: true,
};
const alternate = {
  ...catalog,
  scraperID: "alternative",
  label: "Alternative Catalog",
  acceptsPastedID: false,
  isDefault: false,
};
const selection = [{ mediaID: "m1" }, { mediaID: "m2" }, { mediaID: "m3" }];
const ids = { pluginID: "filmtv", scraperID: "catalog", candidateID: "thing-1982" };

beforeEach(() => {
  vi.resetAllMocks();
  vi.mocked(fixMatchSources).mockResolvedValue([alternate, catalog]);
  vi.mocked(fixMatchCurrent).mockResolvedValue({
    pluginID: "filmtv",
    scraperID: "alternative",
    candidateID: "thing-2011",
    title: "The Thing",
    year: 2011,
  });
  vi.mocked(fixMatchCandidates).mockResolvedValue([
    {
      ...ids,
      id: "thing-2011",
      title: "The Thing",
      year: 2011,
      confidence: 0.8,
      scored: true,
      threshold: 0.8,
    },
    {
      ...ids,
      id: ids.candidateID,
      title: "The Thing",
      year: 1982,
      confidence: 0.97,
      scored: true,
      threshold: 0.8,
    },
  ]);
  vi.mocked(fixMatchPreview).mockResolvedValue([
    {
      field: "summary",
      current: "An old overview.",
      proposed: "A corrected overview.",
      changed: true,
    },
  ]);
  vi.mocked(fixMatchLockedFields).mockResolvedValue([{ field: "title", value: "My title" }]);
  vi.mocked(fixMatch).mockImplementation(async ({ mediaID }) => ({
    id: mediaID,
    title: movie.title,
    year: movie.year,
    favorited: false,
  }));
  vi.mocked(fetchMedia).mockImplementation(async (id) => ({
    id,
    title: `Item ${id}`,
    year: movie.year,
    favorited: false,
  }));
});

async function open(selection: React.ComponentProps<typeof AssignDialog>["selection"] = [movie]) {
  const user = userEvent.setup();
  render(
    <I18nProvider>
      <AssignDialog selection={selection} />
    </I18nProvider>,
  );
  await user.click(screen.getByRole("button", { name: "Assign…" }));
  const dialog = within(await screen.findByRole("dialog", { name: "Assign metadata" }));
  await dialog.findByLabelText("Search title");
  return { user, dialog };
}

async function preview({ user, dialog }: Awaited<ReturnType<typeof open>>) {
  const search = dialog.getByLabelText("Search title");
  search.focus();
  await user.keyboard("{Enter}");
  const candidate = await dialog.findByRole("button", { name: /The Thing \(1982\).*97%/ });
  candidate.focus();
  await user.keyboard("{Enter}");
  expect(dialog.getByRole("button", { name: "Apply" })).toBeDisabled();
  await user.click(dialog.getByRole("button", { name: "Preview changes" }));
}

describe("Assign metadata", () => {
  // feature:assign-media
  it.each([false, true])(
    "re-matches a movie with an explicit preview and clear title lock: %s",
    async (clear) => {
      const view = await open();
      const { user, dialog } = view;
      expect(dialog.getByText("Matched to The Thing (2011) via Alternative Catalog")).toBeVisible();
      expect(dialog.getByLabelText("Metadata source")).toHaveDisplayValue("Catalog");
      expect(dialog.getByLabelText("Search title")).toHaveValue("The Thing");
      expect(dialog.getByLabelText("Year")).toHaveValue("1982");

      const source = dialog.getByLabelText("Metadata source");
      await user.selectOptions(
        source,
        within(source).getByRole("option", { name: "Alternative Catalog" }),
      );
      expect(dialog.queryByText("Know the catalog ID?")).not.toBeInTheDocument();
      await user.selectOptions(source, within(source).getByRole("option", { name: "Catalog" }));
      await preview(view);
      expect(fixMatchCandidates).toHaveBeenCalledWith({
        mediaID: "m1",
        pluginID: "filmtv",
        scraperID: "catalog",
        query: "The Thing",
        year: 1982,
      });
      expect(await dialog.findByText("A corrected overview.")).toBeVisible();
      expect(dialog.getByText("An old overview.")).toBeVisible();
      expect(fixMatch).not.toHaveBeenCalled();
      const checkbox = within(dialog.getByRole("group", { name: "Locked fields" })).getByRole(
        "checkbox",
        { name: /Replace Title with new value/ },
      );
      expect(checkbox).not.toBeChecked();
      expect(dialog.getByText("My title")).toBeVisible();
      if (clear) await user.click(checkbox);
      await user.click(dialog.getByRole("button", { name: "Apply" }));
      expect(await dialog.findByText("Applied")).toBeVisible();
      expect(fixMatch).toHaveBeenCalledWith({
        mediaID: "m1",
        ...ids,
        ...(clear ? { clearLocks: ["title"] } : {}),
      });
    },
  );

  it("sends a pasted provider ID straight to preview and invalidates the preview if it changes", async () => {
    vi.mocked(fixMatchSources).mockResolvedValue([catalog]);
    vi.mocked(fixMatchCurrent).mockResolvedValue(null);
    const { user, dialog } = await open();
    expect(dialog.getByText("Not matched")).toBeVisible();
    expect(dialog.queryByLabelText("Metadata source")).not.toBeInTheDocument();
    await user.click(dialog.getByText("Know the catalog ID?"));
    const input = dialog.getByLabelText("Paste a provider ID");
    await user.type(input, "tt0084787");
    await user.click(dialog.getByRole("button", { name: "Preview by ID" }));
    expect(await dialog.findByText("A corrected overview.")).toBeVisible();
    expect(fixMatchPreview).toHaveBeenCalledWith({
      mediaID: "m1",
      ...ids,
      candidateID: "tt0084787",
    });
    expect(fixMatchCandidates).not.toHaveBeenCalled();

    await user.type(input, "9");
    const apply = dialog.getByRole("button", { name: "Apply" });
    expect(apply).toBeDisabled();
    await user.click(dialog.getByRole("button", { name: "Preview by ID" }));
    await dialog.findByText("A corrected overview.");
    await user.click(apply);
    expect(fixMatch).toHaveBeenCalledWith({ mediaID: "m1", ...ids, candidateID: "tt00847879" });
  });

  it("confirms a shared candidate, applies in order, and continues after a failed item", async () => {
    vi.mocked(fixMatch).mockImplementation(async ({ mediaID }) => {
      if (mediaID === "m2") throw new Error("Catalog rejected this item");
      return { id: mediaID, title: movie.title, year: movie.year, favorited: false };
    });
    const view = await open(selection);
    await preview(view);
    const { user, dialog } = view;
    const table = within(await dialog.findByRole("table"));
    expect(table.getAllByRole("row")).toHaveLength(4);
    expect(table.getAllByText("The Thing (1982)")).toHaveLength(3);
    const apply = dialog.getByRole("button", { name: "Apply" });
    expect(apply).toBeDisabled();
    await user.click(dialog.getByRole("checkbox", { name: /I confirm/ }));
    await user.click(apply);
    expect(await table.findByText("Failed: Catalog rejected this item")).toBeVisible();
    expect(within(table.getByRole("row", { name: /Item m1/ })).getByText("Applied")).toBeVisible();
    expect(
      await within(table.getByRole("row", { name: /Item m3/ })).findByText("Applied"),
    ).toBeVisible();
    expect(vi.mocked(fixMatch).mock.calls.map(([vars]) => vars)).toEqual(
      selection.map((item) => ({ ...item, ...ids })),
    );
    expect(fixMatchPreview).toHaveBeenCalledTimes(3);
    expect(fixMatchLockedFields).toHaveBeenCalledTimes(3);
  });

  it("blocks a shared candidate for rows with the same structural ordinal", async () => {
    vi.mocked(fetchMedia).mockImplementation(async (id) => ({
      id,
      title: `Item ${id}`,
      year: movie.year,
      index: 0,
      favorited: false,
    }));
    const view = await open(selection);
    await preview(view);
    expect(await view.dialog.findByRole("alert")).toHaveTextContent(
      "Numbered items require their own catalog pairing",
    );
    expect(view.dialog.getByRole("button", { name: "Apply" })).toBeDisabled();
    expect(fixMatch).not.toHaveBeenCalled();
  });
});
