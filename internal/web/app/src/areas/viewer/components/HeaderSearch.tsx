import { Play, Search } from "lucide-react";
import { type KeyboardEvent, useEffect, useMemo, useRef, useState } from "react";
import { Link, useLocation, useNavigate } from "react-router";
import { Autocomplete } from "@/components/ui/astryx-primitives";
import type { SearchResultItem } from "@/lib/graphql/types";
import { useDisplayLocale, useI18n } from "@/lib/i18n";
import { useViewerIdentity } from "@/lib/session";
import { artworkBucketURL, artworkSlotURL } from "../lib/artwork";
import { entityLinkState } from "../lib/entityPreview";
import {
  groupSearchRows,
  HEADER_SEARCH_ROWS,
  searchCountLabel,
  searchPagePath,
  searchRowDetail,
  searchRowOpenPath,
  searchRowPlayPath,
} from "../lib/searchRows";
import { useLiveSearch, useSearchShortcut } from "../lib/useLiveSearch";
import { type PlayerNavState, playQueueOf, queueWindow } from "../lib/watchQueue";
import { preloadPlayer } from "../player/preload";

/** One action on a result row: open the item, or play it. */
interface Cell {
  item: SearchResultItem;
  play: boolean;
}

interface CellGroup {
  key: string;
  label: string;
  items: Cell[];
}

