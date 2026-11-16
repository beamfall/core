// The listener's mute, kept apart from the media element's while the gapless
// join (CORE-1634) mutes the element: the element then shadows a decoded track
// for the transport and is never heard itself. The transport and remote
// control read and set mute here, so they show and change the listener's
// choice, not the shadow's.

const held = new WeakMap<HTMLMediaElement, { muted: boolean }>();

/** Whether the listener has muted the player. */
export function listenerMuted(video: HTMLMediaElement): boolean {
  return held.get(video)?.muted ?? video.muted;
}

/** Sets the listener's mute; while the element is held muted, only the choice changes. */
export function setListenerMuted(video: HTMLMediaElement, muted: boolean) {
  const hold = held.get(video);
  if (!hold) {
    video.muted = muted;
    return;
  }
  if (hold.muted === muted) return;
  hold.muted = muted;
  video.dispatchEvent(new Event("volumechange"));
}

/** Mutes the element, keeping the listener's mute to give back. */
export function holdMute(video: HTMLMediaElement) {
  if (held.has(video)) return;
  held.set(video, { muted: video.muted });
  video.muted = true;
}

/** Whether the element is held muted. */
export function muteHeld(video: HTMLMediaElement): boolean {
  return held.has(video);
}

/** Gives the element the listener's mute back. */
export function releaseMute(video: HTMLMediaElement) {
  const hold = held.get(video);
  if (!hold) return;
  held.delete(video);
  video.muted = hold.muted;
}
