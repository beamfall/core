import { type RefObject, useEffect, useRef, useState } from "react";
import { searchResult } from "@/lib/graphql";
import type { SearchMode, SearchResultItem } from "@/lib/graphql/types";
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
  queueLockKnown: ReadonlySet<string>;
  failed: boolean;
}

const NOTHING: Settled = {
  nextOffset: 0,
  items: null,
  resultTerm: "",
  servedMode: null,
  queueLockKnown: new Set(),
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
    const controller = new AbortController();
    const settle = (next: Settled) => {
      if (controller.signal.aborted) return;
      setSettled(next);
      setPending((current) => (current === request ? "" : current));
    };
    const timer = setTimeout(() => {
      searchResult(term, mode, controller.signal).then(
        (res) =>
          settle({
            items: res.items,
            queueLockKnown: new Set(res.mode === "keyword" ? res.items.map(rowKey) : []),
            resultTerm: term,
            servedMode: res.mode,
            failed: false,
            hasMore: res.hasMore,
            nextOffset: res.items.length,
          }),
        () => settle({ ...NOTHING, items: [], resultTerm: term, failed: true }),
      );
    }, SEARCH_DEBOUNCE_MS);
    return () => {
      clearTimeout(timer);
      controller.abort();
      moreController.current?.abort();
    };
  }, [term, mode]);

  // A watched, watchlist or rating change leaves the shown rows stale. Ask again
  // and swap each row in place, keeping the order on screen; a failure keeps
  // what was shown and the next change tries again.
  const epoch = useEngagementEpoch();
  const seenEpoch = useRef(epoch);
  const loadedOffset = useRef(0);
  useEffect(() => {
    loadedOffset.current = settled.nextOffset;
  }, [settled.nextOffset]);
  useEffect(() => {
    if (seenEpoch.current === epoch) return;
    seenEpoch.current = epoch;
    if (!term) return;
    const controller = new AbortController();
    const loaded = loadedOffset.current;
    async function refreshLoadedPages() {
      const fresh = new Map<string, SearchResultItem>();
      const queueLockKnown = new Set<string>();
      let offset = 0;
      let requestMode = mode;
      do {
        const res = await searchResult(term, requestMode, controller.signal, { offset, limit: 50 });
        if (controller.signal.aborted) return;
        for (const item of res.items) {
          const key = rowKey(item);
          fresh.set(key, item);
          if (res.mode === "keyword") queueLockKnown.add(key);
          else queueLockKnown.delete(key);
        }
        offset += res.items.length;
        if (res.mode !== "keyword" || !res.hasMore || res.items.length === 0) break;
        requestMode = "keyword";
      } while (offset < loaded);
      setSettled((prev) =>
        prev.items && prev.resultTerm === term
          ? {
              ...prev,
              items: prev.items.map((item) => fresh.get(rowKey(item)) ?? item),
              // An omitted result can stay visible for continuity, but its lock is unknown.
              queueLockKnown,
            }
          : prev,
      );
    }
    void refreshLoadedPages().catch(() => {});
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
    searchResult(term, "keyword", controller.signal, { offset })
      .then(
        (res) => {
          if (controller.signal.aborted) return;
          setSettled((prev) => {
            const queueLockKnown = new Set(prev.queueLockKnown);
            // A reply started before a refresh cannot restore queue eligibility.
            const freshLocks = seenEpoch.current === epoch && res.mode === "keyword";
            for (const item of res.items) {
              if (freshLocks) queueLockKnown.add(rowKey(item));
              else queueLockKnown.delete(rowKey(item));
            }
            return {
              ...prev,
              items: mergeSearchPages(prev.items ?? [], res.items),
              queueLockKnown,
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
    queueLockKnown: (item) =>
      !pending && settled.resultTerm === term && settled.queueLockKnown.has(rowKey(item)),
    loadingMore,
    moreFailed,
    loadMore,
  };
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
