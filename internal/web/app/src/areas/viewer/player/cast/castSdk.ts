// Google Cast sender SDK loader (CORE-1679).
//
// The SDK is a third-party script, so it loads only on demand and only in a
// browser that can cast: a secure context in a Chromium browser. Anywhere else
// no script element is created and no request leaves the page. The server's
// CSP allows exactly the gstatic paths the loader and the framework come from.
//
// Only the parts of the SDK the sender uses are typed here.

export const CAST_SDK_URL =
  "https://www.gstatic.com/cv/js/sender/v1/cast_sender.js?loadCastFramework=1";

/** How long to wait for the SDK to report in before giving up. */
const LOAD_TIMEOUT_MS = 15_000;

export interface CastEventPayload {
  castState?: string;
  sessionState?: string;
}

export interface CastDeviceLike {
  friendlyName?: string;
}

export interface CastSessionLike {
  loadMedia(request: unknown): Promise<unknown>;
  getCastDevice(): CastDeviceLike;
}

export interface CastContextLike {
  setOptions(options: { receiverApplicationId: string; autoJoinPolicy: string }): void;
  getCastState(): string;
  /** Resolves null on success, or an error code; some SDK builds reject instead. */
  requestSession(): Promise<unknown>;
  getCurrentSession(): CastSessionLike | null;
  endCurrentSession(stopCasting: boolean): void;
  addEventListener(type: string, handler: (event: CastEventPayload) => void): void;
  removeEventListener(type: string, handler: (event: CastEventPayload) => void): void;
}

export interface RemotePlayerLike {
  isConnected: boolean;
  isMediaLoaded: boolean;
  isPaused: boolean;
  currentTime: number;
  duration: number;
}

export interface RemotePlayerControllerLike {
  playOrPause(): void;
  seek(): void;
  stop(): void;
  addEventListener(type: string, handler: () => void): void;
  removeEventListener(type: string, handler: () => void): void;
}

export interface CastFrameworkLike {
  CastContext: { getInstance(): CastContextLike };
  CastContextEventType: { CAST_STATE_CHANGED: string; SESSION_STATE_CHANGED: string };
  CastState: {
    NO_DEVICES_AVAILABLE: string;
    NOT_CONNECTED: string;
    CONNECTING: string;
    CONNECTED: string;
  };
  SessionState: { SESSION_ENDED: string };
  RemotePlayer: new () => RemotePlayerLike;
  RemotePlayerController: new (player: RemotePlayerLike) => RemotePlayerControllerLike;
  RemotePlayerEventType: { ANY_CHANGE: string };
}

export interface CastTrackLike {
  trackContentId?: string;
  trackContentType?: string;
  subtype?: string;
  name?: string;
  language?: string;
}

export interface CastMediaInfoLike {
  contentType: string;
  streamType?: string;
  hlsSegmentFormat?: string;
  hlsVideoSegmentFormat?: string;
  metadata?: unknown;
  tracks?: CastTrackLike[];
}

export interface CastLoadRequestLike {
  media: CastMediaInfoLike;
  autoplay?: boolean;
  currentTime?: number;
  activeTrackIds?: number[];
}

export interface ChromeCastLike {
  AutoJoinPolicy: { ORIGIN_SCOPED: string };
  media: {
    DEFAULT_MEDIA_RECEIVER_APP_ID: string;
    MediaInfo: new (contentId: string, contentType: string) => CastMediaInfoLike;
    LoadRequest: new (media: CastMediaInfoLike) => CastLoadRequestLike;
    Track: new (trackId: number, trackType: string) => CastTrackLike;
    GenericMediaMetadata: new () => { title?: string };
    TrackType: { TEXT: string };
    TextTrackType: { SUBTITLES: string; CAPTIONS: string };
    StreamType: { BUFFERED: string };
    HlsSegmentFormat: { FMP4: string };
    HlsVideoSegmentFormat: { FMP4: string };
  };
}

/** The loaded SDK: the CAF sender framework plus the chrome.cast media types. */
export interface CastSdk {
  framework: CastFrameworkLike;
  chromeCast: ChromeCastLike;
}

/** The slice of the browser the loader reads; tests pass a fake. */
export interface CastEnvironment {
  isSecureContext?: boolean;
  chrome?: { cast?: ChromeCastLike };
  cast?: { framework?: CastFrameworkLike };
  navigator?: { userAgent?: string };
  document?: Document;
  __onGCastApiAvailable?: (available: boolean) => void;
}

function browserEnvironment(): CastEnvironment | null {
  return typeof window === "undefined" ? null : (window as unknown as CastEnvironment);
}

/**
 * Whether this browser can run the Cast sender: a secure context in a Chromium
 * browser. iOS browsers report a Chromium name but have no Cast sender.
 */
export function castSupported(env: CastEnvironment | null = browserEnvironment()): boolean {
  if (!env || env.isSecureContext !== true || typeof env.chrome !== "object" || !env.chrome) {
    return false;
  }
  const userAgent = env.navigator?.userAgent ?? "";
  return /\bChrome\/\d/.test(userAgent) && !/\b(CriOS|EdgiOS|FxiOS)\//.test(userAgent);
}

function sdkFrom(env: CastEnvironment): CastSdk | null {
  const framework = env.cast?.framework;
  const chromeCast = env.chrome?.cast;
  return framework && chromeCast?.media ? { framework, chromeCast } : null;
}

let pending: Promise<CastSdk | null> | null = null;

/**
 * Load the Cast SDK once. Resolves null when the browser cannot cast, the SDK
 * reports no Cast support, or it does not answer in time. Never rejects.
 */
export function loadCastSdk(
  env: CastEnvironment | null = browserEnvironment(),
): Promise<CastSdk | null> {
  if (!env || !castSupported(env)) return Promise.resolve(null);
  if (pending) return pending;
  const already = sdkFrom(env);
  if (already) {
    pending = Promise.resolve(already);
    return pending;
  }
  const doc = env.document;
  if (!doc) return Promise.resolve(null);
  pending = new Promise<CastSdk | null>((resolve) => {
    const timer = setTimeout(() => resolve(null), LOAD_TIMEOUT_MS);
    env.__onGCastApiAvailable = (available: boolean) => {
      clearTimeout(timer);
      resolve(available ? sdkFrom(env) : null);
    };
    const script = doc.createElement("script");
    script.src = CAST_SDK_URL;
    script.async = true;
    script.onerror = () => {
      clearTimeout(timer);
      resolve(null);
    };
    doc.head.appendChild(script);
  });
  return pending;
}

/** Forget the cached load (tests only). */
export function resetCastSdkForTests(): void {
  pending = null;
}
