import "@testing-library/jest-dom/vitest";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { fetchLibraries, fetchPathPicker } from "@/lib/graphql";
import { I18nProvider } from "@/lib/i18n";
import { enqueueConvertJob, fetchConvertJobs } from "../lib/powertools";
import { ConvertPage } from "./ConvertPage";

vi.mock("@/lib/graphql", () => ({
  fetchLibraries: vi.fn(),
  // PathPickerField imports these; stub so the picker mounts inertly.
  fetchPathPicker: vi.fn().mockResolvedValue({ roles: ["libraryRoot"], roots: [], entries: [] }),
  addPathPickerRoot: vi.fn(),
}));
vi.mock("../lib/powertools", async () => {
  const actual = await vi.importActual<typeof import("../lib/powertools")>("../lib/powertools");
  return { ...actual, fetchConvertJobs: vi.fn(), enqueueConvertJob: vi.fn() };
});

beforeEach(() => {
  vi.clearAllMocks();
  // No Companion host: the host-aware picker must degrade to in-page browse.
  delete (window as { beamfallHost?: unknown }).beamfallHost;
  vi.mocked(fetchLibraries).mockResolvedValue([
    { id: "library-local", title: "Local", pluginID: null, previewClips: false, restricted: false },
  ]);
});

function renderPage() {
  return render(
    <I18nProvider>
      <ConvertPage />
    </I18nProvider>,
  );
}

describe("ConvertPage", () => {
  it("renders the convert form + empty queue with no Companion host", async () => {
    vi.mocked(fetchConvertJobs).mockResolvedValue([]);
    renderPage();
    expect(await screen.findByRole("button", { name: "Convert" })).toBeInTheDocument();
    expect(screen.getByText("Conversion queue")).toBeInTheDocument();
    // In-page browse affordance is present (the picker is not dead off-host).
    expect(screen.getAllByRole("button", { name: /Browse/ }).length).toBeGreaterThan(0);
  });

  it("lists convert jobs in the queue catalog state", async () => {
    vi.mocked(fetchConvertJobs).mockResolvedValue([
      {
        id: "job-convert-1",
        kind: "convert-managed-file",
        status: "running",
        progress: { current: 1, total: 4, unit: "files" },
      },
    ]);
    renderPage();
    expect(await screen.findByText("job-convert-1")).toBeInTheDocument();
  });

  it("degrades gracefully when the server has no convert enqueue field", async () => {
    vi.mocked(fetchConvertJobs).mockResolvedValue([]);
    vi.mocked(enqueueConvertJob).mockResolvedValue({ available: false });
    const user = userEvent.setup();
    renderPage();
    await screen.findByRole("button", { name: "Convert" });

    // Provide a source path then start the conversion.
    const source = screen.getByLabelText("Source file or folder");
    await user.type(source, "/srv/media/a.mov");
    await user.click(screen.getByRole("button", { name: "Convert" }));

    await waitFor(() =>
      expect(
        screen.getByText(/does not accept conversions from the web Console/i),
      ).toBeInTheDocument(),
    );
    // The Convert action disables itself once the server is known unwired.
    expect(screen.getByRole("button", { name: "Convert" })).toBeDisabled();
  });

  it("browses the source with incomingFolder so folders inside the library stay selectable", async () => {
    vi.mocked(fetchConvertJobs).mockResolvedValue([]);
    vi.mocked(fetchPathPicker).mockImplementation(async ({ role, path }) => ({
      runtime: "bare",
      roles: ["libraryRoot", "incomingFolder"],
      role,
      purposeKey: `pathPicker.${role}`,
      previewTimeoutMs: 75,
      maxPreviewEntries: 5,
      roots: [{ name: "srv", path: "/srv", entryClass: "ordinary" }],
      entries: path
        ? [
            {
              name: "Movies",
              path,
              kind: "folder",
              accessError: "none",
              entryClass: "ordinary",
              selectable: role !== "libraryRoot",
              reasonCode: role === "libraryRoot" ? "overlaps-library-root" : undefined,
              mediaCount: 2,
              previewTimeoutMs: 75,
              maxPreviewEntries: 5,
              truncated: false,
              children: [],
            },
          ]
        : [],
    }));
    const user = userEvent.setup();
    renderPage();
    await screen.findByRole("button", { name: "Convert" });

    await user.type(screen.getByLabelText("Source file or folder"), "/srv/Movies");
    await user.click(screen.getByRole("button", { name: "Browse" }));

    const browser = await screen.findByRole("group", { name: "Folder browser" });
    expect(await within(browser).findByRole("button", { name: "Use this folder" })).toBeEnabled();
    expect(fetchPathPicker).toHaveBeenCalledWith(
      expect.objectContaining({ role: "incomingFolder", path: "/srv/Movies" }),
    );
  });
});
