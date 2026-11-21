import { ExternalLink, Pencil, X } from "lucide-react";
import { type Ref, useEffect, useMemo, useRef, useState } from "react";
import { Link } from "react-router";
import { artworkBucketURL, artworkSlotURL } from "@/areas/viewer/lib/artwork";
import { artFrameClass } from "@/areas/viewer/lib/cardArt";
import { entityPath } from "@/areas/viewer/lib/entityAddress";
import { mediaPath } from "@/areas/viewer/lib/mediaAddress";
import {
  searchRowDetail,
  searchRowOpenPath,
  searchRowTypeLabel,
} from "@/areas/viewer/lib/searchRows";
import { Combobox } from "@/components/ui/astryx-primitives";
import { Button } from "@/components/ui/button";
import { searchResult } from "@/lib/graphql";
import type { SearchResultItem } from "@/lib/graphql/types";
import { useI18n } from "@/lib/i18n";
import { cn } from "@/lib/utils";

// EntityPicker (CORE-2915) replaces the raw ID fields in Manage and Console:
// a manager searches by title and picks a row, or pastes an ID they already
// have. Search runs on the existing searchResult field, so the picker only
// offers what the caller's Library scope already lets them see.

/** Which rows the picker offers: Media rows, Entity rows (containers), or both. */
export type EntityPickerKind = "media" | "entity" | "any";

const SEARCH_DEBOUNCE_MS = 120;
const SEARCH_LIMIT = 8;
// searchResult has no kind filter, so a kind-scoped picker pages until it has
// enough rows of that kind or this many rows have been scanned.
const SEARCH_PAGE = 16;
const SEARCH_SCAN_MAX = 64;

type Option = { type: "item"; item: SearchResultItem } | { type: "raw"; ids: string[] };

type SearchState =
  | { status: "idle" }
  | { status: "loading" }
  | { status: "ready"; items: SearchResultItem[]; capped: boolean }
  | { status: "error" };

/** Splits pasted text into IDs on commas and whitespace. */
export function splitIDs(text: string): string[] {
  return text.split(/[\s,]+/).filter(Boolean);
}

function useEntitySearch(term: string, kind: EntityPickerKind): SearchState {
  const [state, setState] = useState<SearchState>({ status: "idle" });
  useEffect(() => {
    const query = term.trim();
    if (!query) {
      setState({ status: "idle" });
      return;
    }
    // Rows from an earlier query are dropped while this one loads, so Enter
    // can never commit a row that does not match the text in the input.
    setState({ status: "loading" });
    const controller = new AbortController();
    const run = async () => {
      const items: SearchResultItem[] = [];
      // capped: the scan stopped at SEARCH_SCAN_MAX with more results left, so
      // an empty list means "refine", not "nothing matches".
      let capped = true;
      for (let offset = 0; offset < SEARCH_SCAN_MAX; offset += SEARCH_PAGE) {
        const res = await searchResult(query, "keyword", controller.signal, {
          offset,
          limit: SEARCH_PAGE,
        });
        const page = res.items ?? [];
        items.push(...page.filter((item) => kind === "any" || item.kind === kind));
        if (items.length >= SEARCH_LIMIT || !res.hasMore || page.length === 0) {
          capped = false;
          break;
        }
      }
      return { items: items.slice(0, SEARCH_LIMIT), capped };
    };
    const timer = setTimeout(() => {
      run().then(
        ({ items, capped }) => {
          if (!controller.signal.aborted) setState({ status: "ready", items, capped });
        },
        () => {
          if (!controller.signal.aborted) setState({ status: "error" });
        },
      );
    }, SEARCH_DEBOUNCE_MS);
    return () => {
      clearTimeout(timer);
      controller.abort();
    };
  }, [term, kind]);
  return state;
}

function optionKey(option: Option): string {
  return option.type === "item" ? `item:${option.item.id}` : `raw:${option.ids.join(",")}`;
}

