import { useState } from "react";
import { useNavigate } from "react-router";
import { displayString } from "@/lib/display";
import type { Section, SourceItem } from "@/lib/graphql/types";
import { useDisplayLocale, useT } from "@/lib/i18n";
import { artworkBucketURL, artworkSlotURL } from "../lib/artwork";
import { rememberRenderFocus } from "../lib/renderFocus";

// Hero Section, ported from the vanilla buildHero (app.js): one prominent
// featured item (the Source's first item) — a wide full-bleed frame with a
// backdrop-or-poster image behind a text-safe scrim, an eyebrow (the Section
// title), a Display-size title, an optional summary, and exactly one focusable
// primary action (no autoplay). Absent artwork leaves the accent-tinted
// fallback gradient (never a grey box).
export function Hero({
  section,
  item,
  navigationID,
}: {
  section: Section;
  item: SourceItem;
  navigationID?: string;
}) {
  const t = useT();
  const displayLocale = useDisplayLocale();
  const navigate = useNavigate();
  const [imageFailed, setImageFailed] = useState(false);
  const background = artworkSlotURL(item, "backdrop") || artworkSlotURL(item, "poster");
  const isMedia = item.kind === "media";

  function activate() {
    if (isMedia) {
      // Carry the title forward so the player's info overlay can name the Media
      // (the playback-info wire does not report it).
      navigate(`/watch/${encodeURIComponent(item.id)}`, {
        state: { title: item.title },
      });
    } else {
      navigate(`/entity/${encodeURIComponent(item.id)}`);
    }
  }

  return (
    <section className="bf-section">
      <div className="bf-hero">
        {background && !imageFailed && (
          <img
            alt=""
            src={artworkBucketURL(background, "w1280")}
            onError={() => setImageFailed(true)}
          />
        )}
        <div aria-hidden className="bf-hero-scrim" />
        <div className="bf-hero-copy">
          <span className="bf-eyebrow">{displayString(section.title, displayLocale)}</span>
          <h2 className="bf-hero-title">{item.title}</h2>
          {item.summary && <p className="bf-hero-summary">{item.summary}</p>}
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
            className="bf-hero-action"
          >
            {isMedia ? t("play") : t("open")}
          </button>
        </div>
      </div>
    </section>
  );
}
