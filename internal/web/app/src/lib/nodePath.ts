import type { NavigationNode } from "@/lib/graphql/types";

// Navigation node addresses (CORE-2322). The server names a Library's node by
// prefixing the Library's ID with the node type, and Library IDs already carry
// that prefix, so the raw ID reads "library-library-films". The address drops
// the repeated leading word; the raw ID still resolves, so old links work.

const DOUBLED_PREFIX = /^([a-z0-9]+)-\1-(?=.)/i;

/** The address segment for a node ID: its repeated leading word collapsed. */
export function nodeSlug(id: string): string {
  const match = DOUBLED_PREFIX.exec(id);
  return match ? id.slice(match[1].length + 1) : id;
}

/** The path a navigation node lives at, optionally with a sub-path ("/all"). */
export function nodePath(id: string, rest = ""): string {
  return `/n/${encodeURIComponent(nodeSlug(id))}${rest}`;
}

/** The node an address names: by its exact ID first, then by its slug. */
export function findNodeBySlug<T extends Pick<NavigationNode, "id">>(
  nodes: readonly T[],
  segment: string | undefined,
): T | undefined {
  if (!segment) return undefined;
  return (
    nodes.find((node) => node.id === segment) ?? nodes.find((node) => nodeSlug(node.id) === segment)
  );
}
