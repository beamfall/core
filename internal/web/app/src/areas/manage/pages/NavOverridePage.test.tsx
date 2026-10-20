import "@testing-library/jest-dom/vitest";
import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import {
  fetchManifest,
  fetchNavOverride,
  fetchNavOverrideHistory,
  resetNavOverride,
  restoreNavOverride,
  setNavOverride,
} from "@/lib/graphql";
import type {
  NavigationManifest,
  NavOverrideHistoryInfo,
  NavOverrideInfo,
} from "@/lib/graphql/types";
import { I18nProvider } from "@/lib/i18n";
import { NavOverridePage } from "./NavOverridePage";

vi.mock("@/lib/graphql", () => ({
  fetchManifest: vi.fn(),
  fetchNavOverride: vi.fn(),
  fetchNavOverrideHistory: vi.fn(),
  setNavOverride: vi.fn(),
  resetNavOverride: vi.fn(),
  restoreNavOverride: vi.fn(),
}));

const manifest = {
  profileID: "p1",
  revealed: false,
  generatedAt: "2026-06-19T00:00:00Z",
  libraries: [],
  navigation: [
    { id: "home", title: { en: "Home" }, view: "grid", layoutID: "browse" },
    { id: "ambient", title: { en: "Ambient" }, view: "grid", layoutID: "browse" },
    { id: "search", title: "Search", view: "search", layoutID: "browse" },
  ],
  layouts: [
    {
      id: "browse",
      sections: [
        { id: "continue", kind: "row", title: { en: "Continue" }, artworkSlot: "" },
        { id: "recent", kind: "row", title: { en: "Recently added" }, artworkSlot: "" },
      ],
    },
  ],
  theme: { accentColor: "#2F7DFF" },
} as unknown as NavigationManifest;

const emptyOverride: NavOverrideInfo = {
  hiddenNodeIDs: [],
  nodeOrder: [],
  nodeLabelOverrides: {},
  themeAccentColor: "",
  layoutSectionOverrides: {},
};

const history: NavOverrideHistoryInfo[] = [
  {
    id: 7,
    scope: "server",
    createdAt: "2026-06-19T14:02:00Z",
    data: {
      ...emptyOverride,
      hiddenNodeIDs: ["ambient"],
      themeAccentColor: "#7A3CF0",
    },
  },
];

beforeEach(() => {
  vi.clearAllMocks();
  vi.mocked(fetchManifest).mockResolvedValue(manifest);
  vi.mocked(fetchNavOverride).mockResolvedValue(emptyOverride);
  vi.mocked(fetchNavOverrideHistory).mockResolvedValue(history);
  vi.mocked(setNavOverride).mockResolvedValue(emptyOverride);
  vi.mocked(resetNavOverride).mockResolvedValue(emptyOverride);
  vi.mocked(restoreNavOverride).mockResolvedValue(emptyOverride);
});

function renderPage() {
  return render(
    <I18nProvider>
      <NavOverridePage />
    </I18nProvider>,
  );
}

// The Theme panel renders before the asynchronous manifest load completes.
// Wait on manifest-backed navigation UI so assertions cannot race the load.
const ready = () => screen.findByRole("switch", { name: "Show Home" });

describe("NavOverridePage", () => {
  it("renders the live manifest nodes and layout sections", async () => {
    renderPage();
    await ready();
    expect(screen.getByText("Home")).toBeInTheDocument();
    expect(screen.getByText("Ambient")).toBeInTheDocument();
    // The browse Layout's sections render in the Sections panel.
    expect(screen.getByText("Continue")).toBeInTheDocument();
    expect(screen.getByText("Recently added")).toBeInTheDocument();
  });

  it("hides a node via the visibility switch", async () => {
    const user = userEvent.setup();
    renderPage();
    await ready();

    await user.click(screen.getByRole("switch", { name: "Show Home" }));
    expect(setNavOverride).toHaveBeenCalledWith({ hiddenNodeIDs: ["home"] });
  });

  it("relabels a node (en) through the inline editor", async () => {
    const user = userEvent.setup();
    renderPage();
    await ready();

    // Open the relabel editor on the first node row.
    await user.click(screen.getAllByRole("button", { name: "Relabel" })[0]);
    const input = screen.getByLabelText("Node label");
    await user.type(input, "Front Page");
    await user.click(screen.getByRole("button", { name: "Save label" }));

    expect(setNavOverride).toHaveBeenCalledWith({
      nodeLabelOverrides: { home: { en: "Front Page" } },
    });
  });

  it("applies a theme accent override", async () => {
    const user = userEvent.setup();
    renderPage();
    await ready();

    const accent = screen.getByLabelText("Accent color");
    await user.type(accent, "#7A3CF0");
    await user.click(screen.getByRole("button", { name: "Apply" }));

    expect(setNavOverride).toHaveBeenCalledWith({ themeAccentColor: "#7A3CF0" });
  });

  it("hides a Section within the selected Layout", async () => {
    const user = userEvent.setup();
    renderPage();
    await ready();

    await user.click(screen.getByRole("switch", { name: "Show Continue" }));
    expect(setNavOverride).toHaveBeenCalledWith({
      layoutSectionOverrides: { browse: { hiddenSectionIDs: ["continue"], sectionOrder: [] } },
    });
  });

  it("restores a history snapshot", async () => {
    const user = userEvent.setup();
    renderPage();
    await ready();

    // The history table row carries its own Restore button.
    const row = screen.getByText("1 hidden · Custom accent").closest("tr");
    expect(row).not.toBeNull();
    await user.click(within(row as HTMLElement).getByRole("button", { name: "Restore" }));
    expect(restoreNavOverride).toHaveBeenCalledWith(7);
  });

  it("resets all overrides", async () => {
    const user = userEvent.setup();
    // With an active override the Reset button is enabled.
    vi.mocked(fetchNavOverride).mockResolvedValue({
      ...emptyOverride,
      hiddenNodeIDs: ["ambient"],
    });
    renderPage();
    await ready();

    await user.click(screen.getByRole("button", { name: "Reset all" }));
    expect(resetNavOverride).toHaveBeenCalled();
  });
});
