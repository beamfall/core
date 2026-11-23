import { Play, Plus, Search, X } from "lucide-react";
import { type FormEvent, Fragment, useEffect, useId, useRef, useState } from "react";
import { useNavigate } from "react-router";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Skeleton } from "@/components/ui/skeleton";
import { displayString } from "@/lib/display";
import { type PartySession, partyAdd } from "@/lib/graphql";
import type { SourceItem } from "@/lib/graphql/types";
import { useDisplayLocale, useT } from "@/lib/i18n";
import type { StringKey } from "@/strings";
import { karaokeInitial } from "../lib/karaoke";
import {
  DEFAULT_BROWSE_STATE,
  fetchLibraryItems,
  LIBRARY_BROWSE_PAGE,
  type LibraryBrowseState,
  type LibraryItemsField,
  type LibraryItemsLetter,
  limitBrowseQuery,
} from "../lib/libraryBrowse";
import { mediaPath } from "../lib/mediaAddress";
import { formatClockTime } from "../lib/transport";
import type { RunPartyOp } from "../party/usePartyHost";
import { LibraryLetterRail } from "./LibraryLetterRail";

// The karaoke Library's Songbook (CORE-3362): one search across artist and
// title, four orders, songs under letter and artist headings with the A to Z
// rail, and Play or Add for a singer on every song. The server sorts, filters
// and indexes the letters; this view only pages through the result.

type SongSort = "artist" | "title" | "plays" | "added";

const SORTS: { value: SongSort; label: StringKey }[] = [
  { value: "artist", label: "karaokeSortArtist" },
  { value: "title", label: "karaokeSortTitle" },
  { value: "plays", label: "karaokeSortPlays" },
  { value: "added", label: "karaokeSortAdded" },
];

/** The plugin's Sortable text field that holds a song's artist. */
const ARTIST_FIELD = "artist";
const SEARCH_DEBOUNCE_MS = 200;

/**
 * The filter chips (CORE-3369): the plugin's Filterable text fields and the
 * values that get a chip, in chip order. A value the Library lacks gets none.
 */
const FILTERS: { field: string; value: string; label: StringKey }[] = [
  { field: "singers", value: "duet", label: "karaokeFilterDuet" },
  { field: "vocals", value: "guide", label: "karaokeFilterGuide" },
  { field: "class", value: "cdg", label: "karaokeFormatCdg" },
  { field: "class", value: "lrc", label: "karaokeFormatLrc" },
  { field: "class", value: "video", label: "karaokeFormatVideo" },
  { field: "class", value: "ultrastar", label: "karaokeFormatUltrastar" },
];
const FACET_FIELDS = [...new Set(FILTERS.map((filter) => filter.field))];

function facetCount(fields: LibraryItemsField[], field: string, value: string): number {
  const facet = fields.find((entry) => entry.field === field);
  const index = facet ? facet.values.indexOf(value) : -1;
  return index < 0 ? 0 : (facet?.counts[index] ?? 0);
}
const RECENT_SINGERS = 6;

function browseState(sort: SongSort, query: string): LibraryBrowseState {
  if (sort === "plays" || sort === "added") {
    return { ...DEFAULT_BROWSE_STATE, query, sort, direction: "desc" };
  }
  return { ...DEFAULT_BROWSE_STATE, query, sort: "title", direction: "asc" };
}

interface SongWindow {
  /** Offset of the first loaded song in the whole result. */
  start: number;
  items: SourceItem[];
  total: number;
  letters: LibraryItemsLetter[];
}

// The app header's height: a row tucked under it is not "showing".
const STICKY_TOP = 72;

