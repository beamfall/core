import { ArrowLeft, Play, Shuffle } from "lucide-react";
import { Fragment, useEffect, useMemo, useState } from "react";
import { Link, useLocation, useNavigate, useParams } from "react-router";
import { AppShell } from "@/components/chrome/AppShell";
import { buttonVariants } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { isAuthRejection } from "@/lib/api";
import { displayString } from "@/lib/display";
import { fetchEntity, fetchMediaForEntity } from "@/lib/graphql";
import type { EntityInfo, Layout, Section, SourceItem } from "@/lib/graphql/types";
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
import { ContainerMediaRows } from "../components/ContainerMediaRows";
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
  ancestorOf,
  groupHeading,
  nextUpName,
  siblingContainers,
  similarContainers,
  similarityGroups,
  textOverview,
  withoutItem,
} from "../lib/containerPage";
import { entityIdFromAddress, entityPath, useCanonicalEntityAddress } from "../lib/entityAddress";
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
import {
  entityPeople,
  layoutListsOnlyMedia,
  listensOverLayout,
  shuffled,
} from "../lib/listeningLayout";
import { mediaPath } from "../lib/mediaAddress";
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
/** How many person Entities a similar-container search reads through. */
const SIMILAR_PEOPLE = 6;
/** The most similar containers the rail lists. */
const SIMILAR_LIMIT = 20;

