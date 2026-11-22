import { Play } from "lucide-react";
import { useState } from "react";
import { useNavigate } from "react-router";
import { buttonVariants } from "@/components/ui/button";
import { displayString } from "@/lib/display";
import type { Section, SourceItem } from "@/lib/graphql/types";
import { useDisplayLocale, useT } from "@/lib/i18n";
import { useViewerIdentity } from "@/lib/session";
import { artworkBucketURL, artworkSlotURL, artworkSrcSet } from "../lib/artwork";
import { cardByline } from "../lib/cardArt";
import { cardRating } from "../lib/cardState";
import { cardHeading } from "../lib/cardTitles";
import { entityLinkState } from "../lib/entityPreview";
import { mediaPath } from "../lib/mediaAddress";
import { rememberRenderFocus } from "../lib/renderFocus";
import { isSpoilerCandidate, useSpoilerGuard } from "../lib/spoilerGuard";
import { runtimeParts, serverDurationSummary } from "../lib/viewerFields";

// Hero Section, ported from the vanilla buildHero (app.js): one prominent
// featured item (the Source's first item) — a wide full-bleed frame with a
// backdrop-or-poster image behind a text-safe scrim, a visually hidden label (the Section
// title), a Display-size title, an optional summary, and exactly one focusable
// primary action (no autoplay). Absent artwork leaves the accent-tinted
// surface fallback.
//
// An item a container holds (an episode in its show) is headed by the
// container's title with the item's own line under it, as cards are
// (cardHeading), followed by a facts row: who made it, year, rating, score
// and runtime.
// The summary is dropped when it only repeats a fact, such as a server
// runtime summary ("49 min"), and while the spoiler guard hides an unplayed
// child (CORE-3057). A hidden child shows only its container's poster, since
// its own poster may be a frame; useHomeArtwork skips its art too.
export function Hero({
  section,
  item,
  navigationID,
  artwork,
}: {
  section: Section;
  item: SourceItem;
  navigationID?: string;
  artwork?: { url: string; ready: boolean; onLoad: () => void; onError: () => void };
}) {
  const t = useT();
  const displayLocale = useDisplayLocale();
  const navigate = useNavigate();
  const identity = useViewerIdentity();
  const [imageFailed, setImageFailed] = useState(false);
  const spoiler = useSpoilerGuard(isSpoilerCandidate(item, item.container?.id));
  const spoilerHidden = spoiler.state(item, item.container?.id) === "hidden";
  const background =
    artwork?.url ??
    (spoilerHidden
      ? (item.container?.artwork?.poster ?? "")
      : artworkSlotURL(item, "backdrop") || artworkSlotURL(item, "poster"));
  const isMedia = item.kind === "media";
  const { heading, lead } = cardHeading(item, true, displayLocale);
  const rating = cardRating(item, displayLocale, t);
  const seconds = item.durationSeconds ?? 0;
  // The server writes a Media item's running time as its summary; that line is
  // the runtime fact for a clip under a minute and is never repeated after it.
  const durationSummary = serverDurationSummary(seconds);
  const runtime = seconds >= 60 ? heroRuntime(seconds, t) : durationSummary;
  const facts = [
    cardByline(item),
    item.year && item.year > 0 ? String(item.year) : "",
    rating?.text ?? "",
    runtime,
  ].filter(Boolean);
  const summary = item.summary?.trim() ?? "";
  const showSummary =
    !!summary &&
    summary !== lead &&
    summary !== durationSummary &&
    !facts.includes(summary) &&
    !spoilerHidden;
  const showFacts = facts.length > 0 || !!rating?.score;

  function activate() {
    if (isMedia) {
      // Carry the title forward so the player's info overlay can name the Media
      // (the playback-info wire does not report it).
      navigate(mediaPath("watch", item.id), {
        state: { title: lead && lead !== heading ? `${heading} · ${lead}` : heading },
      });
    } else {
      navigate(`/entity/${encodeURIComponent(item.id)}`, {
        state: entityLinkState(item, identity),
      });
    }
  }

  return (
    <section className="bf-section" hidden={artwork && !artwork.ready}>
      <div className="bf-hero">
        {background && !imageFailed && (
          <img
            alt=""
            fetchPriority="high"
            src={artworkBucketURL(background, "w1280")}
            srcSet={artworkSrcSet(background, ["w640", "w1280"])}
            sizes="100vw"
            onLoad={artwork?.onLoad}
            onError={artwork?.onError ?? (() => setImageFailed(true))}
          />
        )}
        <div aria-hidden className="bf-hero-scrim" />
        <div className="bf-hero-copy">
          <span className="sr-only">{displayString(section.title, displayLocale)}</span>
          <h2 className="bf-hero-title">{heading}</h2>
          {lead && <p className="bf-hero-lead">{lead}</p>}
          {showFacts && (
            <p className="bf-hero-facts">
              {facts.map((fact) => (
                <span key={fact}>{fact}</span>
              ))}
              {rating?.score && (
                <span>
                  <span aria-hidden>★ {rating.score}</span>
                  <span className="sr-only">{t("cardAudienceScore", { score: rating.score })}</span>
                </span>
              )}
            </p>
          )}
          {showSummary && <p className="bf-hero-summary">{summary}</p>}
          <button
            type="button"
            onClick={activate}
            onFocus={() => {
              if (navigationID) {
                rememberRenderFocus({
                  navigation: navigationID,
                  section: section.id,
                  item: item.id,
                });
              }
            }}
            data-focus-item={navigationID ? "" : undefined}
            data-focus-navigation={navigationID}
            data-focus-section={navigationID ? section.id : undefined}
            data-focus-id={navigationID ? item.id : undefined}
            className={`${buttonVariants({ size: "lg" })} bf-hero-action`}
          >
            {isMedia && <Play aria-hidden fill="currentColor" />}
            {isMedia ? t("play") : t("open")}
          </button>
        </div>
      </div>
    </section>
  );
}

function heroRuntime(seconds: number, t: ReturnType<typeof useT>): string {
  const { hours, minutes } = runtimeParts(seconds);
  return hours > 0
    ? t("mediaDetailRuntimeHours", { hours, minutes })
    : t("mediaDetailRuntimeMinutes", { minutes });
}
