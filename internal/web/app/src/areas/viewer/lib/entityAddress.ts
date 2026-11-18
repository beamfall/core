import { useEffect } from "react";
import { useHref } from "react-router";
import { useSession } from "@/lib/session";
import { slugify } from "./mediaAddress";

// Readable Entity addresses (CORE-2511), modelled on the Media ones
// (mediaAddress.ts). An Entity page's URL reads
//
//   /entity/<readable-words>-<token>
//
// in one path segment, where the token alone names the Entity and the words
// are cosmetic: the ancestor's title (when the page names one) and then the
// Entity's own. The token is the hex digest of a content-derived Entity ID
// ("entity-" plus 32 hex digits), so the client turns it back into the ID with
// no server lookup. Any other ID shape keeps the bare-ID address. A bare or
// stale address resolves through its token and is rewritten once the Entity
// is known.

const CONTENT_ID = /^entity-([0-9a-f]{32})$/;
const TOKEN = /(?:^|-)([0-9a-f]{32})$/;

/** The Entity ID an `/entity/:entityId` segment names: its token, or the segment as typed. */
export function entityIdFromAddress(segment: string | undefined): string {
  let value = segment ?? "";
  try {
    value = decodeURIComponent(value);
  } catch {
    // A malformed escape is read as typed.
  }
  if (CONTENT_ID.test(value)) return value;
  const token = TOKEN.exec(value);
  return token ? `entity-${token[1]}` : value;
}

/**
 * The canonical address of an Entity: the readable form for a content-derived
 * ID with words to show, otherwise the bare-ID form.
 */
export function entityPath(id: string, readable?: { title?: string; parent?: string }): string {
  const content = CONTENT_ID.exec(id);
  if (!content) return `/entity/${encodeURIComponent(id)}`;
  const slug = slugify([readable?.parent, readable?.title].filter(Boolean).join(" "));
  return `/entity/${slug ? `${slug}-${content[1]}` : id}`;
}

/**
 * useCanonicalEntityAddress rewrites the address bar to the Entity's readable
 * address once it is known, in place and with no new history entry, the way
 * useCanonicalMediaAddress does for Media. A Revealed session keeps the bare
 * form: it may be showing a restricted Library, whose titles must not land in
 * the browser history.
 */
export function useCanonicalEntityAddress(
  entity: { id: string; title: string } | null | undefined,
  parentTitle: string | undefined,
  enabled = true,
): void {
  const { revealed } = useSession();
  const base = useHref("/");
  const id = entity?.id;
  const title = entity?.title;
  useEffect(() => {
    if (!enabled || !id) return;
    const root = `${base.endsWith("/") ? base : `${base}/`}entity/`;
    const current = window.location.pathname;
    if (!current.startsWith(root) || entityIdFromAddress(current.slice(root.length)) !== id) {
      return;
    }
    const target = revealed ? entityPath(id) : entityPath(id, { title, parent: parentTitle });
    const next = `${root}${target.slice("/entity/".length)}`;
    if (next === current) return;
    window.history.replaceState(
      window.history.state,
      "",
      `${next}${window.location.search}${window.location.hash}`,
    );
  }, [base, enabled, id, parentTitle, revealed, title]);
}
