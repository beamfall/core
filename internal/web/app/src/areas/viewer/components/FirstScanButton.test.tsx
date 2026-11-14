import "@testing-library/jest-dom/vitest";
import { cleanup, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes } from "react-router";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { resetRescansForTest } from "@/components/beamfall/LibraryRescan";
import { browseServerFolders, fetchJobs, fetchLibraries, scanLibrary } from "@/lib/graphql";
import { I18nProvider } from "@/lib/i18n";
import { FirstScanButton } from "./FirstScanButton";

vi.mock("@/lib/graphql", () => ({
  browseServerFolders: vi.fn(),
  fetchJobs: vi.fn(),
  fetchLibraries: vi.fn(),
  scanLibrary: vi.fn(),
}));

function renderButton() {
  return render(
    <I18nProvider>
      <MemoryRouter initialEntries={["/"]}>
        <Routes>
          <Route path="/" element={<FirstScanButton />} />
          <Route path="/console/libraries" element={<p>libraries-page</p>} />
        </Routes>
      </MemoryRouter>
    </I18nProvider>,
  );
}

const films = { id: "lib-1", title: "Films", roots: ["/media/films"] };

beforeEach(() => {
  vi.clearAllMocks();
  resetRescansForTest();
  vi.mocked(fetchJobs).mockResolvedValue([]);
  vi.mocked(scanLibrary).mockResolvedValue({ id: "job-1", status: "queued" });
  vi.mocked(browseServerFolders).mockResolvedValue({ name: "films" } as never);
});

afterEach(cleanup);

describe("FirstScanButton (CORE-1606)", () => {
  it("starts a scan of the existing Library and shows progress", async () => {
    vi.mocked(fetchLibraries).mockResolvedValue([films] as never);
    renderButton();

    await userEvent.click(screen.getByRole("button", { name: "Start first scan" }));

    expect(await screen.findByRole("status")).toHaveTextContent("Scanning");
    expect(scanLibrary).toHaveBeenCalledWith("lib-1", "/media/films");
  });

  it("names a missing folder with a fix link instead of scanning", async () => {
    vi.mocked(fetchLibraries).mockResolvedValue([films] as never);
    vi.mocked(browseServerFolders).mockRejectedValue(new Error("folder does not exist"));
    renderButton();

    await userEvent.click(screen.getByRole("button", { name: "Start first scan" }));

    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Can't scan /media/films: folder not found.",
    );
    expect(screen.getByRole("link", { name: "Fix the folder" })).toHaveAttribute(
      "href",
      "/console/libraries",
    );
    expect(scanLibrary).not.toHaveBeenCalled();
  });

  it("opens Console Libraries when there is nothing to scan yet", async () => {
    vi.mocked(fetchLibraries).mockResolvedValue([]);
    renderButton();

    await userEvent.click(screen.getByRole("button", { name: "Start first scan" }));

    expect(await screen.findByText("libraries-page")).toBeInTheDocument();
  });
});
