import { useEffect, useState } from "react";
import { useHref, useLocation, useParams } from "react-router";
import { ApiDeadlineError, ApiError, getIdentityEpochSignal, isAuthRejection } from "@/lib/api";
import type { GenericMediaInfo } from "@/lib/graphql/types";
import { useSession, useViewerIdentity } from "@/lib/session";
import { clearViewerCache } from "@/lib/viewerCache";
import { canonicalMediaPath, type MediaRoute, mediaAddressIDs, mediaPath } from "./mediaAddress";
import { fetchMedia } from "./mediaDetail";

function isTransientLookupFailure(error: unknown): boolean {
  if (error instanceof ApiError) {
    return (
      error.status === 408 || error.status === 429 || (error.status >= 500 && error.status < 600)
    );
  }
  return error instanceof TypeError || error instanceof ApiDeadlineError;
}

/** Resolves a bare address. Null means pending and an empty ID means unavailable. */
export function useMediaAddressID(): string | null {
  const [preferred, legacy] = mediaAddressIDs(useParams()["*"]);
  const identity = useViewerIdentity();
  const { key } = useLocation();
  const epoch = getIdentityEpochSignal();
  const scope = JSON.stringify([key, identity, preferred]);
  const [resolution, setResolution] = useState<{
    scope: string;
    epoch: AbortSignal | undefined;
    id: string;
  } | null>(null);
  useEffect(() => {
    if (!legacy) return;
    const controller = new AbortController();
    let retry: ReturnType<typeof setTimeout> | undefined;
    let delay = 1_000;
    const current = () => !controller.signal.aborted && !epoch?.aborted;
    const resolve = () => {
      if (!current()) return;
      void fetchMedia(preferred, controller.signal).then(
        (media) => {
          if (current()) setResolution({ scope, epoch, id: media ? preferred : legacy });
        },
        (error: unknown) => {
          if (!current()) return;
          if (!isTransientLookupFailure(error)) {
            if (isAuthRejection(error)) clearViewerCache();
            setResolution({ scope, epoch, id: "" });
            return;
          }
          retry = setTimeout(resolve, delay);
          delay = Math.min(delay * 2, 30_000);
        },
      );
    };
    resolve();
    return () => {
      controller.abort();
      clearTimeout(retry);
    };
  }, [epoch, legacy, preferred, scope]);
  if (!legacy) return preferred;
  return resolution?.scope === scope && resolution.epoch === epoch ? resolution.id : null;
}

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
    if (
      !current.startsWith(root) ||
      !mediaAddressIDs(current.slice(root.length)).includes(media.id)
    ) {
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
