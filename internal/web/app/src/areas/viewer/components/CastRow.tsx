import { ChevronRight } from "lucide-react";
import { Link } from "react-router";
import type { MediaCredit } from "@/lib/graphql/types";
import { useDisplayLocale, useT } from "@/lib/i18n";
import { artworkBucketURL, artworkSrcSet } from "../lib/artwork";
import { castGroups, creditPart, creditPhoto, nameInitial, peopleHeading } from "../lib/people";

// The cast row on a Media detail page (CORE-2406): the item's person-archetype
// credits in credit order, each a headshot card (the person's poster, or the
// name's initial) with the part they play, linking to the person page.
// Credits carrying a department are grouped under it; the rest share one row.
// With `preview` (the detail page, CORE-2503) only the first group shows, at
// most `preview.max` people, and "See all" opens the full Cast & Crew view
// when anyone is left out. The row is headed with the credits' shared kind
// label when they declare one (CORE-3337), else "People".
export function CastRow({
  credits,
  preview,
  castHeading,
}: {
  credits: readonly MediaCredit[] | null | undefined;
  preview?: { max: number; to: string };
  /** Heading for the credits without a department when other groups exist. */
  castHeading?: string;
}) {
  const t = useT();
  const locale = useDisplayLocale();
  const all = castGroups(credits);
  if (all.length === 0) return null;
  const groups = preview ? [{ ...all[0], credits: all[0].credits.slice(0, preview.max) }] : all;
  const hidden = preview && (all.length > 1 || all[0].credits.length > preview.max);
  // The credits' own kind label ("Artists") when they share one, else "People" (CORE-3337).
  const title = peopleHeading(credits, locale) || t("mediaDetailPeople");
  return (
    <section className="bf-section flex flex-col gap-3" aria-labelledby="bf-media-people">
      <h2 id="bf-media-people" className="bf-section-title">
        <span>{title}</span>
        {hidden && (
          <Link to={preview.to} className="bf-see-all">
            {t("seeAll")}
            <ChevronRight aria-hidden className="size-4" />
          </Link>
        )}
      </h2>
      {groups.map((group) => {
        const heading = group.department || (all.length > 1 ? (castHeading ?? "") : "");
        return (
          <div key={group.department} className="bf-cast-group">
            {heading && <h3 className="bf-cast-group-title">{heading}</h3>}
            <ul className="bf-shelf bf-cast" aria-label={heading || title}>
              {group.credits.map((credit) => (
                <CastCard key={credit.entityID} credit={credit} />
              ))}
            </ul>
          </div>
        );
      })}
    </section>
  );
}

function CastCard({ credit }: { credit: MediaCredit }) {
  const photo = creditPhoto(credit);
  const part = creditPart(credit);
  return (
    <li className="bf-cast-card">
      <Link className="bf-cast-link" to={`/entity/${encodeURIComponent(credit.entityID)}`}>
        <span className="bf-cast-photo" aria-hidden>
          {photo ? (
            <img
              src={artworkBucketURL(photo, "w320")}
              srcSet={artworkSrcSet(photo, ["w320", "w640"])}
              sizes="120px"
              alt=""
              loading="lazy"
            />
          ) : (
            <span className="bf-cast-initial">{nameInitial(credit.title)}</span>
          )}
        </span>
        <span className="bf-cast-name">{credit.title}</span>
        {part && <span className="bf-cast-part">{part}</span>}
      </Link>
    </li>
  );
}
