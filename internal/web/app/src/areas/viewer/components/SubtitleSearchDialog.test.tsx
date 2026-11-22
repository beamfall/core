import "@testing-library/jest-dom/vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ApiError } from "@/lib/api";
import { I18nProvider } from "@/lib/i18n";
import {
  downloadSubtitle,
  type SubtitleSearchResult,
  searchSubtitles,
} from "../lib/subtitleSearch";
import { SubtitleSearchDialog } from "./SubtitleSearchDialog";

vi.mock("../lib/subtitleSearch", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../lib/subtitleSearch")>()),
  searchSubtitles: vi.fn(),
  downloadSubtitle: vi.fn(),
}));

const result: SubtitleSearchResult = {
  provider: "opensubtitles",
  fileId: "42",
  language: "fr",
  release: "Test.Tone.1080p",
  rating: 8.5,
  downloads: 100,
  hashMatch: true,
  sdh: false,
  forced: false,
};

function renderDialog(onDownloaded = vi.fn()) {
  render(
    <I18nProvider initialLocale="en">
      <SubtitleSearchDialog
        mediaID="m-1"
        title="Test tone"
        onClose={() => {}}
        onDownloaded={onDownloaded}
      />
    </I18nProvider>,
  );
  return onDownloaded;
}

beforeEach(() => {
  vi.mocked(searchSubtitles).mockResolvedValue([result]);
  vi.mocked(downloadSubtitle).mockResolvedValue({ file: "tone.fr.srt", language: "fr" });
});

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

describe("SubtitleSearchDialog", () => {
  it("searches the typed languages and lists language, rating, hash match and source", async () => {
    renderDialog();
    fireEvent.change(screen.getByRole("textbox", { name: /^Languages/ }), { target: { value: "fr, pt-BR" } });
    fireEvent.click(screen.getByRole("button", { name: "Search" }));
    expect(await screen.findByText("French")).toBeInTheDocument();
    expect(searchSubtitles).toHaveBeenCalledWith("m-1", ["fr", "pt-BR"]);
    expect(screen.getByText("Test.Tone.1080p")).toBeInTheDocument();
    expect(
      screen.getByText("Exact file match · Rating 8.5 · From opensubtitles"),
    ).toBeInTheDocument();
  });

  it("downloads a result and tells the caller to refresh its tracks", async () => {
    const onDownloaded = renderDialog();
    fireEvent.click(screen.getByRole("button", { name: "Search" }));
    fireEvent.click(await screen.findByRole("button", { name: "Download" }));
    expect(await screen.findByText("Added French subtitles.")).toBeInTheDocument();
    expect(downloadSubtitle).toHaveBeenCalledWith("m-1", result);
    expect(onDownloaded).toHaveBeenCalledTimes(1);
  });

  it("explains a missing provider key", async () => {
    vi.mocked(searchSubtitles).mockRejectedValue(
      new ApiError("POST failed", 409, { code: "NO_KEY" }),
    );
    renderDialog();
    fireEvent.click(screen.getByRole("button", { name: "Search" }));
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Online subtitle search needs a provider API key on the server.",
    );
  });

  it("says when nothing was found and when a download is refused", async () => {
    vi.mocked(searchSubtitles).mockResolvedValueOnce([]);
    renderDialog();
    fireEvent.click(screen.getByRole("button", { name: "Search" }));
    expect(await screen.findByText("No subtitles found.")).toBeInTheDocument();
    vi.mocked(downloadSubtitle).mockRejectedValue(new ApiError("POST failed", 409, "read-only"));
    fireEvent.click(screen.getByRole("button", { name: "Search" }));
    fireEvent.click(await screen.findByRole("button", { name: "Download" }));
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "This library can't store new subtitle files.",
    );
  });
});