export function KaraokeSongbook({
  libraryID,
  session,
  run,
}: {
  libraryID: string;
  /** The live party, null when none, undefined while it loads. */
  session: PartySession | null | undefined;
  run: RunPartyOp;
}) {
  const t = useT();
  const displayLocale = useDisplayLocale();
  const navigate = useNavigate();
  const searchRef = useRef<HTMLInputElement>(null);
  const listRef = useRef<HTMLOListElement>(null);
  const sentinelRef = useRef<HTMLDivElement>(null);
  const searchID = useId();
  const [text, setText] = useState("");
  const [query, setQuery] = useState("");
  const [sort, setSort] = useState<SongSort>("artist");
  const [result, setResult] = useState<SongWindow | null>(null);
  const [failed, setFailed] = useState(false);
  const [busy, setBusy] = useState(false);
  const [adding, setAdding] = useState<string | null>(null);
  const [retry, setRetry] = useState(0);
  // One value per field; choosing a field's other value replaces it.
  const [filters, setFilters] = useState<Record<string, string>>({});
  const filtered = Object.keys(filters).length > 0;
  // Counted before the field filters and kept across their reloads, so the
  // chips hold still (and keep the focus) while the list changes under them.
  const [fields, setFields] = useState<LibraryItemsField[]>([]);
  // The Library's song count, read with no search or filter. Facets count the
  // whole Library, so a chip whose count equals it filters nothing and is left
  // out; a search's total is not comparable.
  const [librarySongs, setLibrarySongs] = useState<{ libraryID: string; total: number } | null>(
    null,
  );
  const loadRef = useRef<AbortController | null>(null);
  const focusOffsetRef = useRef<number | null>(null);

  const artistOf = (item: SourceItem) =>
    item.subtitle ? displayString(item.subtitle, displayLocale) : "";

  // The search follows typing after a short pause.
  useEffect(() => {
    const timer = window.setTimeout(
      () => setQuery(limitBrowseQuery(text.trim())),
      SEARCH_DEBOUNCE_MS,
    );
    return () => window.clearTimeout(timer);
  }, [text]);

  // "/" puts the cursor in the search, as in most song catalogs.
  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if (event.key !== "/" || event.metaKey || event.ctrlKey || event.altKey) return;
      const target = event.target as HTMLElement | null;
      if (target?.closest("input, textarea, select, [contenteditable='true']")) return;
      event.preventDefault();
      searchRef.current?.focus();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, []);

  function load(offset: number, mode: "replace" | "append" | "prepend") {
    loadRef.current?.abort();
    const controller = new AbortController();
    loadRef.current = controller;
    setBusy(true);
    const limit = LIBRARY_BROWSE_PAGE;
    const start = mode === "prepend" ? Math.max(0, offset - limit) : offset;
    fetchLibraryItems(
      {
        libraryID,
        state: browseState(sort, query),
        fieldSort: sort === "artist" ? ARTIST_FIELD : undefined,
        fieldFilters: filters,
        facetFields: mode === "replace" ? FACET_FIELDS : undefined,
        limit: mode === "prepend" ? offset - start : limit,
        offset: start,
      },
      controller.signal,
    )
      .then((page) => {
        if (controller.signal.aborted) return;
        setFailed(false);
        if (mode === "replace") {
          setFields(page.facets.fields ?? []);
          if (!filtered && !query) setLibrarySongs({ libraryID, total: page.total });
        }
        setResult((current) => {
          if (mode === "replace" || !current) {
            return { start, items: page.items, total: page.total, letters: page.letters };
          }
          return mode === "append"
            ? { ...current, items: [...current.items, ...page.items], total: page.total }
            : { ...current, start, items: [...page.items, ...current.items], total: page.total };
        });
      })
      .catch(() => {
        if (!controller.signal.aborted) setFailed(true);
      })
      .finally(() => {
        if (loadRef.current === controller) setBusy(false);
      });
  }

  // A new search or order starts the result over from the top.
  useEffect(() => {
    setResult(null);
    setAdding(null);
    load(0, "replace");
    return () => loadRef.current?.abort();
  }, [libraryID, query, sort, filters, retry]);

  const end = result ? result.start + result.items.length : 0;
  const more = result !== null && end < result.total;

  // The next page loads as the end of the list comes into view.
  useEffect(() => {
    const sentinel = sentinelRef.current;
    if (!sentinel || !more || busy || failed || typeof IntersectionObserver === "undefined") return;
    const observer = new IntersectionObserver(
      (entries) => {
        if (entries.some((entry) => entry.isIntersecting)) load(end, "append");
      },
      { rootMargin: "600px 0px" },
    );
    observer.observe(sentinel);
    return () => observer.disconnect();
  }, [more, busy, failed, end]);

  // After a letter jump, the first song of that letter takes the focus once it lands.
  useEffect(() => {
    const offset = focusOffsetRef.current;
    if (offset === null || !result) return;
    const row = listRef.current?.querySelector<HTMLElement>(`[data-offset="${offset}"]`);
    if (!row) return;
    focusOffsetRef.current = null;
    row.scrollIntoView({ block: "start" });
    row.querySelector<HTMLElement>("button")?.focus({ preventScroll: true });
  });

  // The rail highlights the letter of the first song showing below the sticky
  // header, so it follows the scroll rather than the loaded window's start.
  // Rows are in scroll order, so a binary search measures a handful of them
  // rather than every row in the window.
  const [topOffset, setTopOffset] = useState<number | null>(null);
  const showRail = (sort === "artist" || sort === "title") && (result?.letters.length ?? 0) > 0;
  useEffect(() => {
    if (!result || !showRail) return;
    let frame = 0;
    const rows = listRef.current?.querySelectorAll<HTMLElement>("li[data-offset]") ?? [];
    const measure = () => {
      frame = 0;
      let low = 0;
      let high = rows.length;
      while (low < high) {
        const mid = (low + high) >> 1;
        if (rows[mid].getBoundingClientRect().bottom > STICKY_TOP) high = mid;
        else low = mid + 1;
      }
      setTopOffset(low < rows.length ? Number(rows[low].dataset.offset) : null);
    };
    const schedule = () => {
      if (!frame) frame = requestAnimationFrame(measure);
    };
    measure();
    window.addEventListener("scroll", schedule, { capture: true, passive: true });
    window.addEventListener("resize", schedule, { passive: true });
    return () => {
      window.removeEventListener("scroll", schedule, { capture: true });
      window.removeEventListener("resize", schedule);
      if (frame) cancelAnimationFrame(frame);
    };
  }, [result, showRail]);

  function jump(offset: number, focusItem: boolean) {
    if (!result) return;
    if (offset >= result.start && offset < end) {
      const row = listRef.current?.querySelector<HTMLElement>(`[data-offset="${offset}"]`);
      row?.scrollIntoView({ block: "start" });
      if (focusItem) row?.querySelector<HTMLElement>("button")?.focus({ preventScroll: true });
      return;
    }
    // The current window stays up until the jump lands, so the letter rail
    // keeps its focus.
    focusOffsetRef.current = focusItem ? offset : null;
    load(offset, "replace");
  }

  // Only the artist order groups: its letters come from the same artist field
  // the server indexes, while a title's index letter follows its sort title,
  // which the wire does not carry.
  const grouped = sort === "artist";
  const headingOf = (item: SourceItem) => (sort === "artist" ? artistOf(item) : item.title);
  // The artist order reads the letter off the song itself, so songs with no
  // artist (sorted last, outside the index) mark nothing. A title's index
  // letter follows its sort title, so that order looks it up in the index, as
  // does an artist initial the index spells differently.
  const topItem = result?.items[(topOffset ?? result.start) - result.start];
  const indexLetter = result
    ? (result.letters.findLast((entry) => entry.offset <= (topOffset ?? result.start))?.letter ??
      null)
    : null;
  const artistLetter = topItem && artistOf(topItem) ? karaokeInitial(artistOf(topItem)) : null;
  const currentLetter = !grouped
    ? indexLetter
    : !artistLetter
      ? null
      : result?.letters.some((entry) => entry.letter === artistLetter)
        ? artistLetter
        : indexLetter;
  const partyHere = session ? session.libraryIDs.includes(libraryID) : false;

  const onSearchSubmit = (event: FormEvent) => {
    event.preventDefault();
    setQuery(limitBrowseQuery(text.trim()));
  };

  return (
    <section className="bf-songbook" aria-labelledby={`${searchID}-title`}>
      <h2 id={`${searchID}-title`} className="sr-only">
        {t("karaokeSongbookPivot")}
      </h2>
      <form role="search" className="bf-songbook-search" onSubmit={onSearchSubmit}>
        <Search className="bf-songbook-search-icon" aria-hidden />
        <label htmlFor={`${searchID}-q`} className="sr-only">
          {t("karaokeSearchLabel")}
        </label>
        <Input
          ref={searchRef}
          id={`${searchID}-q`}
          type="search"
          value={text}
          autoComplete="off"
          placeholder={t("karaokeSearchPlaceholder")}
          onChange={(event) => setText(event.target.value)}
          onKeyDown={(event) => {
            if (event.key === "Escape" && text) {
              event.preventDefault();
              setText("");
            }
          }}
        />
        {text ? (
          <Button
            type="button"
            variant="ghost"
            size="icon"
            className="bf-songbook-search-clear"
            aria-label={t("libraryBrowseSearchClear")}
            onClick={() => {
              setText("");
              searchRef.current?.focus();
            }}
          >
            <X aria-hidden />
          </Button>
        ) : (
          <kbd className="bf-songbook-kbd" aria-hidden>
            /
          </kbd>
        )}
      </form>
      <div className="bf-songbook-bar">
        <p className="bf-songbook-count" aria-live="polite">
          {result
            ? query || filtered
              ? t("karaokeMatchCount", { count: result.total })
              : t("karaokeSongCount", { count: result.total })
            : ""}
        </p>
        <fieldset className="bf-songbook-sorts">
          <legend className="sr-only">{t("karaokeSortLabel")}</legend>
          {SORTS.map((option) => (
            <button
              key={option.value}
              type="button"
              className="bf-songbook-sort"
              aria-pressed={sort === option.value}
              onClick={() => setSort(option.value)}
            >
              {t(option.label)}
            </button>
          ))}
        </fieldset>
      </div>
      {fields.length > 0 && (
        <FilterChips
          fields={fields}
          filters={filters}
          everySong={librarySongs?.libraryID === libraryID ? librarySongs.total : undefined}
          onToggle={(field, value) =>
            setFilters((current) => {
              const next = { ...current };
              if (next[field] === value) delete next[field];
              else next[field] = value;
              return next;
            })
          }
        />
      )}
      {session !== undefined && !partyHere && (
        <p className="bf-songbook-hint">{session ? t("karaokeOtherParty") : t("karaokeNoParty")}</p>
      )}

      {failed && !result ? (
        <div role="alert" className="bf-songbook-empty">
          <p>{t("karaokeLoadError")}</p>
          <Button variant="outline" size="sm" onClick={() => setRetry((n) => n + 1)}>
            {t("tryAgain")}
          </Button>
        </div>
      ) : !result ? (
        <div aria-busy="true" className="flex flex-col gap-2">
          <span className="sr-only">{t("loading")}</span>
          {Array.from({ length: 8 }, (_, i) => (
            <Skeleton key={i} className="h-12 w-full" />
          ))}
        </div>
      ) : result.total === 0 ? (
        <div className="bf-songbook-empty">
          <p>
            {query
              ? t("karaokeNoMatch", { query })
              : filtered
                ? t("karaokeFilterNoMatch")
                : t("karaokeLibraryEmpty")}
          </p>
          {filtered && (
            <Button variant="outline" size="sm" onClick={() => setFilters({})}>
              {t("karaokeFilterClear")}
            </Button>
          )}
        </div>
      ) : (
        <div className="bf-songbook-body">
          <div className="min-w-0 flex-1">
            {result.start > 0 && (
              <Button
                variant="ghost"
                size="sm"
                className="bf-songbook-more"
                disabled={busy}
                onClick={() => load(result.start, "prepend")}
              >
                {t("karaokeShowEarlier")}
              </Button>
            )}
            <ol ref={listRef} className="bf-songbook-list">
              {result.items.map((item, index) => {
                const offset = result.start + index;
                const previous = index > 0 ? result.items[index - 1] : undefined;
                const heading = headingOf(item);
                const letter = karaokeInitial(heading);
                const newLetter =
                  grouped && (!previous || karaokeInitial(headingOf(previous)) !== letter);
                const newArtist =
                  sort === "artist" &&
                  (!previous ||
                    artistOf(previous).toLocaleLowerCase() !== heading.toLocaleLowerCase());
                return (
                  <Fragment key={item.id}>
                    {newLetter && (
                      <li className="bf-songbook-letter" aria-hidden>
                        {letter}
                      </li>
                    )}
                    {newArtist && heading && (
                      <li className="bf-songbook-artist">
                        <h3>{heading}</h3>
                      </li>
                    )}
                    <SongRow
                      item={item}
                      offset={offset}
                      artist={sort === "artist" ? "" : artistOf(item)}
                      canAdd={partyHere && !item.locked}
                      adding={adding === item.id}
                      onPlay={() => navigate(mediaPath("watch", item.id))}
                      onToggleAdd={() => setAdding((id) => (id === item.id ? null : item.id))}
                    >
                      {adding === item.id && session && (
                        <SingerPicker
                          session={session}
                          onCancel={() => setAdding(null)}
                          onAdd={async (who) => {
                            const ok = await run(
                              () => partyAdd({ mediaID: item.id, ...who }),
                              t("partyHostAdded", { title: item.title }),
                            );
                            if (ok) setAdding((id) => (id === item.id ? null : id));
                          }}
                        />
                      )}
                    </SongRow>
                  </Fragment>
                );
              })}
            </ol>
            {more && (
              <div ref={sentinelRef} className="bf-songbook-more">
                <Button
                  variant="ghost"
                  size="sm"
                  disabled={busy}
                  onClick={() => load(end, "append")}
                >
                  {t("karaokeLoadMore")}
                </Button>
              </div>
            )}
            {failed && (
              <p role="alert" className="bf-songbook-hint">
                {t("karaokeLoadError")}
              </p>
            )}
          </div>
          {showRail && (
            <LibraryLetterRail letters={result.letters} current={currentLetter} onJump={jump} />
          )}
        </div>
      )}
    </section>
  );
}

