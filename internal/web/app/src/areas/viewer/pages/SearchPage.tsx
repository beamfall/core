import { type ReactNode, useEffect, useMemo, useRef, useState } from "react";
import { useSearchParams } from "react-router";
import { AppShell } from "@/components/chrome/AppShell";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { fetchClientPrefs, fetchSemanticCapability } from "@/lib/graphql";
import type { ClientPref, SearchMode, SemanticCapabilityInfo } from "@/lib/graphql/types";
import { useDisplayLocale, useI18n } from "@/lib/i18n";
import type { StringKey } from "@/strings";
import { MediaItemCard } from "../components/MediaItemCard";
import { ViewerActions } from "../components/ViewerActions";
import { availableModes } from "../lib/searchModes";
import { groupSearchRows, searchCountLabel, searchRowDetail } from "../lib/searchRows";
import { useLiveSearch, useSearchShortcut } from "../lib/useLiveSearch";
import { playQueueOf } from "../lib/watchQueue";

const MODE_LABEL: Record<SearchMode, StringKey> = {
  keyword: "searchModeKeyword",
  semantic: "searchModeSemantic",
  recommend: "searchModeRecommend",
  // auto is never user-selectable; it degrades to keyword/semantic and is only
  // ever echoed back in a response — labeled as keyword if it ever surfaced.
  auto: "searchModeKeyword",
};

