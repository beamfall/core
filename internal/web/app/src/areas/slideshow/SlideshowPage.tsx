import { ChevronLeft, ChevronRight, Maximize, Pause, Play, Settings, X } from "lucide-react";
import { useCallback, useEffect, useId, useMemo, useRef, useState } from "react";
import { useNavigate, useSearchParams } from "react-router";
import { Button } from "@/components/ui/button";
import { displayString } from "@/lib/display";
import type { LibrarySummary, SourceItem } from "@/lib/graphql/types";
import { useDisplayLocale, useT } from "@/lib/i18n";
import { useSession, useViewerIdentity } from "@/lib/session";
import { useHiddenItems } from "../viewer/actions";
import { artworkBucketURL, artworkSlotURL } from "../viewer/lib/artwork";
import { fetchViewerCollections, type LibraryCollections } from "../viewer/lib/collections";
import { fetchMediaDetailFields } from "../viewer/lib/mediaDetail";
import { fetchPreviewInfo } from "../viewer/lib/playback";
import {
  clampInterval,
  DEFAULT_INTERVAL_SECONDS,
  fetchSlideshowItems,
  MAX_INTERVAL_SECONDS,
  MIN_INTERVAL_SECONDS,
  nextDelay,
  parseSlideshowSource,
  SLIDESHOW_SOURCE_KINDS,
  type Slide,
  type SlideshowSource,
  type SlideshowSourceKind,
  sameSource,
  slideDate,
  slidesFrom,
  slideshowPath,
} from "./lib/slideshow";
import {
  defaultSlideshowMotion,
  oscilluxSlideMotion,
  SLIDESHOW_MOTIONS,
  type SlideMotionDriver,
  type SlideshowMotion,
  slideshowMotionAvailable,
} from "./lib/slideshowMotion";
import "./slideshow.css";

// The web photo slideshow (CORE-2981, photos design §3.1). It reads only what
// the server lists for the acting Profile and Reveal state, drops locked and
// hidden items, and is engagement-silent: slides use artwork derivatives and
// generated preview clips, never a playback decision or session. Audio the
// docked player is playing keeps going underneath (PlayerHost sits above the
// routes).

/** The slide image derivative: large enough for a screen, never the original. */
const SLIDE_BUCKET = "w1280";
const PREFS_KEY = "bf.slideshow.prefs";
const CHROME_HIDE_MS = 3000;
const NO_LIBRARIES: readonly LibrarySummary[] = [];

interface SlideshowPrefs {
  interval: number;
  motion: SlideshowMotion;
  clock: boolean;
  caption: boolean;
  nowPlaying: boolean;
  darkRoom: boolean;
}

function defaultPrefs(): SlideshowPrefs {
  return {
    interval: DEFAULT_INTERVAL_SECONDS,
    motion: defaultSlideshowMotion(),
    clock: true,
    caption: true,
    nowPlaying: true,
    darkRoom: false,
  };
}

function loadPrefs(): SlideshowPrefs {
  const prefs = defaultPrefs();
  try {
    const stored = JSON.parse(localStorage.getItem(PREFS_KEY) ?? "null");
    if (!stored || typeof stored !== "object") return prefs;
    const motion = (SLIDESHOW_MOTIONS as readonly unknown[]).includes(stored.motion)
      ? (stored.motion as SlideshowMotion)
      : prefs.motion;
    return {
      interval: clampInterval(stored.interval),
      motion: slideshowMotionAvailable(motion) ? motion : prefs.motion,
      clock: typeof stored.clock === "boolean" ? stored.clock : prefs.clock,
      caption: typeof stored.caption === "boolean" ? stored.caption : prefs.caption,
      nowPlaying: typeof stored.nowPlaying === "boolean" ? stored.nowPlaying : prefs.nowPlaying,
      darkRoom: typeof stored.darkRoom === "boolean" ? stored.darkRoom : prefs.darkRoom,
    };
  } catch {
    return prefs;
  }
}

function savePrefs(prefs: SlideshowPrefs) {
  try {
    localStorage.setItem(PREFS_KEY, JSON.stringify(prefs));
  } catch {
    // Storage is a convenience; the slideshow runs without it.
  }
}

function slideImageURL(item: SourceItem): string {
  return artworkBucketURL(artworkSlotURL(item, "poster"), SLIDE_BUCKET);
}

