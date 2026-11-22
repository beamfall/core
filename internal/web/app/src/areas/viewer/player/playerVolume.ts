import { setListenerMuted } from "./listenerMute";

// The listener's volume and mute (CORE-3078). Per device, not per Profile: kept
// in localStorage so a mute survives the next item, a reload and the next visit.
// A blocked or cleared store means full volume, unmuted. Starting muted is
// always allowed under the autoplay policy; unmuting writes muted:false.

const KEY = "beamfall.playerVolume";

export interface PlayerVolume {
  muted: boolean;
  volume: number;
}

export function loadPlayerVolume(): PlayerVolume {
  try {
    const raw = JSON.parse(
      window.localStorage.getItem(KEY) ?? "null",
    ) as Partial<PlayerVolume> | null;
    const volume = typeof raw?.volume === "number" && Number.isFinite(raw.volume) ? raw.volume : 1;
    return { muted: raw?.muted === true, volume: Math.max(0, Math.min(1, volume)) };
  } catch {
    return { muted: false, volume: 1 };
  }
}

export function savePlayerVolume(value: PlayerVolume) {
  try {
    window.localStorage.setItem(KEY, JSON.stringify(value));
  } catch {
    // A blocked store only forgets the choice.
  }
}

/** Gives a new player element the device's remembered volume and mute. */
export function applyPlayerVolume(video: HTMLMediaElement) {
  const saved = loadPlayerVolume();
  video.volume = saved.volume;
  if (saved.muted) setListenerMuted(video, true);
}