// Viewer search (/search), mode-aware (ADR-0083 §30). The query lives in `?q=`
// so a search can be reloaded, shared and reached from the header field, and
// results update as the user types. Keyword is the always-available FTS floor;
// semantic and recommend appear in the toggle ONLY when the server reports them
// available to this reveal-filtered session (semanticAvailable over
// semanticCapability; recommend over the recommend.opt-in clientPref). When a
// non-keyword mode is requested but the server serves keyword (plugin absent or
// index empty), we say so honestly without failing. Restricted-result hints are
// ignored here: no-access must collapse into the same empty state as no matches.
export function SearchPage() {
  const { t, locale } = useI18n();
  const displayLocale = useDisplayLocale();
  const [params, setParams] = useSearchParams();
  const query = params.get("q") ?? "";
  const [mode, setMode] = useState<SearchMode>("keyword");
  const [caps, setCaps] = useState<SemanticCapabilityInfo[]>([]);
  const [prefs, setPrefs] = useState<ClientPref[]>([]);
  const inputRef = useRef<HTMLInputElement | null>(null);
  const live = useLiveSearch(query, mode);
  useSearchShortcut(inputRef);

  // Probe availability once. Either probe failing simply leaves that mode out of
  // the toggle (keyword stays available), so the surface degrades gracefully.
  useEffect(() => {
    let alive = true;
    void Promise.allSettled([fetchSemanticCapability(), fetchClientPrefs()]).then(([c, p]) => {
      if (!alive) return;
      if (c.status === "fulfilled") setCaps(c.value);
      if (p.status === "fulfilled") setPrefs(p.value);
    });
    return () => {
      alive = false;
    };
  }, []);

  const modes = availableModes(caps, prefs);
  // If the selected mode is no longer offered (caps/prefs changed), fall back to
  // keyword so the toggle and the request never disagree.
  useEffect(() => {
    if (!modes.includes(mode)) setMode("keyword");
  }, [modes, mode]);

  function setQuery(next: string) {
    // Replace, so Back leaves the search instead of undoing it a letter at a time.
    setParams(next ? { q: next } : {}, { replace: true });
  }

  function clear() {
    setQuery("");
    inputRef.current?.focus();
  }

  const groups = useMemo(
    () => groupSearchRows(live.items ?? [], t, [displayLocale, locale]),
    [live.items, t, displayLocale, locale],
  );
  // Playable results are the player's queue in the order they are shown (CORE-1143).
  const queue = useMemo(() => playQueueOf(groups.flatMap((group) => group.rows)), [groups]);
  const total = live.items?.length ?? 0;
  const answered = live.items !== null && !live.failed;
  // 4-state honesty: requested non-keyword but keyword was served → the plugin
  // is absent or the index is empty; say so without failing.
  // Only once the current query and mode have been answered, not while they load.
  const fellBack = answered && !live.loading && mode !== "keyword" && live.servedMode === "keyword";

  return (
    <AppShell actions={<ViewerActions />}>
      <div className="mx-auto flex max-w-3xl flex-col gap-4">
        <h1 className="bf-page-title">{t("search")}</h1>
        <form onSubmit={(e) => e.preventDefault()} className="flex flex-wrap gap-2">
          <Input
            ref={inputRef}
            type="search"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder={t("searchPlaceholder")}
            aria-label={t("search")}
            aria-keyshortcuts="/ Control+K Meta+K"
            autoFocus
            className="min-w-48 flex-1"
          />
          {modes.length > 1 && (
            <Select value={mode} onValueChange={(v) => setMode(v as SearchMode)}>
              <SelectTrigger className="w-40" aria-label={t("searchModeLabel")}>
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {modes.map((m) => (
                  <SelectItem key={m} value={m}>
                    {t(MODE_LABEL[m])}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          )}
          {query && (
            <Button type="button" variant="outline" onClick={clear}>
              {t("searchClear")}
            </Button>
          )}
        </form>
        {live.failed && (
          <p className="text-sm text-[var(--state-danger)]" role="alert">
            {t("searchUnavailable")}
          </p>
        )}
        {fellBack && (
          <p className="text-sm text-[var(--text-secondary)]" role="status">
            {t("searchModeFellBackToKeyword")}
          </p>
        )}
        {!live.term && <SearchEmpty title={t("searchPrompt")} lead={t("searchPromptLead")} />}
        {live.term && answered && total === 0 && (
          <SearchEmpty
            title={t("searchNoResultsFor", { query: live.resultTerm })}
            lead={t("searchNoResultsLead")}
            action={
              <Button type="button" variant="outline" onClick={clear}>
                {t("searchClear")}
              </Button>
            }
          />
        )}
        {live.term && answered && total > 0 && (
          <section aria-labelledby="search-results-heading" className="flex flex-col gap-4">
            <div>
              <h2 id="search-results-heading" className="sr-only">
                {t("searchResults")}
              </h2>
              <p className="text-sm text-[var(--text-secondary)]" role="status">
                {searchCountLabel(t, total, live.resultTerm)}
              </p>
            </div>
            {groups.map((group) => (
              <section key={group.key} aria-label={group.label} className="flex flex-col gap-2">
                <h3 className="text-base font-semibold text-[var(--text-primary)]">
                  {group.label}
                </h3>
                <ul className="bf-grid">
                  {group.rows.map((item, index) => (
                    <MediaItemCard
                      key={`${item.kind}:${item.id}`}
                      // The card's second line carries the year and Library so
                      // same-titled results can be told apart.
                      item={{ ...item, summary: searchRowDetail(item) || item.summary }}
                      posInSet={index + 1}
                      setSize={group.rows.length}
                      queue={queue}
                    />
                  ))}
                </ul>
              </section>
            ))}
          </section>
        )}
      </div>
    </AppShell>
  );
}

// A calm centered zero-state for the search surface — reuses the Viewer's
// `.bf-empty` card (chrome.css, "never a grey void") so the pre-query and
// no-matches states read as intentional rather than a floating box over black.
function SearchEmpty({ title, lead, action }: { title: string; lead: string; action?: ReactNode }) {
  return (
    <div className="bf-empty flex flex-col gap-2" role="status">
      <p className="text-lg font-semibold text-[var(--text-primary)]">{title}</p>
      <p className="text-sm text-[var(--text-secondary)]">{lead}</p>
      {action && <div className="mt-2">{action}</div>}
    </div>
  );
}
