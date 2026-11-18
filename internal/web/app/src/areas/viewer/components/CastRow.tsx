import { Link } from "react-router";
import type { MediaCredit } from "@/lib/graphql/types";
import { useT } from "@/lib/i18n";
import { artworkBucketURL, artworkSrcSet } from "../lib/artwork";
import { castGroups, creditPart, nameInitial } from "../lib/people";

// The cast row on a Media detail page (CORE-2406): the item's person-archetype
// credits in credit order, each a headshot card (the person's poster, or the
// name's initial) with the part they play, linking to the person page.
// Credits carrying a department are grouped under it; the rest share one row.
export function CastRow({ credits }: { credits: readonly MediaCredit[] | null | undefined }) {
  const t = useT();
  const groups = castGroups(credits);
  if (groups.length === 0) return null;
  return (
    <section className="bf-section flex flex-col gap-3" aria-labelledby="bf-media-people">
      <h2 id="bf-media-people" className="bf-section-title">
        <span>{t("mediaDetailPeople")}</span>
      </h2>
      {groups.map((group) => (
        <div key={group.department} className="bf-cast-group">
          {group.department && groups.length > 1 && (
            <h3 className="bf-cast-group-title">{group.department}</h3>
          )}
          <ul className="bf-shelf bf-cast" aria-label={group.department || t("mediaDetailPeople")}>
            {group.credits.map((credit) => (
              <CastCard key={credit.entityID} credit={credit} />
            ))}
          </ul>
        </div>
      ))}
    </section>
  );
}

function CastCard({ credit }: { credit: MediaCredit }) {
  const photo = credit.artworkSlots?.poster ?? credit.artworkSlots?.thumb ?? "";
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
