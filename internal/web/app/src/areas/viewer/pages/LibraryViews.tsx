import { ChevronLeft } from "lucide-react";
import { type ReactNode, useCallback, useEffect, useRef, useState } from "react";
import { useNavigate, useSearchParams } from "react-router";
import { AppShell } from "@/components/chrome/AppShell";
import { Button } from "@/components/ui/button";
import { displayString } from "@/lib/display";
import type { DisplayString } from "@/lib/graphql/types";
import { useDisplayLocale, useT } from "@/lib/i18n";
import { nodePath } from "@/lib/nodePath";
import { MediaItemCard } from "../components/MediaItemCard";
import { useVirtualGrid, VIRTUAL_GRID_MIN_ITEMS } from "../components/useVirtualGrid";
import { ViewerActions } from "../components/ViewerActions";
import { FOLDER_VIEW } from "../lib/libraryFolders";
import {
  entityCardLine,
  fetchLibraryEntities,
  LIBRARY_ENTITY_PAGE,
  LIBRARY_VIEW_PARAM,
  type LibraryEntitiesPage,
  type LibraryEntityItem,
  type LibraryView,
  libraryViews,
  MEDIA_VIEW,
  selectedView,
  viewSearchParams,
} from "../lib/libraryViews";
import { LibraryFolders } from "./LibraryFolders";

// A wide viewport shows more cards than the See All cap allows for.
const ENTITY_MAX_RENDERED = 240;

type Plan = { page: LibraryEntitiesPage; limit: number } | "failed" | null;

// The Library page's views (CORE-2362). It reads which Entity kinds the
// Library is browsed by, then shows the view the URL names: an Entity kind's
// paged card grid, the Media view (`media`, the flat Library browse) or the
// Folders view (`folders`, CORE-2359). With no view named it opens on the first
// kind. A Library with no browse kinds, or whose kinds cannot be read, opens on
// the Media view with a switcher to Folders.
export function LibraryViews({
  libraryID,
  nodeID,
  title,
  media,
}: {
  libraryID: string;
  nodeID: string;
  title: DisplayString;
  /** Renders the Media view, with the switcher to place under its heading. */
  media: (switcher: ReactNode) => ReactNode;
}) {
  const t = useT();
  const displayLocale = useDisplayLocale();
  const [searchParams, setSearchParams] = useSearchParams();
  const requested = searchParams.get(LIBRARY_VIEW_PARAM);
  const [plan, setPlan] = useState<Plan>(null);

  // One read per Library learns its kinds. It already asks for the first page
  // of the kind the URL names (or of the first kind), so that grid paints from
  // it; the Media and Folders views need only the kinds.
  const firstRequestRef = useRef(requested);
  useEffect(() => {
    const first = firstRequestRef.current;
    const kindless = first === MEDIA_VIEW || first === FOLDER_VIEW;
    const limit = kindless ? 1 : LIBRARY_ENTITY_PAGE;
    const controller = new AbortController();
    fetchLibraryEntities(
      { libraryID, kind: first && !kindless ? first : undefined, limit, offset: 0 },
      controller.signal,
    )
      .then((page) => setPlan({ page, limit }))
      .catch(() => {
        if (!controller.signal.aborted) setPlan("failed");
      });
    return () => controller.abort();
  }, [libraryID]);

  const read = plan && plan !== "failed" ? plan.page : null;
  const views =
    plan === null
      ? []
      : libraryViews(
          read?.kinds ?? [],
          read?.allLabel,
          displayLocale,
          t("libraryViewAll"),
          t("libraryBrowseViewFolders"),
        );
  const view = selectedView(views, requested);
  const choose = (id: string) =>
    setSearchParams(viewSearchParams(searchParams, id), { replace: true });
  const switcher =
    views.length > 0 && view ? (
      <LibraryViewSwitcher views={views} selected={view.id} onSelect={choose} />
    ) : null;

  if (requested === FOLDER_VIEW) {
    return (
      <LibraryFolders
        libraryID={libraryID}
        nodeID={nodeID}
        heading={displayString(title, displayLocale)}
        switcher={switcher}
      />
    );
  }
  if (plan === null && requested !== MEDIA_VIEW) {
    return (
      <EntityPageFrame nodeID={nodeID} heading={displayString(title, displayLocale)} status="">
        <p className="text-sm text-[var(--text-muted)]">{t("loading")}</p>
      </EntityPageFrame>
    );
  }
  if (!view?.kind || plan === null || plan === "failed") return media(switcher);
  const seed =
    plan.limit === LIBRARY_ENTITY_PAGE && plan.page.kind === view.kind ? plan.page : null;
  return (
    <LibraryEntityGrid
      key={view.kind}
      libraryID={libraryID}
      nodeID={nodeID}
      heading={displayString(title, displayLocale)}
      kind={view.kind}
      label={view.label}
      seed={seed}
      switcher={switcher}
    />
  );
}

