import { afterEach, describe, expect, it, vi } from "vitest";
import { setListenerMuted } from "./listenerMute";
import { applyPlayerVolume, loadPlayerVolume, savePlayerVolume } from "./playerVolume";

afterEach(() => {
  window.localStorage.clear();
  vi.restoreAllMocks();
});

describe("playerVolume (CORE-3078)", () => {
  it("defaults to full volume, unmuted", () => {
    expect(loadPlayerVolume()).toEqual({ muted: false, volume: 1 });
  });

  it("remembers a mute and starts the next element muted at the saved volume", () => {
    savePlayerVolume({ muted: true, volume: 0.4 });
    const video = document.createElement("video");
    applyPlayerVolume(video);
    expect(video.muted).toBe(true);
    expect(video.volume).toBeCloseTo(0.4);
  });

  it("unmuting clears the remembered mute", () => {
    savePlayerVolume({ muted: true, volume: 0.4 });
    const video = document.createElement("video");
    applyPlayerVolume(video);
    setListenerMuted(video, false);
    savePlayerVolume({ muted: video.muted, volume: video.volume });
    const next = document.createElement("video");
    applyPlayerVolume(next);
    expect(next.muted).toBe(false);
  });

  it("ignores a corrupt store and survives a blocked one", () => {
    window.localStorage.setItem("beamfall.playerVolume", "{nope");
    expect(loadPlayerVolume()).toEqual({ muted: false, volume: 1 });
    vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
      throw new Error("blocked");
    });
    expect(() => savePlayerVolume({ muted: true, volume: 1 })).not.toThrow();
  });
});
