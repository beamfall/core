import { useEffect, useMemo, useState } from "react";
import { useNavigate } from "react-router";
import { isAuthRejection } from "@/lib/api";
import { displayString } from "@/lib/display";
import type { FieldFilters, Section, SourceItem } from "@/lib/graphql/types";
import { fetchHomeLibraries, type HomeLibrary } from "@/lib/graphql/viewer";
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

interface HomeRecentFeed {
  libraries: readonly HomeLibrary[];
  shelves: ReadonlyMap<string, SourceItem[]>;
  fallbackItems: SourceItem[];
  pending: number;
  failed: boolean;
}

const EMPTY_FEED: HomeRecentFeed = {
  libraries: [],
  shelves: new Map(),
  fallbackItems: [],
  pending: 0,
  failed: false,
};

export function useHomeRecent(
  section: Section | undefined,
  extraFilters: FieldFilters | null,
  attempt: number,
  onConnectionLost: () => void,
) {
  const identity = useViewerIdentity();
  const engagementEpoch = useEngagementEpoch();
  // A new identity, revalidation or retry retires the previous request and its artwork.
  const epoch = JSON.stringify([identity, engagementEpoch, attempt, extraFilters]);
  const request = useMemo(() => ({ section, extraFilters, epoch }), [section, extraFilters, epoch]);
  const [result, setResult] = useState<{ request: typeof request; feed: HomeRecentFeed }>();

  useEffect(() => {
    const { section } = request;
    if (!section) return;
    const controller = new AbortController();
    const context = { libraryID: "", entityID: "", extraFilters: request.extraFilters, home: true };
    let feed = { ...EMPTY_FEED, pending: 1 };
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
    const settle = (next: HomeRecentFeed, pending = feed.pending - 1) => {
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
    fetchHomeLibraries(controller.signal).then((libraries) => {
      if (controller.signal.aborted) return;
      if (libraries === null) {
        // Core already applies ShowOnHome to its aggregate read.
        fetchSectionItems(section, context, controller.signal).then((fallbackItems) => {
          if (!controller.signal.aborted) settle({ ...feed, fallbackItems });
        }, fail);
        return;
      }
      const visible = libraries.filter((library) => library.showOnHome !== false);
      settle({ ...feed, libraries: visible }, visible.length);
      for (const { id } of visible) {
        fetchSectionItems(section, { ...context, libraryID: id }, controller.signal).then(
          (items) => {
            if (controller.signal.aborted) return;
            const shelves = items.length ? new Map(feed.shelves).set(id, items) : feed.shelves;
            settle({ ...feed, shelves });
          },
          fail,
        );
      }
    }, fail);
    return stop;
  }, [request, onConnectionLost]);

  return useMemo(() => {
    const { libraries, shelves, fallbackItems, pending, failed } =
      result?.request === request
        ? result.feed
        : { ...EMPTY_FEED, pending: Number(Boolean(section)) };
    const state: SectionHydrationState = pending
      ? "loading"
      : shelves.size || fallbackItems.length
        ? "visible"
        : failed
          ? "failed"
          : "empty";
    return {
      state,
      libraries,
      shelves,
      fallbackItems,
      heroItems: fallbackItems.length
        ? fallbackItems
        : libraries.flatMap(({ id }) => shelves.get(id) ?? []),
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
  fallbackItems,
  pending,
  seeAll,
  navigationID,
}: {
  section: Section;
  libraries: readonly HomeLibrary[];
  shelves: ReadonlyMap<string, SourceItem[]>;
  fallbackItems: SourceItem[];
  pending: boolean;
  seeAll?: SeeAllTarget;
  navigationID?: string;
}) {
  const t = useT();
  const locale = useDisplayLocale();
  const navigate = useNavigate();
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
      {libraries.map((library) => {
        const items = shelves.get(library.id);
        if (!items) return null;
        return (
          <SectionView
            key={library.id}
            section={{ ...section, id: `${section.id}:${library.id}`, title: library.title }}
            items={groupItems(items)}
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
