import { graphql } from "@/lib/api";
import type { SourceItem } from "@/lib/graphql/types";

// Library folder view (CORE-2359): one Library browsed by the folders its
// Media files sit in, one level at a time. The server reads the folders from
// the catalog with the Library browse's visibility (graph libraryFolder), so
// this module is only the request shape and the URL <-> folder mapping.

export const LIBRARY_FOLDER_PAGE = 200;

// The folder view travels in the URL as `view=folders`, the open folder as
// `folder=<path>`; no folder means the Library's roots.
const VIEW_PARAM = "view";
const FOLDER_VIEW = "folders";
const FOLDER_PARAM = "folder";

export function isFolderView(params: URLSearchParams): boolean {
  return params.get(VIEW_PARAM) === FOLDER_VIEW;
}

export function folderPath(params: URLSearchParams): string {
  return params.get(FOLDER_PARAM)?.trim() ?? "";
}

/** The search string that opens a folder (or the root list, for no path). */
export function folderSearch(path = ""): string {
  const params = new URLSearchParams({ [VIEW_PARAM]: FOLDER_VIEW });
  if (path) params.set(FOLDER_PARAM, path);
  return `?${params.toString()}`;
}

/** One folder and how many Media the viewer can reach at or below it. */
export interface LibraryFolderEntry {
  path: string;
  name: string;
  mediaCount: number;
}

/** One step from a Library root down to the open folder. */
export interface LibraryFolderCrumb {
  path: string;
  name: string;
}

export interface LibraryFolderPage {
  /** The open folder, or "" when the page lists the Library's roots. */
  path: string;
  /** How many roots the Library lists, so the view links back only to more than one. */
  rootCount: number;
  crumbs: LibraryFolderCrumb[];
  folders: LibraryFolderEntry[];
  /** The Media directly in the folder, across every page. */
  total: number;
  items: SourceItem[];
}

export interface LibraryFolderRequest {
  libraryID: string;
  path: string;
  limit: number;
  offset: number;
}

const LIBRARY_FOLDER_QUERY =
  "query LibraryFolder($libraryID: String!, $path: String, $limit: Int, $offset: Int) { libraryFolder(libraryID: $libraryID, path: $path, limit: $limit, offset: $offset) { path rootCount crumbs { path name } folders { path name mediaCount } total items { id kind title summary artwork { poster } artworkSlots locked mediaClass year engagement { positionSeconds durationSeconds completed totalCount remainingCount } } } }";

export async function fetchLibraryFolder(
  request: LibraryFolderRequest,
  signal?: AbortSignal,
): Promise<LibraryFolderPage> {
  const variables: Record<string, unknown> = {
    libraryID: request.libraryID,
    limit: request.limit,
    offset: request.offset,
  };
  if (request.path) variables.path = request.path;
  const data = await graphql<{ libraryFolder: LibraryFolderPage | null }>(
    LIBRARY_FOLDER_QUERY,
    variables,
    signal,
  );
  const page = data.libraryFolder;
  return {
    path: page?.path ?? "",
    rootCount: page?.rootCount ?? 0,
    crumbs: page?.crumbs ?? [],
    folders: page?.folders ?? [],
    total: page?.total ?? 0,
    items: page?.items ?? [],
  };
}