// The header search field, on every viewer page. Results appear as the user
// types, grouped by type. It is an autocomplete over a grid so each row can
// offer two actions from the keyboard: Up/Down pick a row, Enter opens it, and
// Right then Enter plays it. Enter with no row picked opens the full search page.
export function HeaderSearch() {
  const { t, locale } = useI18n();
  const displayLocale = useDisplayLocale();
  const navigate = useNavigate();
  const location = useLocation();
  const identity = useViewerIdentity();
  const inputRef = useRef<HTMLInputElement | null>(null);
  const highlighted = useRef<Cell | undefined>(undefined);
  const [anchor, setAnchor] = useState<HTMLDivElement | null>(null);
  const [query, setQuery] = useState("");
  const [open, setOpen] = useState(false);
  const live = useLiveSearch(query, "keyword");
  useSearchShortcut(inputRef);

  const groups = useMemo<CellGroup[]>(
    () =>
      groupSearchRows((live.items ?? []).slice(0, HEADER_SEARCH_ROWS), t, [
        displayLocale,
        locale,
      ]).map((group) => ({
        key: group.key,
        label: group.label,
        items: group.rows.flatMap((item) =>
          searchRowPlayPath(item)
            ? [
                { item, play: false },
                { item, play: true },
              ]
            : [{ item, play: false }],
        ),
      })),
    [live.items, t, displayLocale, locale],
  );
  const total = live.items?.length ?? 0;
  const settled = live.items !== null && !live.loading;
  // Until a new query settles the rows on show answer the previous one, so they
  // cannot be picked.
  const stale = live.loading;

  // Leaving the page (a result was opened, or the user navigated) closes the panel.
  useEffect(() => {
    setOpen(false);
    setQuery("");
  }, [location.key]);

  const panelOpen = open && live.term !== "";
  const hasRows = groups.length > 0;
  // Playable rows are the player's queue in the order they are listed (CORE-1143).
  const queue = useMemo(
    () =>
      playQueueOf(groups.flatMap((group) => group.items.filter((c) => !c.play).map((c) => c.item))),
    [groups],
  );

  function activate(cell: Cell) {
    const { item } = cell;
    if (cell.play) {
      const state: PlayerNavState = { title: item.title };
      // A one-item queue still goes, so it ends an earlier Play all queue.
      if (identity && queue.length > 0) {
        state.queue = queueWindow(queue, item.id);
        state.queueIdentity = identity;
      }
      navigate(searchRowPlayPath(item), { state });
    } else {
      navigate(searchRowOpenPath(item), {
        state: item.kind === "media" ? { title: item.title } : entityLinkState(item, identity),
      });
    }
  }

  function onKeyDown(event: KeyboardEvent<HTMLInputElement>) {
    if (event.key === "Enter" && live.term && (stale || !highlighted.current)) {
      event.preventDefault();
      navigate(searchPagePath(live.term));
    } else if (event.key === "Escape" && !panelOpen && query) {
      event.preventDefault();
      setQuery("");
    }
  }

  let announcement = "";
  if (panelOpen && settled && !live.failed) {
    announcement =
      total > 0
        ? searchCountLabel(t, total, live.resultTerm)
        : t("searchNoResultsFor", { query: live.resultTerm });
  }

  return (
    // On a phone the field rests as a search glyph and, while focused, spans
    // the header row so the query has room.
    <div
      role="search"
      className="relative max-sm:has-[input:focus]:fixed max-sm:has-[input:focus]:inset-x-3 max-sm:has-[input:focus]:top-2 max-sm:has-[input:focus]:z-50"
      ref={setAnchor}
    >
      <Autocomplete.Root
        items={groups}
        mode="none"
        grid
        openOnInputClick
        value={query}
        onValueChange={(value, details) => {
          // Picking a row navigates; only typing changes the query.
          if (details.reason === "input-change" || details.reason === "input-clear") {
            setQuery(value);
          }
        }}
        open={panelOpen}
        onOpenChange={setOpen}
        onItemHighlighted={(cell: Cell | undefined) => {
          highlighted.current = cell;
        }}
        itemToStringValue={(cell: Cell) => cell.item.title}
      >
        <Search
          aria-hidden="true"
          className="pointer-events-none absolute top-1/2 left-2.5 size-4 -translate-y-1/2 text-[var(--text-secondary)]"
        />
        <Autocomplete.Input
          ref={inputRef}
          type="search"
          aria-label={t("search")}
          aria-keyshortcuts="/ Control+K Meta+K"
          spellCheck={false}
          placeholder={t("searchPlaceholder")}
          onKeyDown={onKeyDown}
          className="h-9 w-9 rounded-[var(--radius-control)] border border-[var(--border-subtle)] bg-[var(--bg-raised)] pr-2 pl-8 text-sm text-[var(--text-primary)] outline-none transition-[width,border-color] placeholder:text-transparent max-sm:focus:w-full focus:placeholder:text-[var(--text-muted)] focus-visible:border-[var(--beam-500)] focus-visible:ring-2 focus-visible:ring-[var(--beam-500)]/40 max-sm:focus:h-10 sm:w-56 sm:placeholder:text-[var(--text-muted)] 2xl:w-64 2xl:pr-7 2xl:focus:w-80"
        />
        {!query && (
          <kbd
            aria-hidden="true"
            className="pointer-events-none absolute top-1/2 right-2 hidden -translate-y-1/2 rounded border border-[var(--border-subtle)] px-1.5 text-xs text-[var(--text-muted)] 2xl:block"
          >
            /
          </kbd>
        )}
        <div role="status" aria-live="polite" className="sr-only">
          {announcement}
        </div>
        {/* Rendered inside the header so the viewer register's styles apply. */}
        <Autocomplete.Portal container={anchor}>
          <Autocomplete.Positioner align="end" sideOffset={8} className="z-50 outline-none">
            <Autocomplete.Popup className="w-[min(26rem,calc(100vw-2rem))] rounded-[var(--radius-card)] border border-[var(--border-subtle)] bg-[var(--bg-surface)] shadow-xl outline-none">
              <Autocomplete.List
                aria-label={t("searchResults")}
                aria-busy={stale}
                hidden={!hasRows}
                className={`max-h-[min(28rem,70vh)] overflow-y-auto py-1 ${stale ? "opacity-60" : ""}`}
              >
                {(group: CellGroup) => (
                  <Autocomplete.Group key={group.key} items={group.items}>
                    <Autocomplete.GroupLabel className="px-3 pt-2 pb-1 text-xs font-medium text-[var(--text-muted)]">
                      {group.label}
                    </Autocomplete.GroupLabel>
                    {group.items
                      .filter((cell) => !cell.play)
                      .map((cell) => (
                        <ResultRow
                          key={`${cell.item.kind}:${cell.item.id}`}
                          open={cell}
                          play={group.items.find((other) => other.play && other.item === cell.item)}
                          typeLabel={group.label}
                          playLabel={t("searchPlayItem", { title: cell.item.title })}
                          disabled={stale}
                          onActivate={activate}
                        />
                      ))}
                  </Autocomplete.Group>
                )}
              </Autocomplete.List>
              {!hasRows && (
                <div className="px-4 py-5 text-sm">
                  {live.failed && settled && (
                    <p className="text-[var(--state-danger)]">{t("searchUnavailable")}</p>
                  )}
                  {!live.failed && settled && (
                    <>
                      <p className="font-semibold text-[var(--text-primary)]">
                        {t("searchNoResultsFor", { query: live.resultTerm })}
                      </p>
                      <p className="mt-1 text-[var(--text-secondary)]">
                        {t("searchNoResultsLead")}
                      </p>
                    </>
                  )}
                  {!settled && (
                    <p className="text-[var(--text-secondary)]">{t("searchSearching")}</p>
                  )}
                </div>
              )}
              {(hasRows || (settled && !live.failed)) && (
                <Link
                  to={searchPagePath(live.term)}
                  className="block border-t border-[var(--border-subtle)] px-3 py-2 text-sm font-semibold text-[var(--text-secondary)] hover:text-[var(--text-primary)] focus-visible:text-[var(--text-primary)]"
                >
                  {hasRows ? t("searchSeeAll", { query: live.term }) : t("searchOpenFull")}
                </Link>
              )}
            </Autocomplete.Popup>
          </Autocomplete.Positioner>
        </Autocomplete.Portal>
      </Autocomplete.Root>
    </div>
  );
}

