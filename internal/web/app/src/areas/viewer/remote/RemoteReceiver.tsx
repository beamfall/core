import { X } from "lucide-react";
import { useCallback, useEffect, useRef, useState } from "react";
import { useNavigate } from "react-router";
import { fetchMySessions } from "@/lib/graphql";
import { useT } from "@/lib/i18n";
import {
  claimControlEvent,
  deliverPlayerCommand,
  parseSessionControlFrame,
  remotePlayerTarget,
  restartInPlace,
  SESSION_CONTROL_KIND,
} from "./remoteControl";
import "./remote.css";

const MESSAGE_SECONDS = 10;

interface RemoteReceiver {
  /** Feed every viewer SSE frame here; non-control frames are ignored. */
  onEvent: (event: MessageEvent) => void;
  message: string | null;
  dismissMessage: () => void;
}

/**
 * Applies `session.control` commands addressed to this tab's session. The own
 * session id is looked up on the first command, not at mount, so a viewer that
 * is never controlled makes no extra request.
 */
export function useRemoteReceiver(profileID: string): RemoteReceiver {
  const navigate = useNavigate();
  const [message, setMessage] = useState<string | null>(null);
  const ownSession = useRef<Promise<string> | null>(null);

  // Picking another Profile re-resolves the session and drops its message.
  useEffect(() => {
    ownSession.current = null;
    setMessage(null);
  }, [profileID]);

  const resolveOwnSession = useCallback(() => {
    if (!ownSession.current) {
      ownSession.current = fetchMySessions().then(
        (info) => info.sessions.find((session) => session.current)?.id ?? "",
      );
      // A failed lookup is retried on the next command.
      ownSession.current.catch(() => {
        ownSession.current = null;
      });
    }
    return ownSession.current;
  }, []);

  const onEvent = useCallback(
    (event: MessageEvent) => {
      if (event.type !== SESSION_CONTROL_KIND) return;
      const frame = parseSessionControlFrame(event.data, Date.now());
      if (!frame) return;
      void resolveOwnSession()
        .then((own) => {
          if (
            !own ||
            frame.target !== own ||
            !claimControlEvent(event.lastEventId, frame.occurredAt)
          )
            return;
          const { command } = frame;
          const player = remotePlayerTarget();
          if (command.command === "play-media" && command.mediaID) {
            if (player && restartInPlace(command, player)) return;
            // An explicit position, zero included, overrides saved progress.
            const at =
              command.positionMs === undefined
                ? ""
                : `?t=${Math.max(0, Math.floor(command.positionMs / 1000))}`;
            navigate(`/watch/${encodeURIComponent(command.mediaID)}${at}`);
          } else if (command.command === "display-message" && command.text) {
            setMessage(command.text);
          } else {
            deliverPlayerCommand(command);
          }
        })
        .catch(() => {});
    },
    [navigate, resolveOwnSession],
  );

  useEffect(() => {
    if (message === null) return;
    const timer = window.setTimeout(() => setMessage(null), MESSAGE_SECONDS * 1000);
    return () => window.clearTimeout(timer);
  }, [message]);

  const dismissMessage = useCallback(() => setMessage(null), []);
  return { onEvent, message, dismissMessage };
}

/** A message another device sent with `display-message`; it clears itself. */
export function RemoteMessage({
  message,
  onDismiss,
}: {
  message: string | null;
  onDismiss: () => void;
}) {
  return message === null ? null : <RemoteMessageToast message={message} onDismiss={onDismiss} />;
}

function RemoteMessageToast({ message, onDismiss }: { message: string; onDismiss: () => void }) {
  const t = useT();
  return (
    <div className="bf-remote-message" role="status">
      <div className="min-w-0 flex-1">
        <p className="bf-remote-message-label">{t("remoteMessageFrom")}</p>
        <p className="bf-remote-message-text">{message}</p>
      </div>
      <button
        type="button"
        className="bf-remote-message-dismiss"
        aria-label={t("remoteMessageDismiss")}
        onClick={onDismiss}
      >
        <X aria-hidden />
      </button>
    </div>
  );
}