function SongRow({
  item,
  offset,
  artist,
  canAdd,
  adding,
  onPlay,
  onToggleAdd,
  children,
}: {
  item: SourceItem;
  offset: number;
  /** Shown under the title when no artist heading names it. */
  artist: string;
  canAdd: boolean;
  adding: boolean;
  onPlay: () => void;
  onToggleAdd: () => void;
  children?: React.ReactNode;
}) {
  const t = useT();
  const seconds = item.durationSeconds ?? 0;
  return (
    <li className="bf-song" data-offset={offset} data-adding={adding || undefined}>
      <div className="bf-song-row">
        <div className="bf-song-text">
          <span className="bf-song-title">{item.title}</span>
          {artist && <span className="bf-song-artist">{artist}</span>}
        </div>
        {seconds > 0 && <span className="bf-song-time">{formatClockTime(seconds)}</span>}
        <div className="bf-song-actions">
          <Button
            variant="ghost"
            size="sm"
            disabled={item.locked}
            aria-label={t("karaokePlaySong", { title: item.title })}
            onClick={onPlay}
          >
            <Play aria-hidden />
            <span className="bf-song-action-text">{t("partyHostPlay")}</span>
          </Button>
          {canAdd && (
            <Button
              variant={adding ? "secondary" : "outline"}
              size="sm"
              aria-expanded={adding}
              aria-label={t("karaokeAddSong", { title: item.title })}
              onClick={onToggleAdd}
            >
              <Plus aria-hidden />
              <span className="bf-song-action-text">{t("partyHostAdd")}</span>
            </Button>
          )}
        </div>
      </div>
      {children}
    </li>
  );
}

