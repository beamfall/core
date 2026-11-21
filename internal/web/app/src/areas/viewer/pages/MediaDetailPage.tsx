import { ArrowLeft, ChevronRight, Play, RotateCcw } from "lucide-react";
import { useEffect, useState } from "react";
import { Link, useLocation, useNavigate } from "react-router";
import { AppShell } from "@/components/chrome/AppShell";
import { buttonVariants } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { isAuthRejection } from "@/lib/api";
import { displayString } from "@/lib/display";
import type { EntityFieldInfo, GenericMediaInfo, SourceItem } from "@/lib/graphql/types";
import { useDisplayLocale, useT } from "@/lib/i18n";
import { useSession, useViewerIdentity } from "@/lib/session";
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
import { MediaMoreRows } from "../components/MediaMoreRows";
import { MediaRelationRows } from "../components/MediaRelationRows";
import {
  MediaTrackPickers,
  shownVersion,
  useMediaTracks,
  versionFileFacts,
} from "../components/MediaTrackPickers";
import { NotAvailable } from "../components/NotAvailable";
import { PosterGlyph } from "../components/PosterGlyph";
import { SectionView } from "../components/SectionView";
import { ViewerActions } from "../components/ViewerActions";
import { WatchTogetherButton } from "../components/WatchTogetherButton";
import { artworkBucketURL, artworkSlotURL, artworkSrcSet } from "../lib/artwork";
import { artFrameClass } from "../lib/cardArt";
import { audioPeople, type ContainerLink, useContainerContext } from "../lib/containerContext";
import { mediaPath } from "../lib/mediaAddress";
import {
  fetchMedia,
  fetchMediaDetailFields,
  type MediaDetail,
  mediaDetailCacheKey,
  mediaLinkState,
  readMediaPreview,
  resumeProgress,
} from "../lib/mediaDetail";
import { castHasDetail, creditsWithJob } from "../lib/people";
import { streamFacts } from "../lib/streamFacts";
import { useCanonicalMediaAddress, useMediaAddress } from "../lib/useCanonicalMediaAddress";
import {
  type CategoryFacts,
  type DetailScore,
  detailCreditRows,
  detailFacts,
  fieldDisplayValue,
  formatScore,
  runtimeParts,
  viewerFacingFields,
} from "../lib/viewerFields";
import {
  newStartID,
  type PlayerNavState,
  readPlayerNavState,
  type StartTracks,
} from "../lib/watchQueue";
import { preloadPlayer } from "../player/preload";
import { PlayOnButton } from "../remote/PlayOnButton";
import { castCrewPath } from "./CastCrewPage";
import "./media-detail.css";

const POSTER_BUCKETS = ["w320", "w640"] as const;
const POSTER_SIZES = "(max-width: 640px) 120px, 240px";
/** A wide still (an ordered item's thumb) fills more of the lockup (CORE-2469). */
const STILL_SIZES = "(max-width: 640px) 320px, 400px";
const BACKDROP_BUCKETS = ["w640", "w1280"] as const;
const TITLE_ID = "bf-media-detail-title";
/** An overview longer than this is clamped with a More toggle. */
const OVERVIEW_CLAMP_CHARS = 240;

type Status = "loading" | "ready" | "missing" | "failed";