/** The row of view chips: one per view, the open one pressed. */
export function LibraryViewSwitcher({
  views,
  selected,
  onSelect,
}: {
  views: readonly LibraryView[];
  selected: string;
  onSelect: (id: string) => void;
}) {
  const t = useT();
  return (
    <div role="group" aria-label={t("libraryViewSwitchLabel")} className="flex flex-wrap gap-2">
      {views.map((view) => (
        <button
          key={view.id}
          type="button"
          className="bf-chip"
          aria-pressed={view.id === selected}
          onClick={() => onSelect(view.id)}
        >
          {view.label}
        </button>
      ))}
    </div>
  );
}

function EntityPageFrame({
  nodeID,
  heading,
  status,
  switcher,
  children,
}: {
  nodeID: string;
  heading: string;
  status: string;
  switcher?: ReactNode;
  children: ReactNode;
}) {
  const t = useT();
  const navigate = useNavigate();
  return (
    <AppShell actions={<ViewerActions />}>
      <div className="flex flex-col gap-5">
        <div className="flex flex-col items-start gap-1">
          <Button
            variant="ghost"
            size="sm"
            className="-ml-2"
            onClick={() => navigate(nodePath(nodeID))}
          >
            <ChevronLeft aria-hidden />
            {t("back")}
          </Button>
          <div className="flex w-full flex-wrap items-center gap-x-3 gap-y-3">
            <h1 className="bf-page-title">{heading}</h1>
            <p role="status" className="text-sm text-[var(--text-muted)]">
              {status}
            </p>
          </div>
        </div>
        {switcher}
        {children}
      </div>
    </AppShell>
  );
}

