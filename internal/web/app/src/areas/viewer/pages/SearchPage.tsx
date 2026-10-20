import { type FormEvent, useEffect, useRef, useState } from "react";
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
import {
  fetchClientPrefs,
  fetchSemanticCapability,
  searchResult as searchResultQuery,
} from "@/lib/graphql";
import type {
  ClientPref,
  SearchMode,
  SearchResultItem,
  SemanticCapabilityInfo,
} from "@/lib/graphql/types";
import { useT } from "@/lib/i18n";
import type { StringKey } from "@/strings";
import { MediaItemCard } from "../components/MediaItemCard";
import { ViewerActions } from "../components/ViewerActions";
import { availableModes } from "../lib/searchModes";

const MODE_LABEL: Record<SearchMode, StringKey> = {
  keyword: "searchModeKeyword",
  semantic: "searchModeSemantic",
  recommend: "searchModeRecommend",
  // auto is never user-selectable; it degrades to keyword/semantic and is only
  // ever echoed back in a response — labeled as keyword if it ever surfaced.
  auto: "searchModeKeyword",
};

// Viewer search (/search), now mode-aware (ADR-0083 §30). Keyword is the always-
// available FTS floor; semantic and recommend appear in the toggle ONLY when the
// server reports them available to this reveal-filtered session (semanticAvailable
// over semanticCapability; recommend over the recommend.opt-in clientPref). When a
// non-keyword mode is requested but the server serves keyword (plugin absent or
// index empty), we say so honestly without failing. Restricted-result hints are
// ignored here: no-access must collapse into the same empty state as no matches.
export function SearchPage() {
  const t = useT();
  const [query, setQuery] = useState("");
  const [mode, setMode] = useState<SearchMode>("keyword");
  const [caps, setCaps] = useState<SemanticCapabilityInfo[]>([]);
  const [prefs, setPrefs] = useState<ClientPref[]>([]);
  const [results, setResults] = useState<SearchResultItem[] | null>(null);
  const [notice, setNotice] = useState<StringKey | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const searchSeq = useRef(0);

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

  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    const seq = searchSeq.current + 1;
    searchSeq.current = seq;
    const text = query.trim();
    setError(null);
    setNotice(null);
    if (!text) {
      setResults(null);
      return;
    }
    setBusy(true);
    try {
      const res = await searchResultQuery(text, mode);
      if (seq !== searchSeq.current) return;
      setResults(res.items);
      // 4-state honesty: requested non-keyword but keyword was served → the
      // plugin is absent or the index is empty; say so without failing.
      if (mode !== "keyword" && res.mode === "keyword") {
        setNotice("searchModeFellBackToKeyword");
      }
    } catch {
      if (seq !== searchSeq.current) return;
      setError(t("searchUnavailable"));
      setResults([]);
    } finally {
      if (seq === searchSeq.current) setBusy(false);
    }
  }

  function clear() {
    searchSeq.current += 1;
    setQuery("");
    setResults(null);
    setError(null);
    setNotice(null);
  }

  return (
    <AppShell actions={<ViewerActions />}>
      <div className="mx-auto flex max-w-3xl flex-col gap-4">
        <h1 className="bf-page-title">{t("searchResults")}</h1>
        <form onSubmit={onSubmit} className="flex flex-wrap gap-2">
          <Input
            type="search"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder={t("searchPlaceholder")}
            aria-label={t("search")}
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
          <Button type="submit" disabled={busy}>
            {t("search")}
          </Button>
          <Button type="button" variant="outline" onClick={clear}>
            {t("clear")}
          </Button>
        </form>
        {error && (
          <p className="text-sm text-[var(--red-500)]" role="alert">
            {error}
          </p>
        )}
        {notice && !error && (
          <p className="text-sm text-[var(--text-secondary)]" role="status">
            {t(notice)}
          </p>
        )}
        {results === null && !error && (
          <SearchEmpty title={t("searchPrompt")} lead={t("searchPromptLead")} />
        )}
        {results !== null && !error && results.length === 0 && (
          <SearchEmpty title={t("searchEmpty")} lead={t("searchEmptyLead")} />
        )}
        {results && results.length > 0 && (
          <ul className="bf-grid">
            {results.map((item, index) => (
              <MediaItemCard
                key={`${item.id}-${index}`}
                item={item}
                posInSet={index + 1}
                setSize={results.length}
              />
            ))}
          </ul>
        )}
      </div>
    </AppShell>
  );
}

// A calm centered zero-state for the search surface — reuses the Viewer's
// `.bf-empty` card (chrome.css, "never a grey void") so the pre-query and
// no-matches states read as intentional rather than a floating box over black.
function SearchEmpty({ title, lead }: { title: string; lead: string }) {
  return (
    <div className="bf-empty flex flex-col gap-2" role="status">
      <p className="text-lg font-semibold text-[var(--text-primary)]">{title}</p>
      <p className="text-sm text-[var(--text-secondary)]">{lead}</p>
    </div>
  );
}
