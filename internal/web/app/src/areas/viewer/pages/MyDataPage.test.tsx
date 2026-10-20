import type { ReactNode } from "react";
import "@testing-library/jest-dom/vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import {
  deleteMyEngagement,
  fetchMyDataSummary,
  fetchMyEngagement,
  fetchMyEngagementLog,
  fetchMySessions,
} from "@/lib/graphql";
import { I18nProvider } from "@/lib/i18n";
import { MyDataPage } from "./MyDataPage";

vi.mock("@/components/chrome/AppShell", () => ({
  AppShell: ({ children }: { children: ReactNode }) => <>{children}</>,
}));
vi.mock("../components/ViewerActions", () => ({ ViewerActions: () => null }));
vi.mock("@/lib/graphql", () => ({
  fetchMySessions: vi.fn(),
  fetchMyEngagement: vi.fn(),
  deleteMyEngagement: vi.fn(),
  fetchMyDataSummary: vi.fn(),
  fetchMyEngagementLog: vi.fn(),
}));

beforeEach(() => {
  vi.mocked(fetchMySessions).mockReset().mockResolvedValue({
    sessions: [],
    streamTokens: [],
  });
  vi.mocked(fetchMyEngagement).mockReset().mockResolvedValue({
    schema: "v1",
    exportedAt: "2026-06-28T00:00:00Z",
    playbackProgress: [],
    favorites: [],
    queue: [],
  });
  vi.mocked(fetchMyDataSummary).mockReset().mockResolvedValue({
    plays: 0,
    watched: 0,
    favorites: 0,
    queueEntries: 0,
    libraryCount: 0,
    libraryBreakdown: [],
  });
  vi.mocked(fetchMyEngagementLog).mockReset().mockResolvedValue({
    total: 0,
    events: [],
  });
  vi.mocked(deleteMyEngagement).mockReset().mockResolvedValue({ scope: "all" });
  vi.stubGlobal(
    "confirm",
    vi.fn(() => true),
  );
  vi.stubGlobal(
    "prompt",
    vi.fn(() => "1234"),
  );
});

describe("MyDataPage delete", () => {
  it("retries deleteMyEngagement with the Reveal PIN when required", async () => {
    vi.mocked(deleteMyEngagement)
      .mockRejectedValueOnce(new Error("reveal pin required"))
      .mockResolvedValueOnce({ scope: "all" });

    render(
      <I18nProvider>
        <MyDataPage />
      </I18nProvider>,
    );

    const user = userEvent.setup();
    await user.click(await screen.findByRole("button", { name: "Delete all my engagement" }));

    await waitFor(() => expect(deleteMyEngagement).toHaveBeenNthCalledWith(1, "all"));
    expect(prompt).toHaveBeenCalledWith("Enter your Reveal PIN to confirm deletion.");
    expect(deleteMyEngagement).toHaveBeenNthCalledWith(2, "all", "1234");
    expect(await screen.findByText("Your engagement data was deleted.")).toBeInTheDocument();
  });
});