// One Entity kind's cards, paged by the server and windowed here like the
// Media view. A card's line names the Entities it sits under, else how many
// Media it holds.
function LibraryEntityGrid({
  libraryID,
  nodeID,
  heading,
  kind,
  label,
  seed,
  switcher,
}: {
  libraryID: string;
  nodeID: string;
  heading: string;
  kind: string;
  label: string;
  /** The kind's first page, when the page already read it. */
  seed: LibraryEntitiesPage | null;
  switcher: ReactNode;
}) {
  const t = useT();
  const [total, setTotal] = useState<number | null>(() => seed?.total ?? null);
  const [failed, setFailed] = useState(false);
  const [, setLoadedTick] = useState(0);
  const pagesRef = useRef(new Map<number, LibraryEntityItem[]>(seed ? [[0, seed.items]] : []));
  const pendingRef = useRef(new Set<number>());
  const generationRef = useRef(0);

  const loadPage = useCallback(
    (pageIndex: number) => {
      if (pagesRef.current.has(pageIndex) || pendingRef.current.has(pageIndex)) return;
      const generation = generationRef.current;
      pendingRef.current.add(pageIndex);
      fetchLibraryEntities({
        libraryID,
        kind,
        limit: LIBRARY_ENTITY_PAGE,
        offset: pageIndex * LIBRARY_ENTITY_PAGE,
      })
        .then((page) => {
          if (generation !== generationRef.current) return;
          pendingRef.current.delete(pageIndex);
          pagesRef.current.set(pageIndex, page.items);
          setTotal(page.total);
          setLoadedTick((tick) => tick + 1);
        })
        .catch(() => {
          if (generation !== generationRef.current) return;
          pendingRef.current.delete(pageIndex);
          setFailed(true);
        });
    },
    [libraryID, kind],
  );

  useEffect(() => loadPage(0), [loadPage]);

  // A retry drops what was read and starts again from the first page.
  function retry() {
    generationRef.current += 1;
    pagesRef.current = new Map();
    pendingRef.current = new Set();
    setFailed(false);
    setTotal(null);
    loadPage(0);
  }

  const itemCount = total ?? 0;
  const listRef = useRef<HTMLUListElement | null>(null);
  const gridWindow = useVirtualGrid({
    gridRef: listRef,
    itemCount,
    enabled: itemCount > VIRTUAL_GRID_MIN_ITEMS,
    maxItems: ENTITY_MAX_RENDERED,
  });
  const start = gridWindow?.startIndex ?? 0;
  const end = gridWindow?.endIndex ?? Math.min(itemCount, LIBRARY_ENTITY_PAGE);

  useEffect(() => {
    if (end <= start) return;
    const first = Math.floor(start / LIBRARY_ENTITY_PAGE);
    const last = Math.floor((end - 1) / LIBRARY_ENTITY_PAGE);
    for (let pageIndex = first; pageIndex <= last; pageIndex += 1) loadPage(pageIndex);
  }, [start, end, loadPage]);

  const count = (mediaCount: number) => t("libraryBrowseCount", { count: mediaCount });
  const cells = [];
  for (let index = start; index < end; index += 1) {
    const item = pagesRef.current.get(Math.floor(index / LIBRARY_ENTITY_PAGE))?.[
      index % LIBRARY_ENTITY_PAGE
    ];
    cells.push(
      item ? (
        <MediaItemCard
          key={item.id}
          item={{ ...item, summary: entityCardLine(item, count) }}
          posInSet={index + 1}
          setSize={itemCount}
        />
      ) : (
        <PendingCard key={`pending-${index}`} />
      ),
    );
  }

  const status =
    total === null ? (failed ? "" : t("loading")) : t("libraryBrowseCount", { count: total });
  return (
    <EntityPageFrame nodeID={nodeID} heading={heading} status={status} switcher={switcher}>
      <ul ref={listRef} aria-label={label} aria-busy={total === null} className="bf-grid min-w-0">
        {gridWindow && gridWindow.topPad > 0 && <Spacer height={gridWindow.topPad} />}
        {cells}
        {gridWindow && gridWindow.bottomPad > 0 && <Spacer height={gridWindow.bottomPad} />}
      </ul>
      {failed && (
        <div role="alert" className="bf-empty flex flex-col gap-3">
          <p className="text-sm text-[var(--text-secondary)]">{t("libraryBrowseLoadFailed")}</p>
          <Button variant="outline" onClick={retry}>
            {t("jobRetryAction")}
          </Button>
        </div>
      )}
      {total === 0 && !failed && (
        <div role="status" className="bf-empty flex flex-col gap-3">
          <p className="text-sm text-[var(--text-secondary)]">{t("libraryEmptyLead")}</p>
        </div>
      )}
    </EntityPageFrame>
  );
}

function Spacer({ height }: { height: number }) {
  return (
    <li
      aria-hidden
      role="presentation"
      className="bf-grid-spacer list-none"
      style={{ gridColumn: "1 / -1", height: `${height}px`, flex: "none" }}
    />
  );
}

// Holds the place of a card whose page has not arrived yet; never focusable
// and hidden from assistive tech.
function PendingCard() {
  return (
    <li className="list-none" aria-hidden="true">
      <div className="bf-card" style={{ pointerEvents: "none" }}>
        <div className="bf-poster" />
        <div className="bf-card-copy">
          <span className="bf-card-title">&nbsp;</span>
        </div>
      </div>
    </li>
  );
}
