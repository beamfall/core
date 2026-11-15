import type Hls from "hls.js";
import { useCallback, useState } from "react";
import { useDisplayLocale, useT } from "@/lib/i18n";
import { hlsStreamOf, type PlaybackInfo, type PlaybackStream } from "../lib/playback";
import {
  buildRenditionMenuModel,
  buildTrackMenuModel,
  type RenditionMenuOption,
  renditionIndex,
  type TrackMenuOption,
} from "../lib/transport";
import { audioTrackList, audioTracksArray } from "./media-tracks";
import { audioTrackLabel, captionTrackLabel, type TrackLabelText } from "./streamTracks";
import {
  type StreamTrackControl,
  type SwitchResult,
  selectHlsLevel,
  selectHlsRendition,
} from "./usePlayer";

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
//
// When playbackInfo lists the file's audio and caption tracks (CORE-1709) the
// Audio and Subtitles menus are built from those instead, on every route: the
// live lists hold only what one stream carries (a server stream maps a single
// audio track and no subtitles). Ids stay positional ("off" plus zero-based
// indices) so remote control and the shortcuts address them the same way.

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

/** The id of the disabled row that says why some subtitles are missing. */
export const SUBTITLES_UNAVAILABLE_ID = "unavailable";

function unavailableSubtitleKey(reason: string) {
  switch (reason) {
    case "no-forced-subtitle-in-preferred-language":
      return "subtitlesUnavailableNoForced" as const;
    case "no-client-renderable-subtitle-track":
      return "subtitlesUnavailableUnrenderable" as const;
    default:
      return "subtitlesUnavailable" as const;
  }
}

