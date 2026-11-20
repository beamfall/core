import { useEffect, useMemo, useState } from "react";
import { useNavigate } from "react-router";
import { Button } from "@/components/ui/button";
import { getIdentityEpochSignal, isAuthRejection } from "@/lib/api";
import { displayString } from "@/lib/display";
import type { FieldFilters, Section, SourceItem } from "@/lib/graphql/types";
import { fetchHomeLibraries, type HomeLibrary } from "@/lib/graphql/viewer";
import { useDisplayLocale, useT } from "@/lib/i18n";
import { useViewerIdentity } from "@/lib/session";
import { clearViewerCache, readViewerCache, writeViewerCache } from "@/lib/viewerCache";
import { useEngagementEpoch } from "../actions/engagementSync";
import { artworkSlotURL } from "../lib/artwork";
import { groupByContainer } from "../lib/containerGroups";
import { fetchSectionItems } from "../lib/sections";
import {
  SECTION_LOAD_TIMEOUT_MS,
  type SectionHydrationState,
  SectionSkeleton,
} from "./HydratedSection";
import { SectionHeading, SectionView, type SeeAllTarget } from "./SectionView";

interface HomeRecentData {
  libraries: readonly HomeLibrary[];
  shelves: ReadonlyMap<string, SourceItem[]>;
  fallbackItems: SourceItem[];
}

interface HomeRecentFeed {
  data: HomeRecentData;
  pending: number;
  failed: boolean;
}

const EMPTY_DATA: HomeRecentData = {
  libraries: [],
  shelves: new Map(),
  fallbackItems: [],
};

// Keep one snapshot per identity so a new visibility or authorization result
// supersedes earlier Home data, including data for a different filter.
const HOME_RECENT_CACHE_KEY = "home-recent";

function cachedRecentData(identity: string | null, scope: string): HomeRecentData {
  const cached = readViewerCache<{ scope: string; data: HomeRecentData }>(
    identity,
    HOME_RECENT_CACHE_KEY,
  );
  return cached?.scope === scope ? cached.data : EMPTY_DATA;
}

function heroCandidates(items: SourceItem[]): SourceItem[] {
  const unlocked = items.filter((item) => !item.locked);
  // The shelf can show a container poster even when its individual items lack art.
  return [...unlocked, ...groupByContainer(unlocked, () => "")];
}

