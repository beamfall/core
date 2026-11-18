import { useEffect, useState } from "react";
import { NativeSelect } from "@/components/ui/select";
import { useDisplayLocale, useT } from "@/lib/i18n";
import { useSession, useViewerIdentity } from "@/lib/session";
import { fetchMediaTracks, type MediaTrackVersion } from "../lib/mediaDetail";
import { readVersionChoice, writeVersionChoice } from "../lib/versionChoice";
import type { StartTracks } from "../lib/watchQueue";
import {
  audioTrackLabel,
  captionTrackLabel,
  codecName,
  distinctLabels,
  type TrackLabelText,
} from "../player/streamTracks";

/** The select value that leaves a track to the server, as playback does without a pick. */
const AUTO = "";
const OFF = "off";

const SELECT_CLASS =
  "h-10 max-w-full rounded-[var(--radius-control)] border border-input bg-[var(--control-bg)] px-3 text-sm";

interface Listing {
  key: string;
  versions: MediaTrackVersion[];
}

/**
 * The item's Versions and tracks (mediaTracks), read once per item and
 * viewer identity; [] until they arrive and when the read fails. The page
 * reads it alongside the item and shares it between the pickers and Details.
 */
export function useMediaTracks(mediaID: string): MediaTrackVersion[] {
  const identity = useViewerIdentity();
  const key = `${identity ?? ""}:${mediaID}`;
  const [listing, setListing] = useState<Listing | null>(null);

  useEffect(() => {
    if (!mediaID) return;
    const controller = new AbortController();
    void fetchMediaTracks(mediaID, controller.signal)
      .catch((): MediaTrackVersion[] => [])
      .then((versions) => {
        if (!controller.signal.aborted) setListing({ key, versions });
      });
    return () => controller.abort();
  }, [key, mediaID]);

  return listing?.key === key ? listing.versions : [];
}

/** The picked Version, else the remembered one; undefined leaves the choice to the server. */
function pickedVersion(
  versions: readonly MediaTrackVersion[],
  picks: StartTracks | null,
  profileID: string | undefined,
  mediaID: string,
): MediaTrackVersion | undefined {
  const pickedID =
    (picks?.mediaID === mediaID ? picks.versionID : undefined) ??
    readVersionChoice(profileID, mediaID);
  return versions.find((candidate) => candidate.id === pickedID);
}

/** The Version the pickers show: the picked one, else the remembered one, else the server's default. */
export function shownVersion(
  versions: readonly MediaTrackVersion[],
  picks: StartTracks | null,
  profileID: string | undefined,
  mediaID: string,
): MediaTrackVersion | undefined {
  return pickedVersion(versions, picks, profileID, mediaID) ?? versions[0];
}

/** The localized words a track label is built from. */
export function trackLabelText(t: ReturnType<typeof useT>, locale: string): TrackLabelText {
  return {
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
    // The listing carries no stream URLs, so how a muxed track reaches the
    // screen is decided at playback, not here.
    burnedIn: "",
  };
}

/** One row of the Details section's file facts. */
export interface FileFact {
  key: string;
  label: string;
  value: string;
}

/**
 * The shown Version's file facts for Details (CORE-2508): its label, video
 * codec with HDR format, and default audio track. Unknown parts are left out.
 */
export function versionFileFacts(
  version: MediaTrackVersion | undefined,
  t: ReturnType<typeof useT>,
  locale: string,
): FileFact[] {
  if (!version) return [];
  const audio = version.audioTracks.find((track) => track.default) ?? version.audioTracks[0];
  const video = [version.videoCodec ? codecName(version.videoCodec) : "", version.hdr ?? ""]
    .filter(Boolean)
    .join(" · ");
  return [
    { key: "version", label: t("version"), value: version.label.trim() },
    { key: "video", label: t("mediaDetailVideo"), value: video },
    {
      key: "audio",
      label: t("audio"),
      value: audio ? audioTrackLabel(audio, trackLabelText(t, locale)) : "",
    },
  ].filter((fact) => fact.value !== "");
}

