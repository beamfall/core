import { ArrowLeft, Film, Play } from "lucide-react";
import { useEffect, useState } from "react";
import { useLocation, useNavigate, useParams } from "react-router";
import { AppShell } from "@/components/chrome/AppShell";
import { Skeleton } from "@/components/ui/skeleton";
import { isAuthRejection } from "@/lib/api";
import { displayString } from "@/lib/display";
import type { EntityFieldInfo, SourceItem } from "@/lib/graphql/types";
import { useDisplayLocale, useT } from "@/lib/i18n";
import { useViewerIdentity } from "@/lib/session";
import {
  clearViewerCache,
  deleteViewerCache,
  readViewerCache,
  writeViewerCache,
} from "@/lib/viewerCache";
import { findCachedSectionItem } from "../components/HydratedSection";
import { NotAvailable } from "../components/NotAvailable";
import { SectionView } from "../components/SectionView";
import { ViewerActions } from "../components/ViewerActions";
import { artworkBucketURL, artworkSlotURL, artworkSrcSet } from "../lib/artwork";
import {
  fetchMedia,
  fetchMediaDetailFields,
  findCachedSiblings,
  type MediaDetail,
  mediaDetailCacheKey,
  readMediaPreview,
  resumeProgress,
} from "../lib/mediaDetail";
import { type PlayerNavState, readPlayerNavState } from "../lib/watchQueue";
import { preloadPlayer } from "../player/preload";
import "./media-detail.css";

const POSTER_BUCKETS = ["w320", "w640"] as const;
const POSTER_SIZES = "(max-width: 640px) 160px, 240px";
const TITLE_ID = "bf-media-detail-title";

type Status = "loading" | "ready" | "missing" | "failed";

