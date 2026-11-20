import { useEffect, useMemo, useState } from "react";
import { useNavigate } from "react-router";
import { isAuthRejection } from "@/lib/api";
import { displayString } from "@/lib/display";
import type { FieldFilters, LibrarySummary, Section, SourceItem } from "@/lib/graphql/types";
import { useDisplayLocale, useT } from "@/lib/i18n";
import { useViewerIdentity } from "@/lib/session";
import { clearViewerCache } from "@/lib/viewerCache";
import { useEngagementEpoch } from "../actions/engagementSync";
import { artworkSlotURL } from "../lib/artwork";
import { groupByContainer } from "../lib/containerGroups";
import { fetchSectionItems } from "../lib/sections";
import { Hero } from "./Hero";
import {
  SECTION_LOAD_TIMEOUT_MS,
  type SectionHydrationState,
  SectionSkeleton,
} from "./HydratedSection";
import { SectionHeading, SectionView, type SeeAllTarget } from "./SectionView";

const HOME_WINDOW = { limit: 200, offset: 0 };

interface HomeRecentFeed {
  aggregate: SourceItem[];
  scoped: ReadonlyMap<string, SourceItem[]>;
  pending: number;
  failed: boolean;
}

const EMPTY_FEED: HomeRecentFeed = { aggregate: [], scoped: new Map(), pending: 0, failed: false };
const itemKey = (item: SourceItem) => `${item.kind}:${item.id}`;

export function useHomeRecent(
  section: Section | undefined,
  libraries: readonly LibrarySummary[],
  extraFilters: FieldFilters | null,
  attempt: number,
  onConnectionLost: () => void,
) {
  const identity = useViewerIdentity();
  const engagementEpoch = useEngagementEpoch();
  // A new identity, revalidation or retry retires the previous request and its artwork.
  const epoch = JSON.stringify([identity, engagementEpoch, attempt, extraFilters]);
  const request = useMemo(
    () => ({ section, libraries, extraFilters, epoch }),
    [section, libraries, extraFilters, epoch],
  );
  const [result, setResult] = useState<{ request: typeof request; feed: HomeRecentFeed }>();

  useEffect(() => {
    if (!request.section) return;
    const controller = new AbortController();
    const context = { libraryID: "", entityID: "", extraFilters: request.extraFilters, home: true };
    let feed = { ...EMPTY_FEED, pending: 1 + request.libraries.length };
    const publish = (next: HomeRecentFeed) => {
      feed = next;
      setResult({ request, feed });
    };
    const stop = () => {
      window.clearTimeout(timeout);
      controller.abort();
    };
    const timeout = window.setTimeout(() => {
      stop();
      publish({ ...feed, pending: 0, failed: true });
      onConnectionLost();
    }, SECTION_LOAD_TIMEOUT_MS);
    const settle = (next: HomeRecentFeed) => {
      const pending = feed.pending - 1;
      if (!pending) window.clearTimeout(timeout);
      publish({ ...next, pending });
    };
    const fail = (error: unknown) => {
      if (controller.signal.aborted) return;
      if (isAuthRejection(error)) {
        stop();
        clearViewerCache();
        publish(EMPTY_FEED);
      } else {
        settle({ ...feed, failed: true });
      }
      onConnectionLost();
    };
    fetchSectionItems(request.section, { ...context, paging: HOME_WINDOW }, controller.signal).then(
      (aggregate) => {
        if (!controller.signal.aborted) settle({ ...feed, aggregate });
      },
      fail,
    );
    for (const { id } of request.libraries) {
      fetchSectionItems(request.section, { ...context, libraryID: id }, controller.signal).then(
        (items) => {
          if (controller.signal.aborted) return;
          const scoped = new Map(feed.scoped).set(id, items);
          settle({ ...feed, scoped });
        },
        fail,
      );
    }
    return stop;
  }, [request, onConnectionLost]);

  return useMemo(() => {
    const { aggregate, scoped, pending, failed } =
      result?.request === request
        ? result.feed
        : { ...EMPTY_FEED, pending: Number(Boolean(section)) };
    // An aggregate Home request applies ShowOnHome. Scoped requests currently
    // ignore it (CORE-1496), so require a shared item before showing that shelf.
    const homeItems = new Set(aggregate.map(itemKey));
    const shelves = new Map(
      [...scoped].filter(([, items]) => items.some((item) => homeItems.has(itemKey(item)))),
    );
    const state: SectionHydrationState = pending
      ? "loading"
      : shelves.size
        ? "visible"
        : failed
          ? "failed"
          : "empty";
    return {
      state,
      shelves,
      heroItems: [...aggregate, ...[...shelves.values()].flat()],
      pending: Boolean(pending),
      failed,
      epoch: request.epoch,
    };
  }, [request, result, section]);
}

export function HomeRecentShelves({
  section,
  libraries,
  shelves,
  pending,
  seeAll,
  navigationID,
}: {
  section: Section;
  libraries: readonly LibrarySummary[];
  shelves: ReadonlyMap<string, SourceItem[]>;
  pending: boolean;
  seeAll?: SeeAllTarget;
  navigationID?: string;
}) {
  const t = useT();
  const locale = useDisplayLocale();
  const navigate = useNavigate();
  if (!shelves.size) return pending ? <SectionSkeleton hero={false} /> : null;
  return (
    <section className="flex flex-col gap-6">
      <SectionHeading
        title={displayString(section.title, locale)}
        onSeeAll={seeAll ? () => navigate(seeAll.href, { state: seeAll.state }) : undefined}
      />
      {libraries.map((library) => {
        const items = shelves.get(library.id);
        if (!items) return null;
        return (
          <SectionView
            key={library.id}
            section={{ ...section, id: `${section.id}:${library.id}`, title: library.title }}
            items={groupByContainer(items, (count) => t("cardNewCount", { count }))}
            navigationID={navigationID}
            headingLevel="h3"
            intrinsicArtwork
          />
        );
      })}
    </section>
  );
}

function featuredArtwork(items: readonly SourceItem[], failed: ReadonlySet<string>) {
  for (const item of items) {
    if (item.locked) continue;
    for (const slot of ["backdrop", "poster", "thumb"]) {
      const url = artworkSlotURL(item, slot);
      if (url && !failed.has(url)) return { item, url };
    }
  }
}

export function HomeArtworkHero({
  section,
  items,
  pending,
  failed: feedFailed,
  navigationID,
  onHydrationStateChange,
}: {
  section: Section;
  items: readonly SourceItem[];
  pending: boolean;
  failed: boolean;
  navigationID?: string;
  onHydrationStateChange: (state: SectionHydrationState) => void;
}) {
  const [failed, setFailed] = useState<ReadonlySet<string>>(() => new Set());
  const [loaded, setLoaded] = useState("");
  const feature = featuredArtwork(items, failed);
  const ready = Boolean(feature && feature.url === loaded);
  const state = ready
    ? "visible"
    : feature || pending
      ? "loading"
      : feedFailed
        ? "failed"
        : "empty";
  useEffect(() => onHydrationStateChange(state), [onHydrationStateChange, state]);
  if (!feature) return pending ? <SectionSkeleton hero /> : null;
  return (
    <Hero
      key={feature.url}
      section={section}
      item={feature.item}
      navigationID={navigationID}
      artwork={{
        url: feature.url,
        ready,
        onLoad: () => setLoaded(feature.url),
        onError: () => setFailed((current) => new Set(current).add(feature.url)),
      }}
    />
  );
}