// The Version, Audio and Subtitles pickers of a Media detail page (CORE-2442),
// read from the stored inventory (mediaTracks) without starting a session.
// Every pick is handed to the page and rides into playback in the watch
// route's nav state, so blocked browser storage cannot split a Version from
// the tracks picked with it; a Version pick is also remembered as the player
// remembers it (versionChoice), best-effort. A pick names a track of the
// Version on screen, so with two or more Versions it carries that Version:
// otherwise the server could play another file whose stream numbers mean
// other tracks. Version starts at Auto (the server's choice) unless one is
// picked or remembered. Each picker shows only when there is a choice, and a
// failed read shows none.
export function MediaTrackPickers({
  mediaID,
  versions,
  picks,
  onPick,
}: {
  mediaID: string;
  /** The item's listing from useMediaTracks, shared with the Details section. */
  versions: MediaTrackVersion[];
  picks: StartTracks | null;
  onPick: (picks: StartTracks) => void;
}) {
  const t = useT();
  const locale = useDisplayLocale();
  const profileID = useSession().session?.profileID;

  if (versions.length === 0) return null;
  const current: StartTracks = picks?.mediaID === mediaID ? picks : { mediaID };
  const chosen = pickedVersion(versions, current, profileID, mediaID);
  // Auto lists the server's default, the first in label order.
  const version = chosen ?? versions[0];
  const audioIndex = version.audioTracks.some((track) => track.streamIndex === current.audioIndex)
    ? current.audioIndex
    : undefined;
  const captionID =
    current.captionID === null ||
    version.captionTracks.some((track) => track.id === current.captionID)
      ? current.captionID
      : undefined;

  const showVersion = versions.length > 1;
  const showAudio = version.audioTracks.length > 1;
  const showCaptions = version.captionTracks.length > 0;
  if (!showVersion && !showAudio && !showCaptions) return null;

  const pickTracks = (next: Omit<StartTracks, "mediaID" | "versionID">) => {
    if (!showVersion) {
      onPick({ mediaID, ...next });
      return;
    }
    writeVersionChoice(profileID, mediaID, version.id);
    onPick({ mediaID, versionID: version.id, ...next });
  };

  const labelText = trackLabelText(t, locale);
  const numbered = (number: number) => t("trackDuplicateNumber", { number });
  const versionLabels = distinctLabels(
    versions.map((candidate) =>
      [
        candidate.label || t("versionDefault"),
        candidate.videoCodec ? codecName(candidate.videoCodec) : "",
        candidate.hdr ?? "",
      ]
        .filter(Boolean)
        .join(" · "),
    ),
    numbered,
  );
  const audioLabels = distinctLabels(
    version.audioTracks.map((track) => audioTrackLabel(track, labelText)),
    numbered,
  );
  const captionLabels = distinctLabels(
    version.captionTracks.map((track) => captionTrackLabel(track, labelText)),
    numbered,
  );

  return (
    <div className="bf-detail-tracks">
      {showVersion && (
        <label className="bf-detail-track">
          <span>{t("version")}</span>
          <NativeSelect
            className={SELECT_CLASS}
            value={chosen ? chosen.id : AUTO}
            onValueChange={(id) => {
              if (id === (chosen ? chosen.id : AUTO)) return;
              writeVersionChoice(profileID, mediaID, id);
              // Stream numbers and caption ids belong to one file.
              onPick({ mediaID, versionID: id });
            }}
          >
            <option value={AUTO}>{t("auto")}</option>
            {versions.map((candidate, index) => (
              <option key={candidate.id} value={candidate.id}>
                {versionLabels[index]}
              </option>
            ))}
          </NativeSelect>
        </label>
      )}
      {showAudio && (
        <label className="bf-detail-track">
          <span>{t("audio")}</span>
          <NativeSelect
            className={SELECT_CLASS}
            value={audioIndex === undefined ? AUTO : String(audioIndex)}
            onValueChange={(value) =>
              pickTracks({
                audioIndex: value === AUTO ? undefined : Number(value),
                captionID,
              })
            }
          >
            <option value={AUTO}>{t("auto")}</option>
            {version.audioTracks.map((track, index) => (
              <option key={track.streamIndex} value={String(track.streamIndex)}>
                {audioLabels[index]}
              </option>
            ))}
          </NativeSelect>
        </label>
      )}
      {showCaptions && (
        <label className="bf-detail-track">
          <span>{t("subtitles")}</span>
          <NativeSelect
            className={SELECT_CLASS}
            value={captionID === undefined ? AUTO : (captionID ?? OFF)}
            onValueChange={(value) =>
              pickTracks({
                audioIndex,
                captionID: value === AUTO ? undefined : value === OFF ? null : value,
              })
            }
          >
            <option value={AUTO}>{t("auto")}</option>
            <option value={OFF}>{t("subtitlesOff")}</option>
            {version.captionTracks.map((track, index) => (
              <option key={track.id} value={track.id}>
                {captionLabels[index]}
              </option>
            ))}
          </NativeSelect>
        </label>
      )}
    </div>
  );
}
