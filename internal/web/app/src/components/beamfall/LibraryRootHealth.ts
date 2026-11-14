import { browseServerFolders, fetchLibraries } from "@/lib/graphql";
import type { LibraryInfo } from "@/lib/graphql/types";
import type { StringKey } from "@/strings";

// Library root health (CORE-1606). A Library whose folder is gone or unreadable
// must never look healthy: Overview lists it under Needs attention, the
// Libraries card marks the folder, and Home's first scan says why it cannot
// start. The server stats each folder through browseServerFolders (depth 0, so
// nothing is listed), which answers "folder does not exist" or "folder is not
// readable" for a broken path. Any other failure (offline, locked) is not a
// folder problem, so it reports nothing rather than a false alarm.

export type RootProblemReason = "missing" | "unreadable";

export interface RootProblem {
  libraryID: string;
  title: string;
  root: string;
  reason: RootProblemReason;
}

export const ROOT_PROBLEM_LABEL: Record<RootProblemReason, StringKey> = {
  missing: "libraryRootMissing",
  unreadable: "libraryRootUnreadable",
};

function reasonFor(error: unknown): RootProblemReason | null {
  const message = error instanceof Error ? error.message : String(error);
  if (/does not exist|not a folder/i.test(message)) return "missing";
  if (/not readable/i.test(message)) return "unreadable";
  return null;
}

/** Checks one folder on the server; null when it is readable or the check could not run. */
export async function probeRoot(root: string): Promise<RootProblemReason | null> {
  try {
    await browseServerFolders(root, 0);
    return null;
  } catch (error) {
    return reasonFor(error);
  }
}

/** Every broken folder of the given Libraries, in Library then folder order. */
export async function libraryRootProblems(
  libraries: Pick<LibraryInfo, "id" | "title" | "roots">[],
): Promise<RootProblem[]> {
  const checks = libraries.flatMap((library) =>
    (library.roots ?? []).map(async (root) => {
      const reason = await probeRoot(root);
      return reason ? { libraryID: library.id, title: library.title, root, reason } : null;
    }),
  );
  return (await Promise.all(checks)).filter((p): p is RootProblem => p !== null);
}

/** Loads the Libraries and checks their folders. */
export async function fetchLibraryRootProblems(signal?: AbortSignal): Promise<RootProblem[]> {
  return libraryRootProblems(await fetchLibraries(signal));
}
