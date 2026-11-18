import { useEffect, useState } from "react";
import { fetchRelatedMedia, fetchSourceItems } from "@/lib/graphql";
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
}

const NONE: SourceItem[] = [];

// The closing rows of a Media detail page (CORE-2444): "More with" the first
// billed person, read through the entity-media Source, then Related titles from
// relatedMedia. Both reads are bounded on the server and leave out what the
// Profile muted, like browse and Home. Each row leaves out the current item and
// is hidden while empty, so a failed read shows nothing. Cards open the item's
// page, like the rest of browse.
export function MediaMoreRows({
  mediaID,
  credits,
}: {
  mediaID: string;
  credits: readonly MediaCredit[] | undefined;
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

  useEffect(() => {
    const controller = new AbortController();
    const moreWith = leadID
      ? fetchSourceItems(
          {
            kind: "entity-media",
            entityKind: "",
            libraryID: "",
            entityID: leadID,
            fieldFilters: null,
            entityFilter: null,
            // One extra, since the current item is left out.
            limit: RELATED_LIMIT + 1,
            offset: 0,
          },
          controller.signal,
        ).catch(() => NONE)
      : Promise.resolve(NONE);
    const related = fetchRelatedMedia(mediaID, RELATED_LIMIT, controller.signal).catch(() => NONE);
    void Promise.all([moreWith, related]).then(([byLead, ranked]) => {
      if (controller.signal.aborted) return;
      setRows({
        key,
        lead: leadID ? leadTitle : "",
        moreWith: byLead.filter((item) => item?.id && item.id !== mediaID).slice(0, RELATED_LIMIT),
        related: ranked.filter((item) => item?.id && item.id !== mediaID).slice(0, RELATED_LIMIT),
      });
    });
    return () => controller.abort();
  }, [key, mediaID, leadID, leadTitle]);

  const current = rows?.key === key ? rows : null;
  if (!current) return null;
  return (
    <>
      {current.lead && current.moreWith.length > 0 && (
        <SectionView
          section={shelf("more-with", t("mediaDetailMoreWith", { name: current.lead }))}
          items={current.moreWith}
          navigationID={`media:${mediaID}`}
        />
      )}
      {current.related.length > 0 && (
        <SectionView
          section={shelf("related", t("mediaDetailRelated"))}
          items={current.related}
          navigationID={`media:${mediaID}`}
        />
      )}
    </>
  );
}

function shelf(id: string, title: string): Section {
  return { id: `media-${id}`, kind: "shelf", title, artworkSlot: "poster", source: { kind: id } };
}