export function useHomeRecent(
  section: Section | undefined,
  extraFilters: FieldFilters | null,
  attempt: number,
  onConnectionLost: () => void,
) {
  const identity = useViewerIdentity();
  const engagementEpoch = useEngagementEpoch();
  const scope = JSON.stringify([section, extraFilters]);
  // Revalidate on engagement changes without remounting the current hero.
  const epoch = JSON.stringify([identity, scope, attempt]);
  const request = useMemo(() => ({ section, extraFilters, epoch }), [section, extraFilters, epoch]);
  const [result, setResult] = useState<{ request: typeof request; feed: HomeRecentFeed }>();

  useEffect(() => {
    const { section } = request;
    if (!section) return;
    const controller = new AbortController();
    const identitySignal = getIdentityEpochSignal();
    const signal = identitySignal
      ? AbortSignal.any([controller.signal, identitySignal])
      : controller.signal;
    const context = { libraryID: "", entityID: "", extraFilters: request.extraFilters, home: true };
    let feed = { data: cachedRecentData(identity, scope), pending: 1, failed: false };
    const publish = (next: HomeRecentFeed) => {
      feed = next;
      setResult({ request, feed });
    };
    const stop = () => {
      window.clearTimeout(timeout);
      controller.abort();
    };
    const timeout = window.setTimeout(() => {
      if (signal.aborted) return;
      stop();
      publish({ ...feed, pending: 0, failed: true });
      onConnectionLost();
    }, SECTION_LOAD_TIMEOUT_MS);
    const settle = (next: HomeRecentFeed, pending = feed.pending - 1) => {
      if (!pending) window.clearTimeout(timeout);
      publish({ ...next, pending });
    };
    const save = (data: HomeRecentData, pending?: number) => {
      writeViewerCache(identity, HOME_RECENT_CACHE_KEY, { scope, data });
      settle({ ...feed, data }, pending);
    };
    const fail = (error: unknown) => {
      if (signal.aborted) return;
      if (isAuthRejection(error)) {
        stop();
        clearViewerCache();
        publish({ data: EMPTY_DATA, pending: 0, failed: true });
      } else {
        settle({ ...feed, failed: true });
      }
      onConnectionLost();
    };
    fetchHomeLibraries(signal).then((libraries) => {
      if (signal.aborted) return;
      if (libraries === null) {
        // Core already applies ShowOnHome to its aggregate read.
        save({ ...EMPTY_DATA, fallbackItems: feed.data.fallbackItems }, 1);
        fetchSectionItems(section, context, signal).then((fallbackItems) => {
          if (!signal.aborted) save({ ...EMPTY_DATA, fallbackItems });
        }, fail);
        return;
      }
      const visible = libraries.filter((library) => library.showOnHome !== false);
      const visibleIDs = new Set(visible.map(({ id }) => id));
      const shelves = new Map([...feed.data.shelves].filter(([id]) => visibleIDs.has(id)));
      save({ ...EMPTY_DATA, libraries: visible, shelves }, visible.length);
      for (const { id } of visible) {
        fetchSectionItems(section, { ...context, libraryID: id }, signal).then((items) => {
          if (signal.aborted) return;
          const shelves = new Map(feed.data.shelves);
          if (items.length) shelves.set(id, items);
          else shelves.delete(id);
          save({ ...feed.data, shelves });
        }, fail);
      }
    }, fail);
    publish(feed);
    return stop;
  }, [request, identity, scope, engagementEpoch, onConnectionLost]);

  return useMemo(() => {
    const { data, pending, failed } =
      result?.request === request
        ? result.feed
        : {
            data: section ? cachedRecentData(identity, scope) : EMPTY_DATA,
            pending: Number(Boolean(section)),
            failed: false,
          };
    const { libraries, shelves, fallbackItems } = data;
    const state: SectionHydrationState =
      shelves.size || fallbackItems.length
        ? "visible"
        : pending
          ? "loading"
          : failed
            ? "failed"
            : "empty";
    return {
      state,
      libraries,
      shelves,
      fallbackItems,
      heroItems: fallbackItems.length
        ? heroCandidates(fallbackItems)
        : libraries.flatMap(({ id }) => heroCandidates(shelves.get(id) ?? [])),
      pending: Boolean(pending),
      epoch: request.epoch,
    };
  }, [request, result, section, identity, scope]);
}