function Poster({ item, className }: { item: SearchResultItem; className?: string }) {
  const [failed, setFailed] = useState(false);
  const art = artworkSlotURL(item, "poster") || artworkSlotURL(item, "thumb");
  return (
    <span className={cn(artFrameClass(item), "block shrink-0 rounded", className)}>
      {art && !failed && (
        <img
          alt=""
          loading="lazy"
          src={artworkBucketURL(art, "w320")}
          onError={() => setFailed(true)}
          className="size-full object-cover"
        />
      )}
    </span>
  );
}

function useRowDetail() {
  const { t, locale, displayLocale } = useI18n();
  return (item: SearchResultItem) =>
    [searchRowTypeLabel(item, t, [displayLocale, locale]), searchRowDetail(item)]
      .filter(Boolean)
      .join(" · ");
}

interface SearchBoxProps {
  id?: string;
  kind: EntityPickerKind;
  multiple: boolean;
  disabled?: boolean;
  ariaLabel?: string;
  className?: string;
  inputRef?: Ref<HTMLInputElement>;
  onPick: (ids: string[], item?: SearchResultItem) => void;
}

function EntitySearchBox({
  id,
  kind,
  multiple,
  disabled,
  ariaLabel,
  className,
  inputRef,
  onPick,
}: SearchBoxProps) {
  const { t } = useI18n();
  const rowDetail = useRowDetail();
  const [query, setQuery] = useState("");
  const search = useEntitySearch(query, kind);
  const anchorRef = useRef<HTMLDivElement>(null);

  const options = useMemo<Option[]>(() => {
    const rows: Option[] =
      search.status === "ready" ? search.items.map((item) => ({ type: "item", item })) : [];
    const ids = splitIDs(query);
    // A single picker takes one pasted token; text with spaces is a title search.
    if (ids.length > 0 && (multiple || ids.length === 1)) rows.push({ type: "raw", ids });
    return rows;
  }, [search, query, multiple]);

  const status =
    search.status === "loading"
      ? t("entityPickerSearching")
      : search.status === "error"
        ? t("entityPickerUnavailable")
        : search.status === "ready" && search.items.length === 0
          ? t(search.capped ? "entityPickerRefine" : "entityPickerNoMatches")
          : "";

  return (
    <Combobox.Root<Option>
      items={options}
      value={null}
      filter={null}
      autoHighlight
      inputValue={query}
      onInputValueChange={(next) => setQuery(next)}
      itemToStringLabel={(option) => (option.type === "item" ? option.item.title : "")}
      isItemEqualToValue={(a, b) => optionKey(a) === optionKey(b)}
      onValueChange={(option) => {
        if (!option) return;
        if (option.type === "item") onPick([option.item.id], option.item);
        else onPick(option.ids);
        setQuery("");
      }}
      disabled={disabled}
    >
      <div ref={anchorRef} className={cn("w-72 max-w-full", className)}>
        <Combobox.Input
          ref={inputRef}
          id={id}
          aria-label={ariaLabel}
          placeholder={t("entityPickerPlaceholder")}
          autoComplete="off"
          spellCheck={false}
          className="border-input flex h-10 w-full min-w-0 rounded-md border bg-[var(--control-bg)] px-3 py-1 text-base placeholder:text-muted-foreground hover:border-[var(--border-strong)] focus-visible:border-ring disabled:cursor-not-allowed disabled:opacity-50 md:text-sm"
        />
      </div>
      <Combobox.Portal>
        <Combobox.Positioner
          anchor={anchorRef}
          align="start"
          sideOffset={4}
          className="z-50 outline-none"
        >
          <Combobox.Popup className="w-[min(26rem,calc(100vw-2rem))] min-w-(--anchor-width) rounded-[var(--radius-card)] border border-[var(--border-subtle)] bg-[var(--bg-surface)] shadow-xl outline-none">
            <Combobox.Status className="px-3 pt-2 text-xs text-[var(--text-secondary)] empty:hidden">
              {status}
            </Combobox.Status>
            <Combobox.List
              aria-label={t("entityPickerResults")}
              aria-busy={search.status === "loading"}
              className="max-h-80 overflow-y-auto p-1 empty:hidden"
            >
              {(option: Option) =>
                option.type === "item" ? (
                  <Combobox.Item
                    key={optionKey(option)}
                    value={option}
                    className="flex cursor-pointer items-center gap-3 rounded-[var(--radius-control)] px-2 py-1.5 outline-none data-highlighted:bg-[var(--bg-raised)] data-highlighted:ring-1 data-highlighted:ring-[var(--focus-ring)] data-highlighted:ring-inset"
                  >
                    <Poster item={option.item} className="w-8!" />
                    <span className="flex min-w-0 flex-col">
                      <span className="truncate text-sm font-semibold text-[var(--text-primary)]">
                        {option.item.title}
                      </span>
                      <span className="truncate text-xs text-[var(--text-secondary)]">
                        {rowDetail(option.item)}
                      </span>
                    </span>
                  </Combobox.Item>
                ) : (
                  <Combobox.Item
                    key={optionKey(option)}
                    value={option}
                    className="cursor-pointer truncate rounded-[var(--radius-control)] px-2 py-2 font-mono text-xs text-[var(--text-secondary)] outline-none data-highlighted:bg-[var(--bg-raised)] data-highlighted:text-[var(--text-primary)] data-highlighted:ring-1 data-highlighted:ring-[var(--focus-ring)] data-highlighted:ring-inset"
                  >
                    {option.ids.length === 1
                      ? t("entityPickerUseID", { id: option.ids[0] })
                      : t("entityPickerUseIDs", { count: option.ids.length })}
                  </Combobox.Item>
                )
              }
            </Combobox.List>
          </Combobox.Popup>
        </Combobox.Positioner>
      </Combobox.Portal>
    </Combobox.Root>
  );
}