// One Media item (/media/:mediaId, CORE-1144). Generic by invariant #1: the
// header shows the item's title, year, summary and whatever fields its plugin
// declared, each under the plugin's own label; core names none of them.
//
// media(id) carries no artwork and no playback position, so both come only
// from the card that opened the page (identity-bound navigation state, or the
// same item already hydrated into a section for this identity). A deep link
// therefore paints a title-led header with a plain Play; the player still
// resumes from the server timeline either way.
//
// Instant paint: a detail already loaded for this identity renders from the
// viewer cache and revalidates behind it. "Unknown media" (missing or hidden,
// no oracle) ends on the leak-safe NotAvailable surface; any other failure
// with nothing to show offers a retry.
export function MediaDetailPage() {
  const { mediaId = "" } = useParams();
  const location = useLocation();
  const navigate = useNavigate();
  const identity = useViewerIdentity();
  const t = useT();
  const locale = useDisplayLocale();
  const cacheKey = mediaDetailCacheKey(mediaId);
  const [detail, setDetail] = useState<MediaDetail | null>(
    () => readViewerCache<MediaDetail>(identity, cacheKey) ?? null,
  );
  const [status, setStatus] = useState<Status>(detail ? "ready" : "loading");
  const [attempt, setAttempt] = useState(0);

  const preview = readMediaPreview(location.state, identity, mediaId);
  const card: SourceItem | undefined = preview?.item ?? findCachedSectionItem(identity, mediaId);
  const siblings = findCachedSiblings(identity, mediaId, preview?.sectionID);

  // `attempt` is a dependency so that Try again re-runs the request.
  useEffect(() => {
    if (!mediaId) return;
    let alive = true;
    const controller = new AbortController();
    const cached = readViewerCache<MediaDetail>(identity, cacheKey);
    setDetail(cached ?? null);
    setStatus(cached ? "ready" : "loading");
    // The fields are secondary: their failure keeps whatever was cached.
    const fields = fetchMediaDetailFields(mediaId, controller.signal).catch(
      (): EntityFieldInfo[] | null => null,
    );
    fetchMedia(mediaId, controller.signal)
      .then(async (media) => {
        if (!alive) return;
        if (!media) {
          deleteViewerCache(identity, cacheKey);
          setDetail(null);
          setStatus("missing");
          return;
        }
        const next = { media, fields: (await fields) ?? cached?.fields ?? [] };
        if (!alive) return;
        writeViewerCache(identity, cacheKey, next);
        setDetail(next);
        setStatus("ready");
      })
      .catch((err: unknown) => {
        if (!alive) return;
        if (isAuthRejection(err)) {
          clearViewerCache();
          setDetail(null);
          setStatus("missing");
          return;
        }
        // A failed revalidation keeps the detail already shown for this identity.
        if (!cached) setStatus("failed");
      });
    return () => {
      alive = false;
      controller.abort();
    };
  }, [identity, mediaId, cacheKey, attempt]);

  function goBack() {
    // "default" is the entry the app was loaded on: nothing in-app to return to.
    if (location.key !== "default") navigate(-1);
    else navigate("/");
  }

  // Between a route change and the effect, state still holds the previous item.
  const current = detail?.media.id === mediaId ? detail : null;
  const media = current?.media;
  const title = media?.title || card?.title || "";

  function play() {
    // Forward the queue the opening card carried (CORE-1143), still bound to
    // the viewer identity it was listed for.
    const { queue } = readPlayerNavState(location.state, identity);
    const state: PlayerNavState = { title };
    if (identity && queue) {
      state.queue = queue;
      state.queueIdentity = identity;
    }
    navigate(`/watch/${encodeURIComponent(mediaId)}`, { state });
  }

  const back = (
    <button type="button" className="bf-detail-back" onClick={goBack}>
      <ArrowLeft aria-hidden />
      {t("back")}
    </button>
  );

  const retry = (
    <div className="bf-detail-error" role="alert">
      <p className="bf-detail-summary">{t("mediaDetailLoadFailed")}</p>
      <button type="button" className="bf-detail-back" onClick={() => setAttempt((n) => n + 1)}>
        {t("tryAgain")}
      </button>
    </div>
  );

  if (status === "missing") {
    return (
      <AppShell actions={<ViewerActions />}>
        <div className="bf-detail">
          {back}
          <NotAvailable />
        </div>
      </AppShell>
    );
  }

  if (!title) {
    return (
      <AppShell actions={<ViewerActions />}>
        <div className="bf-detail">
          {back}
          {status === "failed" ? retry : <DetailSkeleton label={t("loading")} />}
        </div>
      </AppShell>
    );
  }

  const poster = card ? artworkSlotURL(card, "poster") || artworkSlotURL(card, "thumb") : "";
  const wash = card ? artworkSlotURL(card, "backdrop") || poster : "";
  const progress = resumeProgress(card);
  const summary = media?.summary || card?.summary || "";
  const fields = current?.fields ?? [];
  const progressText = progress ? t("mediaDetailProgress", { percent: progress.percent }) : "";

  return (
    <AppShell actions={<ViewerActions />}>
      <article className="bf-detail" aria-labelledby={TITLE_ID}>
        {back}
        <div className="bf-detail-stage">
          {wash && (
            <div className="bf-detail-wash" aria-hidden>
              <img alt="" decoding="async" src={artworkBucketURL(wash, "w640")} />
            </div>
          )}
          <div className="bf-detail-panel">
            <DetailPoster key={poster} url={poster} />
            <div className="bf-detail-copy">
              <h1 id={TITLE_ID} className="bf-page-title">
                {title}
              </h1>
              {media?.year ? (
                <ul className="bf-detail-facts">
                  <li className="bf-detail-fact">{media.year}</li>
                </ul>
              ) : null}
              {progress && (
                <div className="bf-detail-progress">
                  <div
                    className="bf-detail-progress-track"
                    role="progressbar"
                    aria-label={progressText}
                    aria-valuemin={0}
                    aria-valuemax={100}
                    aria-valuenow={progress.percent}
                    aria-valuetext={progressText}
                  >
                    <div
                      className="bf-detail-progress-fill"
                      style={{ width: `${progress.percent}%` }}
                    />
                  </div>
                  <span aria-hidden>{progressText}</span>
                </div>
              )}
              <button
                type="button"
                className="bf-hero-action bf-detail-play"
                aria-label={t(progress ? "mediaDetailResumeLabel" : "mediaDetailPlayLabel", {
                  title,
                })}
                onClick={play}
                onPointerEnter={preloadPlayer}
                onFocus={preloadPlayer}
              >
                <Play aria-hidden />
                {progress ? t("mediaDetailResume") : t("play")}
              </button>
              {summary && <p className="bf-detail-summary">{summary}</p>}
              {!media && status === "loading" && <Skeleton aria-hidden className="h-4 w-40" />}
              {status === "failed" && !media && retry}
            </div>
          </div>
        </div>

        {fields.length > 0 && (
          <section className="bf-section flex flex-col gap-3" aria-labelledby="bf-media-fields">
            <h2 id="bf-media-fields" className="bf-section-title">
              <span>{t("mediaDetailDetails")}</span>
            </h2>
            <dl className="bf-metadata">
              {fields.map((field, i) => (
                <div key={`${field.name}-${i}`} className="contents">
                  <dt>{displayString(field.label, locale) || field.name}</dt>
                  <dd>{field.value}</dd>
                </div>
              ))}
            </dl>
          </section>
        )}

        {siblings && (
          <SectionView
            section={{
              ...siblings.section,
              kind: "shelf",
              title: t("mediaDetailMoreFrom", {
                section: displayString(siblings.section.title, locale),
              }),
            }}
            items={siblings.items}
            navigationID={`media:${mediaId}`}
          />
        )}
      </article>
    </AppShell>
  );
}

function DetailPoster({ url }: { url: string }) {
  const [failed, setFailed] = useState(false);
  return (
    <div className="bf-poster bf-detail-poster">
      {url && !failed ? (
        <img
          alt=""
          decoding="async"
          src={artworkBucketURL(url, "w320")}
          srcSet={artworkSrcSet(url, POSTER_BUCKETS)}
          sizes={POSTER_SIZES}
          onError={() => setFailed(true)}
        />
      ) : (
        // The same glyph a card draws when it has no artwork.
        <Film aria-hidden strokeWidth={1.5} className="bf-poster-glyph size-[28%]" />
      )}
    </div>
  );
}

function DetailSkeleton({ label }: { label: string }) {
  return (
    <div className="bf-detail-stage" role="status" aria-busy="true" aria-label={label}>
      <div className="bf-detail-panel">
        <Skeleton aria-hidden className="aspect-[2/3] w-full" />
        <div className="bf-detail-copy" aria-hidden>
          <Skeleton className="h-9 w-3/4" />
          <Skeleton className="h-5 w-16 rounded-full" />
          <Skeleton className="h-10 w-28" />
          <Skeleton className="h-4 w-full max-w-xl" />
          <Skeleton className="h-4 w-2/3 max-w-md" />
        </div>
      </div>
    </div>
  );
}
