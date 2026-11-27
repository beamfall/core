import { type FormEvent, useEffect, useId, useState } from "react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { graphqlErrorCode } from "@/lib/api";
import {
  type FeedSearchResult,
  type FeedSubscription,
  type FeedTrendingResult,
  fetchTrendingFeeds,
  followFeed,
  searchFeeds,
} from "@/lib/graphql";
import { useDisplayLocale, useT } from "@/lib/i18n";
import {
  asFeedURL,
  canDiscoverFeedURL,
  DEFAULT_LATEST_N,
  errorClassKey,
  FOLLOW_POLICIES,
  feedSearchErrorMessage,
  followErrorClass,
  policyHintKey,
  policyLabelKey,
} from "../../lib/feeds";

type SearchState =
  | { kind: "idle" }
  | { kind: "searching" }
  | { kind: "failed"; message?: string }
  | { kind: "results"; query: string; results: FeedSearchResult[] }
  | { kind: "address"; feedURL: string };

// Follow a feed (CORE-2957): search the plugins' feed directories or paste a
// feed address, pick how entries download (Latest 3 by default), then Follow.
// Everything is not offered here: it needs the catalogue size, which the
// followed feed's row shows before confirming.
export function FeedFollowPanel({
  libraryID,
  onFollowed,
}: {
  libraryID: string;
  onFollowed: (subscription: FeedSubscription) => void;
}) {
  const t = useT();
  const id = useId();
  const locale = useDisplayLocale();
  const [trending, setTrending] = useState<FeedTrendingResult[]>([]);
  const [input, setInput] = useState("");
  const [state, setState] = useState<SearchState>({ kind: "idle" });
  const [policy, setPolicy] = useState<string>("latest");
  const [following, setFollowing] = useState<string | null>(null);
  const [followed, setFollowed] = useState<Set<string>>(() => new Set());
  // null: no failure; "" a failure without a classified cause.
  const [followFailed, setFollowFailed] = useState<string | null>(null);

  // Trending is best-effort: no source enabled, or a failure, shows nothing.
  useEffect(() => {
    let live = true;
    const [language, region] = locale.split("-");
    fetchTrendingFeeds(region?.toLowerCase(), language?.toLowerCase())
      .then((results) => live && setTrending(results))
      .catch(() => live && setTrending([]));
    return () => {
      live = false;
    };
  }, [locale]);

  async function submit(event: FormEvent) {
    event.preventDefault();
    const value = input.trim();
    if (!value) return;
    setFollowFailed(null);
    const feedURL = asFeedURL(value);
    if (feedURL && !canDiscoverFeedURL(feedURL)) {
      setState({ kind: "address", feedURL });
      return;
    }
    setState({ kind: "searching" });
    try {
      const results = await searchFeeds(feedURL ?? value);
      if (feedURL && results.length === 0) {
        setState({ kind: "address", feedURL });
        return;
      }
      setState({ kind: "results", query: value, results });
    } catch (err) {
      if (feedURL && (graphqlErrorCode(err) !== "INVALID" || hasOpaqueQuery(feedURL))) {
        // Keep direct Follow available when discovery fails on quota (THROTTLED),
        // the 1 MiB discovery cap, an upstream error, or an opaque query that
        // followFeed still accepts for a valid HTTPS feed URL.
        setState({ kind: "address", feedURL });
        return;
      }
      setState({ kind: "failed", message: feedSearchErrorMessage(err) });
    }
  }

  async function follow(feedURL: string) {
    setFollowing(feedURL);
    setFollowFailed(null);
    try {
      const subscription = await followFeed(feedURL, {
        libraryID,
        downloadPolicy: policy,
        latestN: policy === "latest" ? DEFAULT_LATEST_N : undefined,
      });
      setFollowed((prev) => new Set(prev).add(feedURL));
      onFollowed(subscription);
    } catch (err) {
      setFollowFailed(followErrorClass(err));
    } finally {
      setFollowing(null);
    }
  }

  function followButton(feedURL: string, title: string) {
    const done = followed.has(feedURL);
    return (
      <Button
        type="button"
        size="sm"
        disabled={done || following !== null}
        aria-label={done ? undefined : t("feedsFollowLabel", { title })}
        onClick={() => void follow(feedURL)}
      >
        {done ? t("feedsFollowingNow") : t("feedsFollow")}
      </Button>
    );
  }

  function resultList(results: FeedSearchResult[]) {
    return (
      <ul className="m-0 flex list-none flex-col gap-2 p-0">
        {results.map((result) => {
          const title = result.title || t("feedsUntitled");
          return (
            <li key={result.feedURL} className="flex items-center gap-3">
              <FeedArtwork title={title} src={result.artworkURL} />
              <div className="flex min-w-0 flex-1 flex-col">
                <span className="font-medium">{title}</span>
                {result.description && (
                  <span className="break-words text-sm text-[var(--text-muted)]">
                    {brief(result.description)}
                  </span>
                )}
              </div>
              {followButton(result.feedURL, title)}
            </li>
          );
        })}
      </ul>
    );
  }

  return (
    <section aria-labelledby={`${id}-heading`} className="flex flex-col gap-4">
      <h2 id={`${id}-heading`} className="bf-section-title m-0 text-lg font-semibold">
        {t("feedsFollowHeading")}
      </h2>
      <form role="search" className="flex flex-wrap items-center gap-2" onSubmit={submit}>
        <label htmlFor={`${id}-query`} className="sr-only">
          {t("feedsSearchLabel")}
        </label>
        <Input
          id={`${id}-query`}
          type="search"
          data-unsaved-until-submit
          className="max-w-xl flex-1"
          placeholder={t("feedsSearchLabel")}
          value={input}
          onChange={(event) => setInput(event.target.value)}
        />
        <Button type="submit" variant="outline" disabled={state.kind === "searching"}>
          {t("feedsSearchButton")}
        </Button>
      </form>

      <fieldset className="m-0 flex flex-col gap-1 border-0 p-0">
        <legend className="mb-1 text-sm font-medium">{t("feedsPolicyLabel")}</legend>
        <div className="flex flex-wrap gap-x-5 gap-y-1">
          {FOLLOW_POLICIES.map((value) => (
            <label key={value} className="flex items-center gap-2 text-sm">
              <input
                type="radio"
                name={`${id}-policy`}
                value={value}
                checked={policy === value}
                onChange={() => setPolicy(value)}
              />
              {t(policyLabelKey(value), { count: DEFAULT_LATEST_N })}
            </label>
          ))}
        </div>
        <p className="m-0 text-sm text-[var(--text-muted)]">{t(policyHintKey(policy))}</p>
      </fieldset>

      <div aria-live="polite" className="flex flex-col gap-2">
        {state.kind === "searching" && (
          <p className="m-0 text-sm text-[var(--text-muted)]">{t("feedsSearching")}</p>
        )}
        {state.kind === "failed" && (
          <p role="alert" className="m-0 text-sm text-[var(--state-danger)]">
            {state.message || t("feedsSearchFailed")}
          </p>
        )}
        {state.kind === "results" && state.results.length === 0 && (
          <p className="m-0 text-sm text-[var(--text-muted)]">
            {t("feedsNoResults", { query: state.query })}
          </p>
        )}
        {followFailed !== null && (
          <p role="alert" className="m-0 text-sm text-[var(--state-danger)]">
            {t("feedsFollowFailed")}
            {followFailed && <> {t(errorClassKey(followFailed))}.</>}
          </p>
        )}
      </div>

      {state.kind === "address" && (
        <ul className="m-0 flex list-none flex-col gap-2 p-0">
          <li className="flex items-center gap-3">
            <FeedArtwork title={t("feedsPastedURL")} />
            <div className="flex min-w-0 flex-1 flex-col">
              <span className="font-medium">{t("feedsPastedURL")}</span>
              <span className="break-all text-sm text-[var(--text-muted)]">{state.feedURL}</span>
            </div>
            {followButton(state.feedURL, t("feedsPastedURL"))}
          </li>
        </ul>
      )}
      {state.kind === "results" && state.results.length > 0 && resultList(state.results)}
      {state.kind === "idle" && trending.length > 0 && (
        <section aria-labelledby={`${id}-trending`} className="flex flex-col gap-2">
          <h3 id={`${id}-trending`} className="m-0 text-base font-semibold">
            {t("feedsTrendingHeading")}
          </h3>
          {resultList(trending)}
        </section>
      )}
    </section>
  );
}

// The page's content policy admits only same-origin images, so a directory's
// remote artwork shows as a lettered tile until the server proxies it.
function FeedArtwork({ title, src }: { title: string; src?: string | null }) {
  if (src?.startsWith("/")) {
    return <img src={src} alt="" className="size-12 shrink-0 rounded-md object-cover" />;
  }
  return (
    <span
      aria-hidden
      className="flex size-12 shrink-0 items-center justify-center rounded-md bg-[var(--bg-raised)] text-lg font-semibold text-[var(--text-secondary)]"
    >
      {title.trim().charAt(0).toUpperCase()}
    </span>
  );
}

function hasOpaqueQuery(feedURL: string): boolean {
  try {
    return new URL(feedURL).search.includes(";");
  } catch {
    return false;
  }
}

/** A search result's description cut to a few lines' worth at a word break,
 * so a long publisher blurb does not push the next result off screen. */
function brief(text: string, limit = 240): string {
  const flat = text.replace(/\s+/g, " ").trim();
  if (flat.length <= limit) return flat;
  const cut = flat.slice(0, limit);
  const space = cut.lastIndexOf(" ");
  return `${(space > limit / 2 ? cut.slice(0, space) : cut).trimEnd()}…`;
}
