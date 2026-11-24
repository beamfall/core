import { Pause, Play, Square, Volume2, VolumeX } from "lucide-react";
import { useCallback, useEffect, useState } from "react";
import { useSearchParams } from "react-router";
import { AppShell } from "@/components/chrome/AppShell";
import { Button } from "@/components/ui/button";
import { commandPlayerDisplay, fetchPlayerDisplays } from "@/lib/graphql";
import type { PlayerDisplayInfo } from "@/lib/graphql/types";
import { useT } from "@/lib/i18n";
import { ViewerActions } from "../components/ViewerActions";
import { formatClockTime } from "../lib/transport";
import { displayErrorText, displayPresenceText } from "../remote/PlayOnButton";
import "../remote/remote.css";

type Lookup =
  | { kind: "loading" }
  | { kind: "missing" }
  | { kind: "found"; display: PlayerDisplayInfo };

type Command = "pause" | "resume" | "stop" | "mute" | "unmute";

/**
 * The page a player display's QR code opens (ADR-0240 D9):
 * `/web/play-on?display=<handle>`. The handle resolves only against the
 * caller's own `playerDisplays`, after sign-in; an unknown, revoked, unbound or
 * uncontrolled handle, and any load failure, show the same message, so the page
 * reveals nothing about displays the caller cannot control.
 */
export function PlayOnDisplayPage() {
  const t = useT();
  const [params] = useSearchParams();
  const handle = params.get("display")?.trim() ?? "";
  const [lookup, setLookup] = useState<Lookup>({ kind: "loading" });
  const [notice, setNotice] = useState<string | null>(null);

  const refresh = useCallback(() => {
    if (!handle) {
      setLookup({ kind: "missing" });
      return;
    }
    fetchPlayerDisplays()
      .then((displays) => {
        const display = displays.find((d) => d.handle === handle);
        setLookup(display ? { kind: "found", display } : { kind: "missing" });
      })
      .catch(() => setLookup({ kind: "missing" }));
  }, [handle]);

  useEffect(refresh, [refresh]);

  async function send(display: PlayerDisplayInfo, command: Command) {
    if (!display.sessionID) return;
    try {
      await commandPlayerDisplay({ sessionID: display.sessionID, command });
      setNotice(t("remoteSent"));
      refresh();
    } catch (err) {
      setNotice(displayErrorText(err, display.displayName, t) ?? t("remoteSendError"));
    }
  }

  return (
    <AppShell actions={<ViewerActions />}>
      <div className="mydata mx-auto flex max-w-2xl flex-col gap-6">
        <header className="mydata-hero flex flex-col gap-1.5">
          <h1 className="bf-page-title">
            {lookup.kind === "found" ? lookup.display.displayName : t("playerDisplayPageTitle")}
          </h1>
        </header>
        <section className="mydata-panel bf-play-on-display">
          {lookup.kind === "loading" ? (
            <p className="mydata-muted" role="status">
              {t("playerDisplayLoading")}
            </p>
          ) : lookup.kind === "missing" ? (
            <p className="mydata-muted" role="alert">
              {t("playerDisplayNotAvailable")}
            </p>
          ) : (
            <DisplayCard display={lookup.display} send={send} />
          )}
          {notice && (
            <p className="bf-play-on-status" role="status">
              {notice}
            </p>
          )}
        </section>
      </div>
    </AppShell>
  );
}

function DisplayCard({
  display,
  send,
}: {
  display: PlayerDisplayInfo;
  send: (display: PlayerDisplayInfo, command: Command) => void;
}) {
  const t = useT();
  const item = display.currentItem;
  const offline = display.presence === "offline" || !display.sessionID;
  return (
    <>
      <p className="m-0">{displayPresenceText(display, t)}</p>
      {display.presence === "ready" && (
        <p className="mydata-muted m-0">{t("playerDisplayIdle", { name: display.displayName })}</p>
      )}
      {item && item.durationSeconds > 0 && (
        <p className="mydata-muted m-0">
          {t("playerDisplayPosition", {
            position: formatClockTime(item.positionSeconds),
            duration: formatClockTime(item.durationSeconds),
          })}
        </p>
      )}
      {!offline && display.presence === "playing" && (
        <div className="bf-play-on-display-controls">
          <Button variant="outline" size="sm" onClick={() => send(display, "pause")}>
            <Pause className="size-3.5" aria-hidden />
            {t("remotePause")}
          </Button>
          <Button variant="outline" size="sm" onClick={() => send(display, "resume")}>
            <Play className="size-3.5" aria-hidden />
            {t("remoteResume")}
          </Button>
          <Button variant="outline" size="sm" onClick={() => send(display, "stop")}>
            <Square className="size-3.5" aria-hidden />
            {t("remoteStop")}
          </Button>
          <Button variant="ghost" size="sm" onClick={() => send(display, "mute")}>
            <VolumeX className="size-3.5" aria-hidden />
            {t("remoteMute")}
          </Button>
          <Button variant="ghost" size="sm" onClick={() => send(display, "unmute")}>
            <Volume2 className="size-3.5" aria-hidden />
            {t("remoteUnmute")}
          </Button>
        </div>
      )}
    </>
  );
}
