import "@testing-library/jest-dom/vitest";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { addPathPickerRoot, fetchPathPicker } from "@/lib/graphql";
import type { PathPickerEntryInfo, PathPickerInfo, PathPickerRole } from "@/lib/graphql/types";
import { I18nProvider } from "@/lib/i18n";
import { PathPickerField } from "./index";
import { pathPickerCrumbs, pathPickerParent, pathPickerRootFor } from "./PathPickerField";

vi.mock("@/lib/graphql", () => ({
  addPathPickerRoot: vi.fn(),
  fetchPathPicker: vi.fn(),
}));

const roles: PathPickerRole[] = [
  "libraryRoot",
  "incomingFolder",
  "downloadDestination",
  "backupDestination",
];

const roots = [
  { name: "media", path: "/media", entryClass: "mount-point" as const },
  { name: "mnt", path: "/mnt", entryClass: "mount-point" as const },
];

function entry(path: string, extra: Partial<PathPickerEntryInfo> = {}): PathPickerEntryInfo {
  return {
    name: path.slice(path.lastIndexOf("/") + 1),
    path,
    kind: "folder",
    accessError: "none",
    entryClass: "ordinary",
    selectable: true,
    mediaCount: 0,
    previewTimeoutMs: 1000,
    maxPreviewEntries: 50,
    truncated: false,
    ...extra,
  };
}

// A small fake server tree shaped like the real pathPicker answer: the browsed
// folder is entries[0] and its contents are entries[0].children.
const tree: Record<string, PathPickerEntryInfo[]> = {
  "/mnt": [entry("/mnt/user")],
  "/mnt/user": [
    entry("/mnt/user/movies", { mediaCount: 12 }),
    entry("/mnt/user/tv", { mediaCount: 1 }),
    entry("/mnt/user/private", { accessError: "permission-denied", selectable: false }),
    entry("/mnt/user/notes.txt", { kind: "file" }),
  ],
  "/mnt/user/movies": [],
  "/mnt/user/tv": [],
};

function answer(role: PathPickerRole, path?: string): PathPickerInfo {
  const base = {
    runtime: "bare" as const,
    roles,
    role,
    purposeKey: `pathPicker.${role}`,
    previewTimeoutMs: 1000,
    maxPreviewEntries: 50,
    roots,
  };
  if (!path) return { ...base, entries: roots.map((root) => entry(root.path)) };
  if (path === "/elsewhere") {
    return {
      ...base,
      entries: [entry(path, { selectable: false, reasonCode: "outside-roots" })],
    };
  }
  if (!(path in tree)) {
    return {
      ...base,
      entries: [entry(path, { selectable: false, reasonCode: "server-cannot-see-path" })],
    };
  }
  return { ...base, entries: [entry(path, { children: tree[path] })] };
}

function renderField(
  role: PathPickerRole = "libraryRoot",
  props: Partial<Parameters<typeof PathPickerField>[0]> = {},
) {
  function Controlled() {
    const [value, setValue] = useState("");
    return (
      <I18nProvider>
        <PathPickerField
          id={`field-${role}`}
          label={`${role} path`}
          role={role}
          value={value}
          onChange={setValue}
          {...props}
        />
      </I18nProvider>
    );
  }
  return render(<Controlled />);
}

const browser = () => screen.getByRole("group", { name: "Folder browser" });
const row = (name: RegExp | string) => within(browser()).findByRole("button", { name });

describe("path helpers", () => {
  it("finds the location, parent and breadcrumb of a path", () => {
    expect(pathPickerRootFor("/mnt/user/tv", roots)?.path).toBe("/mnt");
    expect(pathPickerRootFor("/mntx", roots)).toBeUndefined();
    expect(pathPickerParent("/mnt/user/tv", roots)).toBe("/mnt/user");
    expect(pathPickerParent("/mnt/user", roots)).toBe("/mnt");
    expect(pathPickerParent("/mnt", roots)).toBe("");
    expect(pathPickerCrumbs("/mnt/user/tv", roots)).toEqual([
      { label: "/mnt", path: "/mnt" },
      { label: "user", path: "/mnt/user" },
      { label: "tv", path: "/mnt/user/tv" },
    ]);
  });
});

