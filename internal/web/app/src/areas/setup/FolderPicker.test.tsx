import "@testing-library/jest-dom/vitest";
import { cleanup, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { FolderEntryInfo } from "@/lib/graphql/types";
import { breadcrumbs, FolderPicker } from "./FolderPicker";

afterEach(cleanup);

const labels = {
  use: "Use this folder",
  loading: "Loading folders…",
  empty: "No subfolders here.",
  close: "Close folder picker",
};

function node(path: string, children: FolderEntryInfo[]): FolderEntryInfo {
  return {
    name: path.split("/").pop() ?? path,
    path,
    kind: "folder",
    size: 0,
    modifiedAt: "",
    children,
  };
}

function dir(name: string, path: string): FolderEntryInfo {
  return { name, path, kind: "folder", size: 0, modifiedAt: "" };
}

describe("breadcrumbs", () => {
  it("always starts at the filesystem root and accumulates segments", () => {
    expect(breadcrumbs("/media/films")).toEqual([
      { label: "/", path: "/" },
      { label: "media", path: "/media" },
      { label: "films", path: "/media/films" },
    ]);
  });

  it("returns just the root for the root path", () => {
    expect(breadcrumbs("/")).toEqual([{ label: "/", path: "/" }]);
  });
});

describe("FolderPicker", () => {
  it("descends into a folder via onOpenFolder", async () => {
    const onOpenFolder = vi.fn();
    render(
      <FolderPicker
        node={node("/media", [dir("films", "/media/films")])}
        busy={false}
        error={null}
        onOpenFolder={onOpenFolder}
        onUse={vi.fn()}
        onClose={vi.fn()}
        labels={labels}
      />,
    );
    await userEvent.click(screen.getByText("films"));
    expect(onOpenFolder).toHaveBeenCalledWith("/media/films");
  });

  it("navigates up through a breadcrumb segment", async () => {
    const onOpenFolder = vi.fn();
    render(
      <FolderPicker
        node={node("/media/films", [])}
        busy={false}
        error={null}
        onOpenFolder={onOpenFolder}
        onUse={vi.fn()}
        onClose={vi.fn()}
        labels={labels}
      />,
    );
    await userEvent.click(screen.getByRole("button", { name: "media" }));
    expect(onOpenFolder).toHaveBeenCalledWith("/media");
  });

  it("commits the current folder with Use this folder", async () => {
    const onUse = vi.fn();
    render(
      <FolderPicker
        node={node("/media/films", [])}
        busy={false}
        error={null}
        onOpenFolder={vi.fn()}
        onUse={onUse}
        onClose={vi.fn()}
        labels={labels}
      />,
    );
    await userEvent.click(screen.getByRole("button", { name: labels.use }));
    expect(onUse).toHaveBeenCalledTimes(1);
  });

  it("renders only directory children and keeps the current path visible", () => {
    render(
      <FolderPicker
        node={node("/media", [
          dir("films", "/media/films"),
          {
            name: "poster.jpg",
            path: "/media/poster.jpg",
            kind: "file",
            size: 42,
            modifiedAt: "",
          },
        ])}
        busy={false}
        error={null}
        onOpenFolder={vi.fn()}
        onUse={vi.fn()}
        onClose={vi.fn()}
        labels={labels}
      />,
    );
    expect(screen.getByText("films")).toBeInTheDocument();
    expect(screen.queryByText("poster.jpg")).not.toBeInTheDocument();
    expect(screen.getByText("/media")).toBeInTheDocument();
  });

  it("shows the error message when a folder cannot be read", () => {
    render(
      <FolderPicker
        node={node("/media", [])}
        busy={false}
        error="Couldn't read that folder."
        onOpenFolder={vi.fn()}
        onUse={vi.fn()}
        onClose={vi.fn()}
        labels={labels}
      />,
    );
    expect(screen.getByText("Couldn't read that folder.")).toBeInTheDocument();
  });
});
