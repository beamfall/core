import { useEffect, useRef, useState } from "react";
import { fetchMediaForEntity } from "@/lib/graphql";
import type { MediaCredit, Section, SourceItem } from "@/lib/graphql/types";
import { useT } from "@/lib/i18n";
import { useViewerIdentity } from "@/lib/session";
import { rankRelated, relatedSignals } from "../lib/related";
import { SectionView } from "./SectionView";

interface Rows {
  key: string;
  lead: MediaCredit | null;
  moreWith: SourceItem[];
  related: SourceItem[];
}

const NONE: SourceItem[] = [];

// The closing rows of a Media detail page (CORE-2444): "More with" the first
// billed person, then Related titles ranked by shared credits (lib/related).
// Each row leaves out the current item and is hidden while empty, so a failed
// read shows nothing. Cards open the item's page, like the rest of browse.
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
  // signals themselves change.
  const signals = relatedSignals(credits);
  const signalKey = [...signals.categories, ...signals.people].map((c) => c.entityID).join(",");
  const signalsRef = useRef(signals);
  signalsRef.current = signals;

  // signalKey stands for signalsRef.current in the dependencies.
  useEffect(() => {
    const controller = new AbortController();
    const { categories, people } = signalsRef.current;
    const lead = people[0] ?? null;
    const read = (credit: MediaCredit) =>
      fetchMediaForEntity(credit.entityID, controller.signal).catch(() => NONE);
    // Without a category nothing can qualify as Related, so only the lead is read.
    const readPeople = categories.length > 0 ? people : people.slice(0, 1);
    void Promise.all([Promise.all(categories.map(read)), Promise.all(readPeople.map(read))]).then(
      ([byCategory, byPerson]) => {
        if (controller.signal.aborted) return;
        setRows({
          key,
          lead,
          moreWith: (byPerson[0] ?? NONE).filter((item) => item?.id && item.id !== mediaID),
          related: rankRelated(mediaID, byCategory, byPerson),
        });
      },
    );
    return () => controller.abort();
  }, [key, mediaID, signalKey]);

  const current = rows?.key === key ? rows : null;
  if (!current) return null;
  return (
    <>
      {current.lead && current.moreWith.length > 0 && (
        <SectionView
          section={shelf("more-with", t("mediaDetailMoreWith", { name: current.lead.title }))}
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
