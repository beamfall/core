import { useCallback, useEffect, useRef, useState } from "react";
import { useLocation, useNavigate } from "react-router";
import { AppShell } from "@/components/chrome/AppShell";
import { Button } from "@/components/ui/button";
import { displayString } from "@/lib/display";
import type { SourceItem } from "@/lib/graphql/types";
import { useDisplayLocale, useT } from "@/lib/i18n";
import { MediaItemCard } from "../components/MediaItemCard";
import { useVirtualGrid, VIRTUAL_GRID_MIN_ITEMS } from "../components/useVirtualGrid";
import { ViewerActions } from "../components/ViewerActions";
import { fetchSectionItems, SEEALL_PAGE } from "../lib/sections";
import type { SeeAllContext } from "../lib/seeall";

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
  const gridRef = useRef<HTMLUListElement | null>(null);
  const gridWindow = useVirtualGrid({
    gridRef,
    itemCount: items.length,
    enabled: items.length > VIRTUAL_GRID_MIN_ITEMS,
  });
  const start = gridWindow?.startIndex ?? 0;
  const end = gridWindow?.endIndex ?? items.length;
  const visibleItems = gridWindow ? items.slice(start, end) : items;

  const loadPage = useCallback(async () => {
    if (!ctx) return;
    setBusy(true);
    try {
      const page = await fetchSectionItems(ctx.section, {
        libraryID: ctx.libraryID,
        entityID: ctx.entityID,
        paging: { limit: SEEALL_PAGE, offset: offsetRef.current },
      });
      setItems((prev) => [...prev, ...page]);
      offsetRef.current += page.length;
      setHasMore(page.length >= SEEALL_PAGE);
    } catch {
      setHasMore(false);
    } finally {
      setBusy(false);
    }
  }, [ctx]);

  useEffect(() => {
    // Reset and load the first page when the context changes.
    offsetRef.current = 0;
    setItems([]);
    setHasMore(true);
    void loadPage();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [ctx?.section.id, ctx?.libraryID, ctx?.entityID]);

  function goBack() {
    navigate(ctx?.returnTo || "/");
  }

  if (!ctx) {
    return (
      <AppShell actions={<ViewerActions />}>
        <div className="flex flex-col gap-3">
          <Button variant="outline" size="sm" onClick={() => navigate("/")}>
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
        <div className="flex items-center gap-3">
          <Button variant="outline" size="sm" onClick={goBack}>
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
