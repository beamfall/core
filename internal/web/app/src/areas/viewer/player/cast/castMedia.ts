// What the web sender asks the server for, and what it hands the Cast receiver
// (CORE-1679).
//
// The receiver is Google's Default Media Receiver, so the sender declares that
// receiver's capability profile to /api/playback-info and lets the server
// choose a route the receiver can play: H.264 and stereo AAC in MP4, or the
// server's HLS transcode. A `chromecast-` client marks the Stream Token as
// receiver-bound (ADR-0194 §6): the server serves the bytes only when a
// quirks-DB selector resolves the receiver. The shipped baseline resolves
// `chromecast-default-receiver` (CORE-2563); any other id is refused.
import { postJSON } from "@/lib/api";
import type { CapabilityProfile } from "../../lib/capabilityProfile";
import type { PlaybackCaptionTrack, PlaybackInfo, PlaybackStream } from "../../lib/playback";
import type { CastLoadRequestLike, ChromeCastLike } from "./castSdk";

/** The Default Media Receiver floor every Cast device plays (CORE-2563 quirks baseline): 720p H.264 up to High 4.1, stereo AAC, WebVTT. */
export const CAST_RECEIVER_PROFILE: CapabilityProfile = {
  client: "chromecast-default-receiver",
  containers: ["mp4"],
  directCodecs: ["h264"],
  videoCodecs: [
    {
      codec: "h264",
      profiles: ["Constrained Baseline", "Baseline", "Main", "High"],
      maxHeight: 720,
      maxLevel: 41,
    },
  ],
  audioCodecs: [{ codec: "aac", maxChannels: 2 }],
  subtitleFormats: ["webvtt"],
  downstreamAudio: "stereo",
};

/** One caption track of a playback-info response; every URL serves WebVTT unless it ends otherwise. */
export type CastCaptionTrack = PlaybackCaptionTrack;

/** The server's subtitle pre-selection: a language and role identity, not a track id. */
export interface CastSelectedSubtitle {
  track?: { language?: string; kind?: string; roles?: { forced?: boolean; sdh?: boolean } };
}

/** Playback info as the receiver reads it: the subtitle pre-selection may be absent or null. */
export type CastPlaybackInfo = Omit<PlaybackInfo, "selectedSubtitleTrack"> & {
  selectedSubtitleTrack?: CastSelectedSubtitle | null;
};

/** Ask the server for the receiver's route. A new playback session, separate from the local one. */
export function fetchCastPlaybackInfo(mediaID: string): Promise<CastPlaybackInfo> {
  return postJSON<CastPlaybackInfo>("/api/playback-info", {
    mediaID,
    capabilityProfile: CAST_RECEIVER_PROFILE,
  });
}

/** A text track the receiver side-loads. Ids are 1-based, as the Cast SDK expects. */
export interface CastTextTrack {
  trackId: number;
  url: string;
  language: string;
  captions: boolean;
}

/** Everything the receiver is told, before it becomes Cast SDK objects. */
export interface CastLoad {
  contentId: string;
  contentType: string;
  hls: boolean;
  title: string;
  startSeconds: number;
  tracks: CastTextTrack[];
  activeTrackIds: number[];
}

export type CastLoadProblem = "no-route";

function absolute(url: string, origin: string): string {
  return new URL(url, origin).toString();
}

function isWebVTT(track: CastCaptionTrack): boolean {
  if (!track.url) return false;
  const path = track.url.split("?")[0] ?? "";
  return path.endsWith(".vtt");
}

function selectedTrackIndex(
  tracks: CastCaptionTrack[],
  selected: CastSelectedSubtitle | null | undefined,
): number {
  const ref = selected?.track;
  if (!ref || !ref.language) return -1;
  const forced = !!ref.roles?.forced;
  const sdh = !!ref.roles?.sdh;
  const exact = tracks.findIndex(
    (track) => track.language === ref.language && !!track.forced === forced && !!track.sdh === sdh,
  );
  if (exact >= 0) return exact;
  return tracks.findIndex((track) => track.language === ref.language && !!track.forced === forced);
}

/**
 * Build the receiver load from the server's answer. The route is the server's
 * first stream (its choice for the declared profile). The start is the local
 * position when playback already moved, else the server's resume point.
 */
export function buildCastLoad(
  info: CastPlaybackInfo,
  options: { origin: string; title: string; localSeconds?: number },
): CastLoad | CastLoadProblem {
  const stream: PlaybackStream | undefined = (info.streams ?? [])[0];
  if (!stream?.url) return "no-route";
  const hls = stream.kind === "hls" || /\.m3u8(\?|$)/.test(stream.url);
  const vtt = (info.captionTracks ?? []).filter(isWebVTT);
  const tracks = vtt.map((track, index) => ({
    trackId: index + 1,
    url: absolute(track.url ?? "", options.origin),
    language: track.language ?? "",
    captions: !!track.sdh,
  }));
  const selected = selectedTrackIndex(vtt, info.selectedSubtitleTrack);
  const local = options.localSeconds ?? 0;
  const resume = info.timeline?.positionSeconds ?? 0;
  return {
    contentId: absolute(stream.url, options.origin),
    contentType: hls ? "application/x-mpegurl" : "video/mp4",
    hls,
    title: options.title,
    startSeconds: Math.max(0, local >= 1 ? local : resume),
    tracks,
    activeTrackIds: selected >= 0 ? [selected + 1] : [],
  };
}

/** Turn a load into the Cast SDK's LoadRequest. */
export function toLoadRequest(chromeCast: ChromeCastLike, load: CastLoad): CastLoadRequestLike {
  const media = new chromeCast.media.MediaInfo(load.contentId, load.contentType);
  media.streamType = chromeCast.media.StreamType.BUFFERED;
  if (load.hls) {
    // The server's HLS ladder writes fragmented-MP4 segments.
    media.hlsSegmentFormat = chromeCast.media.HlsSegmentFormat.FMP4;
    media.hlsVideoSegmentFormat = chromeCast.media.HlsVideoSegmentFormat.FMP4;
  }
  const metadata = new chromeCast.media.GenericMediaMetadata();
  metadata.title = load.title;
  media.metadata = metadata;
  media.tracks = load.tracks.map((entry) => {
    const track = new chromeCast.media.Track(entry.trackId, chromeCast.media.TrackType.TEXT);
    track.trackContentId = entry.url;
    track.trackContentType = "text/vtt";
    track.subtype = entry.captions
      ? chromeCast.media.TextTrackType.CAPTIONS
      : chromeCast.media.TextTrackType.SUBTITLES;
    track.language = entry.language;
    track.name = entry.language;
    return track;
  });
  const request = new chromeCast.media.LoadRequest(media);
  request.autoplay = true;
  request.currentTime = load.startSeconds;
  request.activeTrackIds = load.activeTrackIds;
  return request;
}