export function useLiveTracks({
  videoRef,
  hlsRef,
  stream,
  streams,
  info,
  trackControl,
  switchToHls,
  manualRenditionID,
  setManualRenditionID,
  onSubtitleChanged,
}: {
  videoRef: React.RefObject<HTMLVideoElement | null>;
  hlsRef: React.RefObject<Hls | null>;
  stream: PlaybackStream | null;
  /** The playback info: its audioTracks and captionTracks feed the menus. */
  info?: PlaybackInfo | null;
  /** Applies a pick among info's tracks; without it the live lists drive the menus. */
  trackControl?: StreamTrackControl;
  /** The whole stream inventory; its HLS sibling feeds the direct-play quality menu. */
  streams?: PlaybackStream[];
  /** Switches direct play to the HLS sibling at the current position. */
  switchToHls?: (renditionKey?: string) => SwitchResult;
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
  const locale = useDisplayLocale();

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

  const fileAudio = trackControl ? (info?.audioTracks ?? []) : [];
  const fileCaptions = trackControl ? (info?.captionTracks ?? []) : [];
  const unavailableReason = trackControl ? (info?.subtitlesUnavailableReason ?? "") : "";
  const fileSubtitles = fileCaptions.length > 0 || unavailableReason !== "";
  const labelText: TrackLabelText = {
    locale,
    unknownLanguage: t("trackLanguageUnknown"),
    mono: t("trackChannelsMono"),
    stereo: t("trackChannelsStereo"),
    channels: (count) => t("trackChannelsCount", { count }),
    commentary: t("trackRoleCommentary"),
    described: t("trackRoleDescribed"),
    forced: t("trackRoleForced"),
    sdh: t("trackRoleSDH"),
    external: t("trackExternal"),
    burnedIn: t("trackBurnedIn"),
  };
  const option = (id: string, label: string, selected: boolean): MenuOption => ({
    id,
    label,
    selected,
    disabled: false,
    reason: "",
  });

  let audioOptions: MenuOption[];
  if (fileAudio.length > 0) {
    const chosen = fileAudio.some((track) => track.selected)
      ? fileAudio.findIndex((track) => track.selected)
      : Math.max(
          0,
          fileAudio.findIndex((track) => track.default),
        );
    audioOptions = fileAudio.map((track, index) =>
      option(String(index), audioTrackLabel(track, labelText), index === chosen),
    );
  } else {
    audioOptions = audioModel.map((entry, index) => ({ ...entry, id: String(index) }));
  }

  // Subtitle menu: the leading entry is "off", the rest are zero-based indices.
  let subtitleOptions: MenuOption[];
  if (fileSubtitles && trackControl) {
    const active = trackControl.activeCaptionID();
    subtitleOptions = [
      option("off", labels.subtitlesOff, active === null),
      ...fileCaptions.map((track, index) =>
        option(String(index), captionTrackLabel(track, labelText), track.id === active),
      ),
    ];
    if (unavailableReason) {
      // Never silently absent: say why a subtitle the file has is not offered.
      subtitleOptions.push({
        id: SUBTITLES_UNAVAILABLE_ID,
        label: t(unavailableSubtitleKey(unavailableReason)),
        selected: false,
        disabled: true,
        reason: "",
      });
    }
  } else {
    subtitleOptions = subtitleModel.map((entry, index) => ({
      ...entry,
      id: index === 0 ? "off" : String(index - 1),
    }));
  }

  const alternate = stream?.kind === "hls" ? null : hlsStreamOf(streams);
  const renditionModel = buildRenditionMenuModel({
    stream,
    alternate,
    manualRenditionID,
    labels: { auto: t("auto"), directPlay: t("directPlay") },
  });

  const selectAudio = useCallback(
    (id: string) => {
      const fileTrack = fileAudio[Number(id)];
      if (trackControl && fileAudio.length > 0) {
        if (fileTrack) trackControl.selectAudio(fileTrack.streamIndex);
        nudge();
        return;
      }
      const v = videoRef.current;
      const h = hlsRef.current;
      const list = audioTrackList(v);
      // The menu lists hls.js tracks only when it has some (pickTrackSource).
      if (h && Array.isArray(h.audioTracks) && h.audioTracks.length > 0) {
        h.audioTrack = Number(id);
      } else if (list && list[Number(id)]) {
        for (let index = 0; index < list.length; index++) {
          list[index].enabled = false;
        }
        list[Number(id)].enabled = true;
      }
      nudge();
    },
    [videoRef, hlsRef, nudge, trackControl, fileAudio],
  );

  const selectSubtitle = useCallback(
    (id: string) => {
      if (trackControl && fileSubtitles) {
        if (id === "off") trackControl.selectCaption(null);
        else {
          const track = fileCaptions[Number(id)];
          if (!track) return;
          trackControl.selectCaption(track.id);
        }
        onSubtitleChanged();
        nudge();
        return;
      }
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
      } else if (h && Array.isArray(h.subtitleTracks) && h.subtitleTracks.length > 0) {
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
    [videoRef, hlsRef, onSubtitleChanged, nudge, trackControl, fileSubtitles, fileCaptions],
  );

  const selectQuality = useCallback(
    (id: string) => {
      const h = hlsRef.current;
      if (id === "auto") {
        setManualRenditionID("");
        if (h) selectHlsLevel(h, -1);
      } else if (id !== "direct") {
        // A key that matches no rendition changes nothing: never pin level 0.
        if (stream?.kind !== "hls" && !h) {
          // Direct play: step down by switching to the HLS sibling, pinned.
          const sibling = hlsStreamOf(streams);
          if (!sibling || renditionIndex(sibling.renditions, id) < 0) return;
          if (switchToHls?.(id) === "hlsjs") setManualRenditionID(id);
        } else {
          // Native HLS exposes no level control: stay on Auto rather than
          // marking a pin the element cannot honour.
          const at = renditionIndex(stream?.renditions, id);
          if (at < 0 || !h) return;
          // hls.js orders levels by ascending bitrate, not the server's list order.
          if (!selectHlsRendition(h, stream?.renditions?.[at])) return;
          setManualRenditionID(id);
        }
      }
      nudge();
    },
    [hlsRef, stream, streams, switchToHls, setManualRenditionID, nudge],
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
