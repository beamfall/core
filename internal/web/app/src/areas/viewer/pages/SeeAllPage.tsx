import { ChevronLeft } from "lucide-react";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useLocation, useNavigate } from "react-router";
import { AppShell } from "@/components/chrome/AppShell";
import { Button } from "@/components/ui/button";
import { displayString } from "@/lib/display";
import type { SourceItem } from "@/lib/graphql/types";
import { useDisplayLocale, useT } from "@/lib/i18n";
import { useEngagementEpoch } from "../actions";
import { MediaItemCard } from "../components/MediaItemCard";
import { useVirtualGrid, VIRTUAL_GRID_MIN_ITEMS } from "../components/useVirtualGrid";
import { ViewerActions } from "../components/ViewerActions";
import { fetchSectionItems, SEEALL_PAGE } from "../lib/sections";
import type { SeeAllContext } from "../lib/seeall";
import { playQueueOf } from "../lib/watchQueue";

// See All (/seeall/:sourceId), ported from the vanilla openSeeAll /
// loadSeeAllPage (app.js). Renders one Source as a full grid, paged 60 at a
// time through the same Section card path as browse (so a result activates
// identically — Media plays, entities open detail). Load More disappears when a
// page comes back short (the Source is exhausted). The Source context rides
// react-router navigation state; a direct visit without it degrades to a Back
// affordance (the vanilla view is reachable only from a shelf).
export function SeeAllPage() {
  const t = useT();
  const displayLocale = useDisplayLocale();
  const navigate = useNavigate();
  const location = useLocation();
  const ctx = location.state as SeeAllContext | null;

  const [items, setItems] = useState<SourceItem[]>([]);
  const [hasMore, setHasMore] = useState(true);
  const [busy, setBusy] = useState(false);
  const offsetRef = useRef(0);
  // Bumped by every reset or reload, so a response from before it is dropped.
  const generationRef = useRef(0);
  const engagementEpoch = useEngagementEpoch();
  const seenEpochRef = useRef(engagementEpoch);
  const gridRef = useRef<HTMLUListElement | null>(null);
  const gridWindow = useVirtualGrid({
    gridRef,
    itemCount: items.length,
    enabled: items.length > VIRTUAL_GRID_MIN_ITEMS,
  });
  const start = gridWindow?.startIndex ?? 0;
  const end = gridWindow?.endIndex ?? items.length;
  const visibleItems = gridWindow ? items.slice(start, end) : items;
  const queue = useMemo(() => playQueueOf(items), [items]);

  const loadPage = useCallback(async () => {
    if (!ctx) return;
    const generation = generationRef.current;
    setBusy(true);
    try {
      const page = await fetchSectionItems(ctx.section, {
        libraryID: ctx.libraryID,
        entityID: ctx.entityID,
        paging: { limit: SEEALL_PAGE, offset: offsetRef.current },
      });
      if (generationRef.current !== generation) return;
      setItems((prev) => [...prev, ...page]);
      offsetRef.current += page.length;
      setHasMore(page.length >= SEEALL_PAGE);
    } catch {
      if (generationRef.current === generation) setHasMore(false);
    } finally {
      if (generationRef.current === generation) setBusy(false);
    }
  }, [ctx]);

  // Reads the span already shown again and swaps it in whole, so a list that
  // changed underneath (an item left the watchlist) neither keeps the old rows
  // nor pages on from a stale offset.
  const reloadShown = useCallback(async () => {
    if (!ctx) return;
    const generation = ++generationRef.current;
    const want = Math.max(offsetRef.current, SEEALL_PAGE);
    const next: SourceItem[] = [];
    let more = true;
    setBusy(true);
    try {
      while (more && next.length < want) {
        const page = await fetchSectionItems(ctx.section, {
          libraryID: ctx.libraryID,
          entityID: ctx.entityID,
          paging: { limit: SEEALL_PAGE, offset: next.length },
        });
        if (generationRef.current !== generation) return;
        next.push(...page);
        more = page.length >= SEEALL_PAGE;
      }
      setItems(next);
      offsetRef.current = next.length;
      setHasMore(more);
    } catch {
      // Keep what is shown; the next change or Load More tries again.
    } finally {
      if (generationRef.current === generation) setBusy(false);
    }
  }, [ctx]);

  useEffect(() => {
    // Reset and load the first page when the context changes.
    generationRef.current += 1;
    offsetRef.current = 0;
    setItems([]);
    setHasMore(true);
    void loadPage();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [ctx?.section.id, ctx?.libraryID, ctx?.entityID]);

  useEffect(() => {
    if (seenEpochRef.current === engagementEpoch) return;
    seenEpochRef.current = engagementEpoch;
    void reloadShown();
  }, [engagementEpoch, reloadShown]);

  function goBack() {
    navigate(ctx?.returnTo || "/");
  }

  if (!ctx) {
    return (
      <AppShell actions={<ViewerActions />}>
        <div className="flex flex-col gap-3">
          <Button
            variant="ghost"
            size="sm"
            className="-ml-2 self-start"
            onClick={() => navigate("/")}
          >
            <ChevronLeft aria-hidden />
            {t("back")}
          </Button>
          <p className="text-sm text-[var(--text-muted)]">{t("libraryEmptyLead")}</p>
        </div>
      </AppShell>
    );
  }

  return (
    <AppShell actions={<ViewerActions />}>
      <div className="flex flex-col gap-4">
        <div className="flex flex-col items-start gap-1">
          <Button variant="ghost" size="sm" className="-ml-2" onClick={goBack}>
            <ChevronLeft aria-hidden />
            {t("back")}
          </Button>
          <h1 className="bf-page-title">{displayString(ctx.section.title, displayLocale)}</h1>
        </div>
        <ul ref={gridRef} className="bf-grid">
          {gridWindow && gridWindow.topPad > 0 && (
            <li
              aria-hidden
              role="presentation"
              className="bf-grid-spacer"
              style={{ gridColumn: "1 / -1", height: `${gridWindow.topPad}px` }}
            />
          )}
          {visibleItems.map((item, offset) => {
            const index = start + offset;
            return (
              <MediaItemCard
                key={`${item.id}-${index}`}
                item={item}
                posInSet={index + 1}
                setSize={items.length}
                queue={queue}
              />
            );
          })}
          {gridWindow && gridWindow.bottomPad > 0 && (
            <li
              aria-hidden
              role="presentation"
              className="bf-grid-spacer"
              style={{ gridColumn: "1 / -1", height: `${gridWindow.bottomPad}px` }}
            />
          )}
        </ul>
        {hasMore && (
          <div className="flex justify-center">
            <Button onClick={loadPage} disabled={busy}>
              {t("loadMore")}
            </Button>
          </div>
        )}
      </div>
    </AppShell>
  );
}
