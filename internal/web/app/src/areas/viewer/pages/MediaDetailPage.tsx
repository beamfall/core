import { ArrowLeft, ChevronRight, Play, RotateCcw } from "lucide-react";
import { useEffect, useState } from "react";
import { Link, useLocation, useNavigate, useParams } from "react-router";
import { AppShell } from "@/components/chrome/AppShell";
import { buttonVariants } from "@/components/ui/button";
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
import { ItemActions } from "../actions";
import { CastRow } from "../components/CastRow";
import { findCachedSectionItem } from "../components/HydratedSection";
import { MediaDetailMoreMenu } from "../components/MediaDetailMoreMenu";
import { NotAvailable } from "../components/NotAvailable";
import { PosterGlyph } from "../components/PosterGlyph";
import { SectionView } from "../components/SectionView";
import { ViewerActions } from "../components/ViewerActions";
import { WatchTogetherButton } from "../components/WatchTogetherButton";
import { artworkBucketURL, artworkSlotURL, artworkSrcSet } from "../lib/artwork";
import { artFrameClass } from "../lib/cardArt";
import { audioPeople, useContainerContext } from "../lib/containerContext";
import { mediaIdFromAddress } from "../lib/mediaAddress";
import {
  fetchMedia,
  fetchMediaDetailFields,
  findCachedSiblings,
  type MediaDetail,
  mediaDetailCacheKey,
  mediaLinkState,
  readMediaPreview,
  resumeProgress,
} from "../lib/mediaDetail";
import { useCanonicalMediaAddress } from "../lib/useCanonicalMediaAddress";
import {
  type DetailScore,
  detailFacts,
  fieldDisplayValue,
  formatScore,
  runtimeParts,
  viewerFacingFields,
} from "../lib/viewerFields";
import { type PlayerNavState, readPlayerNavState } from "../lib/watchQueue";
import { preloadPlayer } from "../player/preload";
import { PlayOnButton } from "../remote/PlayOnButton";
import "./media-detail.css";

const POSTER_BUCKETS = ["w320", "w640"] as const;
const POSTER_SIZES = "(max-width: 640px) 120px, 240px";
const BACKDROP_BUCKETS = ["w640", "w1280"] as const;
const TITLE_ID = "bf-media-detail-title";
/** An overview longer than this is clamped with a More toggle. */
const OVERVIEW_CLAMP_CHARS = 240;

type Status = "loading" | "ready" | "missing" | "failed";

