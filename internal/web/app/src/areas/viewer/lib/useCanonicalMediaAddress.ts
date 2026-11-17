import { useEffect } from "react";
import { useHref } from "react-router";
import type { GenericMediaInfo } from "@/lib/graphql/types";
import { useSession } from "@/lib/session";
import { canonicalMediaPath, type MediaRoute, mediaIdFromAddress, mediaPath } from "./mediaAddress";

/**
 * useCanonicalMediaAddress rewrites the address bar to an item's canonical
 * readable address (CORE-2329) once the item is loaded: a bare-ID link, an
 * old slug or a renamed item's address becomes the current one in place, with
 * no new history entry.
 *
 * The rewrite goes through history.replaceState, not a router navigation: the
 * address is cosmetic (its token names the same item), and a navigation would
 * hand the watch route a new location state that re-attaches the running
 * player. The router reads the rewritten address on the next Back or reload.
 *
 * A Revealed session keeps the bare-ID address: it may be showing a
 * restricted Library, whose titles must not land in the browser history.
 */
export function useCanonicalMediaAddress(
  route: MediaRoute,
  media: Pick<GenericMediaInfo, "id" | "title" | "mediaClass" | "credits" | "libraryID"> | null,
  enabled = true,
): void {
  const { manifest, revealed } = useSession();
  const libraries = manifest?.libraries;
  const base = useHref("/");
  useEffect(() => {
    if (!enabled || !media?.id) return;
    const root = `${base.endsWith("/") ? base : `${base}/`}${route}/`;
    const current = window.location.pathname;
    // Only the page showing this item owns the address (a docked player
    // sits over another page).
    if (!current.startsWith(root) || mediaIdFromAddress(current.slice(root.length)) !== media.id) {
      return;
    }
    const target = revealed
      ? mediaPath(route, media.id)
      : canonicalMediaPath(route, media, libraries ?? []);
    const next = `${root}${target.slice(route.length + 2)}`;
    if (next === current) return;
    window.history.replaceState(
      window.history.state,
      "",
      `${next}${window.location.search}${window.location.hash}`,
    );
  }, [base, enabled, libraries, media, revealed, route]);
}
