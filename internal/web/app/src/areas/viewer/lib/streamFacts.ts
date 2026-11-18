import type { GenericMediaInfo } from "@/lib/graphql/types";
import { channelLayout, codecName, type TrackLabelText } from "../player/streamTracks";
import type { MediaTrackVersion } from "./mediaDetail";
import { hdrName } from "./playback";

// The stream badges of a Media detail page's facts row (CORE-2469): the stored
// frame size as a resolution, the default Version's HDR format and its default
// audio track's codec and channel layout. Each is left out when unknown.

/** "4K", "1080p", "720p", else the frame height ("480p"); "" without a size. */
export function resolutionName(width = 0, height = 0): string {
  if (width <= 0 && height <= 0) return "";
  if (width >= 7000 || height >= 4000) return "8K";
  if (width >= 3200 || height >= 2000) return "4K";
  if (width >= 1800 || height >= 1000) return "1080p";
  if (width >= 1200 || height >= 700) return "720p";
  return height > 0 ? `${height}p` : "";
}

/** The facts row's stream badges, in order: resolution, HDR, audio. */
export function streamFacts(
  media: Pick<GenericMediaInfo, "videoWidth" | "videoHeight"> | null | undefined,
  versions: readonly MediaTrackVersion[],
  text: Pick<TrackLabelText, "mono" | "stereo" | "channels">,
): Array<{ key: string; text: string }> {
  // The server lists its default Version first, as the pickers' Auto does.
  const version = versions[0];
  const audio = version?.audioTracks.find((track) => track.default) ?? version?.audioTracks[0];
  const audioText = audio?.codec
    ? [codecName(audio.codec), channelLayout(audio.channels, text)].filter(Boolean).join(" ")
    : "";
  return [
    { key: "resolution", text: resolutionName(media?.videoWidth, media?.videoHeight) },
    { key: "hdr", text: hdrName(version?.hdr) },
    { key: "audio", text: audioText },
  ].filter((fact) => fact.text !== "");
}
