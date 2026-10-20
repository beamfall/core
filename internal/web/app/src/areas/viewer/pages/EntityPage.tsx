import { useEffect, useState } from "react";
import { useParams } from "react-router";
import { AppShell } from "@/components/chrome/AppShell";
import { Skeleton } from "@/components/ui/skeleton";
import { fetchEntity, fetchMediaForEntity } from "@/lib/graphql";
import type { EntityInfo, Layout, Section, SourceItem } from "@/lib/graphql/types";
import { useSession } from "@/lib/session";
import { LayoutView } from "../components/LayoutView";
import { MediaItemCard } from "../components/MediaItemCard";
import { NotAvailable } from "../components/NotAvailable";
import type { SeeAllTarget } from "../components/SectionView";
import { ViewerActions } from "../components/ViewerActions";
import { seeAllTargetFor } from "../lib/seeall";

// Entity detail (/entity/:entityId), ported from the vanilla openEntity
// (app.js). The header shows the Entity's declared fields (server-stringified
// values). When the manifest carries a detail Layout for this Entity's Kind
// (role: "detail", matching entityKind), its Sections render through the same
// LayoutView as browse, with entityID riding the variables for entity-media
// Sources. Otherwise the hand-rolled mediaForEntity list renders. `kind` is a
// plugin display label, rendered as text only (invariant #1 / #5).
export function EntityPage() {
  const { entityId } = useParams();
  const { manifest } = useSession();
  const [entity, setEntity] = useState<EntityInfo | null>(null);
  const [loading, setLoading] = useState(true);
  const [notFound, setNotFound] = useState(false);
  const [fallback, setFallback] = useState<SourceItem[] | null>(null);

  const detailLayout: Layout | null =
    (manifest?.layouts || []).find((l) => l.role === "detail" && l.entityKind === entity?.kind) ||
    null;

  useEffect(() => {
    if (!entityId) return;
    let alive = true;
    setLoading(true);
    setNotFound(false);
    setEntity(null);
    setFallback(null);
    fetchEntity(entityId)
      .then((e) => {
        if (!alive) return;
        if (!e) {
          setNotFound(true);
          return;
        }
        setEntity(e);
      })
      .catch(() => {
        if (alive) setNotFound(true);
      })
      .finally(() => {
        if (alive) setLoading(false);
      });
    return () => {
      alive = false;
    };
  }, [entityId]);

  // No detail Layout for this Kind: load the hand-rolled related-Media list.
  useEffect(() => {
    if (!entity || !entityId) return;
    if (detailLayout) return;
    let alive = true;
    fetchMediaForEntity(entityId)
      .then((items) => {
        if (alive) setFallback(items);
      })
      .catch(() => {
        if (alive) setFallback([]);
      });
    return () => {
      alive = false;
    };
  }, [entity, entityId, detailLayout]);

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
      {loading && (
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
      {entity && (
        <div className="flex flex-col gap-8">
          <header className="flex flex-col gap-3">
            {entity.kind && <span className="bf-entity-kind">{entity.kind}</span>}
            <h1 className="bf-page-title">{entity.title}</h1>
            {entity.fields.length > 0 && (
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
