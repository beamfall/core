import "@testing-library/jest-dom/vitest";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import {
  dismissUnmatched,
  fetchLibraries,
  fetchUnmatchedQueue,
  fetchUnmatchedRecords,
  fetchUnmatchedSuppressions,
  fixMatch,
  fixMatchCandidates,
  fixMatchPreview,
  identifyLookup,
  runHealthBulkOp,
} from "@/lib/graphql";
import { I18nProvider } from "@/lib/i18n";
import { UnmatchedPage } from "./UnmatchedPage";

vi.mock("@/lib/graphql", () => ({
  fetchUnmatchedQueue: vi.fn(),
  fetchLibraries: vi.fn(),
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

const queueItem = {
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

const secondItem = {
  ...queueItem,
  mediaID: "media-second",
  title: "Second feature",
  candidateTitle: "",
  failureReason: "",
};

beforeEach(() => {
  vi.clearAllMocks();
  vi.mocked(fetchUnmatchedQueue).mockResolvedValue([queueItem]);
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
  vi.mocked(dismissUnmatched).mockResolvedValue(true);
  vi.mocked(runHealthBulkOp).mockResolvedValue({ affected: 1 });
  vi.mocked(fixMatchCandidates).mockResolvedValue([
    {
      id: "candidate-1982",
      title: "The Thing",
      year: 1982,
      pluginID: "filmtv",
      scraperID: "tmdb",
      confidence: 0.92,
      threshold: 0.8,
      scored: true,
    },
    {
      id: "candidate-2011",
      title: "The Thing",
      year: 2011,
      pluginID: "filmtv",
      scraperID: "tmdb",
      confidence: 0.41,
      threshold: 0.8,
      scored: true,
      reason: "below-threshold",
    },
  ]);
  vi.mocked(fixMatchPreview).mockResolvedValue([
    { field: "title", current: "The Thing 2011", proposed: "The Thing · 1982 cut", changed: true },
    { field: "summary", current: "", proposed: "", changed: false },
  ]);
  vi.mocked(identifyLookup).mockResolvedValue({ id: "tt0084787", title: "The Thing", year: 1982 });
  vi.mocked(fixMatch).mockResolvedValue({
    id: "media-thing",
    title: "The Thing",
    year: 1982,
    favorited: false,
  });
  vi.mocked(fetchUnmatchedRecords).mockResolvedValue([]);
  vi.mocked(fetchUnmatchedSuppressions).mockResolvedValue([]);
});

function renderPage() {
  return render(
    <I18nProvider>
      <UnmatchedPage />
    </I18nProvider>,
  );
}

describe("UnmatchedPage", () => {
  // METAPARITY-4 (ADR-0068 §A): the failure-reason taxonomy on the row,
  // per-candidate confidence + source attribution, a preview that writes
  // nothing, then apply.
  it("shows the failure reason, scores candidates, previews, then applies", async () => {
    const user = userEvent.setup();
    renderPage();

    expect(await screen.findAllByText("The Thing 2011")).toHaveLength(2);
    expect(screen.getAllByText("Best candidate below the threshold").length).toBeGreaterThan(0);

    await user.clear(screen.getByLabelText("Search title"));
    await user.type(screen.getByLabelText("Search title"), "The Thing 1982");
    await user.click(screen.getByRole("button", { name: "Search" }));

    expect(fixMatchCandidates).toHaveBeenCalledWith({
      mediaID: "media-thing",
      pluginID: "filmtv",
      scraperID: "tmdb",
      query: "The Thing 1982",
    });
    // Per-candidate confidence and source attribution are both rendered.
    expect(await screen.findByText("92%")).toBeVisible();
    expect(screen.getAllByText(/From filmtv\/tmdb/)).toHaveLength(2);
    expect(screen.getByText(/92% confidence · threshold 80%/)).toBeVisible();

    // Preview writes nothing and names current → proposed per field.
    await user.click(screen.getByRole("button", { name: "Preview changes" }));
    expect(await screen.findByText("The Thing · 1982 cut")).toBeVisible();
    expect(fixMatchPreview).toHaveBeenCalledWith({
      mediaID: "media-thing",
      pluginID: "filmtv",
      scraperID: "tmdb",
      candidateID: "candidate-1982",
    });
    expect(screen.getByText("No change")).toBeVisible();
    expect(fixMatch).not.toHaveBeenCalled();

    await user.click(screen.getByRole("button", { name: "Apply + pin" }));
    expect(fixMatch).toHaveBeenCalledWith({
      mediaID: "media-thing",
      pluginID: "filmtv",
      scraperID: "tmdb",
      candidateID: "candidate-1982",
    });
    await waitFor(() => expect(screen.queryByText("The Thing 2011")).not.toBeInTheDocument());
  });

  // METAPARITY-4 paste-an-id: the operator who already knows the provider ID
  // skips the search, and the fetched entry is presented as UNSCORED — nothing
  // was compared, so a 0% confidence would be a lie.
  it("fetches a pasted provider ID as an unscored candidate", async () => {
    const user = userEvent.setup();
    renderPage();

    expect(await screen.findAllByText("The Thing 2011")).toHaveLength(2);
    await user.type(screen.getByLabelText("Paste a provider ID"), "tt0084787");
    await user.click(screen.getByRole("button", { name: "Fetch by ID" }));

    expect(identifyLookup).toHaveBeenCalledWith({
      mediaID: "media-thing",
      pluginID: "filmtv",
      scraperID: "tmdb",
      candidateID: "tt0084787",
    });
    expect(await screen.findByText("Not scored")).toBeVisible();
    expect(screen.getByText("This plugin does not score matches")).toBeVisible();
  });

  it("clears visible queue rows through dismissUnmatched after a confirm", async () => {
    const user = userEvent.setup();
    renderPage();

    expect(await screen.findAllByText("The Thing 2011")).toHaveLength(2);
    await user.click(screen.getByRole("button", { name: "Clear queue" }));
    // Nothing is dismissed until the dialog is confirmed.
    expect(dismissUnmatched).not.toHaveBeenCalled();
    const dialog = await screen.findByRole("alertdialog");
    expect(within(dialog).getByText("1 open")).toBeVisible();

    vi.mocked(fetchUnmatchedQueue).mockResolvedValue([]);
    await user.click(within(dialog).getByRole("button", { name: "Clear queue" }));

    expect(dismissUnmatched).toHaveBeenCalledWith("media-thing");
    await waitFor(() => expect(screen.getByText("No unmatched records.")).toBeVisible());
  });

  // A partially failed Clear queue reloads from the server: rows the server
  // already dismissed disappear, the row it refused stays, and the error is
  // named — local state is never trusted over the server's answer.
  it("reloads the queue from the server when Clear queue partially fails", async () => {
    const user = userEvent.setup();
    vi.mocked(fetchUnmatchedQueue).mockResolvedValue([queueItem, secondItem]);
    vi.mocked(dismissUnmatched).mockImplementation(async (mediaID) => {
      if (mediaID === "media-second") throw new Error("refused");
      return true;
    });
    renderPage();
    await screen.findByText("Second feature");

    await user.click(screen.getByRole("button", { name: "Clear queue" }));
    const dialog = await screen.findByRole("alertdialog");
    vi.mocked(fetchUnmatchedQueue).mockResolvedValue([secondItem]);
    await user.click(within(dialog).getByRole("button", { name: "Clear queue" }));

    await waitFor(() => expect(fetchUnmatchedQueue).toHaveBeenCalledTimes(2));
    await waitFor(() => expect(screen.queryByText("The Thing 2011")).not.toBeInTheDocument());
    expect(screen.getAllByText("Second feature").length).toBeGreaterThan(0);
    expect(screen.getByText("Could not load unmatched records.")).toBeVisible();
  });

  // Switching rows remounts the editor: a preview held for row A must never
  // be applied to row B. Apply stays disabled for B until B has its own
  // candidate, and the write then carries B's id with B's candidate.
  it("never applies row A's previewed candidate to row B", async () => {
    const user = userEvent.setup();
    vi.mocked(fetchUnmatchedQueue).mockResolvedValue([queueItem, secondItem]);
    renderPage();
    await screen.findByText("Second feature");

    await user.click(screen.getByRole("button", { name: "Search" }));
    await screen.findByText("92%");
    await user.click(screen.getByRole("button", { name: "Preview changes" }));
    await screen.findByText("The Thing · 1982 cut");

    await user.click(screen.getByText("Second feature").closest("button")!);
    expect(screen.queryByText("The Thing · 1982 cut")).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Apply + pin" })).toBeDisabled();
    expect(screen.getByLabelText("Search title")).toHaveValue("Second feature");

    vi.mocked(fixMatchCandidates).mockResolvedValue([
      {
        id: "candidate-second",
        title: "Second feature",
        year: 1999,
        pluginID: "filmtv",
        scraperID: "tmdb",
        confidence: 0.9,
        threshold: 0.8,
        scored: true,
      },
    ]);
    await user.click(screen.getByRole("button", { name: "Search" }));
    await screen.findByText("90%");
    await user.click(screen.getByRole("button", { name: "Apply + pin" }));

    await waitFor(() =>
      expect(fixMatch).toHaveBeenCalledWith({
        mediaID: "media-second",
        pluginID: "filmtv",
        scraperID: "tmdb",
        candidateID: "candidate-second",
      }),
    );
    expect(fixMatch).toHaveBeenCalledTimes(1);
  });
});
