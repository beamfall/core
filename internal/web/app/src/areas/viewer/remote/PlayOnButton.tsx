import { Cast } from "lucide-react";
import { type KeyboardEvent, useId, useState } from "react";
import { Button } from "@/components/ui/button";
import { controlSession, fetchMySessions } from "@/lib/graphql";
import type { MySessionInfo } from "@/lib/graphql/types";
import { useT } from "@/lib/i18n";
import "./remote.css";

type Status =
  | { kind: "idle" }
  | { kind: "loading" }
  | { kind: "sent"; device: string }
  | { kind: "failed" }
  | { kind: "empty" };

function liveOtherDevices(sessions: MySessionInfo[], now: number): MySessionInfo[] {
  return sessions.filter(
    (session) => !session.current && (!session.expiresAt || Date.parse(session.expiresAt) > now),
  );
}

/**
 * "Play on…" (CORE-1678): starts this Media on another device signed in to the
 * Profile. The device list loads when the menu opens, not with the page.
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
  const [status, setStatus] = useState<Status>({ kind: "idle" });

  function toggle() {
    if (open) {
      setOpen(false);
      return;
    }
    setOpen(true);
    setStatus({ kind: "loading" });
    fetchMySessions()
      .then((info) => {
        const others = liveOtherDevices(info.sessions, Date.now());
        setDevices(others);
        setStatus(others.length === 0 ? { kind: "empty" } : { kind: "idle" });
      })
      .catch(() => setStatus({ kind: "failed" }));
  }

  async function playOn(session: MySessionInfo) {
    const device = session.deviceName || t("remoteDeviceUnnamed");
    setOpen(false);
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
      setOpen(false);
    }
  }

  const statusText =
    status.kind === "loading"
      ? t("playOnLoading")
      : status.kind === "empty"
        ? t("playOnEmpty")
        : status.kind === "sent"
          ? t("playOnSent", { device: status.device })
          : status.kind === "failed"
            ? t("playOnFailed")
            : "";

  return (
    <div className="bf-play-on" onKeyDown={onKeyDown}>
      <Button
        variant="outline"
        size="sm"
        aria-label={t("playOnLabel", { title })}
        aria-expanded={open}
        aria-controls={menuID}
        onClick={toggle}
      >
        <Cast aria-hidden />
        {t("playOn")}
      </Button>
      {open && devices.length > 0 && status.kind === "idle" && (
        <ul id={menuID} className="bf-play-on-menu">
          {devices.map((session) => (
            <li key={session.id}>
              <button type="button" className="bf-play-on-item" onClick={() => playOn(session)}>
                {session.deviceName || t("remoteDeviceUnnamed")}
              </button>
            </li>
          ))}
        </ul>
      )}
      <p className="bf-play-on-status" role="status">
        {statusText}
      </p>
    </div>
  );
}