// Entity detail (/entity/:entityId, a bare ID or a readable address naming one,
// CORE-2511), ported from the vanilla openEntity
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
  const { entityId: address = "" } = useParams();
  const entityId = entityIdFromAddress(address);
  const location = useLocation();
  const { manifest, revealed } = useSession();
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
  // Without a summary, a free-text field reads as the clamped overview (CORE-2522).
  const { overview: summary, fields } = textOverview(
    entitySummary(entity),
    heroFields(entity, facts.year),
  );
  const backdrop = entity?.artworkSlots?.backdrop ?? "";
  // A person's headshot is its poster (CORE-2406).
  const photo = entity?.artworkSlots?.poster ?? "";
  const heroGrouped = heroGroups(entity?.relatedEntities);
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
  const isAudio = isAudioListing(fallback ?? NO_ITEMS, listing.rows ?? {});
  const listeningTracks = useMemo(() => {
    if (trackOrder) return trackOrder;
    if (entity?.archetype !== "person" || !listedItems || !listing.rows || !isAudio) return null;
    return listedItems.filter((item) => item.kind === "media");
  }, [trackOrder, entity, listedItems, listing.rows, isAudio]);
  const people = trackOrder ? entityPeople(entity) : [];
  const unwatched = unwatchedCount(fallback);
  const remainingLabel = t(isAudio ? "cardUnplayed" : "entityDetailUnwatched", {
    count: unwatched,
  });
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
    ...(!photo && !isAudio && unwatched > 0 ? [{ key: "unwatched", text: remainingLabel }] : []),
  ].filter((item) => item.text !== "");
  const itemCount = (count: number) => t("entityDetailItemCount", { count });
  // A middle-level parent heads its own children with their count; an audio
  // container's track list and a person's Media keep their own headings.
  const childCount =
    entity?.archetype === "container" && !trackOrder && fallback
      ? fallback.filter((item) => item.kind === "media").length
      : 0;
  // A container with Media of its own names the container it sits under and
  // drops that container's one-card rail (CORE-2510).
  const ancestor = useMemo(
    () =>
      ancestorOf(entity, fallback ? fallback.filter((item) => item.kind === "media").length : 0),
    [entity, fallback],
  );
  const groups = { ...heroGrouped, containers: withoutItem(heroGrouped.containers, ancestor?.id) };
  // Whether the ancestor (and so the readable address) is settled.
  const ancestorKnown = !!entity && (entity.archetype !== "container" || fallback !== null);
  useCanonicalEntityAddress(entity, ancestor?.title, ancestorKnown);
  const [ancestorEntity, setAncestorEntity] = useState<EntityInfo | null>(null);
  const siblings = siblingContainers(
    ancestorEntity?.id === ancestor?.id ? ancestorEntity : null,
    entity,
  );
  const [similar, setSimilar] = useState<{ id: string; items: SourceItem[] } | null>(null);
  const similarItems = similar?.id === entityId ? similar.items : [];
  // A detail Layout that only lists a container's own video Media renders as
  // rows that open each item's detail page (CORE-2510).
  const listsOwnMedia = !!detailLayout && layoutListsOnlyMedia(detailLayout) && listens === false;
  const mediaRows = listsOwnMedia && entity?.archetype === "container";
  const rowsTitle = detailLayout
    ? displayString(detailLayout.sections[0]?.title, displayLocale)
    : "";

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

  // The ancestor's own read lists this container's siblings (CORE-2522).
  const ancestorID = ancestor?.id ?? "";
  useEffect(() => {
    if (!ancestorID) return;
    let alive = true;
    const controller = new AbortController();
    fetchEntity(ancestorID, controller.signal)
      .then((read) => {
        if (alive) setAncestorEntity(read);
      })
      .catch((err: unknown) => {
        if (alive && isAuthRejection(err)) clearViewerCache();
      });
    return () => {
      alive = false;
      controller.abort();
    };
  }, [ancestorID]);

  // A top-level container finds similar ones through its category and person
  // Entities (CORE-2512). Each read may fail on its own; the rail shows what
  // the rest found.
  const similarFrom =
    entity?.archetype === "container" && entity.id === entityId && ancestorKnown && !ancestor
      ? entity
      : null;
  useEffect(() => {
    if (!similarFrom) return;
    const through = similarityGroups(similarFrom);
    const ids = [
      ...through.filter((g) => g.archetype === "category").flatMap((g) => g.items),
      ...through
        .filter((g) => g.archetype === "person")
        .flatMap((g) => g.items)
        .slice(0, SIMILAR_PEOPLE),
    ].map((item) => item.id);
    const unique = [...new Set(ids)];
    if (unique.length === 0) return;
    let alive = true;
    const controller = new AbortController();
    Promise.allSettled(unique.map((id) => fetchEntity(id, controller.signal))).then((reads) => {
      if (!alive) return;
      const found = reads.map((read) => (read.status === "fulfilled" ? read.value : null));
      setSimilar({
        id: similarFrom.id,
        items: similarContainers(similarFrom, found, SIMILAR_LIMIT),
      });
    });
    return () => {
      alive = false;
      controller.abort();
    };
  }, [similarFrom]);

  // Plays or resumes the next-up child with the container's order as the queue.
  function playNext() {
    if (!nextUp) return;
    const { item } = nextUp;
    const state: PlayerNavState = { title: item.title };
    if (identity && fallbackQueue && fallbackQueue.length > 0) {
      state.queue = queueWindow(fallbackQueue, item.id);
      state.queueIdentity = identity;
    }
    navigate(mediaPath("watch", item.id), { state });
  }

  // Plays every listed track in a fresh random order (CORE-2362).
  function shuffleAll() {
    if (!listeningTracks || listeningTracks.length === 0) return;
    const order = shuffled(listeningTracks);
    navigate(mediaPath("watch", order[0].id), {
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
                  {!isAudio && unwatched > 0 && (
                    <span className="bf-card-badge bf-entity-unwatched">
                      <span aria-hidden="true">{unwatched}</span>
                      <span className="sr-only">{remainingLabel}</span>
                    </span>
                  )}
                </div>
              )}
              <div className="bf-detail-copy">
                {ancestor && (
                  <p className="bf-entity-parent">
                    <Link
                      to={entityPath(ancestor.id, revealed ? undefined : { title: ancestor.title })}
                      state={entityLinkState(ancestor, identity)}
                    >
                      {ancestor.title}
                    </Link>
                  </p>
                )}
                <h1 className="bf-page-title">{entity?.title ?? previewTitle}</h1>
                {siblings && (
                  <nav
                    className="bf-entity-siblings"
                    aria-label={groupHeading(siblings.group, displayLocale)}
                  >
                    <ul>
                      {siblings.items.map((item) => (
                        <li key={item.id}>
                          <Link
                            to={entityPath(
                              item.id,
                              revealed ? undefined : { title: item.title, parent: ancestor?.title },
                            )}
                            state={entityLinkState(item, identity)}
                            aria-current={item.id === entityId ? "page" : undefined}
                          >
                            {item.title}
                          </Link>
                        </li>
                      ))}
                    </ul>
                  </nav>
                )}
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
                      <span className="bf-entity-ondeck">
                        {t(isAudio ? "entityDetailUpNext" : "entityDetailOnDeck")}
                      </span>
                      <span>
                        {isAudio
                          ? nextUpName(nextUp.item, displayLocale)
                          : t("entityDetailNextUp", {
                              title: nextUpName(nextUp.item, displayLocale),
                            })}
                      </span>
                    </p>
                  </div>
                )}
                {entity && (
                  <ItemActions
                    target={{ entityID: entityId }}
                    isAudio={isAudio}
                    hideTitle={entity.title}
                  />
                )}
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
              title={groupHeading(group, displayLocale)}
              items={group.items.map((item) => withItemCount(item, itemCount, displayLocale))}
            />
          ))}

          {mediaRows ? (
            fallback &&
            (fallback.some((item) => item.kind === "media") ? (
              <section className="bf-section flex flex-col gap-3">
                {rowsTitle && (
                  <h2 className="bf-section-title">
                    <span>{rowsTitle}</span>
                  </h2>
                )}
                <ContainerMediaRows
                  items={fallback}
                  label={
                    rowsTitle ||
                    t("entityDetailChildrenLabel", { title: entity?.title ?? previewTitle })
                  }
                />
              </section>
            ) : (
              <LayoutView
                layout={detailLayout}
                libraryID=""
                entityID={entityId || ""}
                buildSeeAll={buildSeeAll}
                navigationID={`entity:${entityId || ""}`}
              />
            ))
          ) : listsOwnMedia && !entity ? null : detailLayout && listens === false ? (
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

          {/* Similar containers, ranked by shared categories and people (CORE-2512). */}
          {similarItems.length > 0 && (
            <RelatedGroup title={t("mediaDetailRelated")} items={similarItems} />
          )}

          {groups.other.map((group) => (
            <RelatedGroup
              key={group.kind}
              title={groupHeading(group, displayLocale)}
              items={group.items}
            />
          ))}
        </div>
      )}
    </AppShell>
  );
}

/** One related group as a poster grid under its heading. */
function RelatedGroup({ title, items }: { title: string; items: readonly SourceItem[] }) {
  return (
    <section className="bf-section flex flex-col gap-3">
      <h2 className="bf-section-title">
        <span>{title}</span>
      </h2>
      <ul className="bf-grid">
        {items.map((item, index) => (
          <MediaItemCard
            key={`${item.id}-${index}`}
            item={item}
            posInSet={index + 1}
            setSize={items.length}
            nameContainer={false}
          />
        ))}
      </ul>
    </section>
  );
}
