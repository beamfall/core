import { ArrowLeft, Play, Shuffle } from "lucide-react";
import { Fragment, useEffect, useMemo, useState } from "react";
import { Link, useLocation, useNavigate, useParams } from "react-router";
import { AppShell } from "@/components/chrome/AppShell";
import { buttonVariants } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { isAuthRejection } from "@/lib/api";
import { displayString } from "@/lib/display";
import { fetchEntity, fetchMediaForEntity } from "@/lib/graphql";
import type {
  EntityInfo,
  Layout,
  RelatedEntityGroup,
  Section,
  SourceItem,
} from "@/lib/graphql/types";
import { useDisplayLocale, useT } from "@/lib/i18n";
import { useSession, useViewerIdentity } from "@/lib/session";
import {
  clearViewerCache,
  deleteViewerCache,
  readViewerCache,
  writeViewerCache,
} from "@/lib/viewerCache";
import { ItemActions } from "../actions";
import { useEngagementEpoch } from "../actions/engagementSync";
import { CastRow } from "../components/CastRow";
import { DetailBackdrop, DetailOverview } from "../components/DetailStage";
import { EntityMediaBrowse } from "../components/EntityMediaBrowse";
import { findCachedSectionItem } from "../components/HydratedSection";
import { LayoutView } from "../components/LayoutView";
import { MediaItemCard } from "../components/MediaItemCard";
import { NotAvailable } from "../components/NotAvailable";
import type { SeeAllTarget } from "../components/SectionView";
import { useListingRows } from "../components/useListingRows";
import { ViewerActions } from "../components/ViewerActions";
import { artworkBucketURL, artworkSrcSet } from "../lib/artwork";
import { nextUpChild, nextUpChildContainer } from "../lib/containerChildren";
import {
  entityFacts,
  entitySummary,
  groupCredits,
  heroFields,
  heroGroups,
  unwatchedCount,
  withItemCount,
} from "../lib/entityHero";
import { entityLinkState, readEntityPreview } from "../lib/entityPreview";
import { entityPeople, listensOverLayout, shuffled } from "../lib/listeningLayout";
import { seeAllTargetFor } from "../lib/seeall";
import { audioQueue, containerTrackRows, isAudioListing, playState } from "../lib/trackList";
import { fieldDisplayValue, formatScore } from "../lib/viewerFields";
import { type PlayerNavState, playQueueOf, queueWindow } from "../lib/watchQueue";
import { preloadPlayer } from "../player/preload";
import "./media-detail.css";
import "./entity-detail.css";

const NO_ITEMS: SourceItem[] = [];
const entityCacheKey = (id: string) => `entity:${id}`;
const entityMediaCacheKey = (id: string) => `entity-media:${id}`;