describe("PathPickerField", () => {
  beforeEach(() => {
    vi.mocked(fetchPathPicker)
      .mockReset()
      .mockImplementation(({ role, path }) => Promise.resolve(answer(role, path ?? undefined)));
    vi.mocked(addPathPickerRoot)
      .mockReset()
      .mockImplementation((path) => Promise.resolve({ name: "x", path, entryClass: "ordinary" }));
    delete window.beamfallHost;
  });

  it("lists locations first, then a folder's subfolders, never the folder itself", async () => {
    const user = userEvent.setup();
    renderField();

    await user.click(screen.getByRole("button", { name: "Browse" }));
    await user.click(await row("/mnt"));
    await user.click(await row("user"));

    const list = await within(browser()).findByRole("list", { name: "Folders in /mnt/user" });
    const names = within(list)
      .getAllByRole("button")
      .map((b) => b.textContent);
    expect(names).toEqual(["movies12 media files", "tv1 media file", "privateNo access"]);
    expect(within(list).getByRole("button", { name: /private/ })).toBeDisabled();
    expect(within(browser()).getByRole("status")).toHaveTextContent("3 folders in /mnt/user");
  });

  it("shows where you are and goes up with the breadcrumb and the Up button", async () => {
    const user = userEvent.setup();
    renderField();
    await user.type(screen.getByLabelText("libraryRoot path"), "/mnt/user/tv");
    await user.click(screen.getByRole("button", { name: "Browse" }));

    const crumbs = await within(browser()).findByRole("navigation", { name: "Current location" });
    await waitFor(() => expect(within(crumbs).getByText("tv")).toHaveAttribute("aria-current"));
    expect(await within(browser()).findByText("No folders inside this one.")).toBeInTheDocument();

    await user.click(within(browser()).getByRole("button", { name: "Up one level" }));
    await row(/movies/);
    await user.click(within(crumbs).getByRole("button", { name: "Locations" }));
    await row("/media");
    expect(within(browser()).getByRole("button", { name: "Up one level" })).toBeDisabled();
    expect(within(browser()).getByRole("button", { name: "Use this folder" })).toBeDisabled();
  });

  it.each(roles)("fills %s with the folder being shown and closes", async (role) => {
    const user = userEvent.setup();
    renderField(role);

    await user.click(screen.getByRole("button", { name: "Browse" }));
    await user.click(await row("/mnt"));
    await user.click(await row("user"));
    await user.click(await row(/movies/));
    await within(browser()).findByText("No folders inside this one.");
    await user.click(within(browser()).getByRole("button", { name: "Use this folder" }));

    expect(screen.getByLabelText(`${role} path`)).toHaveValue("/mnt/user/movies");
    expect(screen.queryByRole("group", { name: "Folder browser" })).toBeNull();
    expect(screen.getByRole("button", { name: "Browse" })).toHaveFocus();
    expect(fetchPathPicker).toHaveBeenLastCalledWith(
      expect.objectContaining({ role, path: "/mnt/user/movies" }),
    );
  });

  it("hands the folder to onSelect under a custom action label", async () => {
    const user = userEvent.setup();
    const onSelect = vi.fn();
    renderField("libraryRoot", { selectLabel: "Add this folder", onSelect });

    await user.click(screen.getByRole("button", { name: "Browse" }));
    await user.click(await row("/mnt"));
    await row("user");
    await user.click(within(browser()).getByRole("button", { name: "Add this folder" }));

    expect(onSelect).toHaveBeenCalledWith("/mnt");
    expect(screen.getByLabelText("libraryRoot path")).toHaveValue("");
  });

  it("stays open on the folder when onSelect reports it was not taken", async () => {
    const user = userEvent.setup();
    const onSelect = vi.fn().mockResolvedValueOnce(false).mockResolvedValue(true);
    renderField("libraryRoot", { selectLabel: "Add this folder", onSelect });

    await user.click(screen.getByRole("button", { name: "Browse" }));
    await user.click(await row("/mnt"));
    await row("user");
    const add = within(browser()).getByRole("button", { name: "Add this folder" });
    await user.click(add);

    await waitFor(() => expect(onSelect).toHaveBeenCalledTimes(1));
    expect(browser()).toBeInTheDocument();
    expect(add).toBeEnabled();

    await user.click(add);
    await waitFor(() => expect(screen.queryByRole("group", { name: "Folder browser" })).toBeNull());
    expect(onSelect).toHaveBeenCalledTimes(2);
  });

  it("holds the commit action back while selectDisabled is set", async () => {
    const user = userEvent.setup();
    const onSelect = vi.fn();
    renderField("libraryRoot", { selectLabel: "Add this folder", onSelect, selectDisabled: true });

    await user.click(screen.getByRole("button", { name: "Browse" }));
    await user.click(await row("/mnt"));
    await row("user");
    const add = within(browser()).getByRole("button", { name: "Add this folder" });

    expect(add).toBeDisabled();
    await user.click(add);
    expect(onSelect).not.toHaveBeenCalled();
  });

  it("stays on the folder opened while an earlier commit was still saving", async () => {
    const user = userEvent.setup();
    let finishCommit: (taken: boolean) => void = () => {};
    const onSelect = vi.fn(
      () =>
        new Promise<boolean>((resolve) => {
          finishCommit = resolve;
        }),
    );
    renderField("libraryRoot", { selectLabel: "Add this folder", onSelect });

    await user.click(screen.getByRole("button", { name: "Browse" }));
    await user.click(await row("/mnt"));
    await user.click(within(browser()).getByRole("button", { name: "Add this folder" }));
    await user.click(await row("user"));
    await row(/movies/);
    finishCommit(true);

    await waitFor(() => expect(onSelect).toHaveBeenCalledWith("/mnt"));
    expect(await row(/movies/)).toBeInTheDocument();
    expect(browser()).toBeInTheDocument();
  });

  it("moves with the keyboard and closes on Escape", async () => {
    const user = userEvent.setup();
    renderField();
    await user.type(screen.getByLabelText("libraryRoot path"), "/mnt/user");
    await user.click(screen.getByRole("button", { name: "Browse" }));

    await waitFor(async () => expect(await row(/movies/)).toHaveFocus());
    await user.keyboard("{ArrowDown}");
    await waitFor(async () => expect(await row(/^tv/)).toHaveFocus());
    await user.keyboard("{Backspace}");
    await waitFor(async () => expect(await row("user")).toHaveFocus());
    await user.keyboard("{Enter}");
    await waitFor(async () => expect(await row(/movies/)).toHaveFocus());
    await user.keyboard("{Escape}");

    expect(screen.queryByRole("group", { name: "Folder browser" })).toBeNull();
    expect(screen.getByRole("button", { name: "Browse" })).toHaveFocus();
  });

  it("offers to allow browsing a typed folder outside the locations", async () => {
    const user = userEvent.setup();
    renderField();
    await user.type(screen.getByLabelText("libraryRoot path"), "/elsewhere");
    await user.click(screen.getByRole("button", { name: "Browse" }));

    expect(await within(browser()).findByText(/doesn't browse this folder yet/)).toBeVisible();
    expect(within(browser()).getByRole("button", { name: "Use this folder" })).toBeDisabled();
    await user.click(within(browser()).getByRole("button", { name: "Allow browsing here" }));

    expect(addPathPickerRoot).toHaveBeenCalledWith("/elsewhere");
    expect(screen.getByLabelText("libraryRoot path")).toHaveValue("/elsewhere");
  });

  it("explains a folder the server cannot find", async () => {
    const user = userEvent.setup();
    renderField();
    await user.type(screen.getByLabelText("libraryRoot path"), "/mnt/typo");
    await user.click(screen.getByRole("button", { name: "Browse" }));

    expect(await within(browser()).findByText(/can't find this folder/)).toBeVisible();
    expect(within(browser()).getByRole("button", { name: "Use this folder" })).toBeDisabled();
  });

  it("reports a failed load and retries it", async () => {
    const user = userEvent.setup();
    vi.mocked(fetchPathPicker).mockRejectedValueOnce(new Error("network down"));
    renderField();

    await user.click(screen.getByRole("button", { name: "Browse" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("network down");
    await user.click(screen.getByRole("button", { name: "Try again" }));

    await row("/mnt");
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("suppresses download destinations in an App Store host before querying", () => {
    window.beamfallHost = {
      kind: "apple-manager",
      platform: "ipad",
      clientClass: "ipad-manager-app-store",
    };

    renderField("downloadDestination", { suppressAppStoreDownload: true });

    expect(screen.queryByLabelText("downloadDestination path")).toBeNull();
    expect(fetchPathPicker).not.toHaveBeenCalled();
  });
});