/** What the play queue is playing, as the player publishes it to the Media Session. */
function readNowPlaying(): string {
  const session = typeof navigator !== "undefined" ? navigator.mediaSession : undefined;
  if (!session || session.playbackState !== "playing" || !session.metadata) return "";
  const { title, artist } = session.metadata;
  return [title, artist].filter(Boolean).join(" · ");
}

function interactiveTarget(target: EventTarget | null): boolean {
  if (!(target instanceof HTMLElement)) return false;
  return !!target.closest("input, select, textarea, [contenteditable='true']");
}

/** The slideshow route. A Profile or Reveal change remounts it, so nothing carries across. */
export function SlideshowPage() {
  const identity = useViewerIdentity();
  return <Slideshow key={identity ?? ""} />;
}

function Slideshow() {
  const t = useT();
  const locale = useDisplayLocale();
  const navigate = useNavigate();
  const [searchParams] = useSearchParams();
  const source = useMemo(() => parseSlideshowSource(searchParams), [searchParams]);
  const { manifest } = useSession();
  const libraries = manifest?.libraries ?? NO_LIBRARIES;
  const { mutes, failed: hiddenFailed, isHidden } = useHiddenItems();
  const hiddenReady = mutes !== null || hiddenFailed;

  const rootRef = useRef<HTMLDivElement | null>(null);
  const [prefs, setPrefs] = useState<SlideshowPrefs>(loadPrefs);
  const [sheetOpen, setSheetOpen] = useState(source === null);
  const [items, setItems] = useState<SourceItem[] | null>(null);
  const [loadFailed, setLoadFailed] = useState(false);
  const [broken, setBroken] = useState<ReadonlySet<string>>(() => new Set());
  const [index, setIndex] = useState(0);
  const [paused, setPaused] = useState(false);
  // When the current slide was painted, and how long it had already shown before a pause.
  const [shown, setShown] = useState<{ id: string; at: number; held: number } | null>(null);
  const [chrome, setChrome] = useState(true);
  const [now, setNow] = useState(() => new Date());
  const [nowPlaying, setNowPlaying] = useState("");
  const [clipURLs, setClipURLs] = useState<Record<string, string>>({});
  const [captions, setCaptions] = useState<Record<string, string>>({});

  const updatePrefs = useCallback((patch: Partial<SlideshowPrefs>) => {
    setPrefs((current) => {
      const next = { ...current, ...patch };
      savePrefs(next);
      return next;
    });
  }, []);

  // Load the Source's items, progressively, whenever the Source changes.
  useEffect(() => {
    setItems(null);
    setLoadFailed(false);
    setBroken(new Set());
    setIndex(0);
    setShown(null);
    if (!source) return;
    const controller = new AbortController();
    fetchSlideshowItems(source, (next) => setItems(next), controller.signal).catch(() => {
      if (!controller.signal.aborted) setLoadFailed(true);
    });
    return () => controller.abort();
  }, [source]);

  const slides = useMemo<Slide[]>(() => {
    if (!items || !hiddenReady) return [];
    return slidesFrom(items, isHidden).filter((slide) => !broken.has(slide.item.id));
  }, [items, hiddenReady, isHidden, broken]);
  const count = slides.length;
  const current = count > 0 ? slides[index % count] : null;
  const intervalMs = prefs.interval * 1000;

  const step = useCallback(
    (delta: number) => {
      if (count === 0) return;
      setShown(null);
      setIndex((value) => ((((value % count) + delta) % count) + count) % count);
    },
    [count],
  );

  const markPainted = useCallback((id: string) => {
    setShown((value) => (value?.id === id ? value : { id, at: performance.now(), held: 0 }));
  }, []);

  const markBroken = useCallback((id: string) => {
    setShown(null);
    setBroken((value) => new Set(value).add(id));
  }, []);

  // A slide stays for the interval from when it was painted; a pause holds it.
  const running = !paused && !sheetOpen;
  useEffect(() => {
    if (!running || !current || !shown || shown.id !== current.item.id || count < 2) return;
    const delay = nextDelay(shown.at, performance.now(), intervalMs, shown.held);
    const timer = window.setTimeout(() => step(1), delay);
    return () => window.clearTimeout(timer);
  }, [running, current, shown, count, intervalMs, step]);

  // Pausing banks the time already shown; resuming restarts the clock from now.
  const wasRunning = useRef(running);
  useEffect(() => {
    if (wasRunning.current === running) return;
    wasRunning.current = running;
    setShown((value) => {
      if (!value) return value;
      const at = performance.now();
      return running
        ? { ...value, at }
        : { ...value, at, held: Math.min(intervalMs, value.held + (at - value.at)) };
    });
  }, [running, intervalMs]);

  // Preload the next slide's image so the cut lands on a decoded picture.
  useEffect(() => {
    if (count < 2) return;
    const next = slides[(index + 1) % count];
    const url = slideImageURL(next.item);
    if (!url) return;
    const image = new Image();
    image.decoding = "async";
    image.src = url;
    image.decode?.().catch(() => {});
  }, [slides, index, count]);

  // A clip's generated preview clip: an asset read, not a playback decision.
  const clipID = current?.clip ? current.item.id : "";
  useEffect(() => {
    if (!clipID || clipID in clipURLs) return;
    let cancelled = false;
    fetchPreviewInfo(clipID)
      .then((info) => info.assets?.previewClip ?? "")
      .catch(() => "")
      .then((url) => {
        if (!cancelled) setClipURLs((value) => ({ ...value, [clipID]: url }));
      });
    return () => {
      cancelled = true;
    };
  }, [clipID, clipURLs]);

  // The caption's date, read once per slide while the caption is on.
  const captionID = prefs.caption ? (current?.item.id ?? "") : "";
  useEffect(() => {
    if (!captionID || captionID in captions) return;
    const controller = new AbortController();
    fetchMediaDetailFields(captionID, controller.signal)
      .then((fields) => slideDate(fields, locale))
      .catch(() => (controller.signal.aborted ? null : ""))
      .then((date) => {
        if (date !== null) setCaptions((value) => ({ ...value, [captionID]: date }));
      });
    return () => controller.abort();
  }, [captionID, captions, locale]);

  // The motion seam: Oscillux drives slide changes once it can (CORE-2980).
  const driverRef = useRef<SlideMotionDriver | null>(null);
  const previousSlideRef = useRef<HTMLElement | null>(null);
  useEffect(() => {
    const driver = oscilluxSlideMotion(prefs.motion);
    driverRef.current = driver;
    return () => {
      driver?.dispose();
      driverRef.current = null;
    };
  }, [prefs.motion]);
  const slideRef = useCallback((element: HTMLElement | null) => {
    if (!element) return;
    driverRef.current?.advance(previousSlideRef.current, element);
    previousSlideRef.current = element;
  }, []);

  // The clock and now-playing line refresh once a second.
  useEffect(() => {
    const tick = () => {
      setNow(new Date());
      setNowPlaying(readNowPlaying());
    };
    tick();
    const timer = window.setInterval(tick, 1000);
    return () => window.clearInterval(timer);
  }, []);

  // Keep the screen awake while slides run.
  useEffect(() => {
    if (!running || !navigator.wakeLock) return;
    let lock: WakeLockSentinel | null = null;
    let released = false;
    navigator.wakeLock
      .request("screen")
      .then((sentinel) => {
        if (released) void sentinel.release();
        else lock = sentinel;
      })
      .catch(() => {});
    return () => {
      released = true;
      void lock?.release().catch(() => {});
    };
  }, [running]);

  // Controls show on pointer movement and hide after a few still seconds.
  const hideTimer = useRef<number | undefined>(undefined);
  const wake = useCallback(() => {
    setChrome(true);
    window.clearTimeout(hideTimer.current);
    hideTimer.current = window.setTimeout(() => setChrome(false), CHROME_HIDE_MS);
  }, []);
  useEffect(() => {
    wake();
    return () => window.clearTimeout(hideTimer.current);
  }, [wake]);

  const close = useCallback(() => {
    if (document.fullscreenElement) void document.exitFullscreen().catch(() => {});
    const idx = (window.history.state as { idx?: number } | null)?.idx ?? 0;
    if (idx > 0) navigate(-1);
    else navigate("/");
  }, [navigate]);

  const toggleFullscreen = useCallback(() => {
    if (document.fullscreenElement) {
      void document.exitFullscreen().catch(() => {});
    } else {
      void rootRef.current?.requestFullscreen?.().catch(() => {});
    }
  }, []);

  const startSource = useCallback(
    (next: SlideshowSource) => {
      setSheetOpen(false);
      if (!sameSource(next, source)) navigate(slideshowPath(next), { replace: true });
    },
    [navigate, source],
  );

  // Keys resolve in the capture phase so the docked player's shortcuts skip them.
  useEffect(() => {
    const keydown = (event: KeyboardEvent) => {
      if (event.defaultPrevented || event.metaKey || event.ctrlKey || event.altKey) return;
      if (event.key === "Escape") {
        event.preventDefault();
        event.stopPropagation();
        if (sheetOpen && source) setSheetOpen(false);
        else close();
        return;
      }
      if (sheetOpen || interactiveTarget(event.target)) return;
      const actions: Record<string, () => void> = {
        " ": () => setPaused((value) => !value),
        ArrowLeft: () => step(-1),
        ArrowRight: () => step(1),
        o: () => {
          const on = !(prefs.clock || prefs.caption || prefs.nowPlaying);
          updatePrefs({ clock: on, caption: on, nowPlaying: on });
        },
        d: () => updatePrefs({ darkRoom: !prefs.darkRoom }),
        f: toggleFullscreen,
        s: () => setSheetOpen(true),
      };
      const action = actions[event.key.length === 1 ? event.key.toLowerCase() : event.key];
      if (!action) return;
      event.preventDefault();
      event.stopPropagation();
      wake();
      action();
    };
    window.addEventListener("keydown", keydown, true);
    return () => window.removeEventListener("keydown", keydown, true);
  }, [sheetOpen, source, close, step, prefs, updatePrefs, toggleFullscreen, wake]);

  const clock = useMemo(() => {
    try {
      return new Intl.DateTimeFormat(locale, { hour: "numeric", minute: "2-digit" }).format(now);
    } catch {
      return now.toLocaleTimeString();
    }
  }, [now, locale]);

  const loading = !!source && !loadFailed && (items === null || !hiddenReady);
  let message = "";
  if (loadFailed) message = t("slideshowFailed");
  else if (loading && !current) message = t("slideshowLoading");
  else if (source && !current) message = t("slideshowEmpty");

  const date = current ? (captions[current.item.id] ?? "") : "";
  const place = current ? displayString(current.item.subtitle, locale) : "";
  const showOverlay = !!current && (prefs.clock || prefs.caption || prefs.nowPlaying);

  return (
    <div
      ref={rootRef}
      className="bf-slideshow"
      role="region"
      aria-label={t("slideshowRegion")}
      data-chrome={chrome || sheetOpen ? "shown" : "hidden"}
      onPointerMove={wake}
    >
      <div className="bf-slideshow-stage">
        {current && (
          <SlideView
            key={current.item.id}
            slide={current}
            clipURL={clipURLs[current.item.id]}
            slideRef={slideRef}
            onPainted={markPainted}
            onBroken={markBroken}
          />
        )}
      </div>
      {prefs.darkRoom && <div className="bf-slideshow-dim" data-testid="slideshow-dim" />}
      {message && (
        <p className="bf-slideshow-message" role="status">
          {message}
        </p>
      )}
      {showOverlay && (
        <div className="bf-slideshow-overlay">
          <div className="bf-slideshow-caption">
            {prefs.caption && date && <p>{date}</p>}
            {prefs.caption && place && <p>{place}</p>}
          </div>
          <div className="bf-slideshow-side">
            {prefs.clock && <span className="bf-slideshow-clock">{clock}</span>}
            {prefs.nowPlaying && nowPlaying && (
              <span className="bf-slideshow-now-playing">{nowPlaying}</span>
            )}
          </div>
        </div>
      )}
      <div className="bf-slideshow-bar" hidden={!chrome || sheetOpen}>
        <button
          type="button"
          className="bf-slideshow-button"
          aria-label={t("slideshowClose")}
          onClick={close}
        >
          <X aria-hidden />
        </button>
        <span className="bf-slideshow-bar-spacer" />
        <button
          type="button"
          className="bf-slideshow-button"
          aria-label={t("slideshowPrevious")}
          disabled={count < 2}
          onClick={() => step(-1)}
        >
          <ChevronLeft aria-hidden />
        </button>
        <button
          type="button"
          className="bf-slideshow-button"
          aria-label={paused ? t("slideshowResume") : t("slideshowPause")}
          onClick={() => setPaused((value) => !value)}
        >
          {paused ? <Play aria-hidden /> : <Pause aria-hidden />}
        </button>
        <button
          type="button"
          className="bf-slideshow-button"
          aria-label={t("slideshowNext")}
          disabled={count < 2}
          onClick={() => step(1)}
        >
          <ChevronRight aria-hidden />
        </button>
        <button
          type="button"
          className="bf-slideshow-button"
          aria-label={t("slideshowFullscreen")}
          onClick={toggleFullscreen}
        >
          <Maximize aria-hidden />
        </button>
        <button
          type="button"
          className="bf-slideshow-button"
          aria-label={t("slideshowSetup")}
          onClick={() => setSheetOpen(true)}
        >
          <Settings aria-hidden />
        </button>
      </div>
      {sheetOpen && (
        <SetupSheet
          source={source}
          libraries={libraries}
          prefs={prefs}
          onPrefs={updatePrefs}
          onStart={startSource}
          onCancel={source ? () => setSheetOpen(false) : close}
        />
      )}
    </div>
  );
}

