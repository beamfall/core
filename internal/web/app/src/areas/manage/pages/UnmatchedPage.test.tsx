import "@testing-library/jest-dom/vitest";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router";
import { beforeEach, describe, expect, it, vi } from "vitest";
import {
  dismissUnmatched,
  fetchLibraries,
  fetchMediaFiles,
  fetchUnmatchedQueuePages,
  fetchUnmatchedRecords,
  fetchUnmatchedSuppressions,
  fixMatch,
  fixMatchCandidates,
  fixMatchPreview,
  identifyLookup,
  runHealthBulkOp,
} from "@/lib/graphql";
import type { UnmatchedItemInfo } from "@/lib/graphql/types";
import { I18nProvider } from "@/lib/i18n";
import { UnmatchedPage } from "./UnmatchedPage";

vi.mock("@/lib/graphql", () => ({
  fetchUnmatchedQueuePages: vi.fn(),
  fetchLibraries: vi.fn(),
  fetchMediaFiles: vi.fn(),
  dismissUnmatched: vi.fn(),
  runHealthBulkOp: vi.fn(),
  fixMatch: vi.fn(),
  fixMatchCandidates: vi.fn(),
  fixMatchPreview: vi.fn(),
  identifyLookup: vi.fn(),
  fetchUnmatchedRecords: vi.fn(),
  fetchUnmatchedSuppressions: vi.fn(),
  createMediaFromUnmatched: vi.fn(),
  suppressUnmatched: vi.fn(),
  resetUnmatchedSuppression: vi.fn(),
}));

// Three items under one folder that tied between same-named entries, and one
// unrelated item that fell below the threshold.
const tied = (n: number): UnmatchedItemInfo => ({
  mediaID: `media-part-${n}`,
  title: `Part ${n}`,
  pluginID: "filmtv",
  scraperID: "tmdb",
  confidence: 0.9,
  threshold: 0.75,
  candidateID: "",
  candidateTitle: "The Outsider",
  failureReason: "ambiguous",
});
const below: UnmatchedItemInfo = {
  mediaID: "media-thing",
  title: "The Thing 2011",
  pluginID: "filmtv",
  scraperID: "tmdb",
  confidence: 0.52,
  threshold: 0.8,
  candidateID: "wrong",
  candidateTitle: "The Thing",
  failureReason: "below-threshold",
};

const queue = (items: UnmatchedItemInfo[], complete = true) => ({ items, complete });

const candidates = [
  {
    id: "outsider-2018",
    title: "The Outsider",
    year: 2018,
    pluginID: "filmtv",
    scraperID: "tmdb",
    confidence: 0.95,
    threshold: 0.75,
    scored: true,
  },
  {
    id: "outsider-2020",
    title: "The Outsider",
    year: 2020,
    pluginID: "filmtv",
    scraperID: "tmdb",
    confidence: 0.95,
    threshold: 0.75,
    scored: true,
  },
];

beforeEach(() => {
  vi.clearAllMocks();
  vi.mocked(fetchUnmatchedQueuePages).mockResolvedValue(queue([tied(1), below, tied(2), tied(3)]));
  vi.mocked(fetchLibraries).mockResolvedValue([
    {
      id: "lib-1",
      title: "Library",
      pluginID: "filmtv",
      previewClips: false,
      restricted: false,
      roots: [],
    },
  ]);
  vi.mocked(fetchMediaFiles).mockImplementation(async (mediaID) => [
    {
      mediaID,
      path:
        mediaID === "media-thing"
          ? "/media/The Thing/The Thing 2011.mkv"
          : "/media/The Outsider (2020)/Disc 1/part.mkv",
      contentFingerprint: "",
      versionLabel: "",
      partOrdinal: 0,
    },
  ]);
  vi.mocked(dismissUnmatched).mockResolvedValue(true);
  vi.mocked(runHealthBulkOp).mockResolvedValue({ affected: 1 });
  vi.mocked(fixMatchCandidates).mockResolvedValue(candidates);
  vi.mocked(fixMatchPreview).mockResolvedValue([
    { field: "title", current: "Part 1", proposed: "The Outsider · 2020", changed: true },
    { field: "summary", current: "", proposed: "", changed: false },
  ]);
  vi.mocked(identifyLookup).mockResolvedValue({
    id: "tt-pasted",
    title: "The Outsider",
    year: 2020,
  });
  vi.mocked(fixMatch).mockResolvedValue({
    id: "media-part-1",
    title: "The Outsider",
    year: 2020,
    favorited: false,
  });
  vi.mocked(fetchUnmatchedRecords).mockResolvedValue([]);
  vi.mocked(fetchUnmatchedSuppressions).mockResolvedValue([]);
});

