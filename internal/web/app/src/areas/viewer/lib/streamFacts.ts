import { channelLayout, codecName, type TrackLabelText } from "../player/streamTracks";
import type { MediaTrackVersion } from "./mediaDetail";
import { hdrName } from "./playback";

// The stream badges of a Media detail page's meta line (CORE-2469, CORE-2930):
// the shown Version's resolution and HDR format and its default audio track's
// codec and channel layout. Each is left out when unknown. The resolution is
// the server's label for that Version (CORE-2485), and the Version is the one
// the track pickers show, so a Version pick updates the badges.

/** The meta line's stream badges, in order: resolution, HDR, audio. */
export function streamFacts(
  version: MediaTrackVersion | undefined,
  text: Pick<TrackLabelText, "mono" | "stereo" | "channels">,
): Array<{ key: string; text: string }> {
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
