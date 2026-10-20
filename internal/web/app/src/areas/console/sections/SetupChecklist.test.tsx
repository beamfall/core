import "@testing-library/jest-dom/vitest";
import { cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { fetchJobs, fetchSetupState, fetchSourceItems, retryJob } from "@/lib/graphql";
import type { JobInfo, SetupState } from "@/lib/graphql/types";
import { I18nProvider } from "@/lib/i18n";
import { SetupChecklist } from "./SetupChecklist";

vi.mock("@/lib/graphql", () => ({
  fetchJobs: vi.fn(),
  fetchSetupState: vi.fn(),
  fetchSourceItems: vi.fn(),
  retryJob: vi.fn(),
}));

const complete: SetupState = {
  adminPasswordSet: true,
  hasLibrary: true,
  hasOwnerProfile: true,
  pluginsActivated: true,
  complete: true,
};

function renderChecklist() {
  return render(
    <I18nProvider>
      <MemoryRouter>
        <SetupChecklist />
      </MemoryRouter>
    </I18nProvider>,
  );
}

beforeEach(() => {
  localStorage.clear();
  vi.mocked(fetchJobs).mockReset().mockResolvedValue([]);
  vi.mocked(fetchSetupState).mockReset();
  vi.mocked(fetchSourceItems).mockReset();
  vi.mocked(retryJob)
    .mockReset()
    .mockResolvedValue({ id: "retry", status: "queued" } as JobInfo);
});

afterEach(cleanup);

describe("SetupChecklist (Seam B funnel spine)", () => {
  it("renders each spine item as a deep-link to its owning station", async () => {
    vi.mocked(fetchSetupState).mockResolvedValue(complete);
    vi.mocked(fetchSourceItems).mockResolvedValue([
      { id: "m1", kind: "media", title: "x", artworkSlots: [] },
    ] as never);

    renderChecklist();

    // Each station is reachable as a link — the spine deep-links, never a dead
    // list. (ADR-0080 / onboarding-funnel §5.)
    const scan = await screen.findByRole("link", { name: /start your first scan/i });
    expect(scan).toHaveAttribute("href", "/console/libraries");
    expect(screen.getByRole("link", { name: /migrate from another server/i })).toHaveAttribute(
      "href",
      "/console/remote",
    );
    expect(screen.getByRole("link", { name: /add a device or browser/i })).toHaveAttribute(
      "href",
      "/promote",
    );
    expect(screen.getByRole("link", { name: /create profiles/i })).toHaveAttribute(
      "href",
      "/console/access",
    );
  });

  it("declares first value only when setup is complete AND content is browsable", async () => {
    vi.mocked(fetchSetupState).mockResolvedValue(complete);
    vi.mocked(fetchSourceItems).mockResolvedValue([
      { id: "m1", kind: "media", title: "x", artworkSlots: [] },
    ] as never);

    renderChecklist();

    await waitFor(() => expect(screen.getByText(/ready to browse/i)).toBeInTheDocument());
  });

  it("keeps guiding forward when setup is green but nothing is browsable yet", async () => {
    vi.mocked(fetchSetupState).mockResolvedValue(complete);
    // Zero browsable items → not first value yet (funnel §2 definition of done).
    vi.mocked(fetchSourceItems).mockResolvedValue([]);

    renderChecklist();

    await waitFor(() =>
      expect(screen.getByText(/start a scan or migrate to bring in content/i)).toBeInTheDocument(),
    );
    expect(screen.queryByText(/ready to browse/i)).not.toBeInTheDocument();
  });

  it("offers resume, re-run, and discard for a partial migration job", async () => {
    vi.mocked(fetchSetupState).mockResolvedValue(complete);
    vi.mocked(fetchSourceItems).mockResolvedValue([]);
    vi.mocked(fetchJobs).mockResolvedValue([
      {
        id: "migration-1",
        kind: "migration",
        status: "canceled",
        progress: { current: 1847, total: 1912, unit: "items" },
      },
    ]);

    renderChecklist();

    expect(await screen.findByText(/migration paused/i)).toBeInTheDocument();
    expect(screen.getByText(/1847\/1912/)).toBeInTheDocument();

    await userEvent.click(screen.getByRole("button", { name: /^resume$/i }));
    expect(retryJob).toHaveBeenCalledWith("migration-1");

    await userEvent.click(screen.getByRole("button", { name: /re-run from start/i }));
    expect(retryJob).toHaveBeenCalledTimes(2);

    await userEvent.click(screen.getByRole("button", { name: /discard & finish later/i }));
    await waitFor(() => expect(screen.queryByText(/migration paused/i)).not.toBeInTheDocument());
    expect(screen.getByRole("link", { name: /migrate from another server/i })).toBeInTheDocument();
  });
});
