import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  addPathPickerRoot,
  browseServerFolders,
  createManagedFolder,
  deleteManagedPath,
  fetchFolderTree,
  fetchPathPicker,
  moveManagedPath,
  renameManagedPath,
  restoreManagedPath,
} from "./manage";

// Request-shape tests for the WEB-5C (ADR-0061) folder-tree read plus the
// manager-gated managed folder/file mutations. We stub the global `fetch` (the
// single network boundary every client call funnels through) and assert the
// exact on-wire GraphQL operation name + the variables object. Server-side
// store/graph tests pin the resolver behavior and root-escape rejection; these
// only guard that the Core Web UI sends the agreed operation and arguments.

interface Recorded {
  query: string;
  variables: Record<string, unknown>;
}

const recorded: Recorded[] = [];

beforeEach(() => {
  recorded.length = 0;
  vi.stubGlobal(
    "fetch",
    vi.fn((_url: string, init?: RequestInit) => {
      const body = init?.body
        ? (JSON.parse(init.body as string) as Recorded)
        : { query: "", variables: {} };
      recorded.push(body);
      let data: Record<string, unknown>;
      if (body.query.includes("browseServerFolders")) {
        data = {
          browseServerFolders: {
            name: "media",
            path: "/srv/media",
            kind: "folder",
            children: [{ name: "Movies", path: "/srv/media/Movies", kind: "folder" }],
          },
        };
      } else if (body.query.includes("pathPicker")) {
        data = {
          pathPicker: {
            runtime: "container",
            roles: ["libraryRoot", "incomingFolder", "backupDestination"],
            role: "libraryRoot",
            purposeKey: "pathPicker.libraryRoot",
            previewTimeoutMs: 75,
            maxPreviewEntries: 5,
            roots: [{ name: "media", path: "/srv/media", entryClass: "ordinary" }],
            entries: [
              {
                name: "media",
                path: "/srv/media",
                kind: "folder",
                accessError: "none",
                entryClass: "ordinary",
                selectable: true,
                mediaCount: 1,
                sampleNames: ["demo.mp4"],
                previewTimeoutMs: 75,
                maxPreviewEntries: 5,
                truncated: false,
              },
            ],
          },
        };
      } else if (body.query.includes("addPathPickerRoot")) {
        data = {
          addPathPickerRoot: { name: "Archive", path: "/srv/Archive", entryClass: "ordinary" },
        };
      } else if (body.query.includes("folderTree")) {
        data = {
          folderTree: {
            name: "",
            path: "",
            kind: "folder",
            children: [{ name: "Movies", path: "/srv/media/Movies", kind: "folder" }],
          },
        };
      } else if (body.query.includes("createManagedFolder")) {
        data = { createManagedFolder: { path: "/srv/media/Movies/New", kind: "folder" } };
      } else if (body.query.includes("renameManagedPath")) {
        data = { renameManagedPath: { path: "/srv/media/Movies/Renamed", kind: "folder" } };
      } else if (body.query.includes("moveManagedPath")) {
        data = { moveManagedPath: { path: "/srv/media/Archive/New", kind: "folder" } };
      } else if (body.query.includes("deleteManagedPath")) {
        data = {
          deleteManagedPath: {
            trashID: "trash-1",
            originalPath: "/srv/media/Movies/Old",
            kind: "folder",
          },
        };
      } else {
        data = { restoreManagedPath: { path: "/srv/media/Movies/Old", kind: "folder" } };
      }
      return Promise.resolve({
        ok: true,
        status: 200,
        json: () => Promise.resolve({ data }),
      } as Response);
    }),
  );
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("fetchFolderTree", () => {
  it("sends the folderTree query with libraryID + depth, null root when omitted", async () => {
    const tree = await fetchFolderTree("library-local");
    expect(tree.children?.[0].path).toBe("/srv/media/Movies");
    expect(recorded).toHaveLength(1);
    expect(recorded[0].query).toContain("folderTree");
    expect(recorded[0].variables).toEqual({
      libraryID: "library-local",
      root: null,
      depth: 2,
    });
  });

  it("forwards an explicit root and depth verbatim", async () => {
    await fetchFolderTree("library-local", "/srv/media/Movies", 1);
    expect(recorded[0].variables).toEqual({
      libraryID: "library-local",
      root: "/srv/media/Movies",
      depth: 1,
    });
  });
});

describe("browseServerFolders", () => {
  it("sends the setup picker query with null path + depth 1 by default", async () => {
    const node = await browseServerFolders();
    expect(node.path).toBe("/srv/media");
    expect(node.children?.[0].path).toBe("/srv/media/Movies");
    expect(recorded).toHaveLength(1);
    expect(recorded[0].query).toContain("query BrowseServerFolders");
    expect(recorded[0].query).not.toContain("libraryID");
    expect(recorded[0].variables).toEqual({ path: null, depth: 1 });
  });

  it("forwards an explicit path verbatim", async () => {
    await browseServerFolders("/srv/media", 1);
    expect(recorded[0].variables).toEqual({ path: "/srv/media", depth: 1 });
  });
});

describe("pathPicker", () => {
  it("sends the server-side role contract query", async () => {
    const picker = await fetchPathPicker({
      role: "libraryRoot",
      path: "/srv/media",
      libraryID: "library-local",
      distributionClass: "appStore",
      depth: 1,
    });
    expect(picker.runtime).toBe("container");
    expect(picker.entries[0].selectable).toBe(true);
    expect(recorded[0].query).toContain("query PathPicker");
    expect(recorded[0].variables).toEqual({
      role: "libraryRoot",
      path: "/srv/media",
      libraryID: "library-local",
      distributionClass: "appStore",
      depth: 1,
    });
  });

  it("sends the audited add-root mutation", async () => {
    const root = await addPathPickerRoot("/srv/Archive");
    expect(root).toEqual({ name: "Archive", path: "/srv/Archive", entryClass: "ordinary" });
    expect(recorded[0].query).toContain("mutation AddPathPickerRoot");
    expect(recorded[0].variables).toEqual({ path: "/srv/Archive" });
  });
});

describe("createManagedFolder", () => {
  it("sends parentPath + name and returns the created path/kind", async () => {
    const created = await createManagedFolder("library-local", "/srv/media/Movies", "New");
    expect(created).toEqual({ path: "/srv/media/Movies/New", kind: "folder" });
    expect(recorded[0].query).toContain("createManagedFolder");
    expect(recorded[0].variables).toEqual({
      libraryID: "library-local",
      parentPath: "/srv/media/Movies",
      name: "New",
    });
  });
});

describe("renameManagedPath", () => {
  it("sends the target path + new name", async () => {
    await renameManagedPath("library-local", "/srv/media/Movies/Old", "Renamed");
    expect(recorded[0].query).toContain("renameManagedPath");
    expect(recorded[0].variables).toEqual({
      libraryID: "library-local",
      path: "/srv/media/Movies/Old",
      name: "Renamed",
    });
  });
});

describe("moveManagedPath", () => {
  it("sends the source path + destination parent", async () => {
    await moveManagedPath("library-local", "/srv/media/Movies/New", "/srv/media/Archive");
    expect(recorded[0].query).toContain("moveManagedPath");
    expect(recorded[0].variables).toEqual({
      libraryID: "library-local",
      path: "/srv/media/Movies/New",
      destinationParentPath: "/srv/media/Archive",
    });
  });
});

describe("deleteManagedPath", () => {
  it("sends the path and returns the trash receipt for restore", async () => {
    const trash = await deleteManagedPath("library-local", "/srv/media/Movies/Old");
    expect(trash).toEqual({
      trashID: "trash-1",
      originalPath: "/srv/media/Movies/Old",
      kind: "folder",
    });
    expect(recorded[0].query).toContain("deleteManagedPath");
    expect(recorded[0].variables).toEqual({
      libraryID: "library-local",
      path: "/srv/media/Movies/Old",
    });
  });
});

describe("restoreManagedPath", () => {
  it("sends the trashID and returns the restored path/kind", async () => {
    const restored = await restoreManagedPath("library-local", "trash-1");
    expect(restored).toEqual({ path: "/srv/media/Movies/Old", kind: "folder" });
    expect(recorded[0].query).toContain("restoreManagedPath");
    expect(recorded[0].variables).toEqual({
      libraryID: "library-local",
      trashID: "trash-1",
    });
  });
});
