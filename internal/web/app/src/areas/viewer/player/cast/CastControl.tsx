import { Cast, Pause, Play, Square } from "lucide-react";
import type { RefObject } from "react";
import { useT } from "@/lib/i18n";
import { formatAriaTime, formatClockTime } from "../../lib/transport";
import { useCast } from "./useCast";
import "./cast.css";

/**
 * The web player's Cast control (CORE-1679). Renders nothing unless the Cast
 * SDK loaded and a receiver is on the network. While casting, the sender's
 * own play/pause, position and stop controls drive the receiver through the
 * RemotePlayerController.
 */
export function CastControl({
  videoRef,
  mediaID,
  mediaTitle,
}: {
  videoRef: RefObject<HTMLVideoElement | null>;
  mediaID: string;
  mediaTitle: string;
}) {
  const t = useT();
  const cast = useCast({ videoRef, mediaID, title: mediaTitle });
  if (!cast.available || !cast.devicesFound) return null;

  const { remote } = cast;
  const statusText =
    cast.status === "starting"
      ? t("castStarting")
      : cast.status === "failed"
        ? t("castFailed")
        : cast.status === "no-route"
          ? t("castNoRoute")
          : cast.connected
            ? cast.device
              ? t("castConnected", { device: cast.device })
              : t("castConnectedUnnamed")
            : "";
  const failed = cast.status === "failed" || cast.status === "no-route";

  return (
    <div className="bf-cast">
      {(!cast.connected || !remote.loaded) && (
        <button
          type="button"
          className="bf-control is-icon"
          onClick={cast.start}
          disabled={cast.status === "starting"}
          aria-label={
            cast.connected && cast.device
              ? t("castPlayHere", { device: cast.device })
              : t("castStart")
          }
        >
          <Cast aria-hidden="true" />
        </button>
      )}
      {cast.connected && remote.loaded && (
        <fieldset className="bf-cast-remote" aria-label={t("castControls")}>
          <button
            type="button"
            className="bf-control is-icon"
            onClick={cast.playOrPause}
            aria-label={remote.paused ? t("castPlay") : t("castPause")}
          >
            {remote.paused ? <Play aria-hidden="true" /> : <Pause aria-hidden="true" />}
          </button>
          <input
            type="range"
            className="bf-cast-seek"
            min={0}
            max={Math.max(remote.duration, 0)}
            step={1}
            value={Math.min(remote.currentTime, remote.duration || remote.currentTime)}
            aria-label={t("castPosition")}
            aria-valuetext={formatAriaTime(remote.currentTime, remote.duration)}
            onChange={(event) => cast.seek(Number(event.currentTarget.value))}
          />
          <span className="bf-cast-time" aria-hidden="true">
            {formatClockTime(remote.currentTime)} / {formatClockTime(remote.duration)}
          </span>
          <button
            type="button"
            className="bf-control is-icon"
            onClick={cast.stop}
            aria-label={t("castStop")}
          >
            <Square aria-hidden="true" />
          </button>
        </fieldset>
      )}
      <span className="bf-player-control-status" role={failed ? "alert" : "status"}>
        {statusText}
      </span>
    </div>
  );
}
