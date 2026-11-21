import { type RefObject, useEffect, useRef, useState } from "react";
import { searchResult } from "@/lib/graphql";
import type { SearchMode, SearchResultItem, SearchResultResponse } from "@/lib/graphql/types";
import { useEngagementEpoch } from "../actions/engagementSync";
import { mergeSearchPages } from "./searchRows";

/** Pause after the last keystroke before asking the server. */
export const SEARCH_DEBOUNCE_MS = 120;

export interface LiveSearch {
  /** The trimmed query the user has typed; "" means no search. */
  term: string;
  /** A request for `term` is waiting or in flight. */
  loading: boolean;
  /** The last settled result set, or null before any settles. */
  items: SearchResultItem[] | null;
  /** The query `items` answers; lags `term` while loading. */
  resultTerm: string;
  /** The mode the server actually served for `items`. */
  servedMode: SearchMode | null;
  queueLockKnown: (item: SearchResultItem) => boolean;
  failed: boolean;
  hasMore?: boolean;
  loadingMore: boolean;
  moreFailed: boolean;
  loadMore: () => void;
}

interface Settled {
  hasMore?: boolean;
  nextOffset: number;
  items: SearchResultItem[] | null;
  resultTerm: string;
  servedMode: SearchMode | null;
  rowReads: ReadonlyMap<string, SearchRowRead>;
  failed: boolean;
}

interface SearchRowRead {
  item: SearchResultItem;
  epoch: number;
  order: number;
  lockKnown: boolean;
}

const NOTHING: Settled = {
  nextOffset: 0,
  items: null,
  resultTerm: "",
  servedMode: null,
  rowReads: new Map(),
  failed: false,
};