/** Where Open takes an ID the picker has no search row for. */
function rawOpenPath(id: string, kind: EntityPickerKind): string {
  return kind === "entity" ? entityPath(id) : mediaPath("media", id);
}

/** Manage's edit surface for a Media ID; Entities have none yet. */
export function entityEditPath(id: string): string {
  return `/manage/items/${encodeURIComponent(id)}`;
}

function PickedLinks({
  id,
  item,
  kind,
}: {
  id: string;
  item?: SearchResultItem;
  kind: EntityPickerKind;
}) {
  const { t } = useI18n();
  const isMedia = item ? item.kind === "media" : kind === "media";
  // Open goes to the item's detail page, not the player.
  const openPath = item
    ? item.kind === "media"
      ? mediaPath("media", item.id)
      : searchRowOpenPath(item)
    : kind === "any"
      ? ""
      : rawOpenPath(id, kind);
  return (
    <>
      {openPath && (
        <Link
          to={openPath}
          className="inline-flex items-center gap-1 text-xs text-[var(--accent)] hover:underline"
        >
          <ExternalLink aria-hidden="true" className="size-3" />
          {t("entityPickerOpen")}
        </Link>
      )}
      {isMedia && (
        <Link
          to={entityEditPath(id)}
          className="inline-flex items-center gap-1 text-xs text-[var(--accent)] hover:underline"
        >
          <Pencil aria-hidden="true" className="size-3" />
          {t("entityPickerEdit")}
        </Link>
      )}
    </>
  );
}

export interface EntityPickerProps {
  /** The input's id, for a <Label htmlFor>. */
  id?: string;
  value: string;
  onChange: (id: string, item?: SearchResultItem) => void;
  kind?: EntityPickerKind;
  disabled?: boolean;
  /** Accessible name when no visible label points at `id`. */
  ariaLabel?: string;
  className?: string;
  /** Hide the picked summary when the page already shows the item. */
  hideSummary?: boolean;
}