export function HomeRecentShelves({
  section,
  libraries,
  shelves,
  fallbackItems,
  pending,
  maxShelves,
  buildSeeAll,
  navigationID,
}: {
  section: Section;
  libraries: readonly HomeLibrary[];
  shelves: ReadonlyMap<string, SourceItem[]>;
  fallbackItems: SourceItem[];
  pending: boolean;
  maxShelves: number;
  buildSeeAll?: (section: Section, libraryID?: string) => SeeAllTarget | undefined;
  navigationID?: string;
}) {
  const t = useT();
  const locale = useDisplayLocale();
  const navigate = useNavigate();
  const [requestedPage, setPage] = useState(0);
  const populated = libraries.filter(({ id }) => shelves.has(id));
  const lastPage = Math.max(0, Math.ceil(populated.length / maxShelves) - 1);
  const page = Math.min(requestedPage, lastPage);
  if (!pending && requestedPage !== page) setPage(page);
  const pageLibraries = populated.slice(page * maxShelves, (page + 1) * maxShelves);
  const seeAll = buildSeeAll?.(section);
  const groupItems = (items: SourceItem[]) =>
    groupByContainer(items, (count) => t("cardNewCount", { count }));
  if (fallbackItems.length) {
    return (
      <SectionView
        section={section}
        items={groupItems(fallbackItems)}
        seeAll={seeAll}
        navigationID={navigationID}
        intrinsicArtwork
      />
    );
  }
  if (!shelves.size) return pending ? <SectionSkeleton hero={false} /> : null;
  return (
    <section className="flex flex-col gap-6">
      <SectionHeading
        title={displayString(section.title, locale)}
        onSeeAll={seeAll ? () => navigate(seeAll.href, { state: seeAll.state }) : undefined}
      />
      {lastPage > 0 && (
        <div className="flex flex-wrap gap-2">
          <Button
            type="button"
            variant="outline"
            size="sm"
            aria-disabled={pending || page === 0}
            className="aria-disabled:opacity-50"
            onClick={() => {
              if (!pending && page > 0) setPage(page - 1);
            }}
          >
            {t("homePreviousShelves")}
          </Button>
          <Button
            type="button"
            variant="outline"
            size="sm"
            aria-disabled={pending || page === lastPage}
            className="aria-disabled:opacity-50"
            onClick={() => {
              if (!pending && page < lastPage) setPage(page + 1);
            }}
          >
            {t("homeNextShelves")}
          </Button>
        </div>
      )}
      {pageLibraries.map((library) => {
        const items = shelves.get(library.id);
        if (!items) return null;
        const shelf = { ...section, id: `${section.id}:${library.id}`, title: library.title };
        return (
          <SectionView
            key={library.id}
            section={shelf}
            items={groupItems(items)}
            seeAll={buildSeeAll?.(shelf, library.id)}
            navigationID={navigationID}
            headingLevel="h3"
            intrinsicArtwork
          />
        );
      })}
    </section>
  );
}

const HOME_ARTWORK_TIMEOUT_MS = 3000;
const HERO_SLOTS = ["backdrop", "poster", "thumb"] as const;

interface ArtworkAttempt {
  itemID: string;
  url: string;
}

export function useHomeArtwork(items: readonly SourceItem[], epoch: string) {
  const [images, setImages] = useState<{
    epoch: string;
    failed: ReadonlySet<string>;
    attempt?: ArtworkAttempt;
    ready: boolean;
  }>(() => ({ epoch, failed: new Set(), ready: false }));
  const { attempt, ready } = images;
  useEffect(() => {
    if (!attempt || ready) return;
    const timeout = window.setTimeout(() => reject(attempt, "timeout"), HOME_ARTWORK_TIMEOUT_MS);
    return () => window.clearTimeout(timeout);
  }, [attempt, ready]);

  function reject(candidate: ArtworkAttempt, reason: "timeout" | "error") {
    setImages((current) =>
      current.attempt !== candidate || (reason === "timeout" && current.ready)
        ? current
        : {
            epoch: current.epoch,
            failed: new Set(current.failed).add(candidate.url),
            ready: false,
          },
    );
  }

  if (images.epoch !== epoch) {
    setImages({ epoch, failed: new Set(), ready: false });
    return;
  }
  if (attempt) {
    // Keep the same image and action while earlier shelves settle. Match
    // against live candidates so hidden, locked, and removed items drop out.
    const item = items.find(
      (item) =>
        item.id === attempt.itemID &&
        HERO_SLOTS.some((slot) => artworkSlotURL(item, slot) === attempt.url),
    );
    if (item) {
      return {
        item,
        artwork: {
          url: attempt.url,
          ready,
          onLoad: () =>
            setImages((current) =>
              current.attempt === attempt ? { ...current, ready: true } : current,
            ),
          onError: () => reject(attempt, "error"),
        },
      };
    }
  }
  for (const item of items) {
    for (const slot of HERO_SLOTS) {
      const url = artworkSlotURL(item, slot);
      if (url && !images.failed.has(url)) {
        setImages({ ...images, attempt: { itemID: item.id, url }, ready: false });
        return;
      }
    }
  }
  if (attempt) setImages({ ...images, attempt: undefined, ready: false });
}
