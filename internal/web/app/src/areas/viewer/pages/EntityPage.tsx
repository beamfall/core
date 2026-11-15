import { Play } from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { useLocation, useNavigate, useParams } from "react-router";
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
import { EntityMediaBrowse } from "../components/EntityMediaBrowse";
import { findCachedSectionItem } from "../components/HydratedSection";
import { LayoutView } from "../components/LayoutView";
import { MediaItemCard } from "../components/MediaItemCard";
import { NotAvailable } from "../components/NotAvailable";
import type { SeeAllTarget } from "../components/SectionView";
import { ViewerActions } from "../components/ViewerActions";
import { nextUpChild, nextUpChildContainer } from "../lib/containerChildren";
import { readEntityPreview } from "../lib/entityPreview";
import { seeAllTargetFor } from "../lib/seeall";
import { viewerFacingFields } from "../lib/viewerFields";
import { type PlayerNavState, playQueueOf, queueWindow } from "../lib/watchQueue";
import { preloadPlayer } from "../player/preload";
import "./media-detail.css";
import "./entity-detail.css";

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
  const fields = entity ? viewerFacingFields(entity.fields) : [];
  const layouts = manifest?.layouts || [];
  const detailLayout: Layout | null =
    (kindHint && layouts.find((l) => l.role === "detail" && l.entityKind === kindHint)) || null;
  // The related list is needed when the known Kind has no detail Layout, or
  // when no Kind could ever match one — that request does not wait on fetchEntity.
  const needsFallback =
    !notFound && !detailLayout && (!!kindHint || !layouts.some((l) => l.role === "detail"));
  // The primary action reads the Entity's own Media whether or not a detail
  // Layout renders them (CORE-1786: one accent action on every container).
  const needsDirectMedia = !notFound;

  const directNext = useMemo(() => (fallback ? nextUpChild(fallback) : null), [fallback]);
  // No playable Media of its own: play on through the next-up child container.
  const childContainer = useMemo(
    () => (fallback && !directNext ? nextUpChildContainer(entity?.relatedEntities) : null),
    [fallback, directNext, entity],
  );
  const childItems =
    childContainer && childMedia?.id === childContainer.id ? childMedia.items : null;
  const queueSource = directNext ? fallback : childItems;
  const fallbackQueue = useMemo(
    () => (queueSource ? playQueueOf(queueSource) : undefined),
    [queueSource],
  );
  const nextUp = useMemo(
    () => directNext ?? (childItems ? nextUpChild(childItems) : null),
    [directNext, childItems],
  );

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
          <header className="bf-detail-stage">
            <div className="bf-detail-lockup bf-entity-lockup">
              <div className="bf-detail-copy">
                <h1 className="bf-page-title">{entity?.title ?? previewTitle}</h1>
                {kindLabel && (
                  <ul className="bf-detail-facts" aria-label={t("mediaDetailFacts")}>
                    <li className="bf-detail-fact bf-entity-kind">{kindLabel}</li>
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
                    <p className="bf-entity-next">
                      {t("entityDetailNextUp", { title: nextUp.item.title })}
                    </p>
                  </div>
                )}
                {entity && <ItemActions target={{ entityID: entityId }} />}
                {fields.length > 0 && (
                  <dl className="bf-metadata">
                    {fields.map((field, i) => (
                      <div key={`${field.name}-${i}`} className="contents">
                        <dt>{displayString(field.label, displayLocale)}</dt>
                        <dd>{field.value}</dd>
                      </div>
                    ))}
                  </dl>
                )}
              </div>
            </div>
          </header>

          {entity?.relatedEntities?.map((group) => (
            <section key={group.kind} className="bf-section flex flex-col gap-3">
              <h2 className="bf-section-title">
                <span>{displayString(group.kindDisplayName, displayLocale) || group.kind}</span>
              </h2>
              <ul className="bf-grid">
                {group.items.map((item, index) => (
                  <MediaItemCard
                    key={`${item.id}-${index}`}
                    item={item}
                    posInSet={index + 1}
                    setSize={group.items.length}
                  />
                ))}
              </ul>
            </section>
          ))}

          {detailLayout ? (
            <LayoutView
              layout={detailLayout}
              libraryID=""
              entityID={entityId || ""}
              buildSeeAll={buildSeeAll}
              navigationID={`entity:${entityId || ""}`}
            />
          ) : (
            needsFallback &&
            fallback && (
              <EntityMediaBrowse
                entity={entity}
                items={fallback}
                label={t("entityDetailChildrenLabel", { title: entity?.title ?? previewTitle })}
              />
            )
          )}
        </div>
      )}
    </AppShell>
  );
}