/** People shown in the detail page's People row before "See all" (CORE-2503). */
const CAST_PREVIEW_MAX = 20;
/** The credit jobs named on the detail page's director and writer lines (CORE-2503). */
const DIRECTOR_JOBS = ["Director"];
const WRITER_JOBS = ["Writer", "Screenplay"];

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
  const { id: addressID, confirmed, pendingID } = useMediaAddress({ cachedDetail: true });
  const mediaId = addressID ?? "";
  const location = useLocation();
  const navigate = useNavigate();
  const identity = useViewerIdentity();
  const t = useT();
  const locale = useDisplayLocale();
  const cacheKey = mediaDetailCacheKey(mediaId);
  const [detail, setDetail] = useState<MediaDetail | null>(
    () => readViewerCache<MediaDetail>(identity, cacheKey) ?? null,
  );
  const [detailStatus, setStatus] = useState<Status>(detail ? "ready" : "loading");
  const status = addressID === null ? "loading" : addressID === "" ? "missing" : detailStatus;
  const [attempt, setAttempt] = useState(0);

  const previewID = pendingID ?? mediaId;
  const preview = readMediaPreview(location.state, identity, previewID);
  const card: SourceItem | undefined = preview?.item ?? findCachedSectionItem(identity, previewID);

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
  useCanonicalMediaAddress("media", media ?? null, confirmed);
  const context = useContainerContext(media, mediaId);
  // Audio names its people under the title, unless a credit carries a headshot
  // or a part (an audio drama's cast): then the cast row shows them instead, so
  // no one is named twice.
  const castRow = !!media && (media.mediaClass !== "audio" || castHasDetail(media.credits));
  const people = castRow ? [] : audioPeople(media);
  const crewLines = [
    {
      key: "directors",
      label: "mediaDetailDirectors" as const,
      people: creditsWithJob(media?.credits, DIRECTOR_JOBS),
    },
    {
      key: "writers",
      label: "mediaDetailWriters" as const,
      people: creditsWithJob(media?.credits, WRITER_JOBS),
    },
  ].filter((line) => line.people.length > 0);
  // Version, audio and subtitle picks made here before playback (CORE-2442), for one Media.
  const [trackPicks, setTrackPicks] = useState<StartTracks | null>(null);
  // Read alongside the item, once, for the pickers, the facts row's stream
  // badges (CORE-2469) and Details (CORE-2509).
  const versions = useMediaTracks(mediaId, attempt);
  const profileID = useSession().session?.profileID;

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
    // Every Play or Start over is its own start, so pressing one while the
    // mini player already plays this Media starts it again with these picks.
    const state: PlayerNavState = { title, startID: newStartID() };
    if (identity && queue) {
      state.queue = queue;
      state.queueIdentity = identity;
    }
    if (
      trackPicks?.mediaID === mediaId &&
      (trackPicks.versionID !== undefined ||
        trackPicks.audioIndex !== undefined ||
        trackPicks.captionID !== undefined)
    ) {
      state.tracks = trackPicks;
    }
    // ?t=0 is the player's "from the beginning" (it also skips pre-play).
    const suffix = fromStart ? "?t=0" : "";
    navigate(`${mediaPath("watch", previewID)}${suffix}`, { state });
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
  const ordered = orderedPosition(media, context.container, locale, t);
  // An ordered item's own still frames it better than the poster it shares
  // with its whole grouping (CORE-2469).
  const still = ordered ? artworkSlotURL(art as SourceItem, "thumb") : "";
  const poster =
    still ||
    artworkSlotURL(art as SourceItem, "poster") ||
    artworkSlotURL(art as SourceItem, "thumb");
  const heading = (ordered && displayString(media?.headline, locale)) || title;
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
    dateField: facts.date?.name,
  });
  const factItems: FactItem[] = [
    {
      key: "year",
      text: facts.date
        ? fieldDisplayValue(facts.date, locale)
        : facts.year
          ? String(facts.year)
          : "",
    },
    { key: "runtime", text: facts.runtimeSeconds ? formatRuntime(facts.runtimeSeconds, t) : "" },
    ...streamFacts(versions, {
      mono: t("trackChannelsMono"),
      stereo: t("trackChannelsStereo"),
      channels: (count) => t("trackChannelsCount", { count }),
    }),
    { key: "rating", text: facts.rating ?? "", variant: "rating" as const },
    ...scoreFact(facts.score, locale, t),
    ...facts.categories.flatMap((group) => categoryFacts(group, t)),
  ].filter((item) => item.text !== "");
  const creditRows = media ? detailCreditRows(media.credits) : [];
  const fileFacts = media
    ? versionFileFacts(shownVersion(versions, trackPicks, profileID, mediaId), t, locale)
    : [];
  const hasDetails = creditRows.length > 0 || fields.length > 0 || fileFacts.length > 0;
  // Until the item arrives, the stage keeps the space its backdrop, facts
  // and overview will take, so they do not push the page down (CORE-2509).
  const reserving = !media && status === "loading";
  const progressText = progress ? t("mediaDetailProgress", { percent: progress.percent }) : "";
  const next = context.next;
  // The grouping is linked above; the nearest container is in the position line.
  const parents = ordered
    ? context.parents.filter((parent) => parent.id !== ordered.container.id)
    : context.parents;
  // The nearest container's other items, in its order (CORE-2469).
  const moreIn = context.items.filter((item) => item.id !== mediaId);

  return (
    <AppShell actions={<ViewerActions />}>
      <article className="bf-detail" aria-labelledby={TITLE_ID}>
        {back}
        <section className="bf-detail-stage" data-backdrop={backdrop || reserving ? "" : undefined}>
          {backdrop && <DetailBackdrop key={backdrop} url={backdrop} />}
          <div className="bf-detail-lockup" data-wide={still ? "" : undefined}>
            <DetailPoster key={poster} url={poster} mediaClass={card?.mediaClass} wide={!!still} />
            <div className="bf-detail-copy">
              {parents.length > 0 && (
                <nav className="bf-detail-parents" aria-label={t("mediaDetailPartOf")}>
                  <ol>
                    {parents.map((parent) => (
                      <li key={parent.id}>
                        <Link to={`/entity/${encodeURIComponent(parent.id)}`}>{parent.title}</Link>
                      </li>
                    ))}
                  </ol>
                </nav>
              )}
              {ordered && (
                <p className="bf-detail-position">
                  <Link to={`/entity/${encodeURIComponent(ordered.container.id)}`}>
                    {ordered.containerText}
                  </Link>
                  {ordered.itemText && <span>{` · ${ordered.itemText}`}</span>}
                </p>
              )}
              <h1 id={TITLE_ID} className="bf-page-title">
                {heading}
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
              {reserving && factItems.length === 0 && <FactsPlaceholder />}
              {factItems.length > 0 && (
                <ul className="bf-detail-facts" aria-label={t("mediaDetailFacts")}>
                  {factItems.map((item) => (
                    <li key={item.key} className="bf-detail-fact" data-variant={item.variant}>
                      {item.to ? (
                        <Link to={item.to}>{item.text}</Link>
                      ) : item.spoken ? (
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
              {crewLines.length > 0 && (
                <dl className="bf-detail-crew">
                  {crewLines.map((line) => (
                    <div key={line.key}>
                      <dt>{t(line.label, { count: line.people.length })}</dt>
                      <dd>
                        {line.people.map((person, index) => (
                          <span key={person.entityID}>
                            {index > 0 && ", "}
                            <Link to={`/entity/${encodeURIComponent(person.entityID)}`}>
                              {person.title}
                            </Link>
                          </span>
                        ))}
                      </dd>
                    </div>
                  ))}
                </dl>
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
                    to={mediaPath("media", next.id)}
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
              {media && (
                <MediaTrackPickers
                  mediaID={mediaId}
                  versions={versions}
                  picks={trackPicks}
                  onPick={setTrackPicks}
                />
              )}
              {/* The Profile's own state for this item (CORE-2436), keyed so a
                  route change to another item starts from that item's state. */}
              {mediaId && (
                <ItemActions
                  key={mediaId}
                  target={{ mediaID: mediaId }}
                  isAudio={(media?.mediaClass ?? card?.mediaClass) === "audio"}
                  className="bf-detail-item-actions"
                />
              )}
              {summary && <DetailOverview key={summary} text={summary} />}
              {reserving && !summary && <OverviewPlaceholder />}
              {status === "failed" && !media && retry}
            </div>
          </div>
        </section>

        {media && castRow && (
          <CastRow
            credits={media.credits}
            preview={{ max: CAST_PREVIEW_MAX, to: castCrewPath(mediaId) }}
          />
        )}

        {/* Details renders with the item, before the rows that load on their
            own, so their arrival does not move it (CORE-2509). */}
        {hasDetails && (
          <section className="bf-section flex flex-col gap-3" aria-labelledby="bf-media-fields">
            <h2 id="bf-media-fields" className="bf-section-title">
              <span>{t("mediaDetailDetails")}</span>
            </h2>
            <dl className="bf-metadata">
              {creditRows.map((row) => (
                <div key={row.key} className="contents">
                  <dt>{displayString(row.label, locale)}</dt>
                  <dd className="bf-detail-credits">
                    {row.credits.map((credit, index) => (
                      <span key={credit.entityID || credit.title}>
                        {index > 0 && ", "}
                        {credit.entityID ? (
                          <Link to={`/entity/${encodeURIComponent(credit.entityID)}`}>
                            {credit.title}
                          </Link>
                        ) : (
                          credit.title
                        )}
                      </span>
                    ))}
                  </dd>
                </div>
              ))}
              {fields.map((field, i) => (
                <div key={`${field.name}-${i}`} className="contents">
                  <dt>{displayString(field.label, locale)}</dt>
                  <dd>{fieldDisplayValue(field, locale)}</dd>
                </div>
              ))}
              {fileFacts.map((fact) => (
                <div key={`file:${fact.key}`} className="contents">
                  <dt>{fact.label}</dt>
                  <dd>{fact.value}</dd>
                </div>
              ))}
            </dl>
          </section>
        )}

        {media && <MediaRelationRows mediaID={mediaId} />}

        {context.container && moreIn.length > 0 && (
          <SectionView
            section={{
              id: "media-more-in",
              kind: "shelf",
              title: t("mediaDetailMoreIn", { title: context.container.title }),
              artworkSlot: "poster",
              source: { kind: "more-in" },
            }}
            items={moreIn}
            navigationID={`media:${mediaId}`}
          />
        )}

        {media && (
          <MediaMoreRows mediaID={mediaId} credits={media.credits} exclude={context.items} />
        )}
      </article>
    </AppShell>
  );
}

/** The position line of an ordered item: "Part 2" (its container) and "Chapter 5" (itself). */
interface OrderedPosition {
  container: ContainerLink;
  containerText: string;
  itemText: string;
}

/**
 * An item with a container and an index reads by its place (CORE-2469): the
 * container's kind name and the item's sequence, then the item's class label
 * and index, each from plugin display names. Without a kind name or sequence
 * the container shows its title; without a class label the index stands alone.
 */
function orderedPosition(
  media: GenericMediaInfo | undefined,
  container: ContainerLink | null,
  locale: string,
  t: ReturnType<typeof useT>,
): OrderedPosition | null {
  if (!media || !container || media.index === undefined) return null;
  const number = (value: number) => value.toLocaleString(locale);
  const kind = displayString(container.kindLabel, locale);
  const containerText =
    kind && media.sequence !== undefined
      ? t("mediaDetailOrdinal", { label: kind, number: number(media.sequence) })
      : container.title;
  const label = displayString(media.classLabel, locale);
  const itemText = label
    ? t("mediaDetailOrdinal", { label, number: number(media.index) })
    : number(media.index);
  return { container, containerText, itemText };
}

/**
 * One chip of the facts row; `spoken` replaces `text` for assistive
 * technology, and a chip with `to` is a link to that page.
 */
interface FactItem {
  key: string;
  text: string;
  variant?: "rating" | "score" | "link";
  spoken?: string;
  to?: string;
}

/** One category kind's chips, each linked to its Entity, then "+N" for the rest (CORE-2504). */
function categoryFacts(group: CategoryFacts, t: ReturnType<typeof useT>): FactItem[] {
  const chips: FactItem[] = group.shown.map((credit) => ({
    key: `category:${group.kind}:${credit.entityID || credit.title}`,
    text: credit.title,
    ...(credit.entityID
      ? { variant: "link" as const, to: `/entity/${encodeURIComponent(credit.entityID)}` }
      : {}),
  }));
  if (group.more > 0) {
    chips.push({
      key: `category:${group.kind}:more`,
      text: `+${group.more}`,
      spoken: t("mediaDetailMoreCount", { count: group.more }),
    });
  }
  return chips;
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

function DetailPoster({
  url,
  mediaClass,
  wide = false,
}: {
  url: string;
  mediaClass?: string;
  wide?: boolean;
}) {
  const [failed, setFailed] = useState(false);
  const frame = wide ? "bf-poster bf-poster-wide" : artFrameClass({ mediaClass });
  return (
    <div className={`${frame} bf-detail-poster`}>
      {url && !failed ? (
        <img
          alt=""
          decoding="async"
          src={artworkBucketURL(url, wide ? "w640" : "w320")}
          srcSet={artworkSrcSet(url, POSTER_BUCKETS)}
          sizes={wide ? STILL_SIZES : POSTER_SIZES}
          onError={() => setFailed(true)}
        />
      ) : (
        // The same glyph a card draws when it has no artwork.
        <PosterGlyph mediaClass={mediaClass} />
      )}
    </div>
  );
}

/** The facts row's space while the item loads. */
function FactsPlaceholder() {
  return (
    <div className="bf-detail-facts" aria-hidden>
      <Skeleton className="h-6 w-12" />
      <Skeleton className="h-6 w-16" />
      <Skeleton className="h-6 w-20" />
    </div>
  );
}

/** The overview's space while the item loads: three summary lines. */
function OverviewPlaceholder() {
  return (
    <div className="bf-detail-overview" aria-hidden>
      <Skeleton className="h-4 w-full max-w-xl" />
      <Skeleton className="h-4 w-full max-w-xl" />
      <Skeleton className="h-4 w-2/3 max-w-md" />
    </div>
  );
}

/**
 * The page before anything is known, in the stage's shape (CORE-2509):
 * backdrop space, poster, title, facts, actions, item actions and overview,
 * so the stage does not grow when the item paints.
 */
function DetailSkeleton({ label }: { label: string }) {
  return (
    <div
      className="bf-detail-stage"
      data-backdrop=""
      role="status"
      aria-busy="true"
      aria-label={label}
    >
      <div className="bf-detail-lockup">
        <Skeleton aria-hidden className="bf-detail-poster aspect-[2/3]" />
        <div className="bf-detail-copy" aria-hidden>
          <Skeleton className="h-9 w-3/4" />
          <FactsPlaceholder />
          <Skeleton className="h-11 w-28" />
          <Skeleton className="h-9 w-48" />
          <OverviewPlaceholder />
        </div>
      </div>
    </div>
  );
}
