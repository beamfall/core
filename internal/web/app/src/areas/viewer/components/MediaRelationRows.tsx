import { useEffect, useState } from "react";
import type { Section, SourceItem } from "@/lib/graphql/types";
import { useT } from "@/lib/i18n";
import { useViewerIdentity } from "@/lib/session";
import { fetchMediaRelations } from "../lib/mediaDetail";
import { SectionView } from "./SectionView";

interface Relations {
  key: string;
  extras: SourceItem[];
  editions: SourceItem[];
}

const EMPTY: SourceItem[] = [];

// The Extras and Editions rows of a Media detail page (CORE-2439), read from
// the stored extra-of and edition-of relations. Each row is hidden while it
// has no items, so a failed read or an item without either shows nothing.
// A card plays its item: an Extra or another Edition is something to watch,
// not a page to open.
export function MediaRelationRows({ mediaID }: { mediaID: string }) {
  const t = useT();
  const identity = useViewerIdentity();
  const key = `${identity ?? ""}:${mediaID}`;
  const [relations, setRelations] = useState<Relations | null>(null);

  useEffect(() => {
    const controller = new AbortController();
    const read = (relation: "extra-of" | "edition-of") =>
      fetchMediaRelations(mediaID, relation, controller.signal).catch(() => EMPTY);
    void Promise.all([read("extra-of"), read("edition-of")]).then(([extras, editions]) => {
      if (!controller.signal.aborted) setRelations({ key, extras, editions });
    });
    return () => controller.abort();
  }, [key, mediaID]);

  // Between a route change and the read, state still holds the previous item's rows.
  const current = relations?.key === key ? relations : null;
  if (!current) return null;
  return (
    <>
      <RelationRow
        id="extras"
        title={t("mediaDetailExtras")}
        items={current.extras}
        mediaID={mediaID}
      />
      <RelationRow
        id="editions"
        title={t("mediaDetailEditions")}
        items={current.editions}
        mediaID={mediaID}
      />
    </>
  );
}

function RelationRow({
  id,
  title,
  items,
  mediaID,
}: {
  id: string;
  title: string;
  items: SourceItem[];
  mediaID: string;
}) {
  if (items.length === 0) return null;
  const section: Section = {
    id: `media-${id}`,
    kind: "shelf",
    title,
    artworkSlot: "poster",
    source: { kind: id },
  };
  return (
    <SectionView section={section} items={items} navigationID={`media:${mediaID}`} playOnActivate />
  );
}
