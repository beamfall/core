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

// A completed scan's progress counts the files it brought in; the default
// stands for a scan that found some.
function scanJob(status: string, files = 3): JobInfo {
  return {
    id: `scan-${status}-${files}`,
    kind: "scan",
    status,
    progress: { current: files, total: files, unit: "files" },
  } as JobInfo;
}

const oneItem = [{ id: "m1", kind: "media", title: "x", artworkSlots: [] }] as never;

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
  it("never asks for a next step beside the all-set footer", async () => {
    vi.mocked(fetchSetupState).mockResolvedValue(complete);
    vi.mocked(fetchSourceItems).mockResolvedValue([
      { id: "m1", kind: "media", title: "x", artworkSlots: [] },
    ] as never);
    vi.mocked(fetchJobs).mockResolvedValue([scanJob("completed")]);

    renderChecklist();

    expect(await screen.findByText(/you're all set/i)).toBeInTheDocument();
    // Migrating and adding a device are optional, not outstanding work.
    expect(screen.getByRole("link", { name: /migrate from another server/i })).toHaveTextContent(
      "Optional",
    );
    expect(screen.getByRole("link", { name: /add a device or browser/i })).toHaveTextContent(
      "Optional",
    );
    expect(screen.queryByText("Next step")).toBeNull();
    expect(screen.queryByText(/leaks no secret/i)).toBeNull();
  });

  it("marks only the unfinished required steps as the next step", async () => {
    vi.mocked(fetchSetupState).mockResolvedValue({
      ...complete,
      hasLibrary: false,
      complete: false,
    });
    vi.mocked(fetchSourceItems).mockResolvedValue([]);

    renderChecklist();

    expect(await screen.findByRole("link", { name: /start your first scan/i })).toHaveTextContent(
      "Next step",
    );
    expect(screen.getAllByText("Next step")).toHaveLength(1);
    expect(screen.getAllByText("Optional")).toHaveLength(2);
  });

  it("keeps the first scan open while a Library exists but nothing was found (CORE-1606)", async () => {
    vi.mocked(fetchSetupState).mockResolvedValue(complete);
    vi.mocked(fetchSourceItems).mockResolvedValue([]);

    renderChecklist();

    const scan = await screen.findByRole("link", { name: /start your first scan/i });
    await waitFor(() => expect(scan).toHaveTextContent("Next step"));
    expect(scan).not.toHaveTextContent("Done");
  });

  it.each(["failed", "canceled"])(
    "keeps the first scan open when a %s scan added items but none completed (CORE-1606)",
    async (status) => {
      vi.mocked(fetchSetupState).mockResolvedValue(complete);
      vi.mocked(fetchSourceItems).mockResolvedValue([
        { id: "m1", kind: "media", title: "x", artworkSlots: [] },
      ] as never);
      vi.mocked(fetchJobs).mockResolvedValue([scanJob(status), scanJob("running")]);

      renderChecklist();

      const scan = await screen.findByRole("link", { name: /start your first scan/i });
      await waitFor(() => expect(scan).toHaveTextContent("Next step"));
      expect(scan).not.toHaveTextContent("Done");
      expect(screen.queryByText(/you're all set/i)).toBeNull();
    },
  );

  it("marks the first scan Done once a scan completed with items in the catalog", async () => {
    vi.mocked(fetchSetupState).mockResolvedValue(complete);
    vi.mocked(fetchSourceItems).mockResolvedValue([
      { id: "m1", kind: "media", title: "x", artworkSlots: [] },
    ] as never);
    vi.mocked(fetchJobs).mockResolvedValue([scanJob("failed"), scanJob("completed")]);

    renderChecklist();

    const scan = await screen.findByRole("link", { name: /start your first scan/i });
    await waitFor(() => expect(scan).toHaveTextContent("Done"));
  });

  it("keeps the first scan open when a completed scan found nothing and a later failed scan left items (CORE-1606)", async () => {
    vi.mocked(fetchSetupState).mockResolvedValue(complete);
    vi.mocked(fetchSourceItems).mockResolvedValue(oneItem);
    vi.mocked(fetchJobs).mockResolvedValue([scanJob("failed"), scanJob("completed", 0)]);

    renderChecklist();

    const scan = await screen.findByRole("link", { name: /start your first scan/i });
    await waitFor(() => expect(vi.mocked(fetchJobs)).toHaveBeenCalled());
    await waitFor(() => expect(scan).toHaveTextContent("Next step"));
    expect(scan).not.toHaveTextContent("Done");
    expect(screen.queryByText(/you're all set/i)).toBeNull();
  });

  it("marks the first scan Done when content came in with no scan on record (restore or import)", async () => {
    vi.mocked(fetchSetupState).mockResolvedValue(complete);
    vi.mocked(fetchSourceItems).mockResolvedValue(oneItem);
    vi.mocked(fetchJobs).mockResolvedValue([
      {
        id: "migration-1",
        kind: "migration",
        status: "completed",
        progress: { current: 12, total: 12, unit: "items" },
      },
    ]);

    renderChecklist();

    const scan = await screen.findByRole("link", { name: /start your first scan/i });
    await waitFor(() => expect(scan).toHaveTextContent("Done"));
    expect(screen.getByText(/you're all set/i)).toBeInTheDocument();
  });

  it("does not mark the first scan Done while the job list cannot load", async () => {
    vi.mocked(fetchSetupState).mockResolvedValue(complete);
    vi.mocked(fetchSourceItems).mockResolvedValue(oneItem);
    vi.mocked(fetchJobs).mockRejectedValue(new Error("offline"));

    renderChecklist();

    const scan = await screen.findByRole("link", { name: /start your first scan/i });
    await waitFor(() => expect(vi.mocked(fetchJobs)).toHaveBeenCalled());
    await waitFor(() => expect(scan).toHaveTextContent("Next step"));
  });

  it("Refresh reloads the scan jobs, so a scan that finished after the page opened shows Done (CORE-1606)", async () => {
    vi.mocked(fetchSetupState).mockResolvedValue(complete);
    vi.mocked(fetchSourceItems).mockResolvedValue(oneItem);
    vi.mocked(fetchJobs).mockResolvedValueOnce([scanJob("running", 0)]);

    renderChecklist();

    const scan = await screen.findByRole("link", { name: /start your first scan/i });
    await waitFor(() => expect(scan).toHaveTextContent("Next step"));

    vi.mocked(fetchJobs).mockResolvedValue([scanJob("completed")]);
    await userEvent.click(screen.getByRole("button", { name: /refresh/i }));

    // The list re-renders while setup state reloads, so look the step up again.
    await waitFor(() =>
      expect(screen.getByRole("link", { name: /start your first scan/i })).toHaveTextContent(
        "Done",
      ),
    );
    expect(screen.getByText(/you're all set/i)).toBeInTheDocument();
  });

  it("does not number some steps and check others", async () => {
    vi.mocked(fetchSetupState).mockResolvedValue({ ...complete, hasOwnerProfile: false });
    vi.mocked(fetchSourceItems).mockResolvedValue([]);

    const { container } = renderChecklist();

    await screen.findByRole("link", { name: /start your first scan/i });
    for (const index of container.querySelectorAll(".console-step-index")) {
      expect(index.textContent).toBe("");
    }
  });

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
      "/console/migrate",
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
    vi.mocked(fetchJobs).mockResolvedValue([scanJob("completed")]);

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
