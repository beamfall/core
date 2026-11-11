import { act, renderHook } from "@testing-library/react";
import type Hls from "hls.js";
import { createRef, type ReactNode, useState } from "react";
import { describe, expect, it } from "vitest";
import { I18nProvider } from "@/lib/i18n";
import type { PlaybackStream } from "../lib/playback";
import { useLiveTracks } from "./useLiveTracks";

const hlsStream: PlaybackStream = {
  kind: "hls",
  url: "/stream.m3u8",
  renditions: [
    { id: "1080", height: 1080 },
    { id: "720", height: 720 },
  ],
};

function wrapper({ children }: { children: ReactNode }) {
  return <I18nProvider initialLocale="en">{children}</I18nProvider>;
}

function renderTracks(hls: Hls | null) {
  const videoRef = createRef<HTMLVideoElement>();
  const hlsRef = { current: hls };
  return renderHook(
    () => {
      const [manualRenditionID, setManualRenditionID] = useState("");
      return useLiveTracks({
        videoRef,
        hlsRef,
        stream: hlsStream,
        manualRenditionID,
        setManualRenditionID,
        onSubtitleChanged: () => {},
      });
    },
    { wrapper },
  );
}

describe("useLiveTracks quality selection", () => {
  it("keeps native HLS on Auto because the element cannot apply a pin", () => {
    const { result } = renderTracks(null);
    act(() => result.current.selectQuality("720"));
    expect(result.current.manualPinActive).toBe(false);
    const selected = result.current.qualityOptions.filter((option) => option.selected);
    expect(selected.map((option) => option.id)).toEqual(["auto"]);
  });

  it("pins the rendition when hls.js can apply it", () => {
    const hls = { currentLevel: -1 } as unknown as Hls;
    const { result } = renderTracks(hls);
    act(() => result.current.selectQuality("720"));
    expect(hls.currentLevel).toBe(1);
    expect(result.current.manualPinActive).toBe(true);
    const selected = result.current.qualityOptions.filter((option) => option.selected);
    expect(selected.map((option) => option.id)).toEqual(["720"]);
  });
});