// Search as the user types. Each change waits out a short debounce, and both the
// timer and any in-flight request are cancelled when the query moves on, so an
// older, slower response can never replace a newer one.
export function useLiveSearch(query: string, mode: SearchMode): LiveSearch {
  const term = query.trim();
  const [settled, setSettled] = useState<Settled>(NOTHING);
  const [pending, setPending] = useState("");
  const [loadingMore, setLoadingMore] = useState(false);
  const [moreFailed, setMoreFailed] = useState(false);
  const moreController = useRef<AbortController | null>(null);
  const epoch = useEngagementEpoch();
  const seenEpoch = useRef(epoch);
  const requestOrder = useRef(0);

  useEffect(() => {
    moreController.current?.abort();
    moreController.current = null;
    setLoadingMore(false);
    setMoreFailed(false);
    if (!term) {
      setSettled(NOTHING);
      setPending("");
      return;
    }
    const request = `${mode}\n${term}`;
    setPending(request);
    setSettled((prev) => ({ ...prev, rowReads: NOTHING.rowReads }));
    const controller = new AbortController();
    const settle = (response: SearchResultResponse | null, readEpoch: number, order: number) => {
      if (controller.signal.aborted) return;
      setSettled((prev) => {
        if (!response) return { ...NOTHING, items: [], resultTerm: term, failed: true };
        const rowReads = new Map(prev.rowReads);
        recordPage(rowReads, response, readEpoch, order);
        return {
          items: response.items.map((item) => rowReads.get(rowKey(item))?.item ?? item),
          rowReads,
          resultTerm: term,
          servedMode: response.mode,
          failed: false,
          hasMore: response.hasMore,
          nextOffset: response.items.length,
        };
      });
      setPending((current) => (current === request ? "" : current));
    };
    const timer = setTimeout(() => {
      const readEpoch = seenEpoch.current;
      const order = ++requestOrder.current;
      searchResult(term, mode, controller.signal).then(
        (res) => settle(res, readEpoch, order),
        () => settle(null, readEpoch, order),
      );
    }, SEARCH_DEBOUNCE_MS);
    return () => {
      clearTimeout(timer);
      controller.abort();
      moreController.current?.abort();
    };
  }, [term, mode]);

  // A watched, watchlist or rating change leaves the shown rows stale. Ask again
  // and swap each verified row in place, keeping the order on screen.
  const loadedSearch = useRef(settled);
  useEffect(() => {
    loadedSearch.current = settled;
  }, [settled]);
  useEffect(() => {
    if (seenEpoch.current === epoch) return;
    seenEpoch.current = epoch;
    if (!term) return;
    const controller = new AbortController();
    const loaded = loadedSearch.current;
    const beforeRefresh = requestOrder.current;
    async function refreshLoadedPages() {
      const fresh = new Map<string, SearchRowRead>();
      let offset = 0;
      let order = beforeRefresh;
      let requestMode = mode;
      do {
        order = ++requestOrder.current;
        const res = await searchResult(term, requestMode, controller.signal, {
          offset,
          limit: 50,
        }).catch(() => null);
        if (controller.signal.aborted) return;
        if (!res) break;
        recordPage(fresh, res, epoch, order);
        offset += res.items.length;
        if (res.mode !== "keyword" || !res.hasMore || res.items.length === 0) break;
        requestMode = "keyword";
      } while (offset < loaded.nextOffset);
      // Previously loaded rows absent from the refresh stay visible, but an
      // older overlapping page must not restore their queue eligibility.
      if (loaded.resultTerm === term) {
        for (const item of loaded.items ?? []) {
          const key = rowKey(item);
          if (!fresh.has(key)) fresh.set(key, { item, epoch, order, lockKnown: false });
        }
      }
      setSettled((prev) => {
        const rowReads = new Map(fresh);
        // Retain overlapping reads when no newer refresh page covered their row.
        for (const [key, read] of prev.rowReads) {
          if (read.order > (fresh.get(key)?.order ?? beforeRefresh)) rowReads.set(key, read);
        }
        return {
          ...prev,
          rowReads,
          items:
            prev.resultTerm === term
              ? (prev.items?.map((item) => rowReads.get(rowKey(item))?.item ?? item) ?? null)
              : prev.items,
        };
      });
    }
    void refreshLoadedPages();
    return () => controller.abort();
  }, [epoch, term, mode]);

  function loadMore() {
    if (
      pending ||
      loadingMore ||
      !settled.hasMore ||
      settled.resultTerm !== term ||
      settled.servedMode !== "keyword"
    )
      return;
    const controller = new AbortController();
    moreController.current?.abort();
    moreController.current = controller;
    setLoadingMore(true);
    setMoreFailed(false);
    const offset = settled.nextOffset;
    const order = ++requestOrder.current;
    searchResult(term, "keyword", controller.signal, { offset })
      .then(
        (res) => {
          if (controller.signal.aborted) return;
          setSettled((prev) => {
            const rowReads = new Map(prev.rowReads);
            recordPage(rowReads, res, epoch, order);
            return {
              ...prev,
              items: mergeSearchPages(
                prev.items ?? [],
                res.items.map((item) => rowReads.get(rowKey(item))?.item ?? item),
              ),
              rowReads,
              nextOffset: offset + res.items.length,
              hasMore: res.mode === "keyword" && res.hasMore === true && res.items.length > 0,
            };
          });
        },
        () => {
          if (!controller.signal.aborted) setMoreFailed(true);
        },
      )
      .finally(() => {
        if (!controller.signal.aborted) setLoadingMore(false);
      });
  }

  return {
    term,
    loading: pending !== "",
    ...settled,
    queueLockKnown: (item) => {
      // Omitted or unread rows can stay visible, with their lock state unknown.
      const read = settled.rowReads.get(rowKey(item));
      return !pending && settled.resultTerm === term && read?.epoch === epoch && read.lockKnown;
    },
    loadingMore,
    moreFailed,
    loadMore,
  };
}

function recordPage(
  reads: Map<string, SearchRowRead>,
  page: SearchResultResponse,
  epoch: number,
  order: number,
): void {
  for (const item of page.items) {
    const key = rowKey(item);
    const previous = reads.get(key);
    if (!previous || previous.order < order) {
      reads.set(key, { item, epoch, order, lockKnown: page.mode === "keyword" });
    }
  }
}

function rowKey(item: SearchResultItem): string {
  return `${item.kind}:${item.id}`;
}

function isEditable(target: EventTarget | null): boolean {
  if (!(target instanceof HTMLElement)) return false;
  return target.isContentEditable || ["INPUT", "TEXTAREA", "SELECT"].includes(target.tagName);
}

// Focuses the search field on "/" or Cmd/Ctrl-K from anywhere on the page. "/"
// is left alone while the user is typing in another field.
export function useSearchShortcut(field: RefObject<HTMLInputElement | null>): void {
  useEffect(() => {
    function onKeyDown(event: KeyboardEvent) {
      if (event.defaultPrevented || event.altKey) return;
      const command = event.metaKey || event.ctrlKey;
      const palette = command && !event.shiftKey && event.key.toLowerCase() === "k";
      const slash = !command && event.key === "/" && !isEditable(event.target);
      if (!palette && !slash) return;
      const input = field.current;
      if (!input) return;
      event.preventDefault();
      input.focus();
      input.select();
    }
    document.addEventListener("keydown", onKeyDown);
    return () => document.removeEventListener("keydown", onKeyDown);
  }, [field]);
}
