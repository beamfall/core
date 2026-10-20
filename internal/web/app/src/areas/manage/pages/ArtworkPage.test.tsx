import "@testing-library/jest-dom/vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { fetchArtworkCandidates } from "@/lib/graphql";
import { I18nProvider } from "@/lib/i18n";
import { ArtworkPage } from "./ArtworkPage";

vi.mock("@/lib/graphql", () => ({
  fetchArtworkCandidates: vi.fn(),
  pinArtworkCandidate: vi.fn(),
}));

describe("ArtworkPage", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.mocked(fetchArtworkCandidates).mockResolvedValue([
      {
        id: "candidate-1",
        slot: "poster",
        previewURL: "/api/media/artwork-candidate/1",
        applied: false,
      },
    ]);
  });

  it("uses native layout semantics while retaining candidate actions", async () => {
    const user = userEvent.setup();
    render(
      <I18nProvider>
        <ArtworkPage />
      </I18nProvider>,
    );
    await user.type(screen.getByLabelText("Media ID"), "media-1");
    await user.click(screen.getByRole("button", { name: "Load" }));

    expect(await screen.findAllByRole("button", { name: /Pin/ })).toHaveLength(3);
    expect(screen.queryByRole("grid")).not.toBeInTheDocument();
    expect(screen.queryByRole("gridcell")).not.toBeInTheDocument();
  });
});