function SlideView({
  slide,
  clipURL,
  slideRef,
  onPainted,
  onBroken,
}: {
  slide: Slide;
  /** Undefined while the preview clip is being looked up; "" when there is none. */
  clipURL: string | undefined;
  slideRef: (element: HTMLElement | null) => void;
  onPainted: (id: string) => void;
  onBroken: (id: string) => void;
}) {
  const { item } = slide;
  const poster = slideImageURL(item);
  const [clipFailed, setClipFailed] = useState(false);
  const playClip = slide.clip && !!clipURL && !clipFailed;

  // A slide with nothing left to show is skipped (a clip waits for its lookup first).
  useEffect(() => {
    if (poster || playClip || (slide.clip && clipURL === undefined)) return;
    onBroken(item.id);
  }, [poster, playClip, slide.clip, clipURL, item.id, onBroken]);

  if (playClip) {
    return (
      <video
        ref={slideRef}
        className="bf-slideshow-media"
        src={clipURL}
        poster={poster || undefined}
        muted
        playsInline
        autoPlay
        loop
        aria-label={displayString(item.title)}
        onLoadedData={() => onPainted(item.id)}
        onError={() => setClipFailed(true)}
      />
    );
  }
  if (!poster) return null;
  return (
    <img
      ref={slideRef}
      className="bf-slideshow-media"
      src={poster}
      alt={displayString(item.title)}
      decoding="async"
      onLoad={() => onPainted(item.id)}
      onError={() => onBroken(item.id)}
    />
  );
}

