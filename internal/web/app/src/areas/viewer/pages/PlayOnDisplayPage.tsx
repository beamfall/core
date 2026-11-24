import { Pause, Play, Square, Volume2, VolumeX } from "lucide-react";
import { useCallback, useEffect, useRef, useState } from "react";
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

const REVALIDATE_MS = 15_000;

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
  // Each lookup gets a number; only the latest may set state, so a slow
  // answer for an earlier handle never lands under the current URL.
  const latest = useRef(0);
  // Separate counters for the handle's lifetime and for commands: a lookup
  // finishing never voids a command's outcome, and only the newest command's
  // outcome becomes the notice.
  const generation = useRef(0);
  const lastCommand = useRef(0);

  const refresh = useCallback(() => {
    const request = ++latest.current;
    const settle = (next: Lookup) => {
      if (request !== latest.current) return;
      setLookup(next);
      if (next.kind === "missing") {
        // A pending command's outcome would name a display that's gone.
        lastCommand.current++;
        setNotice(null);
      }
    };
    if (!handle) {
      settle({ kind: "missing" });
      return Promise.resolve();
    }
    return fetchPlayerDisplays()
      .then((displays) => {
        const display = displays.find((d) => d.handle === handle);
        settle(display ? { kind: "found", display } : { kind: "missing" });
      })
      .catch(() => settle({ kind: "missing" }));
  }, [handle]);

  useEffect(() => {
    setLookup({ kind: "loading" });
    setNotice(null);
    // Revalidate while the page is open, so a display revoked or unbound
    // elsewhere drops to "not available" even when nobody presses a button.
    // The next poll waits for the last to settle, so a slow server never has
    // each answer voided by the poll after it.
    let timer = 0;
    let live = true;
    const lookUp = () => {
      void refresh().finally(() => {
        if (live) timer = window.setTimeout(lookUp, REVALIDATE_MS);
      });
    };
    lookUp();
    return () => {
      live = false;
      window.clearTimeout(timer);
      latest.current++;
      generation.current++;
    };
  }, [refresh]);

  async function send(display: PlayerDisplayInfo, command: Command) {
    if (!display.sessionID) return;
    const gen = generation.current;
    const id = ++lastCommand.current;
    const current = () => gen === generation.current && id === lastCommand.current;
    try {
      await commandPlayerDisplay({ sessionID: display.sessionID, command });
      if (current()) setNotice(t("remoteSent"));
    } catch (err) {
      if (current())
        setNotice(displayErrorText(err, display.displayName, t) ?? t("remoteSendError"));
    }
    // Re-resolve after every command: a revoked controller or a display that
    // went away drops to "not available" instead of keeping its card. Skip it
    // when the page has moved on to another handle since the command left.
    if (gen === generation.current) refresh();
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
      {!offline && display.presence !== "playing" && display.inParty && (
        <div className="bf-play-on-display-controls">
          <Button variant="outline" size="sm" onClick={() => send(display, "stop")}>
            <Square className="size-3.5" aria-hidden />
            {t("remoteStop")}
          </Button>
        </div>
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