// One Media item (/media/:mediaId, CORE-1144, CORE-1785). Generic by
// invariant #1: an artwork-led lockup of the item's title, facts and one
// filled Play/Resume, then the fields a viewer reads under the plugin's own
// labels. Bookkeeping fields (undeclared positions, the class discriminant,
// flag markers) stay in Manage. Container credits link up to the parent
// Entity, and the next item in the container's order is one step away.
//
// media(id) carries artwork slots but no playback position, so progress comes
// only from the card that opened the page (identity-bound navigation state, or
// the same item already hydrated into a section for this identity).
//
// Instant paint: a detail already loaded for this identity renders from the
// viewer cache and revalidates behind it. "Unknown media" (missing or hidden,
// no oracle) ends on the leak-safe NotAvailable surface; any other failure
// with nothing to show offers a retry.
export function MediaDetailPage() {
  // A readable address (CORE-2329) or a bare ID: the token names the item.
  const mediaId = mediaIdFromAddress(useParams()["*"]);
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

  // Between a route change and the effect, state still holds the previous item.
  const current = detail?.media.id === mediaId ? detail : null;
  const media = current?.media;
  useCanonicalMediaAddress("media", media ?? null);
  const context = useContainerContext(media, mediaId);
  const people = audioPeople(media);

  function goBack() {
    // "default" is the entry the app was loaded on: nothing in-app to return to.
    if (location.key !== "default") navigate(-1);
    else navigate("/");
  }

  const title = media?.title || card?.title || "";

  function watch(fromStart: boolean) {
    // Forward the queue the opening card carried (CORE-1143), still bound to
    // the viewer identity it was listed for.
    const { queue } = readPlayerNavState(location.state, identity);
    const state: PlayerNavState = { title };
    if (identity && queue) {
      state.queue = queue;
      state.queueIdentity = identity;
    }
    // ?t=0 is the player's "from the beginning" (it also skips pre-play).
    const suffix = fromStart ? "?t=0" : "";
    navigate(`/watch/${encodeURIComponent(mediaId)}${suffix}`, { state });
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

  const art = { ...(card ?? {}), artworkSlots: { ...card?.artworkSlots, ...media?.artworkSlots } };
  const poster =
    artworkSlotURL(art as SourceItem, "poster") || artworkSlotURL(art as SourceItem, "thumb");
  const backdrop = artworkSlotURL(art as SourceItem, "backdrop");
  const progress = resumeProgress(card);
  const summary = media?.summary || card?.summary || "";
  const allFields = current?.fields ?? [];
  // The probed length first (CORE-2314); a card's engagement duration is what
  // a player last reported and may be short of the real one.
  const facts = detailFacts(
    media,
    allFields,
    media?.durationSeconds ||
      card?.engagement?.durationSeconds ||
      context.current?.engagement?.durationSeconds,
  );
  const fields = viewerFacingFields(allFields, {
    title: media?.title || card?.title,
    year: facts.year,
    runtimeSeconds: facts.runtimeSeconds,
  });
  const factItems: FactItem[] = [
    { key: "year", text: facts.year ? String(facts.year) : "" },
    { key: "runtime", text: facts.runtimeSeconds ? formatRuntime(facts.runtimeSeconds, t) : "" },
    { key: "rating", text: facts.rating ?? "", variant: "rating" as const },
    ...scoreFact(facts.score, locale, t),
    ...facts.categories.map((category) => ({ key: `category:${category}`, text: category })),
  ].filter((item) => item.text !== "");
  const progressText = progress ? t("mediaDetailProgress", { percent: progress.percent }) : "";
  const next = context.next;

  return (
    <AppShell actions={<ViewerActions />}>
      <article className="bf-detail" aria-labelledby={TITLE_ID}>
        {back}
        <section className="bf-detail-stage" data-backdrop={backdrop ? "" : undefined}>
          {backdrop && <DetailBackdrop key={backdrop} url={backdrop} />}
          <div className="bf-detail-lockup">
            <DetailPoster key={poster} url={poster} mediaClass={card?.mediaClass} />
            <div className="bf-detail-copy">
              {context.parents.length > 0 && (
                <nav className="bf-detail-parents" aria-label={t("mediaDetailPartOf")}>
                  <ol>
                    {context.parents.map((parent) => (
                      <li key={parent.id}>
                        <Link to={`/entity/${encodeURIComponent(parent.id)}`}>{parent.title}</Link>
                      </li>
                    ))}
                  </ol>
                </nav>
              )}
              <h1 id={TITLE_ID} className="bf-page-title">
                {title}
              </h1>
              {people.length > 0 && (
                <p className="bf-detail-people">
                  {people.map((person, index) => (
                    <span key={person.id}>
                      {index > 0 && ", "}
                      <Link to={`/entity/${encodeURIComponent(person.id)}`}>{person.title}</Link>
                    </span>
                  ))}
                </p>
              )}
              {factItems.length > 0 && (
                <ul className="bf-detail-facts" aria-label={t("mediaDetailFacts")}>
                  {factItems.map((item) => (
                    <li key={item.key} className="bf-detail-fact" data-variant={item.variant}>
                      {item.spoken ? (
                        <>
                          <span aria-hidden="true">{item.text}</span>
                          <span className="sr-only">{item.spoken}</span>
                        </>
                      ) : (
                        item.text
                      )}
                    </li>
                  ))}
                </ul>
              )}
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
              <div className="bf-detail-actions">
                {/* The one accent-filled action on the page. */}
                <button
                  type="button"
                  className={`${buttonVariants({ size: "lg" })} bf-detail-play`}
                  aria-label={t(progress ? "mediaDetailResumeLabel" : "mediaDetailPlayLabel", {
                    title,
                  })}
                  onClick={() => watch(false)}
                  onPointerEnter={preloadPlayer}
                  onFocus={preloadPlayer}
                >
                  <Play aria-hidden />
                  {progress ? t("mediaDetailResume") : t("play")}
                </button>
                {progress && (
                  <button
                    type="button"
                    className={`${buttonVariants({ variant: "ghost", size: "lg" })} bf-detail-secondary`}
                    aria-label={t("mediaDetailStartOverLabel", { title })}
                    onClick={() => watch(true)}
                  >
                    <RotateCcw aria-hidden />
                    {t("mediaDetailStartOver")}
                  </button>
                )}
                {mediaId && (
                  <WatchTogetherButton
                    mediaID={mediaId}
                    title={title}
                    positionSeconds={progress?.positionSeconds}
                  />
                )}
                {next && (
                  <Link
                    to={`/media/${encodeURIComponent(next.id)}`}
                    state={mediaLinkState(next, identity)}
                    className={`${buttonVariants({ variant: "ghost", size: "lg" })} bf-detail-secondary`}
                    aria-label={t("mediaDetailNextLabel", { title: next.title })}
                  >
                    {t("mediaDetailNext")}
                    <ChevronRight aria-hidden />
                  </Link>
                )}
                {mediaId && (
                  <PlayOnButton
                    mediaID={mediaId}
                    title={title}
                    positionSeconds={progress?.positionSeconds}
                  />
                )}
                {mediaId && <MediaDetailMoreMenu mediaID={mediaId} title={title} />}
              </div>
              {/* The Profile's own state for this item (CORE-2436), keyed so a
                  route change to another item starts from that item's state. */}
              {mediaId && (
                <ItemActions
                  key={mediaId}
                  target={{ mediaID: mediaId }}
                  className="bf-detail-item-actions"
                />
              )}
              {summary && <DetailOverview key={summary} text={summary} />}
              {!media && status === "loading" && <Skeleton aria-hidden className="h-4 w-40" />}
              {status === "failed" && !media && retry}
            </div>
          </div>
        </section>

        {/* Audio names its people under the title instead (audioPeople), so a
            music artist, a person credit that may carry a photo, is named once. */}
        {media && media.mediaClass !== "audio" && <CastRow credits={media.credits} />}

        {fields.length > 0 && (
          <section className="bf-section flex flex-col gap-3" aria-labelledby="bf-media-fields">
            <h2 id="bf-media-fields" className="bf-section-title">
              <span>{t("mediaDetailDetails")}</span>
            </h2>
            <dl className="bf-metadata">
              {fields.map((field, i) => (
                <div key={`${field.name}-${i}`} className="contents">
                  <dt>{displayString(field.label, locale)}</dt>
                  <dd>{fieldDisplayValue(field, locale)}</dd>
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
              // The rest of the row the viewer came from. Naming that row
              // ("More from Next Up") reads as if the items came from it as a
              // place (CORE-2320), so the shelf gets a neutral title.
              title: t("mediaDetailKeepBrowsing"),
            }}
            items={siblings.items}
            navigationID={`media:${mediaId}`}
          />
        )}
      </article>
    </AppShell>
  );
}

/** One chip of the facts row; `spoken` replaces `text` for assistive technology. */
interface FactItem {
  key: string;
  text: string;
  variant?: "rating" | "score";
  spoken?: string;
}

/** The community score chip (CORE-2399): "★ 7.3", spoken with its source and vote count. */
function scoreFact(
  score: DetailScore | undefined,
  locale: string,
  t: ReturnType<typeof useT>,
): FactItem[] {
  if (!score) return [];
  const formatted = formatScore(score.value, locale);
  return [
    {
      key: "score",
      text: `\u2605 ${formatted}`,
      variant: "score",
      spoken: t("mediaDetailScoreLabel", {
        label: displayString(score.label, locale) || t("mediaDetailScore"),
        score: formatted,
        votes: score.votes,
      }),
    },
  ];
}

function formatRuntime(seconds: number, t: ReturnType<typeof useT>): string {
  const { hours, minutes } = runtimeParts(seconds);
  return hours > 0
    ? t("mediaDetailRuntimeHours", { hours, minutes })
    : t("mediaDetailRuntimeMinutes", { minutes });
}

function DetailBackdrop({ url }: { url: string }) {
  const [loaded, setLoaded] = useState(false);
  const [failed, setFailed] = useState(false);
  if (failed) return null;
  return (
    <div className="bf-detail-backdrop" aria-hidden>
      <img
        alt=""
        decoding="async"
        src={artworkBucketURL(url, "w1280")}
        srcSet={artworkSrcSet(url, BACKDROP_BUCKETS)}
        sizes="100vw"
        data-loaded={loaded ? "" : undefined}
        ref={(img) => {
          // A cached image can finish before React attaches onLoad.
          if (img?.complete && img.naturalWidth > 0 && !loaded) setLoaded(true);
        }}
        onLoad={() => setLoaded(true)}
        onError={() => setFailed(true)}
      />
      <div className="bf-detail-scrim" />
    </div>
  );
}

function DetailOverview({ text }: { text: string }) {
  const t = useT();
  const [expanded, setExpanded] = useState(false);
  const clampable = text.length > OVERVIEW_CLAMP_CHARS;
  return (
    <div className="bf-detail-overview">
      <p
        id="bf-media-detail-overview"
        className="bf-detail-summary"
        data-clamped={clampable && !expanded ? "" : undefined}
      >
        {text}
      </p>
      {clampable && (
        <button
          type="button"
          className="bf-detail-more"
          aria-expanded={expanded}
          aria-controls="bf-media-detail-overview"
          onClick={() => setExpanded((open) => !open)}
        >
          {expanded ? t("mediaDetailOverviewLess") : t("mediaDetailOverviewMore")}
        </button>
      )}
    </div>
  );
}

function DetailPoster({ url, mediaClass }: { url: string; mediaClass?: string }) {
  const [failed, setFailed] = useState(false);
  return (
    <div className={`${artFrameClass({ mediaClass })} bf-detail-poster`}>
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
        <PosterGlyph mediaClass={mediaClass} />
      )}
    </div>
  );
}

function DetailSkeleton({ label }: { label: string }) {
  return (
    <div className="bf-detail-stage" role="status" aria-busy="true" aria-label={label}>
      <div className="bf-detail-lockup">
        <Skeleton aria-hidden className="bf-detail-poster aspect-[2/3]" />
        <div className="bf-detail-copy" aria-hidden>
          <Skeleton className="h-9 w-3/4" />
          <Skeleton className="h-5 w-16 rounded-full" />
          <Skeleton className="h-11 w-28" />
          <Skeleton className="h-4 w-full max-w-xl" />
          <Skeleton className="h-4 w-2/3 max-w-md" />
        </div>
      </div>
    </div>
  );
}
