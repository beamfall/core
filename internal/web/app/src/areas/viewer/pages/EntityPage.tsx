import { useEffect, useMemo, useState } from "react";
import { useLocation, useParams } from "react-router";
import { AppShell } from "@/components/chrome/AppShell";
import { Skeleton } from "@/components/ui/skeleton";
import { isAuthRejection } from "@/lib/api";
import { fetchEntity, fetchMediaForEntity } from "@/lib/graphql";
import type { EntityInfo, Layout, Section, SourceItem } from "@/lib/graphql/types";
import { useSession, useViewerIdentity } from "@/lib/session";
import {
  clearViewerCache,
  deleteViewerCache,
  readViewerCache,
  writeViewerCache,
} from "@/lib/viewerCache";
import { findCachedSectionItem } from "../components/HydratedSection";
import { LayoutView } from "../components/LayoutView";
import { MediaItemCard } from "../components/MediaItemCard";
import { NotAvailable } from "../components/NotAvailable";
import type { SeeAllTarget } from "../components/SectionView";
import { ViewerActions } from "../components/ViewerActions";
import { readEntityPreview } from "../lib/entityPreview";
import { seeAllTargetFor } from "../lib/seeall";
import { playQueueOf } from "../lib/watchQueue";

const entityCacheKey = (id: string) => `entity:${id}`;
const entityMediaCacheKey = (id: string) => `entity-media:${id}`;

// Entity detail (/entity/:entityId), ported from the vanilla openEntity
// (app.js). The header shows the Entity's declared fields (server-stringified
// values). When the manifest carries a detail Layout for this Entity's Kind
// (role: "detail", matching entityKind), its Sections render through the same
// LayoutView as browse, with entityID riding the variables for entity-media
// Sources. Otherwise the hand-rolled mediaForEntity list renders. `kind` is a
// plugin display label, rendered as text only (invariant #1 / #5).
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
  const [entity, setEntity] = useState<EntityInfo | null>(
    () => readViewerCache<EntityInfo>(identity, entityCacheKey(entityId)) ?? null,
  );
  const [loading, setLoading] = useState(!entity);
  const [notFound, setNotFound] = useState(false);
  const [fallback, setFallback] = useState<SourceItem[] | null>(
    () => readViewerCache<SourceItem[]>(identity, entityMediaCacheKey(entityId)) ?? null,
  );

  const fallbackQueue = useMemo(() => (fallback ? playQueueOf(fallback) : undefined), [fallback]);

  const navPreview = readEntityPreview(location.state, identity, entityId);
  const previewTitle = navPreview?.title ?? findCachedSectionItem(identity, entityId)?.title ?? "";
  const kindHint = entity?.kind ?? navPreview?.kind;
  const layouts = manifest?.layouts || [];
  const detailLayout: Layout | null =
    (kindHint && layouts.find((l) => l.role === "detail" && l.entityKind === kindHint)) || null;
  // The related list is needed when the known Kind has no detail Layout, or
  // when no Kind could ever match one — that request does not wait on fetchEntity.
  const needsFallback =
    !notFound && !detailLayout && (!!kindHint || !layouts.some((l) => l.role === "detail"));

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
  }, [identity, entityId]);

  // No detail Layout for this Kind: load the hand-rolled related-Media list.
  useEffect(() => {
    if (!entityId || !needsFallback) return;
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
  }, [identity, entityId, needsFallback]);

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
        <div className="flex flex-col gap-3">
          <Skeleton className="h-8 w-64" />
          <Skeleton className="h-4 w-40" />
        </div>
      )}
      {/* fetchEntity returns null for an inaccessible OR a missing entity (the
          server fails closed with no oracle). A direct deep link to either
          resolves to the same leak-safe terminal surface — never "No results",
          which would mislead and could hint that the id is merely empty. */}
      {notFound && !loading && <NotAvailable />}
      {!notFound && (entity || previewTitle) && (
        <div className="flex flex-col gap-8">
          <header className="flex flex-col gap-3">
            {kindHint && <span className="bf-entity-kind">{kindHint}</span>}
            <h1 className="bf-page-title">{entity?.title ?? previewTitle}</h1>
            {!entity && <Skeleton aria-hidden className="h-4 w-40" />}
            {entity && entity.fields.length > 0 && (
              <dl className="bf-metadata mt-2">
                {entity.fields.map((field, i) => (
                  <div key={`${field.name}-${i}`} className="contents">
                    <dt>{field.name}</dt>
                    <dd>{field.value}</dd>
                  </div>
                ))}
              </dl>
            )}
          </header>

          {detailLayout ? (
            <LayoutView
              layout={detailLayout}
              libraryID=""
              entityID={entityId || ""}
              buildSeeAll={buildSeeAll}
              navigationID={`entity:${entityId || ""}`}
            />
          ) : (
            fallback &&
            fallback.length > 0 && (
              <ul className="bf-grid">
                {fallback.map((item, index) => (
                  <MediaItemCard
                    key={`${item.id}-${index}`}
                    item={item}
                    posInSet={index + 1}
                    setSize={fallback.length}
                    queue={fallbackQueue}
                  />
                ))}
              </ul>
            )
          )}
        </div>
      )}
    </AppShell>
  );
}
