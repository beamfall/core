import { ChevronLeft, ChevronRight, Maximize, Pause, Play, Settings, X } from "lucide-react";
import { useCallback, useEffect, useId, useMemo, useRef, useState } from "react";
import { useNavigate, useSearchParams } from "react-router";
import { Button } from "@/components/ui/button";
import { displayString } from "@/lib/display";
import type { LibrarySummary, SourceItem } from "@/lib/graphql/types";
import { useDisplayLocale, useT } from "@/lib/i18n";
import { useSession, useViewerIdentity } from "@/lib/session";
import { useHiddenItems } from "../viewer/actions";
import { hiddenKeyMoved } from "../viewer/actions/hiddenItems";
import { artworkBucketURL, artworkSlotURL } from "../viewer/lib/artwork";
import { fetchViewerCollections, type LibraryCollections } from "../viewer/lib/collections";
import { fetchMediaDetailFields } from "../viewer/lib/mediaDetail";
import { fetchPreviewInfo } from "../viewer/lib/playback";
import {
  isScreensaverSearch,
  saveLastSlideshowSource,
  saveScreensaverEnabled,
  screensaverEnabled,
} from "./lib/screensaver";
import {
  clampInterval,
  clockMinutes,
  DEFAULT_DARK_ROOM_END,
  DEFAULT_DARK_ROOM_START,
  DEFAULT_INTERVAL_SECONDS,
  fetchSlideshowItems,
  inDarkRoomWindow,
  MAX_INTERVAL_SECONDS,
  MIN_INTERVAL_SECONDS,
  nextDarkRoomBoundary,
  nextDelay,
  parseSlideshowSource,
  SLIDESHOW_SOURCE_KINDS,
  type Slide,
  type SlideGroup,
  type SlideShape,
  type SlideshowSource,
  type SlideshowSourceKind,
  sameSource,
  slideDate,
  slideGroupSettled,
  slideGroups,
  slidesFrom,
  slideshowPath,
  unreadPortraitBefore,
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
/** How many slides from the current one have their shape read ahead: this group and the next. */
const SHAPE_LOOKAHEAD = 4;
const NO_LIBRARIES: readonly LibrarySummary[] = [];
const SESSION_RECHECK_MS = 60_000;
/** A screensaver ignores pointer jitter: only a move this far (px), after this grace (ms), closes it. */
const SCREENSAVER_MOVE_PX = 24;
const SCREENSAVER_GRACE_MS = 800;
/** Re-read a preview clip this long before its Stream Token expires. */
const CLIP_EXPIRY_MARGIN_MS = 60_000;

/** A clip's preview URL ("" when it has none) and its token expiry (0 when unstated). */
interface ClipPreview {
  url: string;
  expiresAt: number;
}

function clipPreviewFresh(preview: ClipPreview | undefined, now: number): boolean {
  if (!preview) return false;
  return !preview.url || !preview.expiresAt || now < preview.expiresAt - CLIP_EXPIRY_MARGIN_MS;
}

interface SlideshowPrefs {
  interval: number;
  motion: SlideshowMotion;
  clock: boolean;
  caption: boolean;
  nowPlaying: boolean;
  /** Dark Room's schedule is on; it dims only between start and end (`HH:MM`, local). */
  darkRoom: boolean;
  darkRoomStart: string;
  darkRoomEnd: string;
}

function defaultPrefs(): SlideshowPrefs {
  return {
    interval: DEFAULT_INTERVAL_SECONDS,
    motion: defaultSlideshowMotion(),
    clock: true,
    caption: true,
    nowPlaying: true,
    darkRoom: false,
    darkRoomStart: DEFAULT_DARK_ROOM_START,
    darkRoomEnd: DEFAULT_DARK_ROOM_END,
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
      darkRoomStart:
        clockMinutes(stored.darkRoomStart) === null ? prefs.darkRoomStart : stored.darkRoomStart,
      darkRoomEnd:
        clockMinutes(stored.darkRoomEnd) === null ? prefs.darkRoomEnd : stored.darkRoomEnd,
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

/** Drops the click that follows a press, wherever it lands, for a moment. */
function swallowNextClick() {
  const swallow = (event: Event) => {
    event.preventDefault();
    event.stopPropagation();
    window.removeEventListener("click", swallow, true);
  };
  window.addEventListener("click", swallow, true);
  window.setTimeout(() => window.removeEventListener("click", swallow, true), 600);
}

function landscapeScreen(): boolean {
  return typeof window === "undefined" || window.innerWidth > window.innerHeight;
}

function groupKey(group: SlideGroup): string {
  return group.slides.map((slide) => slide.item.id).join("+");
}

function interactiveTarget(target: EventTarget | null, key: string): boolean {
  if (!(target instanceof HTMLElement)) return false;
  // A focused button keeps Space for its own activation.
  const own =
    key === " "
      ? "input, select, textarea, button, [contenteditable='true']"
      : "input, select, textarea, [contenteditable='true']";
  return !!target.closest(own);
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
  // Screensaver mode (CORE-2981): opened by the idle hook; no chrome, and the first input closes it.
  const screensaver = useMemo(() => isScreensaverSearch(searchParams), [searchParams]);
  const { manifest, refreshSession } = useSession();
  const libraries = manifest?.libraries ?? NO_LIBRARIES;
  const { mutes, failed: hiddenFailed, isHidden } = useHiddenItems();
  const hiddenReady = mutes !== null || hiddenFailed;
  const hiddenKey = useMemo(
    () =>
      mutes
        ?.map((mute) => `${mute.kind}:${mute.targetID}:${mute.includeDescendants}`)
        .sort()
        .join("|") ?? null,
    [mutes],
  );

  const rootRef = useRef<HTMLDivElement | null>(null);
  const [prefs, setPrefs] = useState<SlideshowPrefs>(loadPrefs);
  const [sheetOpen, setSheetOpen] = useState(source === null && !screensaver);
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
  const [clipURLs, setClipURLs] = useState<Record<string, ClipPreview>>({});
  // A hide made elsewhere refetches the Source, so the server drops a hidden
  // container's descendants too; a return to the tab re-reads the session first.
  const [reload, setReload] = useState(0);
  const [revalidating, setRevalidating] = useState(false);
  const [captions, setCaptions] = useState<Record<string, string>>({});
  // Each image's shape once decoded, and whether the screen is landscape (pairing needs both).
  const [shapes, setShapes] = useState<Record<string, SlideShape>>({});
  const [landscape, setLandscape] = useState(landscapeScreen);
  // D dims or undims at once; the override lasts until the schedule next opens
  // or closes (`until`, epoch ms), or for good while the schedule is off.
  const [dimOverride, setDimOverride] = useState<{ on: boolean; until: number | null } | null>(
    null,
  );

  const updatePrefs = useCallback((patch: Partial<SlideshowPrefs>) => {
    setPrefs((current) => {
      const next = { ...current, ...patch };
      savePrefs(next);
      return next;
    });
  }, []);

  const seenHidden = useRef<string | null>(null);
  useEffect(() => {
    if (hiddenKeyMoved(seenHidden, hiddenKey)) setReload((value) => value + 1);
  }, [hiddenKey]);

  // Another tab may switch Profile or Reveal: re-read the session on return and
  // every minute. A Profile change reloads the identity, which remounts this page.
  useEffect(() => {
    let cancelled = false;
    const revalidate = (hideUntilDone: boolean) => {
      if (hideUntilDone) setRevalidating(true);
      void refreshSession()
        .catch(() => {})
        .finally(() => {
          if (!cancelled) setRevalidating(false);
        });
    };
    const visible = () => {
      if (document.visibilityState === "visible") revalidate(true);
    };
    document.addEventListener("visibilitychange", visible);
    const timer = window.setInterval(() => revalidate(false), SESSION_RECHECK_MS);
    return () => {
      cancelled = true;
      document.removeEventListener("visibilitychange", visible);
      window.clearInterval(timer);
    };
  }, [refreshSession]);

  // Load the Source's items, progressively, whenever the Source changes or a hide moves.
  const loadedSource = useRef<SlideshowSource | null>(null);
  useEffect(() => {
    void reload;
    setItems(null);
    setLoadFailed(false);
    setShown(null);
    if (!sameSource(loadedSource.current, source)) {
      loadedSource.current = source;
      setBroken(new Set());
      setIndex(0);
    }
    if (!source) return;
    const controller = new AbortController();
    fetchSlideshowItems(source, (next) => setItems(next), controller.signal).catch(() => {
      if (!controller.signal.aborted) setLoadFailed(true);
    });
    return () => controller.abort();
  }, [source, reload]);

  const slides = useMemo<Slide[]>(() => {
    if (!items || !hiddenReady || revalidating) return [];
    return slidesFrom(items, isHidden).filter((slide) => !broken.has(slide.item.id));
  }, [items, hiddenReady, revalidating, isHidden, broken]);
  const count = slides.length;
  const shapeOf = useCallback((slide: Slide) => shapes[slide.item.id], [shapes]);
  const groups = useMemo(
    () => slideGroups(slides, shapeOf, landscape),
    [slides, shapeOf, landscape],
  );
  // `index` names a slide; the stage shows the group holding it, so a pair steps as one.
  const groupAt = useCallback(
    (slideIndex: number) => {
      const at = count > 0 ? slideIndex % count : 0;
      const found = groups.findIndex(
        (group) => at >= group.start && at < group.start + group.slides.length,
      );
      return Math.max(0, found);
    },
    [groups, count],
  );
  const groupIndex = groupAt(index);
  const group = groups.length > 0 ? groups[groupIndex] : null;
  const current = group ? group.slides[0] : null;
  const currentKey = group ? groupKey(group) : "";
  const settled = group ? slideGroupSettled(slides, group.start, shapeOf, landscape) : false;
  const steps = groups.length;
  const intervalMs = prefs.interval * 1000;

  const step = useCallback(
    (delta: number) => {
      if (steps < 2) return;
      setShown(null);
      setIndex((value) => {
        const next = (((groupAt(value) + delta) % steps) + steps) % steps;
        return groups[next].start;
      });
    },
    [steps, groups, groupAt],
  );

  const markPainted = useCallback((key: string) => {
    setShown((value) => (value?.id === key ? value : { id: key, at: performance.now(), held: 0 }));
  }, []);

  const markBroken = useCallback((id: string) => {
    setShown(null);
    setBroken((value) => new Set(value).add(id));
  }, []);

  // A slide stays for the interval from when it was painted; a pause holds it.
  const running = !paused && !sheetOpen;
  useEffect(() => {
    if (!running || !currentKey || !shown || shown.id !== currentKey || steps < 2) return;
    const delay = nextDelay(shown.at, performance.now(), intervalMs, shown.held);
    const timer = window.setTimeout(() => step(1), delay);
    return () => window.clearTimeout(timer);
  }, [running, currentKey, shown, steps, intervalMs, step]);

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

  // Decode this group's and the next group's images ahead: the cut lands on a
  // decoded picture, and each image's shape decides whether portraits pair.
  // Portraits running back from this group are read too, since they decide where pairs fall.
  const probing = useRef(new Set<string>());
  useEffect(() => {
    if (!group) return;
    const ahead = Array.from(
      { length: Math.min(SHAPE_LOOKAHEAD, count) },
      (_, offset) => (group.start + offset) % count,
    );
    const behind = landscape ? unreadPortraitBefore(slides, group.start, shapeOf) : null;
    for (const at of behind === null ? ahead : [...ahead, behind]) {
      const slide = slides[at];
      const id = slide.item.id;
      if (slide.clip || id in shapes || probing.current.has(id)) continue;
      const url = slideImageURL(slide.item);
      const settle = (shape: SlideShape) => {
        probing.current.delete(id);
        setShapes((value) => (id in value ? value : { ...value, [id]: shape }));
      };
      if (!url) {
        settle("landscape");
        continue;
      }
      probing.current.add(id);
      const image = new Image();
      image.decoding = "async";
      image.onload = () =>
        settle(image.naturalHeight > image.naturalWidth ? "portrait" : "landscape");
      // A failed read never pairs; the stage skips the slide when it fails there too.
      image.onerror = () => settle("landscape");
      image.src = url;
    }
  }, [group, slides, count, shapes, landscape, shapeOf]);

  // The stage's own decode corrects a shape a failed probe guessed.
  const noteShape = useCallback((id: string, shape: SlideShape) => {
    setShapes((value) => (value[id] === shape ? value : { ...value, [id]: shape }));
  }, []);

  useEffect(() => {
    const resize = () => setLandscape(landscapeScreen());
    window.addEventListener("resize", resize);
    return () => window.removeEventListener("resize", resize);
  }, []);

  // A clip's generated preview clip: an asset read, not a playback decision.
  const clipID = current?.clip && settled ? current.item.id : "";
  useEffect(() => {
    if (!clipID || clipPreviewFresh(clipURLs[clipID], Date.now())) return;
    let cancelled = false;
    fetchPreviewInfo(clipID)
      .then((info) => ({
        url: info.assets?.previewClip ?? "",
        expiresAt: Date.parse(info.assets?.expiresAt ?? "") || 0,
      }))
      .catch(() => ({ url: "", expiresAt: 0 }))
      .then((preview) => {
        if (!cancelled) setClipURLs((value) => ({ ...value, [clipID]: preview }));
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

  // Screensaver mode: the first key, click, touch, wheel or deliberate pointer
  // move closes it and does nothing else. A press's click lands after the route
  // changes, so it is swallowed too, or it would open whatever is underneath.
  useEffect(() => {
    if (!screensaver) return;
    const openedAt = performance.now();
    let origin: { x: number; y: number } | null = null;
    let closed = false;
    const dismiss = (event: Event) => {
      event.preventDefault();
      event.stopPropagation();
      if (closed) return;
      closed = true;
      if (event.type === "pointerdown" || event.type === "touchstart") swallowNextClick();
      close();
    };
    const move = (event: PointerEvent) => {
      if (performance.now() - openedAt < SCREENSAVER_GRACE_MS) return;
      if (!origin) {
        origin = { x: event.clientX, y: event.clientY };
        return;
      }
      if (Math.hypot(event.clientX - origin.x, event.clientY - origin.y) >= SCREENSAVER_MOVE_PX) {
        dismiss(event);
      }
    };
    const options = { capture: true, passive: false } as const;
    for (const name of ["keydown", "pointerdown", "touchstart", "wheel"]) {
      window.addEventListener(name, dismiss, options);
    }
    window.addEventListener("pointermove", move, true);
    return () => {
      for (const name of ["keydown", "pointerdown", "touchstart", "wheel"]) {
        window.removeEventListener(name, dismiss, true);
      }
      window.removeEventListener("pointermove", move, true);
    };
  }, [screensaver, close]);

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

  // Dark Room: the schedule decides, and D overrides it until the schedule flips.
  const scheduledDim =
    prefs.darkRoom && inDarkRoomWindow(now, prefs.darkRoomStart, prefs.darkRoomEnd);
  const dimmed =
    dimOverride && (dimOverride.until === null || now.getTime() < dimOverride.until)
      ? dimOverride.on
      : scheduledDim;
  const lastScheduled = useRef(scheduledDim);
  useEffect(() => {
    if (lastScheduled.current === scheduledDim) return;
    lastScheduled.current = scheduledDim;
    setDimOverride(null);
  }, [scheduledDim]);

  // Keys resolve in the capture phase so the docked player's shortcuts skip them.
  useEffect(() => {
    const keydown = (event: KeyboardEvent) => {
      if (screensaver) return;
      if (event.defaultPrevented || event.metaKey || event.ctrlKey || event.altKey) return;
      if (event.key === "Escape") {
        event.preventDefault();
        event.stopPropagation();
        if (sheetOpen && source) setSheetOpen(false);
        else close();
        return;
      }
      if (sheetOpen || interactiveTarget(event.target, event.key)) return;
      const actions: Record<string, () => void> = {
        " ": () => setPaused((value) => !value),
        ArrowLeft: () => step(-1),
        ArrowRight: () => step(1),
        o: () => {
          const on = !(prefs.clock || prefs.caption || prefs.nowPlaying);
          updatePrefs({ clock: on, caption: on, nowPlaying: on });
        },
        d: () => {
          const boundary = prefs.darkRoom
            ? nextDarkRoomBoundary(new Date(), prefs.darkRoomStart, prefs.darkRoomEnd)
            : null;
          setDimOverride({ on: !dimmed, until: boundary ? boundary.getTime() : null });
        },
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
  }, [
    screensaver,
    sheetOpen,
    source,
    close,
    step,
    prefs,
    updatePrefs,
    toggleFullscreen,
    wake,
    dimmed,
  ]);

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

  // The screensaver has nothing to fall back to on the web: a missing, failed or
  // empty Source closes it, and the idle count starts again.
  const nothingToShow = !source || loadFailed || (!loading && !revalidating && !current);
  useEffect(() => {
    if (screensaver && nothingToShow) close();
  }, [screensaver, nothingToShow, close]);

  // The Source this device last played is what its screensaver plays.
  const playing = !!source && !!current;
  useEffect(() => {
    if (!screensaver && playing && source) saveLastSlideshowSource(source);
  }, [screensaver, playing, source]);

  const date = current ? (captions[current.item.id] ?? "") : "";
  const place = current ? displayString(current.item.subtitle, locale) : "";
  const showOverlay = !!current && (prefs.clock || prefs.caption || prefs.nowPlaying);

  return (
    <div
      ref={rootRef}
      className="bf-slideshow"
      role="region"
      aria-label={t("slideshowRegion")}
      data-chrome={!screensaver && (chrome || sheetOpen) ? "shown" : "hidden"}
      data-screensaver={screensaver ? "on" : undefined}
      data-dark-room={dimmed ? "on" : "off"}
      onPointerMove={screensaver ? undefined : wake}
    >
      <h1 className="sr-only">{t("slideshowRegion")}</h1>
      <div className="bf-slideshow-stage">
        {group && settled && (
          <GroupView
            key={currentKey}
            groupKey={currentKey}
            slides={group.slides}
            clipURL={
              current && clipPreviewFresh(clipURLs[current.item.id], Date.now())
                ? clipURLs[current.item.id]?.url
                : undefined
            }
            slideRef={slideRef}
            onPainted={markPainted}
            onBroken={markBroken}
            onShape={noteShape}
          />
        )}
      </div>
      {dimmed && <div className="bf-slideshow-dim" data-testid="slideshow-dim" />}
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
      <div className="bf-slideshow-bar" hidden={screensaver || !chrome || sheetOpen}>
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
          disabled={steps < 2}
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
          disabled={steps < 2}
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
      {sheetOpen && !screensaver && (
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

/**
 * One stage: a slide, or two portraits side by side in two halves. It counts
 * as painted once every image in it is. The Oscillux seam receives the whole
 * stage, so its `fill` and `pair` layouts (CORE-2980) can take over from here.
 */
function GroupView({
  groupKey,
  slides,
  clipURL,
  slideRef,
  onPainted,
  onBroken,
  onShape,
}: {
  groupKey: string;
  slides: Slide[];
  /** The first slide's preview clip (clips never pair). */
  clipURL: string | undefined;
  slideRef: (element: HTMLElement | null) => void;
  onPainted: (key: string) => void;
  onBroken: (id: string) => void;
  onShape: (id: string, shape: SlideShape) => void;
}) {
  const [painted, setPainted] = useState<ReadonlySet<string>>(() => new Set());
  const markPainted = useCallback((id: string) => {
    setPainted((value) => (value.has(id) ? value : new Set(value).add(id)));
  }, []);
  const done = slides.every((slide) => painted.has(slide.item.id));
  useEffect(() => {
    if (done) onPainted(groupKey);
  }, [done, groupKey, onPainted]);

  return (
    <div
      ref={slideRef}
      className="bf-slideshow-slide"
      data-layout={slides.length > 1 ? "pair" : "single"}
    >
      {slides.map((slide, position) => (
        <div key={slide.item.id} className="bf-slideshow-cell">
          <SlideView
            slide={slide}
            clipURL={position === 0 ? clipURL : ""}
            onPainted={markPainted}
            onBroken={onBroken}
            onShape={onShape}
          />
        </div>
      ))}
    </div>
  );
}

function SlideView({
  slide,
  clipURL,
  onPainted,
  onBroken,
  onShape,
}: {
  slide: Slide;
  /** Undefined while the preview clip is being looked up; "" when there is none. */
  clipURL: string | undefined;
  onPainted: (id: string) => void;
  onBroken: (id: string) => void;
  onShape: (id: string, shape: SlideShape) => void;
}) {
  const { item } = slide;
  const poster = slideImageURL(item);
  const [clipFailed, setClipFailed] = useState(false);
  const [posterLoaded, setPosterLoaded] = useState(false);
  const playClip = slide.clip && !!clipURL && !clipFailed;
  const clipPending = slide.clip && clipURL === undefined;

  // The poster starts the interval only once the clip lookup has settled without a clip.
  useEffect(() => {
    if (posterLoaded && !playClip && !clipPending) onPainted(item.id);
  }, [posterLoaded, playClip, clipPending, item.id, onPainted]);

  // A slide with nothing left to show is skipped (a clip waits for its lookup first).
  useEffect(() => {
    if (poster || playClip || (slide.clip && clipURL === undefined)) return;
    onBroken(item.id);
  }, [poster, playClip, slide.clip, clipURL, item.id, onBroken]);

  if (playClip) {
    return (
      <video
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
      className="bf-slideshow-media"
      src={poster}
      alt={displayString(item.title)}
      decoding="async"
      onLoad={(event) => {
        const image = event.currentTarget;
        if (!slide.clip && image.naturalWidth > 0) {
          onShape(item.id, image.naturalHeight > image.naturalWidth ? "portrait" : "landscape");
        }
        setPosterLoaded(true);
      }}
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
  const [idleOpens, setIdleOpens] = useState(screensaverEnabled);

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
        <legend>{t("slideshowDarkRoom")}</legend>
        <label className="bf-slideshow-option">
          <input
            type="checkbox"
            checked={prefs.darkRoom}
            onChange={(e) => onPrefs({ darkRoom: e.target.checked })}
          />
          {t("slideshowDarkRoomSchedule")}
        </label>
        <div className="bf-slideshow-times">
          {(
            [
              ["darkRoomStart", "slideshowDarkRoomStart"],
              ["darkRoomEnd", "slideshowDarkRoomEnd"],
            ] as const
          ).map(([key, label]) => (
            <label key={key} className="bf-slideshow-label">
              {t(label)}
              <input
                type="time"
                value={prefs[key]}
                disabled={!prefs.darkRoom}
                onChange={(e) => {
                  if (clockMinutes(e.target.value) !== null) onPrefs({ [key]: e.target.value });
                }}
              />
            </label>
          ))}
        </div>
      </fieldset>
      <fieldset>
        <legend>{t("slideshowScreensaver")}</legend>
        <label className="bf-slideshow-option">
          <input
            type="checkbox"
            checked={idleOpens}
            onChange={(e) => {
              saveScreensaverEnabled(e.target.checked);
              setIdleOpens(screensaverEnabled());
            }}
          />
          {t("slideshowScreensaverIdle")}
        </label>
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
