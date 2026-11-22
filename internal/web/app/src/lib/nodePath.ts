import type { NavigationNode } from "@/lib/graphql/types";

// Navigation node addresses (CORE-2322). The server used to name a Library's
// node by prefixing the Library's ID with the node type, and Library IDs
// already carry that prefix, so the raw ID read "library-library-films". The
// address drops the repeated leading word. The server now sends the prefix
// once (CORE-2343), so an old link naming the doubled ID resolves by that
// same collapse.

const DOUBLED_PREFIX = /^([a-z0-9]+)-\1-(?=.)/i;

export const HOME_NODE_ID = "home";

/** The address segment, collapsing an old prefix added to the Library ID. */
export function nodeSlug(id: string, libraryID?: string): string {
  if (libraryID) {
    return libraryID.startsWith("library-") && id === `library-${libraryID}` ? libraryID : id;
  }
  const match = DOUBLED_PREFIX.exec(id);
  return match ? id.slice(match[1].length + 1) : id;
}

/** The path a navigation node lives at, optionally with a sub-path ("/all"). */
export function nodePath(id: string, rest = "", libraryID?: string): string {
  return `/n/${encodeURIComponent(nodeSlug(id, libraryID))}${rest}`;
}

/**
 * The node an address names: by its exact ID first, then by its slug, then an
 * old doubled ID ("library-library-films") by its collapsed form.
 */
export function findNodeBySlug<T extends Pick<NavigationNode, "id" | "libraryID">>(
  nodes: readonly T[],
  segment: string | undefined,
): T | undefined {
  if (!segment) return undefined;
  return (
    nodes.find((node) => node.id === segment) ??
    nodes.find((node) => nodeSlug(node.id, node.libraryID) === segment) ??
    nodes.find((node) => node.libraryID === node.id && segment === `library-${node.libraryID}`)
  );
}