function renderPage() {
  return render(
    <MemoryRouter>
      <I18nProvider>
        <UnmatchedPage />
      </I18nProvider>
    </MemoryRouter>,
  );
}

async function openedOnGroup() {
  // The panel opens on the largest group, already searched with its folder.
  await screen.findByRole("button", { name: "Match all 3" });
  await waitFor(() => expect(fixMatchCandidates).toHaveBeenCalled());
}

describe("UnmatchedPage", () => {
  it("groups items and explains each group's problem in plain words", async () => {
    renderPage();
    await openedOnGroup();

    const groupButton = screen.getByRole("button", { name: /^The Outsider \(2020\)\s*3 items/ });
    expect(within(groupButton).getByText("3 items")).toBeVisible();
    expect(screen.getAllByText(/Several catalog entries fit this name equally well/).length).toBe(
      2,
    );
    expect(
      screen.getByText("The folder says 2020, so entries from 2020 are suggested first."),
    ).toBeVisible();
    const other = screen.getByRole("button", { name: /The Thing.*1 item/ });
    expect(
      within(other).getByText(
        /The closest catalog entry, “The Thing”, matched only 52%; 80% is needed./,
      ),
    ).toBeVisible();
  });

  it("searches with the folder's title and year and preselects the matching year", async () => {
    renderPage();
    await openedOnGroup();

    expect(fixMatchCandidates).toHaveBeenCalledWith({
      mediaID: "media-part-1",
      pluginID: "filmtv",
      scraperID: "tmdb",
      query: "The Outsider",
      year: 2020,
    });
    expect(screen.getByLabelText("Search title")).toHaveValue("The Outsider");
    expect(screen.getByLabelText("Year")).toHaveValue("2020");
    const suggested = await screen.findByRole("button", {
      name: /The Outsider \(2020\).*Suggested/,
    });
    expect(suggested).toHaveAttribute("aria-pressed", "true");
  });

  it("matches every item in the group and names the ones that failed", async () => {
    const user = userEvent.setup();
    vi.mocked(fixMatch).mockImplementation(async ({ mediaID }) => {
      if (mediaID === "media-part-2") throw new Error("refused");
      return { id: mediaID, title: "The Outsider", year: 2020, favorited: false };
    });
    renderPage();
    await openedOnGroup();
    await screen.findByRole("button", { name: /Suggested/ });

    vi.mocked(fetchUnmatchedQueuePages).mockResolvedValue(queue([below, tied(2)]));
    await user.click(screen.getByRole("button", { name: "Match all 3" }));

    await waitFor(() => expect(fixMatch).toHaveBeenCalledTimes(3));
    for (const n of [1, 2, 3]) {
      expect(fixMatch).toHaveBeenCalledWith({
        mediaID: `media-part-${n}`,
        pluginID: "filmtv",
        scraperID: "tmdb",
        candidateID: "outsider-2020",
      });
    }
    expect(await screen.findByText("Matched 2 of 3. Could not match: Part 2.")).toBeVisible();
    expect(fetchUnmatchedQueuePages).toHaveBeenCalledTimes(2);
  });

  it("previews without writing, and fetches a pasted ID as unscored", async () => {
    const user = userEvent.setup();
    renderPage();
    await openedOnGroup();
    await screen.findByRole("button", { name: /Suggested/ });

    await user.click(screen.getByRole("button", { name: "Preview changes" }));
    expect(await screen.findByText("The Outsider · 2020")).toBeVisible();
    expect(fixMatchPreview).toHaveBeenCalledWith({
      mediaID: "media-part-1",
      pluginID: "filmtv",
      scraperID: "tmdb",
      candidateID: "outsider-2020",
    });
    expect(fixMatch).not.toHaveBeenCalled();

    await user.click(screen.getByText("Know the catalog ID?"));
    await user.type(screen.getByLabelText("Paste a provider ID"), "tt-pasted");
    await user.click(screen.getByRole("button", { name: "Fetch by ID" }));
    expect(identifyLookup).toHaveBeenCalledWith({
      mediaID: "media-part-1",
      pluginID: "filmtv",
      scraperID: "tmdb",
      candidateID: "tt-pasted",
    });
    expect(await screen.findByText("Not scored")).toBeVisible();
    // A new candidate drops the old preview.
    expect(screen.queryByText("The Outsider · 2020")).not.toBeInTheDocument();
  });

  it("switching groups never carries a selection across", async () => {
    const user = userEvent.setup();
    renderPage();
    await openedOnGroup();
    await screen.findByRole("button", { name: /Suggested/ });

    vi.mocked(fixMatchCandidates).mockResolvedValue([
      { ...candidates[0], id: "thing-1982", title: "The Thing", year: 1982 },
    ]);
    await user.click(screen.getByRole("button", { name: /The Thing.*1 item/ }));
    const apply = await screen.findByRole("button", { name: "Apply + pin" });
    await waitFor(() =>
      expect(fixMatchCandidates).toHaveBeenLastCalledWith({
        mediaID: "media-thing",
        pluginID: "filmtv",
        scraperID: "tmdb",
        query: "The Thing",
      }),
    );
    await screen.findByText("The Thing (1982)");
    await user.click(apply);
    await waitFor(() =>
      expect(fixMatch).toHaveBeenCalledWith({
        mediaID: "media-thing",
        pluginID: "filmtv",
        scraperID: "tmdb",
        candidateID: "thing-1982",
      }),
    );
    expect(fixMatch).toHaveBeenCalledTimes(1);
  });

  it("shows the path and links to Rename and Libraries", async () => {
    renderPage();
    await openedOnGroup();

    expect(screen.getByText("/media/The Outsider (2020)/Disc 1/part.mkv")).toBeVisible();
    expect(screen.getByRole("link", { name: "Open Rename" })).toHaveAttribute(
      "href",
      "/manage/rename",
    );
    expect(screen.getByRole("link", { name: "Open Libraries" })).toHaveAttribute(
      "href",
      "/manage/libraries",
    );
    expect(screen.queryByRole("button", { name: "Rescan these" })).not.toBeInTheDocument();
  });

  it("selects the entry for the year that was searched, not the folder's", async () => {
    const user = userEvent.setup();
    renderPage();
    await openedOnGroup();
    await screen.findByRole("button", { name: /The Outsider \(2020\).*Suggested/ });

    const year = screen.getByLabelText("Year");
    await user.clear(year);
    await user.type(year, "2018");
    await user.click(screen.getByRole("button", { name: "Search" }));

    const suggested = await screen.findByRole("button", {
      name: /The Outsider \(2018\).*Suggested/,
    });
    expect(suggested).toHaveAttribute("aria-pressed", "true");
    expect(
      screen.getByRole("button", { name: /The Outsider \(2020\)/, pressed: false }),
    ).toHaveAttribute("aria-pressed", "false");
  });

  it("drops a preview that arrives after the selection changed", async () => {
    const user = userEvent.setup();
    let release: (value: Awaited<ReturnType<typeof fixMatchPreview>>) => void = () => {};
    vi.mocked(fixMatchPreview).mockReturnValueOnce(
      new Promise((resolve) => {
        release = resolve;
      }),
    );
    renderPage();
    await openedOnGroup();
    await screen.findByRole("button", { name: /Suggested/ });

    await user.click(screen.getByRole("button", { name: "Preview changes" }));
    await user.click(screen.getByRole("button", { name: /The Outsider \(2018\)/ }));
    release([{ field: "title", current: "Part 1", proposed: "Stale preview", changed: true }]);
    await waitFor(() => expect(fixMatchPreview).toHaveBeenCalledTimes(1));
    expect(screen.queryByText("Stale preview")).not.toBeInTheDocument();
  });

  it("locks the group list while a batch writes", async () => {
    const user = userEvent.setup();
    let finish: () => void = () => {};
    vi.mocked(fixMatch).mockImplementation(
      () =>
        new Promise((resolve) => {
          finish = () =>
            resolve({ id: "media-part-1", title: "The Outsider", year: 2020, favorited: false });
        }),
    );
    renderPage();
    await openedOnGroup();
    await screen.findByRole("button", { name: /Suggested/ });

    await user.click(screen.getByRole("button", { name: "Match all 3" }));
    await waitFor(() => expect(fixMatch).toHaveBeenCalledTimes(1));
    expect(screen.getByRole("button", { name: /The Thing.*1 item/ })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Clear queue" })).toBeDisabled();
    vi.mocked(fixMatch).mockResolvedValue({
      id: "media-part-2",
      title: "The Outsider",
      year: 2020,
      favorited: false,
    });
    finish();
    await waitFor(() =>
      expect(screen.getByRole("button", { name: /The Thing.*1 item/ })).toBeEnabled(),
    );
  });

  it("keeps the panel locked until the reload after a batch", async () => {
    const user = userEvent.setup();
    renderPage();
    await openedOnGroup();
    await screen.findByRole("button", { name: /Suggested/ });

    let reload: (value: ReturnType<typeof queue>) => void = () => {};
    vi.mocked(fetchUnmatchedQueuePages).mockReturnValueOnce(
      new Promise((resolve) => {
        reload = resolve;
      }),
    );
    await user.click(screen.getByRole("button", { name: "Match all 3" }));
    await waitFor(() => expect(fixMatch).toHaveBeenCalledTimes(3));
    await waitFor(() => expect(fetchUnmatchedQueuePages).toHaveBeenCalledTimes(2));
    expect(screen.getByRole("button", { name: "Match all 3" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Hide all 3" })).toBeDisabled();
    reload(queue([below]));
    await screen.findByRole("button", { name: "Apply + pin" });
    expect(fixMatch).toHaveBeenCalledTimes(3);
  });

  it("searches the folder's title ahead of the failed catalog guess", async () => {
    vi.mocked(fetchUnmatchedQueuePages).mockResolvedValue(
      queue([{ ...below, candidateTitle: "The Arrival" }]),
    );
    vi.mocked(fetchMediaFiles).mockResolvedValue([
      {
        mediaID: "media-thing",
        path: "/media/Arrival (2016)/file.mkv",
        contentFingerprint: "",
        versionLabel: "",
        partOrdinal: 0,
      },
    ]);
    renderPage();
    await waitFor(() =>
      expect(fixMatchCandidates).toHaveBeenCalledWith({
        mediaID: "media-thing",
        pluginID: "filmtv",
        scraperID: "tmdb",
        query: "Arrival",
        year: 2016,
      }),
    );
  });

  it("explains instead of searching for a source with no catalog", async () => {
    vi.mocked(fetchUnmatchedQueuePages).mockResolvedValue(
      queue([{ ...below, scraperID: "manual", candidateID: "", candidateTitle: "" }]),
    );
    renderPage();
    expect(await screen.findByText(/This source has no catalog to search/)).toBeVisible();
    expect(screen.queryByLabelText("Search title")).not.toBeInTheDocument();
    expect(fixMatchCandidates).not.toHaveBeenCalled();
    expect(screen.queryByText("Know the catalog ID?")).not.toBeInTheDocument();
  });

  it("says when the queue holds more rows than one load reads", async () => {
    vi.mocked(fetchUnmatchedQueuePages).mockResolvedValue(queue([tied(1), below], false));
    renderPage();
    expect(
      await screen.findByText("Showing the first 2 items. Fix or hide some to see the rest."),
    ).toBeVisible();
  });

  it("hides one item, or the whole group after a confirm", async () => {
    const user = userEvent.setup();
    renderPage();
    await openedOnGroup();

    await user.click(screen.getByRole("button", { name: "Hide: Part 2" }));
    expect(dismissUnmatched).toHaveBeenCalledWith("media-part-2");

    await user.click(await screen.findByRole("button", { name: "Hide all 3" }));
    const dialog = await screen.findByRole("alertdialog");
    expect(within(dialog).getByText(/nothing is deleted/)).toBeVisible();
    expect(dismissUnmatched).toHaveBeenCalledTimes(1);
    await user.click(within(dialog).getByRole("button", { name: "Hide all 3" }));
    await waitFor(() => expect(dismissUnmatched).toHaveBeenCalledTimes(4));
  });

  // A partially failed Clear queue reloads from the server: rows the server
  // already dismissed disappear, the row it refused stays, and the error is
  // named — local state is never trusted over the server's answer.
  it("reloads the queue from the server when Clear queue partially fails", async () => {
    const user = userEvent.setup();
    vi.mocked(dismissUnmatched).mockImplementation(async (mediaID) => {
      if (mediaID === "media-thing") throw new Error("refused");
      return true;
    });
    renderPage();
    await openedOnGroup();

    await user.click(screen.getByRole("button", { name: "Clear queue" }));
    const dialog = await screen.findByRole("alertdialog");
    expect(within(dialog).getByText(/4 items/)).toBeVisible();
    vi.mocked(fetchUnmatchedQueuePages).mockResolvedValue(queue([below]));
    await user.click(within(dialog).getByRole("button", { name: "Clear queue" }));

    await waitFor(() => expect(dismissUnmatched).toHaveBeenCalledTimes(4));
    expect(await screen.findByText("Could not load unmatched records.")).toBeVisible();
    await waitFor(() =>
      expect(screen.queryByRole("button", { name: /3 items/ })).not.toBeInTheDocument(),
    );
    expect(screen.getByRole("button", { name: "Apply + pin" })).toBeInTheDocument();
  });

  it("shows the empty state", async () => {
    vi.mocked(fetchUnmatchedQueuePages).mockResolvedValue(queue([]));
    renderPage();
    expect(await screen.findByText("No unmatched records.")).toBeVisible();
  });
});
