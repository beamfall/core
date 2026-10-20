import type Hls from "hls.js";
import { useCallback, useState } from "react";
import { useT } from "@/lib/i18n";
import type { PlaybackStream } from "../lib/playback";
import {
  buildRenditionMenuModel,
  buildTrackMenuModel,
  type RenditionMenuOption,
  type TrackMenuOption,
} from "../lib/transport";
import { audioTrackList, audioTracksArray } from "./media-tracks";

// useLiveTracks bridges the live hls.js / native <video> track objects to the
// pure menu models (transport.ts) and exposes the selection handlers, ported
// from trackList / renderTrackMenus / renderQualityMenu (app.js). The pure
// label/selection/off-entry logic lives in transport.ts; this hook only reads
// the live player objects, stamps each option with the id its handler
// dispatches on (the track index, "off" for the leading subtitle entry, or
// "auto"/"direct"/rendition-id for quality), and applies the selection.
//
// tracksVersion (bumped by usePlayer on every hls.js track-update) and a local
// nudge counter drive re-derivation, so a selection re-renders the menu with the
// new active row.

export interface MenuOption<Id extends string = string> extends TrackMenuOption {
  id: Id;
}

interface LiveTracks {
  audioOptions: MenuOption[];
  subtitleOptions: MenuOption[];
  qualityOptions: RenditionMenuOption[];
  manualPinActive: boolean;
  selectAudio: (id: string) => void;
  selectSubtitle: (id: string) => void;
  selectQuality: (id: string) => void;
}

function nativeAudioTracks(video: HTMLVideoElement) {
  return audioTracksArray(video).map((track) => ({
    label: track.label || track.language || "",
    selected: track.enabled,
  }));
}

function nativeSubtitleTracks(video: HTMLVideoElement) {
  const list = video.textTracks;
  return list
    ? Array.from(list).map((track) => ({
        label: track.label || track.language || "",
        selected: track.mode === "showing",
      }))
    : [];
}

export function useLiveTracks({
  videoRef,
  hlsRef,
  stream,
  manualRenditionID,
  setManualRenditionID,
  onSubtitleChanged,
}: {
  videoRef: React.RefObject<HTMLVideoElement | null>;
  hlsRef: React.RefObject<Hls | null>;
  stream: PlaybackStream | null;
  manualRenditionID: string;
  setManualRenditionID: (id: string) => void;
  /** Called after a subtitle selection so an active sync offset can re-apply. */
  onSubtitleChanged: () => void;
}): LiveTracks {
  // A local nudge so a selection re-renders even when hls.js fires no event
  // (e.g. native <video> track toggles).
  const [, setNudge] = useState(0);
  const nudge = useCallback(() => setNudge((n) => n + 1), []);
  const t = useT();

  const video = videoRef.current;
  const hls = hlsRef.current;

  const labels = {
    audio: t("audio"),
    subtitles: t("subtitles"),
    subtitlesOff: t("subtitlesOff"),
  };

  const hlsAudio =
    hls && Array.isArray(hls.audioTracks)
      ? hls.audioTracks.map((track, index) => ({
          label: track.name || track.lang || "",
          selected: hls.audioTrack === index,
        }))
      : [];
  const nativeAudio = video ? nativeAudioTracks(video) : [];
  const hlsSubtitle =
    hls && Array.isArray(hls.subtitleTracks)
      ? hls.subtitleTracks.map((track, index) => ({
          label: track.name || track.lang || "",
          selected: hls.subtitleTrack === index && hls.subtitleDisplay !== false,
        }))
      : [];
  const nativeSubtitle = video ? nativeSubtitleTracks(video) : [];

  const audioModel = buildTrackMenuModel("audio", { hlsAudio, nativeAudio }, labels);
  const subtitleModel = buildTrackMenuModel("subtitle", { hlsSubtitle, nativeSubtitle }, labels);

  const audioOptions: MenuOption[] = audioModel.map((option, index) => ({
    ...option,
    id: String(index),
  }));
  // Subtitle menu: the leading entry is "off", the rest are zero-based indices.
  const subtitleOptions: MenuOption[] = subtitleModel.map((option, index) => ({
    ...option,
    id: index === 0 ? "off" : String(index - 1),
  }));

  const renditionModel = buildRenditionMenuModel({
    stream,
    manualRenditionID,
    labels: { auto: t("auto"), directPlay: t("directPlay") },
  });

  const selectAudio = useCallback(
    (id: string) => {
      const v = videoRef.current;
      const h = hlsRef.current;
      const list = audioTrackList(v);
      if (h && Array.isArray(h.audioTracks)) {
        h.audioTrack = Number(id);
      } else if (list && list[Number(id)]) {
        for (let index = 0; index < list.length; index++) {
          list[index].enabled = false;
        }
        list[Number(id)].enabled = true;
      }
      nudge();
    },
    [videoRef, hlsRef, nudge],
  );

  const selectSubtitle = useCallback(
    (id: string) => {
      const v = videoRef.current;
      const h = hlsRef.current;
      if (id === "off") {
        if (h) {
          h.subtitleDisplay = false;
          h.subtitleTrack = -1;
        }
        if (v && v.textTracks) {
          for (const track of Array.from(v.textTracks)) {
            track.mode = "disabled";
          }
        }
      } else if (h && Array.isArray(h.subtitleTracks)) {
        h.subtitleDisplay = true;
        h.subtitleTrack = Number(id);
      } else if (v && v.textTracks && v.textTracks[Number(id)]) {
        for (const track of Array.from(v.textTracks)) {
          track.mode = "disabled";
        }
        v.textTracks[Number(id)].mode = "showing";
      }
      onSubtitleChanged();
      nudge();
    },
    [videoRef, hlsRef, onSubtitleChanged, nudge],
  );

  const selectQuality = useCallback(
    (id: string) => {
      const h = hlsRef.current;
      if (id === "auto") {
        setManualRenditionID("");
        if (h) {
          h.currentLevel = -1;
        }
      } else if (id !== "direct") {
        setManualRenditionID(id);
        if (h) {
          const renditions = stream?.renditions || [];
          const renditionIndex = renditions.findIndex(
            (rendition) => String(rendition.id) === String(id),
          );
          h.currentLevel = Math.max(0, renditionIndex);
        }
      }
      nudge();
    },
    [hlsRef, stream, setManualRenditionID, nudge],
  );

  return {
    audioOptions,
    subtitleOptions,
    qualityOptions: renditionModel.options,
    manualPinActive: renditionModel.manualPinActive,
    selectAudio,
    selectSubtitle,
    selectQuality,
  };
}