// Entity detail (/entity/:entityId), ported from the vanilla openEntity
// (app.js). The header shows the Entity's declared fields (server-stringified
// values). When the manifest carries a detail Layout for this Entity's Kind
// (role: "detail", matching entityKind), its Sections render through the same
// LayoutView as browse, with entityID riding the variables for entity-media
// Sources. Otherwise the hand-rolled mediaForEntity list renders, laid out by
// archetype and Media class (EntityMediaBrowse: track list, person). Related
// Entities (either relation direction, grouped by kind) render before both,
// so an Entity linked only to other Entities never opens empty. `kind` is a
// plugin display label, rendered as text only (invariant #1 / #5).
//
// Container lockup (CORE-1786): the header is the media detail stage (title,
// a kind chip, viewer-facing fields only) with one accent-filled action that
// plays or resumes the next-up child of the ordered Media list, and the list
// itself renders as ContainerChildren, grouped by kind in order (list rows on
// a phone). Related Entities stay cards, so a container's own containers read
// as one card each.
//
// Instant paint: an entity (and its related list) already loaded for the bound
// identity renders from the identity-scoped viewer cache and revalidates
// behind it; otherwise the header paints a title from identity-matched
// navigation state or from a section item already hydrated for this identity.
// Requests that do not depend on the entity result start alongside it: the
// detail Layout's sections once its Kind is known from a preview, and the
// related list whenever the manifest has no detail Layout at all.
export function EntityPage() {
  const { entityId = "" } = useParams();
  const location = useLocation();
  const { manifest } = useSession();
  const identity = useViewerIdentity();
  const displayLocale = useDisplayLocale();
  const t = useT();
  const navigate = useNavigate();
  const [entity, setEntity] = useState<EntityInfo | null>(
    () => readViewerCache<EntityInfo>(identity, entityCacheKey(entityId)) ?? null,
  );
  const [loading, setLoading] = useState(!entity);
  const [notFound, setNotFound] = useState(false);
  const [fallback, setFallback] = useState<SourceItem[] | null>(
    () => readViewerCache<SourceItem[]>(identity, entityMediaCacheKey(entityId)) ?? null,
  );

  // Media of the next-up child container, read only when this Entity has no
  // playable Media of its own (a container of containers).
  const [childMedia, setChildMedia] = useState<{ id: string; items: SourceItem[] } | null>(null);

  const navPreview = readEntityPreview(location.state, identity, entityId);
  const previewTitle = navPreview?.title ?? findCachedSectionItem(identity, entityId)?.title ?? "";
  const kindHint = entity?.kind ?? navPreview?.kind;
  const kindLabel = displayString(entity?.kindDisplayName, displayLocale) || kindHint;
  // The hero (CORE-2441): backdrop, poster, facts, overview and the fields
  // left over, all from what the Entity declares.
  const facts = entityFacts(entity);
  const fields = heroFields(entity, facts.year);
  const summary = entitySummary(entity);
  const backdrop = entity?.artworkSlots?.backdrop ?? "";
  // A person's headshot is its poster (CORE-2406).
  const photo = entity?.artworkSlots?.poster ?? "";
  const groups = heroGroups(entity?.relatedEntities);
  const layouts = manifest?.layouts || [];
  const detailLayout: Layout | null =
    (kindHint && layouts.find((l) => l.role === "detail" && l.entityKind === kindHint)) || null;
  // The related list is needed when the known Kind has no detail Layout, or
  // when no Kind could ever match one — that request does not wait on fetchEntity.
  const needsFallback =
    !notFound && !detailLayout && (!!kindHint || !layouts.some((l) => l.role === "detail"));
  // An audio container or person lists its Media the listening way over a
  // detail Layout that only lists them (CORE-2362); null while that is unknown.
  const listens = notFound
    ? false
    : listensOverLayout(detailLayout, entity, fallback, navPreview?.mediaClass);
  // The primary action reads the Entity's own Media whether or not a detail
  // Layout renders them (CORE-1786: one accent action on every container).
  const needsDirectMedia = !notFound;

  // The listed Media's rows, read once for the track list and this page's
  // Play action, so both start and queue an audio container's tracks in the
  // order the list shows (grouped and numbered by stored position, CORE-1635).
  const listedItems = needsFallback || listens ? fallback : null;
  const listing = useListingRows(entity, listedItems ?? NO_ITEMS);
  const trackOrder = useMemo(
    () =>
      containerTrackRows(entity, listedItems ?? NO_ITEMS, listing.rows)?.map(
        (track) => track.item,
      ) ?? null,
    [entity, listedItems, listing.rows],
  );
  // Until an audio container's order is known, offer no Play action that could
  // start the wrong track.
  const orderPending = listing.wanted && listing.rows === null && entity?.archetype === "container";
  const ordered = trackOrder ?? fallback;
  const directNext = useMemo(
    () => (ordered && !orderPending ? nextUpChild(ordered) : null),
    [ordered, orderPending],
  );
  // No playable Media of its own: play on through the next-up child container.
  const childContainer = useMemo(
    () =>
      fallback && !orderPending && !directNext
        ? nextUpChildContainer(entity?.relatedEntities)
        : null,
    [fallback, orderPending, directNext, entity],
  );
  const childItems =
    childContainer && childMedia?.id === childContainer.id ? childMedia.items : null;
  const fallbackQueue = useMemo(() => {
    if (directNext && trackOrder) return audioQueue(trackOrder);
    const queueSource = directNext ? fallback : childItems;
    return queueSource ? playQueueOf(queueSource) : undefined;
  }, [directNext, trackOrder, fallback, childItems]);
  const nextUp = useMemo(
    () => directNext ?? (childItems ? nextUpChild(childItems) : null),
    [directNext, childItems],
  );
  // The listed Media once known to be all audio: what Shuffle plays, and for a
  // container the sign to name its people under the title.
  const listeningTracks = useMemo(() => {
    if (trackOrder) return trackOrder;
    if (entity?.archetype !== "person" || !listedItems || !listing.rows) return null;
    if (!isAudioListing(listedItems, listing.rows)) return null;
    return listedItems.filter((item) => item.kind === "media");
  }, [trackOrder, entity, listedItems, listing.rows]);
  const people = trackOrder ? entityPeople(entity) : [];
  const unwatched = unwatchedCount(fallback);
  const factItems = [
    { key: "kind", text: kindLabel ?? "", className: "bf-entity-kind" },
    { key: "year", text: facts.year ? String(facts.year) : "" },
    { key: "rating", text: facts.rating ?? "", variant: "rating" as const },
    ...(facts.score
      ? [
          {
            key: "score",
            text: `\u2605 ${formatScore(facts.score.value, displayLocale)}`,
            variant: "score" as const,
            spoken: t("mediaDetailScoreLabel", {
              label: displayString(facts.score.label, displayLocale) || t("mediaDetailScore"),
              score: formatScore(facts.score.value, displayLocale),
              votes: facts.score.votes,
            }),
          },
        ]
      : []),
    // Category chips link to their Entity, with "+N" for the rest of a kind (CORE-2504).
    ...facts.categories.flatMap((group) => [
      ...group.shown.map((credit) => ({
        key: `category:${group.kind}:${credit.entityID || credit.title}`,
        text: credit.title,
        ...(credit.entityID
          ? { variant: "link" as const, to: `/entity/${encodeURIComponent(credit.entityID)}` }
          : {}),
      })),
      ...(group.more > 0
        ? [
            {
              key: `category:${group.kind}:more`,
              text: `+${group.more}`,
              spoken: t("mediaDetailMoreCount", { count: group.more }),
            },
          ]
        : []),
    ]),
    // Without a poster to carry it, the unwatched count is a chip.
    ...(!photo && unwatched > 0
      ? [{ key: "unwatched", text: t("entityDetailUnwatched", { count: unwatched }) }]
      : []),
  ].filter((item) => item.text !== "");
  const itemCount = (count: number) => t("entityDetailItemCount", { count });
  // A middle-level parent heads its own children with their count; an audio
  // container's track list and a person's Media keep their own headings.
  const childCount =
    entity?.archetype === "container" && !trackOrder && fallback
      ? fallback.filter((item) => item.kind === "media").length
      : 0;

  // The Entity read carries related-Entity engagement, so it reloads with it.
  const engagementEpoch = useEngagementEpoch();
  useEffect(() => {
    if (!entityId) return;
    let alive = true;
    const controller = new AbortController();
    const key = entityCacheKey(entityId);
    const cached = readViewerCache<EntityInfo>(identity, key);
    setEntity(cached ?? null);
    setLoading(!cached);
    setNotFound(false);
    fetchEntity(entityId, controller.signal)
      .then((e) => {
        if (!alive) return;
        if (!e) {
          deleteViewerCache(identity, key);
          setEntity(null);
          setNotFound(true);
          return;
        }
        writeViewerCache(identity, key, e);
        setEntity(e);
      })
      .catch((err: unknown) => {
        if (!alive) return;
        if (isAuthRejection(err)) clearViewerCache();
        // A failed revalidation keeps the entity already shown for this
        // identity; anything else fails closed to the not-available surface.
        if (cached && !isAuthRejection(err)) return;
        setEntity(null);
        setNotFound(true);
      })
      .finally(() => {
        if (alive) setLoading(false);
      });
    return () => {
      alive = false;
      controller.abort();
    };
  }, [identity, entityId, engagementEpoch]);

  // The Entity's own Media: the hand-rolled list when no detail Layout renders
  // them, and the primary action's next-up child either way. Reloads whenever
  // watched, watchlist or rating state changes under it.
  useEffect(() => {
    if (!entityId || !needsDirectMedia) return;
    let alive = true;
    const controller = new AbortController();
    const key = entityMediaCacheKey(entityId);
    const cached = readViewerCache<SourceItem[]>(identity, key);
    setFallback(cached ?? null);
    fetchMediaForEntity(entityId, controller.signal)
      .then((items) => {
        if (!alive) return;
        writeViewerCache(identity, key, items);
        setFallback(items);
      })
      .catch((err: unknown) => {
        if (!alive) return;
        if (isAuthRejection(err)) clearViewerCache();
        else if (cached) return;
        setFallback([]);
      });
    return () => {
      alive = false;
      controller.abort();
    };
  }, [identity, entityId, needsDirectMedia, engagementEpoch]);

  const childContainerID = childContainer?.id ?? "";
  useEffect(() => {
    if (!childContainerID) return;
    let alive = true;
    const controller = new AbortController();
    fetchMediaForEntity(childContainerID, controller.signal)
      .then((items) => {
        if (alive) setChildMedia({ id: childContainerID, items });
      })
      .catch((err: unknown) => {
        if (!alive) return;
        if (isAuthRejection(err)) clearViewerCache();
        setChildMedia({ id: childContainerID, items: [] });
      });
    return () => {
      alive = false;
      controller.abort();
    };
  }, [childContainerID, engagementEpoch]);

  // Plays or resumes the next-up child with the container's order as the queue.
  function playNext() {
    if (!nextUp) return;
    const { item } = nextUp;
    const state: PlayerNavState = { title: item.title };
    if (identity && fallbackQueue && fallbackQueue.length > 0) {
      state.queue = queueWindow(fallbackQueue, item.id);
      state.queueIdentity = identity;
    }
    navigate(`/watch/${encodeURIComponent(item.id)}`, { state });
  }

  // Plays every listed track in a fresh random order (CORE-2362).
  function shuffleAll() {
    if (!listeningTracks || listeningTracks.length === 0) return;
    const order = shuffled(listeningTracks);
    navigate(`/watch/${encodeURIComponent(order[0].id)}`, {
      state: playState(identity, audioQueue(order), order[0]),
    });
  }

  // The same way back the media detail page offers (CORE-2322).
  function goBack() {
    // "default" is the entry the app was loaded on: nothing in-app to return to.
    if (location.key !== "default") navigate(-1);
    else navigate("/");
  }

  function buildSeeAll(section: Section): SeeAllTarget {
    return seeAllTargetFor({
      section,
      libraryID: "",
      entityID: entityId || "",
      returnTo: `/entity/${encodeURIComponent(entityId || "")}`,
    });
  }

  return (
    <AppShell actions={<ViewerActions />}>
      <button type="button" className="bf-detail-back" onClick={goBack}>
        <ArrowLeft aria-hidden />
        {t("back")}
      </button>
      {loading && !entity && !previewTitle && (
        <div aria-hidden className="flex flex-col gap-4">
          <div className="flex flex-col gap-1">
            <Skeleton className="bf-shimmer h-9 w-72 max-w-full" />
            <Skeleton className="bf-shimmer h-[22px] w-28" />
          </div>
          <Skeleton className="bf-shimmer h-10 w-80 max-w-full" />
        </div>
      )}
      {/* fetchEntity returns null for an inaccessible OR a missing entity (the
          server fails closed with no oracle). A direct deep link to either
          resolves to the same leak-safe terminal surface — never "No results",
          which would mislead and could hint that the id is merely empty. */}
      {notFound && !loading && <NotAvailable />}
      {!notFound && (entity || previewTitle) && (
        <div className="flex flex-col gap-8">
          <header className="bf-detail-stage" data-backdrop={backdrop ? "" : undefined}>
            {backdrop && <DetailBackdrop key={backdrop} url={backdrop} />}
            <div
              className={`bf-detail-lockup bf-entity-lockup${photo ? " bf-entity-has-photo" : ""}`}
            >
              {photo && (
                <div className="bf-entity-poster">
                  <img
                    className="bf-detail-poster bf-entity-photo"
                    src={artworkBucketURL(photo, "w320")}
                    srcSet={artworkSrcSet(photo, ["w320", "w640"])}
                    sizes="(max-width: 640px) 120px, 200px"
                    alt=""
                  />
                  {unwatched > 0 && (
                    <span className="bf-card-badge bf-entity-unwatched">
                      <span aria-hidden="true">{unwatched}</span>
                      <span className="sr-only">
                        {t("entityDetailUnwatched", { count: unwatched })}
                      </span>
                    </span>
                  )}
                </div>
              )}
              <div className="bf-detail-copy">
                <h1 className="bf-page-title">{entity?.title ?? previewTitle}</h1>
                {people.length > 0 && (
                  <p className="bf-entity-people">
                    {people.map((person, index) => (
                      <Fragment key={person.id}>
                        {index > 0 && ", "}
                        <Link
                          to={`/entity/${encodeURIComponent(person.id)}`}
                          state={entityLinkState(person, identity)}
                        >
                          {person.title}
                        </Link>
                      </Fragment>
                    ))}
                  </p>
                )}
                {factItems.length > 0 && (
                  <ul className="bf-detail-facts" aria-label={t("mediaDetailFacts")}>
                    {factItems.map((item) => (
                      <li
                        key={item.key}
                        className={`bf-detail-fact${"className" in item ? ` ${item.className}` : ""}`}
                        data-variant={"variant" in item ? item.variant : undefined}
                      >
                        {"to" in item && item.to ? (
                          <Link to={item.to}>{item.text}</Link>
                        ) : "spoken" in item ? (
                          <>
                            <span aria-hidden="true">{item.text}</span>
                            <span className="sr-only">{item.spoken}</span>
                          </>
                        ) : (
                          item.text
                        )}
                      </li>
                    ))}
                  </ul>
                )}
                {!entity && <Skeleton aria-hidden className="bf-shimmer h-10 w-80 max-w-full" />}
                {nextUp && (
                  <div className="bf-detail-actions">
                    {/* The one accent-filled action: the next-up child. */}
                    <button
                      type="button"
                      className={`${buttonVariants({ size: "lg" })} bf-detail-play`}
                      aria-label={t(
                        nextUp.resume ? "mediaDetailResumeLabel" : "mediaDetailPlayLabel",
                        {
                          title: nextUp.item.title,
                        },
                      )}
                      onClick={playNext}
                      onPointerEnter={preloadPlayer}
                      onFocus={preloadPlayer}
                    >
                      <Play aria-hidden />
                      {nextUp.resume ? t("mediaDetailResume") : t("play")}
                    </button>
                    {listeningTracks && listeningTracks.length > 1 && (
                      <button
                        type="button"
                        className={`${buttonVariants({ variant: "ghost", size: "lg" })} bf-detail-secondary`}
                        onClick={shuffleAll}
                        onPointerEnter={preloadPlayer}
                        onFocus={preloadPlayer}
                      >
                        <Shuffle aria-hidden />
                        {t("libraryBrowseShuffle")}
                      </button>
                    )}
                    <p className="bf-entity-next">
                      <span className="bf-entity-ondeck">{t("entityDetailOnDeck")}</span>
                      <span>{t("entityDetailNextUp", { title: nextUp.item.title })}</span>
                      {displayString(nextUp.item.subtitle, displayLocale) && (
                        <span className="bf-entity-next-sub">
                          {displayString(nextUp.item.subtitle, displayLocale)}
                        </span>
                      )}
                    </p>
                  </div>
                )}
                {entity && <ItemActions target={{ entityID: entityId }} />}
                {summary && <DetailOverview text={summary} id="bf-entity-overview" />}
                {fields.length > 0 && (
                  <dl className="bf-metadata">
                    {fields.map((field, i) => (
                      <div key={`${field.name}-${i}`} className="contents">
                        <dt>{displayString(field.label, displayLocale)}</dt>
                        <dd>{fieldDisplayValue(field, displayLocale)}</dd>
                      </div>
                    ))}
                  </dl>
                )}
              </div>
            </div>
          </header>

          {/* Child containers first, each card counting its items. */}
          {groups.containers.map((group) => (
            <RelatedGroup
              key={group.kind}
              group={group}
              items={group.items.map((item) => withItemCount(item, itemCount, displayLocale))}
            />
          ))}

          {detailLayout && listens === false ? (
            <LayoutView
              layout={detailLayout}
              libraryID=""
              entityID={entityId || ""}
              buildSeeAll={buildSeeAll}
              navigationID={`entity:${entityId || ""}`}
            />
          ) : (
            (needsFallback || listens) &&
            fallback && (
              <>
                {childCount > 0 && (
                  <h2 className="bf-section-title bf-entity-children-count">
                    {itemCount(childCount)}
                  </h2>
                )}
                <EntityMediaBrowse
                  entity={entity}
                  items={fallback}
                  listing={listing}
                  label={t("entityDetailChildrenLabel", { title: entity?.title ?? previewTitle })}
                />
              </>
            )
          )}

          {/* An audio container names its people under the title instead. */}
          {people.length === 0 && <CastRow credits={groupCredits(groups.people, displayLocale)} />}

          {groups.other.map((group) => (
            <RelatedGroup key={group.kind} group={group} items={group.items} />
          ))}
        </div>
      )}
    </AppShell>
  );
}

/** One related group as a poster grid under its display name. */
function RelatedGroup({
  group,
  items,
}: {
  group: RelatedEntityGroup;
  items: readonly SourceItem[];
}) {
  const displayLocale = useDisplayLocale();
  return (
    <section className="bf-section flex flex-col gap-3">
      <h2 className="bf-section-title">
        <span>{displayString(group.kindDisplayName, displayLocale) || group.kind}</span>
      </h2>
      <ul className="bf-grid">
        {items.map((item, index) => (
          <MediaItemCard
            key={`${item.id}-${index}`}
            item={item}
            posInSet={index + 1}
            setSize={items.length}
          />
        ))}
      </ul>
    </section>
  );
}