const SOURCE_KIND_LABELS = {
  "all-items": "slideshowSourceAllItems",
  favorites: "slideshowSourceFavorites",
  "on-this-day": "slideshowSourceOnThisDay",
} as const satisfies Record<SlideshowSourceKind, string>;

const MOTION_LABELS = {
  calm: "slideshowMotionCalm",
  still: "slideshowMotionStill",
  visualiser: "slideshowMotionVisualiser",
} as const satisfies Record<SlideshowMotion, string>;

function sourceValue(source: SlideshowSource | null): string {
  if (!source) return "kind:all-items";
  return source.kind === "collection" ? `collection:${source.collectionID}` : `kind:${source.kind}`;
}

function SetupSheet({
  source,
  libraries,
  prefs,
  onPrefs,
  onStart,
  onCancel,
}: {
  source: SlideshowSource | null;
  libraries: readonly LibrarySummary[];
  prefs: SlideshowPrefs;
  onPrefs: (patch: Partial<SlideshowPrefs>) => void;
  onStart: (source: SlideshowSource) => void;
  onCancel: () => void;
}) {
  const t = useT();
  const id = useId();
  const [choice, setChoice] = useState(() => sourceValue(source));
  const [libraryID, setLibraryID] = useState(() =>
    source && source.kind !== "collection" ? source.libraryID : "",
  );
  const [collections, setCollections] = useState<LibraryCollections[]>([]);

  useEffect(() => {
    if (libraries.length === 0) return;
    let cancelled = false;
    fetchViewerCollections(libraries)
      .then((lists) => {
        if (!cancelled) setCollections(lists.filter((list) => list.collections.length > 0));
      })
      .catch(() => {});
    return () => {
      cancelled = true;
    };
  }, [libraries]);

  const start = () => {
    const split = choice.indexOf(":");
    const kind = choice.slice(0, split);
    const value = choice.slice(split + 1);
    if (kind === "collection") onStart({ kind: "collection", collectionID: value });
    else onStart({ kind: value as SlideshowSourceKind, libraryID });
  };

  return (
    <div
      className="bf-slideshow-sheet"
      role="dialog"
      aria-modal="true"
      aria-labelledby={`${id}-title`}
    >
      <h2 id={`${id}-title`}>{t("slideshowSetup")}</h2>
      <fieldset>
        <label className="bf-slideshow-label" htmlFor={`${id}-source`}>
          {t("slideshowSource")}
        </label>
        <select id={`${id}-source`} value={choice} onChange={(e) => setChoice(e.target.value)}>
          {SLIDESHOW_SOURCE_KINDS.map((kind) => (
            <option key={kind} value={`kind:${kind}`}>
              {t(SOURCE_KIND_LABELS[kind])}
            </option>
          ))}
          {collections.map(({ library, collections: list }) => (
            <optgroup key={library.id} label={`${t("slideshowCollections")} · ${library.title}`}>
              {list.map((collection) => (
                <option key={collection.id} value={`collection:${collection.id}`}>
                  {collection.name}
                </option>
              ))}
            </optgroup>
          ))}
        </select>
        {choice.startsWith("kind:") && (
          <>
            <label className="bf-slideshow-label" htmlFor={`${id}-library`}>
              {t("slideshowLibrary")}
            </label>
            <select
              id={`${id}-library`}
              value={libraryID}
              onChange={(e) => setLibraryID(e.target.value)}
            >
              <option value="">{t("slideshowEveryLibrary")}</option>
              {libraries.map((library) => (
                <option key={library.id} value={library.id}>
                  {library.title}
                </option>
              ))}
            </select>
          </>
        )}
      </fieldset>
      <fieldset>
        <label className="bf-slideshow-label" htmlFor={`${id}-interval`}>
          {t("slideshowInterval")}: {t("slideshowIntervalValue", { seconds: prefs.interval })}
        </label>
        <input
          id={`${id}-interval`}
          type="range"
          min={MIN_INTERVAL_SECONDS}
          max={MAX_INTERVAL_SECONDS}
          step={1}
          value={prefs.interval}
          onChange={(e) => onPrefs({ interval: clampInterval(e.target.value) })}
        />
      </fieldset>
      <fieldset>
        <legend>{t("slideshowMotion")}</legend>
        {SLIDESHOW_MOTIONS.map((motion) => {
          const available = slideshowMotionAvailable(motion);
          return (
            <label key={motion} className="bf-slideshow-option">
              <input
                type="radio"
                name={`${id}-motion`}
                value={motion}
                checked={prefs.motion === motion}
                disabled={!available}
                onChange={() => onPrefs({ motion })}
              />
              {t(MOTION_LABELS[motion])}
              {!available && <small>{t("slideshowMotionUnavailable")}</small>}
            </label>
          );
        })}
      </fieldset>
      <fieldset>
        <legend>{t("slideshowOverlay")}</legend>
        {(
          [
            ["clock", "slideshowShowClock"],
            ["caption", "slideshowShowCaption"],
            ["nowPlaying", "slideshowShowNowPlaying"],
            ["darkRoom", "slideshowDarkRoom"],
          ] as const
        ).map(([key, label]) => (
          <label key={key} className="bf-slideshow-option">
            <input
              type="checkbox"
              checked={prefs[key]}
              onChange={(e) => onPrefs({ [key]: e.target.checked })}
            />
            {t(label)}
          </label>
        ))}
      </fieldset>
      <div className="bf-slideshow-sheet-actions">
        <Button variant="ghost" onClick={onCancel}>
          {t("slideshowClose")}
        </Button>
        <Button onClick={start}>{t("slideshowStart")}</Button>
      </div>
      <p className="bf-slideshow-keys">{t("slideshowKeys")}</p>
    </div>
  );
}