/**
 * Who sings a song: one of the most recent singers in a tap, or a new name.
 * Recent first, so the singer the host just heard is at hand.
 */
function SingerPicker({
  session,
  onAdd,
  onCancel,
}: {
  session: PartySession;
  onAdd: (who: { participantID?: string; participantName?: string }) => Promise<void>;
  onCancel: () => void;
}) {
  const t = useT();
  const id = useId();
  const inputRef = useRef<HTMLInputElement>(null);
  const [name, setName] = useState("");
  const [sending, setSending] = useState(false);
  const recent = [...session.participants]
    .sort((a, b) => b.lastTurnAt.localeCompare(a.lastTurnAt))
    .slice(0, RECENT_SINGERS);

  useEffect(() => {
    if (recent.length === 0) inputRef.current?.focus();
    // Only on open.
  }, []);

  const send = async (who: { participantID?: string; participantName?: string }) => {
    setSending(true);
    try {
      await onAdd(who);
    } finally {
      setSending(false);
    }
  };

  return (
    <div className="bf-singer-picker">
      {recent.length > 0 && (
        <div className="flex flex-col gap-2">
          <p id={`${id}-recent`} className="bf-party-label">
            {t("karaokeRecentSingers")}
          </p>
          <ul className="bf-singer-chips" aria-labelledby={`${id}-recent`}>
            {recent.map((person) => (
              <li key={person.id}>
                <button
                  type="button"
                  className="bf-singer-chip"
                  disabled={sending}
                  onClick={() => void send({ participantID: person.id })}
                >
                  {person.name}
                  {person.waiting > 0 && (
                    <span className="bf-singer-waiting">
                      {t("karaokeSingerWaiting", { count: person.waiting })}
                    </span>
                  )}
                </button>
              </li>
            ))}
          </ul>
        </div>
      )}
      <form
        className="bf-singer-new"
        onSubmit={(event) => {
          event.preventDefault();
          if (name.trim()) void send({ participantName: name.trim() });
        }}
      >
        <label htmlFor={`${id}-name`} className="sr-only">
          {t("partyHostSingerName")}
        </label>
        <Input
          ref={inputRef}
          id={`${id}-name`}
          value={name}
          maxLength={40}
          autoComplete="off"
          placeholder={t("partyHostNewSinger")}
          onChange={(event) => setName(event.target.value)}
          onKeyDown={(event) => {
            if (event.key === "Escape") {
              event.preventDefault();
              onCancel();
            }
          }}
        />
        <Button type="submit" size="sm" disabled={sending || !name.trim()}>
          {t("partyHostAddConfirm")}
        </Button>
        <Button type="button" variant="ghost" size="sm" onClick={onCancel}>
          {t("partyHostCancel")}
        </Button>
      </form>
    </div>
  );
}

function FilterChips({
  fields,
  filters,
  everySong,
  onToggle,
}: {
  fields: LibraryItemsField[];
  filters: Record<string, string>;
  /** The Library's song count, when known. */
  everySong?: number;
  onToggle: (field: string, value: string) => void;
}) {
  const t = useT();
  const chips = FILTERS.map((filter) => ({
    ...filter,
    count: facetCount(fields, filter.field, filter.value),
    pressed: filters[filter.field] === filter.value,
  })).filter((chip) => chip.pressed || (chip.count > 0 && chip.count !== everySong));
  if (chips.length === 0) return null;
  return (
    <fieldset className="bf-songbook-filters">
      <legend className="sr-only">{t("karaokeFilterLabel")}</legend>
      {chips.map((chip) => (
        <button
          key={`${chip.field}:${chip.value}`}
          type="button"
          className="bf-songbook-filter"
          aria-pressed={chip.pressed}
          aria-label={t("karaokeFilterChip", { label: t(chip.label), count: chip.count })}
          onClick={() => onToggle(chip.field, chip.value)}
        >
          {t(chip.label)}
          <span className="bf-songbook-filter-count" aria-hidden>
            {chip.count}
          </span>
        </button>
      ))}
    </fieldset>
  );
}
