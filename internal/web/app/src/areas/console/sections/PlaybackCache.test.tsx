import "@testing-library/jest-dom/vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import {
  evictPlaybackRenditionCache,
  fetchPlaybackCache,
  precomputePlaybackRenditions,
} from "@/lib/graphql";
import { I18nProvider } from "@/lib/i18n";
import { PlaybackCache } from "./PlaybackCache";

vi.mock("@/lib/graphql", () => ({
  fetchPlaybackCache: vi.fn(),
  precomputePlaybackRenditions: vi.fn(),
  evictPlaybackRenditionCache: vi.fn(),
}));

const configured = {
  configured: true,
  ready: 128,
  generating: 6,
  failed: 2,
  evicted: 41,
  readyBytes: 18_400_000_000,
  budgetBytes: 32_000_000_000,
};

beforeEach(() => {
  vi.mocked(fetchPlaybackCache).mockReset();
  vi.mocked(precomputePlaybackRenditions).mockReset();
  vi.mocked(evictPlaybackRenditionCache).mockReset();
  vi.mocked(fetchPlaybackCache).mockResolvedValue(configured);
  vi.mocked(precomputePlaybackRenditions).mockResolvedValue({ affected: 3 });
  vi.mocked(evictPlaybackRenditionCache).mockResolvedValue({ affected: 5 });
});

function renderPanel() {
  return render(
    <I18nProvider>
      <PlaybackCache />
    </I18nProvider>,
  );
}

describe("PlaybackCache", () => {
  it("renders the health snapshot from the cache query", async () => {
    renderPanel();

    expect(await screen.findByText("Configured")).toBeVisible();
    expect(screen.getByText("128")).toBeVisible();
    expect(screen.getByText("Ready")).toBeVisible();
    expect(screen.getByText("17.1 GB / 29.8 GB")).toBeVisible();
    expect(fetchPlaybackCache).toHaveBeenCalled();
  });

  it("flags the over-budget state when ready disk exceeds the budget", async () => {
    vi.mocked(fetchPlaybackCache).mockResolvedValue({
      ...configured,
      readyBytes: 37_000_000_000,
    });
    renderPanel();

    expect(await screen.findByText("Over budget")).toBeVisible();
  });

  it("renders the unconfigured state when no cache root is wired", async () => {
    vi.mocked(fetchPlaybackCache).mockResolvedValue({
      configured: false,
      ready: 0,
      generating: 0,
      failed: 0,
      evicted: 0,
      readyBytes: 0,
      budgetBytes: 0,
    });
    renderPanel();

    expect(
      await screen.findByText("The playback rendition cache is not configured."),
    ).toBeVisible();
    expect(screen.queryByText("Precompute")).toBeNull();
  });

  it("renders the unavailable state when the query fails", async () => {
    vi.mocked(fetchPlaybackCache).mockRejectedValue(new Error("boom"));
    renderPanel();

    expect(
      await screen.findByText("The playback rendition cache is unavailable right now."),
    ).toBeVisible();
  });

  it("precomputes renditions for the entered Media ID", async () => {
    const user = userEvent.setup();
    renderPanel();
    await screen.findByText("Configured");

    await user.type(screen.getByLabelText("Media ID"), "media-7f3a");
    await user.click(screen.getByRole("button", { name: "Precompute" }));

    expect(precomputePlaybackRenditions).toHaveBeenCalledWith("media-7f3a");
    await waitFor(() => expect(fetchPlaybackCache).toHaveBeenCalledTimes(2));
  });

  it("evicts over budget through the confirm dialog", async () => {
    const user = userEvent.setup();
    renderPanel();
    await screen.findByText("Configured");

    await user.click(screen.getByRole("button", { name: "Evict over budget" }));
    await user.click(
      await screen.findByRole("button", {
        name: "Evict over budget",
        hidden: false,
      }),
    );

    await waitFor(() => expect(evictPlaybackRenditionCache).toHaveBeenCalled());
  });
});
