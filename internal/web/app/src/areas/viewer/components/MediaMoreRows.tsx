import { useEffect, useState } from "react";
import { fetchRelatedMedia, fetchSimilarMedia, fetchSourceItems } from "@/lib/graphql";
import type { MediaCredit, Section, SourceItem } from "@/lib/graphql/types";
import { useT } from "@/lib/i18n";
import { useViewerIdentity } from "@/lib/session";
import { moreWithLead, RELATED_LIMIT } from "../lib/related";
import { SectionView } from "./SectionView";

interface Rows {
  key: string;
  /** The More with person's title; empty without a person credit. */
  lead: string;
  moreWith: SourceItem[];
  related: SourceItem[];
  similar: SourceItem[];
}

const NONE: SourceItem[] = [];

// The closing rows of a Media detail page (CORE-2444): "More with" the first
// billed person, read through the entity-media Source, then Related titles from
// relatedMedia. Both reads are bounded on the server and leave out what the
// Profile muted, like browse and Home. Each row leaves out the current item and
// is hidden while empty, so a failed read shows nothing. Cards open the item's
// page, like the rest of browse. Items the page already lists in its More in
// row (`exclude`, the nearest container's items) are left out of both rows
// (CORE-2469). Audio Media also get a Sonically similar row from similarMedia
// (CORE-3075); only audio is ever analysed, so other classes skip the read, and
// the row stays hidden until the Library has been analysed.
export function MediaMoreRows({
  mediaID,
  credits,
  mediaClass,
  exclude = NONE,
}: {
  mediaID: string;
  credits: readonly MediaCredit[] | undefined;
  mediaClass?: string;
  exclude?: readonly SourceItem[];
}) {
  const t = useT();
  const identity = useViewerIdentity();
  const key = `${identity ?? ""}:${mediaID}`;
  const [rows, setRows] = useState<Rows | null>(null);
  // A revalidated read hands over new credit objects; re-read only when the
  // lead itself changes. Related depends only on the Media id.
  const lead = moreWithLead(credits);
  const leadID = lead?.entityID ?? "";
  const leadTitle = lead?.title ?? "";
  const audio = mediaClass === "audio";

  useEffect(() => {
    const controller = new AbortController();
    const moreWith = leadID
      ? fetchSourceItems(
          {
            kind: "entity-media",
            entityKind: "",
            libraryID: "",
            entityID: leadID,
            excludeEditionGroupOf: mediaID,
            fieldFilters: null,
            entityFilter: null,
            limit: RELATED_LIMIT,
            offset: 0,
          },
          controller.signal,
        ).catch(() => NONE)
      : Promise.resolve(NONE);
    const related = fetchRelatedMedia(mediaID, RELATED_LIMIT, controller.signal).catch(() => NONE);
    const similar = audio
      ? fetchSimilarMedia(mediaID, RELATED_LIMIT, controller.signal).catch(() => NONE)
      : Promise.resolve(NONE);
    void Promise.all([moreWith, related, similar]).then(([byLead, ranked, alike]) => {
      if (controller.signal.aborted) return;
      setRows({
        key,
        lead: leadID ? leadTitle : "",
        moreWith: byLead.filter((item) => item?.id && item.id !== mediaID),
        related: ranked.filter((item) => item?.id && item.id !== mediaID),
        similar: alike.filter((item) => item?.id && item.id !== mediaID),
      });
    });
    return () => controller.abort();
  }, [key, mediaID, leadID, leadTitle, audio]);

  const current = rows?.key === key ? rows : null;
  if (!current) return null;
  const listed = new Set(exclude.map((item) => item.id));
  const fresh = (items: SourceItem[]) =>
    items.filter((item) => !listed.has(item.id)).slice(0, RELATED_LIMIT);
  const moreWith = fresh(current.moreWith);
  const related = fresh(current.related);
  const similar = fresh(current.similar);
  return (
    <>
      {current.lead && moreWith.length > 0 && (
        <SectionView
          section={shelf("more-with", t("mediaDetailMoreWith", { name: current.lead }))}
          items={moreWith}
          navigationID={`media:${mediaID}`}
        />
      )}
      {related.length > 0 && (
        <SectionView
          section={shelf("related", t("mediaDetailRelated"))}
          items={related}
          navigationID={`media:${mediaID}`}
        />
      )}
      {similar.length > 0 && (
        <SectionView
          section={shelf("similar", t("mediaDetailSonicallySimilar"))}
          items={similar}
          navigationID={`media:${mediaID}`}
        />
      )}
    </>
  );
}

function shelf(id: string, title: string): Section {
  return { id: `media-${id}`, kind: "shelf", title, artworkSlot: "poster", source: { kind: id } };
}
