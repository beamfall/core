import "@testing-library/jest-dom/vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { fetchLibraries } from "@/lib/graphql";
import { I18nProvider } from "@/lib/i18n";
import {
  commitMediaUploads,
  openMediaUpload,
  proposeMediaUploads,
  searchMediaUploadTargets,
  sendMediaUpload,
} from "../lib/mediaUpload";
import { UploadPage } from "./UploadPage";

vi.mock("@/lib/graphql", () => ({ fetchLibraries: vi.fn() }));
vi.mock("../lib/mediaUpload", async () => {
  const actual = await vi.importActual<typeof import("../lib/mediaUpload")>("../lib/mediaUpload");
  return {
    ...actual,
    openMediaUpload: vi.fn(),
    sendMediaUpload: vi.fn(),
    proposeMediaUploads: vi.fn(),
    searchMediaUploadTargets: vi.fn(),
    commitMediaUploads: vi.fn(),
  };
});

beforeEach(() => {
  vi.clearAllMocks();
  vi.mocked(fetchLibraries).mockResolvedValue([
    { id: "library-local", title: "Local", pluginID: null, previewClips: false, restricted: false },
  ]);
  vi.mocked(openMediaUpload).mockResolvedValue({
    uploadID: "u1",
    fileName: "Harbor Lights (2004).mkv",
    relativeDir: "",
    size: 3,
    offset: 0,
    chunkSize: 4,
  });
  vi.mocked(sendMediaUpload).mockImplementation(async (_file, status, onProgress) => {
    onProgress(status.size);
    return { ...status, offset: status.size };
  });
  vi.mocked(proposeMediaUploads).mockResolvedValue([
    {
      uploadID: "u1",
      fileName: "Harbor Lights (2004).mkv",
      parsed: { title: "Harbor Lights", year: 2004 },
      target: {
        entityID: "e1",
        title: "Harbor Lights",
        kindLabel: "Widget",
        destination: "/lib/Harbor Lights (2004)",
      },
      destination: "/lib/Harbor Lights (2004)",
      rescan: { title: "Harbor Lights", year: 2004 },
      conflict: false,
    },
  ]);
});

describe("UploadPage", () => {
  it("uploads, shows the proposal, lets the admin reassign, then places the file", async () => {
    const user = userEvent.setup();
    vi.mocked(searchMediaUploadTargets).mockResolvedValue([
      { entityID: "e2", title: "Night Tide", kindLabel: "Widget", destination: "/lib/Night Tide" },
    ]);
    vi.mocked(commitMediaUploads).mockResolvedValue([
      { uploadID: "u1", path: "/lib/Night Tide/Harbor Lights (2004).mkv" },
    ]);
    render(
      <I18nProvider>
        <UploadPage />
      </I18nProvider>,
    );
    await screen.findByRole("button", { name: "Choose files" });
    await user.upload(
      screen.getByLabelText("Choose files", { selector: "input" }),
      new File(["abc"], "Harbor Lights (2004).mkv"),
    );
    expect(await screen.findByText("Widget")).toBeInTheDocument();
    const folder = screen.getByLabelText("Folder Harbor Lights (2004).mkv");
    expect(folder).toHaveValue("/lib/Harbor Lights (2004)");

    await user.click(screen.getByRole("button", { name: "Change" }));
    await user.type(screen.getByLabelText("Search items"), "night{Enter}");
    await user.click(await screen.findByRole("button", { name: "Widget: Night Tide" }));
    expect(folder).toHaveValue("/lib/Night Tide");

    await user.click(screen.getByRole("button", { name: "Place files" }));
    await waitFor(() =>
      expect(commitMediaUploads).toHaveBeenCalledWith([
        { uploadID: "u1", destination: "/lib/Night Tide", conflict: "keep" },
      ]),
    );
    expect(await screen.findByText("Placed")).toBeInTheDocument();
  });

  it("defaults a name clash to skip", async () => {
    const user = userEvent.setup();
    vi.mocked(proposeMediaUploads).mockResolvedValue([
      {
        uploadID: "u1",
        fileName: "Harbor Lights (2004).mkv",
        destination: "/lib",
        conflict: true,
      },
    ]);
    vi.mocked(commitMediaUploads).mockResolvedValue([{ uploadID: "u1", error: "exists" }]);
    render(
      <I18nProvider>
        <UploadPage />
      </I18nProvider>,
    );
    await screen.findByRole("button", { name: "Choose files" });
    await user.upload(
      screen.getByLabelText("Choose files", { selector: "input" }),
      new File(["abc"], "Harbor Lights (2004).mkv"),
    );
    expect(await screen.findByText("A file with this name is already there")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Place files" }));
    await waitFor(() =>
      expect(commitMediaUploads).toHaveBeenCalledWith([
        { uploadID: "u1", destination: "/lib", conflict: "fail" },
      ]),
    );
    expect(await screen.findByText("exists")).toBeInTheDocument();
  });
});
