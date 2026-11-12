import { type RefObject, useEffect, useRef, useState } from "react";
import { searchResult } from "@/lib/graphql";
import type { SearchMode, SearchResultItem } from "@/lib/graphql/types";
import { useEngagementEpoch } from "../actions/engagementSync";

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
  failed: boolean;
}

interface Settled {
  items: SearchResultItem[] | null;
  resultTerm: string;
  servedMode: SearchMode | null;
  failed: boolean;
}

const NOTHING: Settled = { items: null, resultTerm: "", servedMode: null, failed: false };

// Search as the user types. Each change waits out a short debounce, and both the
// timer and any in-flight request are cancelled when the query moves on, so an
// older, slower response can never replace a newer one.
export function useLiveSearch(query: string, mode: SearchMode): LiveSearch {
  const term = query.trim();
  const [settled, setSettled] = useState<Settled>(NOTHING);
  const [pending, setPending] = useState("");

  useEffect(() => {
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
          settle({ items: res.items, resultTerm: term, servedMode: res.mode, failed: false }),
        () => settle({ items: [], resultTerm: term, servedMode: null, failed: true }),
      );
    }, SEARCH_DEBOUNCE_MS);
    return () => {
      clearTimeout(timer);
      controller.abort();
    };
  }, [term, mode]);

  // A watched, watchlist or rating change leaves the shown rows stale. Ask again
  // and swap each row in place, keeping the order on screen; a failure keeps
  // what was shown and the next change tries again.
  const epoch = useEngagementEpoch();
  const seenEpoch = useRef(epoch);
  useEffect(() => {
    if (seenEpoch.current === epoch) return;
    seenEpoch.current = epoch;
    if (!term) return;
    const controller = new AbortController();
    searchResult(term, mode, controller.signal).then(
      (res) => {
        if (controller.signal.aborted) return;
        const fresh = new Map(res.items.map((item) => [rowKey(item), item]));
        setSettled((prev) =>
          prev.items && prev.resultTerm === term
            ? { ...prev, items: prev.items.map((item) => fresh.get(rowKey(item)) ?? item) }
            : prev,
        );
      },
      () => {},
    );
    return () => controller.abort();
  }, [epoch, term, mode]);

  return { term, loading: pending !== "", ...settled };
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