/** Search-and-pick for one Media or Entity ID. */
export function EntityPicker({
  id,
  value,
  onChange,
  kind = "media",
  disabled,
  ariaLabel,
  className,
  hideSummary,
}: EntityPickerProps) {
  const { t } = useI18n();
  const rowDetail = useRowDetail();
  const [picked, setPicked] = useState<SearchResultItem | undefined>();
  const inputRef = useRef<HTMLInputElement>(null);
  const item = picked && picked.id === value ? picked : undefined;

  return (
    <div className="flex flex-col gap-2">
      <EntitySearchBox
        id={id}
        kind={kind}
        multiple={false}
        disabled={disabled}
        ariaLabel={ariaLabel}
        className={className}
        inputRef={inputRef}
        onPick={(ids, row) => {
          setPicked(row);
          onChange(ids[0] ?? "", row);
        }}
      />
      {value && !hideSummary && (
        <div
          className="flex max-w-md items-center gap-3 rounded-[var(--radius-control)] border border-[var(--border-subtle)] bg-[var(--bg-raised)] p-2"
          data-testid="entity-picker-selected"
        >
          {item && <Poster item={item} className="w-8!" />}
          <span className="flex min-w-0 flex-1 flex-col">
            <span className="truncate text-sm font-semibold text-[var(--text-primary)]">
              {item ? item.title : <span className="font-mono text-xs">{value}</span>}
            </span>
            {item && (
              <span className="truncate text-xs text-[var(--text-secondary)]">
                {rowDetail(item)}
              </span>
            )}
            <span className="flex gap-3">
              <PickedLinks id={value} item={item} kind={kind} />
            </span>
          </span>
          <Button
            type="button"
            variant="ghost"
            size="icon"
            aria-label={t("entityPickerClear")}
            disabled={disabled}
            onClick={() => {
              setPicked(undefined);
              onChange("");
              inputRef.current?.focus();
            }}
          >
            <X aria-hidden="true" className="size-4" />
          </Button>
        </div>
      )}
    </div>
  );
}

export interface EntityMultiPickerProps {
  id?: string;
  value: string[];
  onChange: (ids: string[]) => void;
  kind?: EntityPickerKind;
  disabled?: boolean;
  ariaLabel?: string;
  className?: string;
}

/** Search-and-pick for a list of Media or Entity IDs; pasted lists split on commas and spaces. */
export function EntityMultiPicker({
  id,
  value,
  onChange,
  kind = "media",
  disabled,
  ariaLabel,
  className,
}: EntityMultiPickerProps) {
  const { t } = useI18n();
  const [known, setKnown] = useState<Map<string, SearchResultItem>>(() => new Map());
  const inputRef = useRef<HTMLInputElement>(null);

  return (
    <div className="flex flex-col gap-2">
      <EntitySearchBox
        id={id}
        kind={kind}
        multiple
        disabled={disabled}
        ariaLabel={ariaLabel}
        className={className}
        inputRef={inputRef}
        onPick={(ids, row) => {
          if (row) setKnown((prev) => new Map(prev).set(row.id, row));
          const next = [...value];
          for (const picked of ids) if (!next.includes(picked)) next.push(picked);
          onChange(next);
        }}
      />
      {value.length > 0 && (
        <ul className="flex max-w-xl flex-wrap gap-2" data-testid="entity-picker-chips">
          {value.map((picked) => {
            const item = known.get(picked);
            const title = item?.title ?? picked;
            return (
              <li
                key={picked}
                className="flex items-center gap-1 rounded-full border border-[var(--border-subtle)] bg-[var(--bg-raised)] py-0.5 pr-1 pl-3 text-xs"
              >
                <span className={cn("max-w-48 truncate", !item && "font-mono")}>{title}</span>
                <Button
                  type="button"
                  variant="ghost"
                  size="icon"
                  className="size-6"
                  aria-label={t("entityPickerRemove", { title })}
                  disabled={disabled}
                  onClick={() => {
                    onChange(value.filter((other) => other !== picked));
                    inputRef.current?.focus();
                  }}
                >
                  <X aria-hidden="true" className="size-3" />
                </Button>
              </li>
            );
          })}
        </ul>
      )}
    </div>
  );
}
