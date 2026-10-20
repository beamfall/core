// Minimal typings for the experimental HTMLMediaElement audio-track API
// (AudioTrack / AudioTrackList). The standard TS DOM lib only declares
// textTracks (subtitles); audioTracks is non-standard and absent from most
// modern browsers, so the player reads it defensively. The vanilla web client
// (internal/web/static/app.js) used video.audioTracks directly; this narrow
// surface preserves that behavior where the browser exposes it, without
// widening the global lib.

export interface AudioTrackLike {
  label: string;
  language: string;
  enabled: boolean;
}

export interface AudioTrackListLike {
  readonly length: number;
  [index: number]: AudioTrackLike;
}

/** Returns the element's experimental audioTracks list, or null when absent. */
export function audioTrackList(video: HTMLVideoElement | null): AudioTrackListLike | null {
  if (!video) return null;
  const list = (video as { audioTracks?: AudioTrackListLike }).audioTracks;
  return list ?? null;
}

/** Snapshots an audio-track list into a plain, iterable array. */
export function audioTracksArray(video: HTMLVideoElement | null): AudioTrackLike[] {
  const list = audioTrackList(video);
  if (!list) return [];
  const tracks: AudioTrackLike[] = [];
  for (let index = 0; index < list.length; index++) {
    tracks.push(list[index]);
  }
  return tracks;
}
