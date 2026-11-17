import "@testing-library/jest-dom/vitest";
import { render, screen, within } from "@testing-library/react";
import { MemoryRouter } from "react-router";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { fetchLibraryHealth } from "@/lib/graphql";
import type { LibraryHealthInfo } from "@/lib/graphql/types";
import { I18nProvider } from "@/lib/i18n";
import { Health } from "./Health";

vi.mock("@/lib/graphql", () => ({
  fetchDuplicateGroups: vi.fn(),
  fetchHealthDrilldown: vi.fn(),
  fetchLibraryHealth: vi.fn(),
  runHealthBulkOp: vi.fn(),
  runHealthBulkOpSelected: vi.fn(),
}));

function renderHealth(rows: LibraryHealthInfo[]) {
  vi.mocked(fetchLibraryHealth).mockResolvedValue(rows);
  return render(
    <I18nProvider>
      <MemoryRouter>
        <Health />
      </MemoryRouter>
    </I18nProvider>,
  );
}

async function libraryRow(title: string): Promise<HTMLElement> {
  const cell = await screen.findByText(title);
  const row = cell.closest("tr");
  if (!row) throw new Error(`no table row for ${title}`);
  return row;
}

// CORE-2340, CORE-2383: counts follow the Library's plugin. The server omits a
// dimension that does not apply; the dashboard shows it as not applicable and
// offers no remediation for it.
describe("Console Library health dimensions", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("shows absent dimensions as not applicable and names a Library no plugin identifies", async () => {
    renderHealth([
      {
        libraryID: "library-plain",
        title: "Home videos",
        counts: { broken: 0, duplicate: 0, "no-scraper": 3 },
      },
    ]);
    const row = await libraryRow("Home videos");
    expect(within(row).getByText("No plugin identifies this library")).toBeInTheDocument();
    expect(within(row).getAllByText("Not applicable")).toHaveLength(3);
    expect(within(row).getByRole("button", { name: "No scraper: 3" })).toBeInTheDocument();
    expect(within(row).queryByRole("link", { name: "Fix match" })).not.toBeInTheDocument();
    expect(within(row).queryByRole("button", { name: "Rescan unmatched" })).not.toBeInTheDocument();
    expect(
      within(row).queryByRole("button", { name: "Rescrape failures" }),
    ).not.toBeInTheDocument();
    expect(
      within(row).queryByRole("button", { name: "Fetch missing artwork" }),
    ).not.toBeInTheDocument();
  });

  it("offers every remediation when the plugin auto-identifies", async () => {
    renderHealth([
      {
        libraryID: "library-scraped",
        title: "Films",
        counts: {
          unmatched: 2,
          broken: 0,
          "scrape-failure": 1,
          "missing-artwork": 4,
          duplicate: 0,
        },
      },
    ]);
    const row = await libraryRow("Films");
    expect(within(row).queryByText("No plugin identifies this library")).not.toBeInTheDocument();
    expect(within(row).queryByText("Not applicable")).toBeInTheDocument();
    expect(within(row).getByRole("link", { name: "Fix match" })).toBeInTheDocument();
    expect(within(row).getByRole("button", { name: "Rescan unmatched" })).toBeEnabled();
    expect(within(row).getByRole("button", { name: "Rescrape failures" })).toBeEnabled();
    expect(within(row).getByRole("button", { name: "Fetch missing artwork" })).toBeEnabled();
  });

  it("disables rescans with a reason when the plugin is searchable but never auto-identifies", async () => {
    renderHealth([
      {
        libraryID: "library-manual",
        title: "Tapes",
        counts: { unmatched: 2, broken: 0, "missing-artwork": 0, duplicate: 0 },
      },
    ]);
    const row = await libraryRow("Tapes");
    expect(within(row).getByRole("link", { name: "Fix match" })).toBeInTheDocument();
    const rescan = within(row).getByRole("button", { name: "Rescan unmatched" });
    expect(rescan).toBeDisabled();
    expect(rescan).toHaveAttribute("title", "Rescans need a plugin that identifies automatically");
  });

  it("sums only the Libraries a dimension applies to", async () => {
    renderHealth([
      {
        libraryID: "library-plain",
        title: "Home videos",
        counts: { broken: 1, duplicate: 0, "no-scraper": 3 },
      },
      {
        libraryID: "library-other",
        title: "Clips",
        counts: { broken: 2, duplicate: 0, "no-scraper": 1 },
      },
    ]);
    const aggregate = await libraryRow("All libraries");
    expect(within(aggregate).getAllByText("Not applicable")).toHaveLength(3);
    expect(within(aggregate).getByText("3")).toBeInTheDocument();
    expect(within(aggregate).getByText("4")).toBeInTheDocument();
  });
});
