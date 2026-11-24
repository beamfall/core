import { Cast } from "lucide-react";
import { type KeyboardEvent, useId, useRef, useState } from "react";
import { Button } from "@/components/ui/button";
import {
  commandPlayerDisplay,
  controlSession,
  fetchMySessions,
  fetchPlayerDisplays,
} from "@/lib/graphql";
import type { MySessionInfo, PlayerDisplayInfo } from "@/lib/graphql/types";
import { useT } from "@/lib/i18n";
import "./remote.css";

type Status =
  | { kind: "idle" }
  | { kind: "loading" }
  | { kind: "sent"; device: string }
  | { kind: "failed" }
  | { kind: "failedText"; text: string }
  | { kind: "empty" };

function liveOtherDevices(sessions: MySessionInfo[], now: number): MySessionInfo[] {
  return sessions.filter(
    (session) => !session.current && (!session.expiresAt || Date.parse(session.expiresAt) > now),
  );
}

/** The picker's text for a display's presence (ADR-0240 D9). */
export function displayPresenceText(
  display: PlayerDisplayInfo,
  t: ReturnType<typeof useT>,
): string {
  if (display.presence === "offline")
    return t("playerDisplayOpenOn", { name: display.displayName });
  if (display.presence === "playing") {
    const item = display.currentItem;
    if (!item || item.privateItem || !item.title) return t("playerDisplayPrivateItem");
    return t("playerDisplayPlaying", { title: item.title });
  }
  return t("playerDisplayReady");
}

/** Maps a refused display command to what the caller can do about it. */
export function displayErrorText(
  err: unknown,
  name: string,
  t: ReturnType<typeof useT>,
): string | null {
  const message = err instanceof Error ? err.message : "";
  if (message.includes("display in party")) return t("playerDisplayInParty", { name });
  if (message.includes("display offline") || message.includes("unknown session"))
    return t("playerDisplayWentOffline", { name });
  return null;
}

/**
 * "Play on…" (CORE-1678): starts this Media on another device signed in to the
 * Profile, or on a player display the caller controls (ADR-0240). Both lists
 * load when the menu opens, not with the page; either may fail alone.
 */
export function PlayOnButton({
  mediaID,
  title,
  positionSeconds,
}: {
  mediaID: string;
  title: string;
  /** Where to start; the resume point when the item is part-watched. */
  positionSeconds?: number;
}) {
  const t = useT();
  const menuID = useId();
  const [open, setOpen] = useState(false);
  const [devices, setDevices] = useState<MySessionInfo[]>([]);
  const [displays, setDisplays] = useState<PlayerDisplayInfo[]>([]);
  const [status, setStatus] = useState<Status>({ kind: "idle" });
  const trigger = useRef<HTMLButtonElement>(null);

  // Closing the list removes the focused device button; return focus to the
  // trigger so keyboard users are not dropped on the page body.
  function close() {
    setOpen(false);
    trigger.current?.focus();
  }

  function toggle() {
    if (open) {
      setOpen(false);
      return;
    }
    setOpen(true);
    setStatus({ kind: "loading" });
    void Promise.allSettled([fetchMySessions(), fetchPlayerDisplays()]).then(
      ([sessions, players]) => {
        const others =
          sessions.status === "fulfilled"
            ? liveOtherDevices(sessions.value.sessions, Date.now())
            : [];
        const shown = players.status === "fulfilled" ? players.value : [];
        setDevices(others);
        setDisplays(shown);
        if (others.length > 0 || shown.length > 0) setStatus({ kind: "idle" });
        else if (sessions.status === "rejected" && players.status === "rejected")
          setStatus({ kind: "failed" });
        else setStatus({ kind: "empty" });
      },
    );
  }

  async function playOnDisplay(display: PlayerDisplayInfo) {
    if (!display.sessionID || display.presence === "offline") return;
    close();
    try {
      await commandPlayerDisplay({
        sessionID: display.sessionID,
        command: "play-media",
        mediaID,
        ...(positionSeconds && positionSeconds > 0
          ? { positionMs: Math.floor(positionSeconds * 1000) }
          : {}),
      });
      setStatus({ kind: "sent", device: display.displayName });
    } catch (err) {
      const text = displayErrorText(err, display.displayName, t);
      setStatus(text ? { kind: "failedText", text } : { kind: "failed" });
    }
  }

  async function playOn(session: MySessionInfo) {
    const device = session.deviceName || t("remoteDeviceUnnamed");
    close();
    try {
      await controlSession({
        sessionID: session.id,
        command: "play-media",
        mediaID,
        ...(positionSeconds && positionSeconds > 0
          ? { positionMs: Math.floor(positionSeconds * 1000) }
          : {}),
      });
      setStatus({ kind: "sent", device });
    } catch {
      setStatus({ kind: "failed" });
    }
  }

  function onKeyDown(event: KeyboardEvent) {
    if (event.key === "Escape" && open) {
      event.stopPropagation();
      close();
    }
  }

  const statusText =
    status.kind === "loading"
      ? t("playOnLoading")
      : status.kind === "empty"
        ? t("playOnNoTargets")
        : status.kind === "sent"
          ? t("playOnSent", { device: status.device })
          : status.kind === "failed"
            ? t("playOnFailed")
            : status.kind === "failedText"
              ? status.text
              : "";

  return (
    <div className="bf-play-on" onKeyDown={onKeyDown}>
      <Button
        ref={trigger}
        variant="ghost"
        size="lg"
        className="bf-detail-secondary"
        aria-label={t("playOnLabel", { title })}
        aria-expanded={open}
        aria-controls={menuID}
        onClick={toggle}
      >
        <Cast aria-hidden />
        {t("playOn")}
      </Button>
      {open && status.kind === "idle" && (devices.length > 0 || displays.length > 0) && (
        <div id={menuID} className="bf-play-on-menu">
          {devices.length > 0 && (
            <>
              {displays.length > 0 && <p className="bf-play-on-group">{t("playOnYourDevices")}</p>}
              <ul>
                {devices.map((session) => (
                  <li key={session.id}>
                    <button
                      type="button"
                      className="bf-play-on-item"
                      onClick={() => playOn(session)}
                    >
                      {session.deviceName || t("remoteDeviceUnnamed")}
                    </button>
                  </li>
                ))}
              </ul>
            </>
          )}
          {displays.length > 0 && (
            <>
              <p className="bf-play-on-group">{t("playOnPlayerDisplays")}</p>
              <ul>
                {displays.map((display) =>
                  display.presence === "offline" || !display.sessionID ? (
                    <li key={display.handle} className="bf-play-on-item" data-offline="true">
                      <span>{display.displayName}</span>
                      <span className="bf-play-on-detail">
                        {t("playerDisplayOpenOn", { name: display.displayName })}
                      </span>
                    </li>
                  ) : (
                    <li key={display.handle}>
                      <button
                        type="button"
                        className="bf-play-on-item"
                        onClick={() => playOnDisplay(display)}
                      >
                        <span>{display.displayName}</span>
                        <span className="bf-play-on-detail">{displayPresenceText(display, t)}</span>
                      </button>
                    </li>
                  ),
                )}
              </ul>
            </>
          )}
        </div>
      )}
      <p className="bf-play-on-status" role="status">
        {statusText}
      </p>
    </div>
  );
}