function ResultRow({
  open,
  play,
  typeLabel,
  playLabel,
  disabled,
  onActivate,
}: {
  open: Cell;
  play: Cell | undefined;
  typeLabel: string;
  playLabel: string;
  disabled: boolean;
  onActivate: (cell: Cell) => void;
}) {
  const { item } = open;
  const [imageFailed, setImageFailed] = useState(false);
  const art = artworkSlotURL(item, "poster") || artworkSlotURL(item, "thumb");
  const detail = [searchRowDetail(item), typeLabel].filter(Boolean).join(" · ");
  return (
    <Autocomplete.Row className="flex items-stretch gap-1 px-1">
      <Autocomplete.Item
        value={open}
        disabled={disabled}
        onClick={() => onActivate(open)}
        className="flex min-w-0 flex-1 cursor-pointer items-center gap-3 rounded-[var(--radius-control)] px-2 py-1.5 outline-none data-highlighted:bg-[var(--bg-raised)] data-highlighted:ring-1 data-highlighted:ring-[var(--focus-ring)] data-highlighted:ring-inset"
      >
        <span className="bf-poster block w-10! shrink-0 rounded">
          {art && !imageFailed && (
            <img
              alt=""
              loading="lazy"
              src={artworkBucketURL(art, "w320")}
              onError={() => setImageFailed(true)}
              className="size-full object-cover"
            />
          )}
        </span>
        <span className="flex min-w-0 flex-col">
          <span className="truncate text-sm font-semibold text-[var(--text-primary)]">
            {item.title}
          </span>
          <span className="truncate text-xs text-[var(--text-secondary)]">{detail}</span>
        </span>
      </Autocomplete.Item>
      {play && (
        <Autocomplete.Item
          value={play}
          disabled={disabled}
          aria-label={playLabel}
          onClick={() => onActivate(play)}
          onPointerEnter={preloadPlayer}
          className="flex size-9 cursor-pointer items-center justify-center self-center rounded-full text-[var(--text-secondary)] outline-none hover:text-[var(--text-primary)] data-highlighted:bg-[var(--beam-500)] data-highlighted:text-[var(--text-on-accent,#000)]"
        >
          <Play aria-hidden="true" className="size-4" />
        </Autocomplete.Item>
      )}
    </Autocomplete.Row>
  );
}
