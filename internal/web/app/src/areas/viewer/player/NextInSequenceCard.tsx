import { ChevronRight } from "lucide-react";
import { Link } from "react-router";
import { useDisplayLocale, useT } from "@/lib/i18n";
import { artworkSlotURL } from "../lib/artwork";
import { mediaPath } from "../lib/mediaAddress";
import { mediaLinkState } from "../lib/mediaDetail";
import { sequenceLabel } from "../lib/relationOrder";
import type { SequenceNext } from "./useSequenceFinish";

/**
 * The player's finish state for a sequenced item (CORE-3013): the next item in
 * its container, with its cover, title and place, as a link to its detail
 * page. Following the link is the listener's choice; the card never counts
 * down or plays anything (ADR-0055 §2.4).
 */
export function NextInSequenceCard({
  next,
  identity,
  onDismiss,
}: {
  next: SequenceNext;
  identity: string | null;
  onDismiss: () => void;
}) {
  const t = useT();
  const locale = useDisplayLocale();
  const { item, container, sequence } = next;
  const cover = artworkSlotURL(item, "poster") || artworkSlotURL(item, "thumb");
  const place = sequence
    ? `${container.title} · ${t("mediaDetailSequenceNumber", { number: sequenceLabel(sequence, locale) })}`
    : container.title;
  return (
    <div
      className="bf-up-next bf-next-in-sequence"
      role="region"
      aria-label={t("mediaDetailNextInSeries")}
    >
      <span className="bf-up-next-title">{t("mediaDetailNextInSeries")}</span>
      <Link
        to={mediaPath("media", item.id)}
        state={mediaLinkState(item, identity)}
        className="bf-next-in-sequence-link"
      >
        <span className="bf-next-in-sequence-cover" aria-hidden>
          {cover && <img src={cover} alt="" loading="lazy" decoding="async" />}
        </span>
        <span className="bf-next-in-sequence-text">
          <span className="bf-up-next-media">{item.title}</span>
          <span className="bf-up-next-countdown">{place}</span>
        </span>
        <ChevronRight aria-hidden className="bf-next-in-sequence-chevron" />
      </Link>
      <div className="bf-up-next-actions">
        <button type="button" className="bf-control" onClick={onDismiss}>
          {t("dismiss")}
        </button>
      </div>
    </div>
  );
}
