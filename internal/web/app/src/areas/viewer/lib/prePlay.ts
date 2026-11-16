import { postJSON } from "@/lib/api";
import { probeCapabilityProfile } from "./capabilityProfile";
import type { PlaybackInfo, PlaybackStream } from "./playback";
import { withQualityCeiling } from "./qualityChoice";

/**
 * The first stream the server offers for one pre-play item (CORE-1701), or
 * null when it has none to play. Unlike fetchPlaybackInfo this never touches
 * the Play all queue: a pre-play item is not part of what the viewer queued.
 */
export async function fetchPrePlayStream(mediaID: string): Promise<PlaybackStream | null> {
  const capabilityProfile = withQualityCeiling(await probeCapabilityProfile());
  const info = await postJSON<PlaybackInfo>("/api/playback-info", { mediaID, capabilityProfile });
  const stream = (info.streams || [])[0];
  return stream?.url ? stream : null;
}
