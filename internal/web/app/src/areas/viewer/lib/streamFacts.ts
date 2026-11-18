import { channelLayout, codecName, type TrackLabelText } from "../player/streamTracks";
import type { MediaTrackVersion } from "./mediaDetail";
import { hdrName } from "./playback";

// The stream badges of a Media detail page's facts row (CORE-2469): the default
// Version's resolution and HDR format and its default audio track's codec and
// channel layout. Each is left out when unknown. The resolution is the server's
// label for that Version (CORE-2485), so the row agrees with the track pickers.

/** The facts row's stream badges, in order: resolution, HDR, audio. */
export function streamFacts(
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
    { key: "resolution", text: version?.resolution ?? "" },
    { key: "hdr", text: hdrName(version?.hdr) },
    { key: "audio", text: audioText },
  ].filter((fact) => fact.text !== "");
}
