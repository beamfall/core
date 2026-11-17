import { Bell } from "lucide-react";
import { useCallback, useEffect, useState } from "react";
import { useNavigate } from "react-router";
import { IconHeading } from "@/components/beamfall/workbench";
import { AppShell } from "@/components/chrome/AppShell";
import { Button } from "@/components/ui/button";
import { fetchNotifications, markNotificationRead } from "@/lib/graphql";
import type { NotificationInfo } from "@/lib/graphql/types";
import { useT } from "@/lib/i18n";
import { ViewerActions } from "../components/ViewerActions";
import {
  decidePartyRequest,
  getWatchParty,
  joinWatchParty,
  useWatchPartyState,
} from "../player/watchPartyStore";

const NOTIFICATION_INBOX_LIMIT = 100;
const NOTIFICATIONS_CHANGED_EVENT = "beamfall:notifications-changed";
/**
 * How often an open, visible inbox reads the server again. The server sends no
 * event when a notification arrives (the inbox is the polled fallback,
 * internal/push), so without this a new one shows only after a tab revisit
 * (CORE-2322).
 */
export const NOTIFICATION_INBOX_POLL_MS = 30_000;

type TFunction = ReturnType<typeof useT>;

// ADR-0039 fallback inbox: durable, reveal-filtered, per-Profile notifications
// for clients that do not grant push or return to the app later.
export function NotificationsPage() {
  const t = useT();
  const navigate = useNavigate();
  const [notifications, setNotifications] = useState<NotificationInfo[]>([]);
  const [joiningID, setJoiningID] = useState("");
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const refreshNotifications = useCallback(
    async (showLoading = false) => {
      setError(null);
      if (showLoading) setLoading(true);
      try {
        setNotifications(await fetchNotifications({ limit: NOTIFICATION_INBOX_LIMIT }));
      } catch {
        setError(t("notificationsError"));
      } finally {
        setLoading(false);
      }
    },
    [t],
  );

  useEffect(() => {
    void refreshNotifications(true);
  }, [refreshNotifications]);

  useEffect(() => {
    function refreshIfForeground() {
      if (document.visibilityState === "hidden") return;
      void refreshNotifications();
    }
    const poll = window.setInterval(refreshIfForeground, NOTIFICATION_INBOX_POLL_MS);
    window.addEventListener("focus", refreshIfForeground);
    document.addEventListener("visibilitychange", refreshIfForeground);
    return () => {
      window.clearInterval(poll);
      window.removeEventListener("focus", refreshIfForeground);
      document.removeEventListener("visibilitychange", refreshIfForeground);
    };
  }, [refreshNotifications]);

  async function onMarkRead(id: string) {
    setError(null);
    try {
      const result = await markNotificationRead(id);
      if (result.found) {
        setNotifications((rows) =>
          rows.map((row) => (row.id === id ? { ...row, read: true } : row)),
        );
        window.dispatchEvent(new Event(NOTIFICATIONS_CHANGED_EVENT));
        return;
      }
      await refreshNotifications();
    } catch {
      setError(t("notificationsMarkReadError"));
    }
  }

  // Join a Watch Party from its invite (CORE-1677): accept, then open the
  // player on the title the party is watching.
  async function onJoinParty(notification: NotificationInfo) {
    if (joiningID) return;
    setError(null);
    setJoiningID(notification.id);
    const item = await joinWatchParty(notification.ref);
    setJoiningID("");
    if (!item) {
      const party = getWatchParty();
      setError(
        party.error === "home-network"
          ? t("watchPartyHomeNetworkOnly")
          : t("watchPartyFailed", { reason: party.errorDetail }),
      );
      return;
    }
    if (!notification.read) void onMarkRead(notification.id);
    navigate(`/watch/${encodeURIComponent(item)}`);
  }

  // Approve or decline a member's request from its notification (CORE-1765).
  // Either way the request is answered, so the notification is marked read.
  async function onDecideRequest(notification: NotificationInfo, approve: boolean) {
    setError(null);
    const { requestID } = partyRequestRef(notification.ref);
    if (!(await decidePartyRequest(requestID, approve))) {
      setError(t("watchPartyFailed", { reason: getWatchParty().errorDetail }));
      return;
    }
    if (!notification.read) void onMarkRead(notification.id);
  }

  const unreadCount = notifications.filter((row) => !row.read).length;

  return (
    <AppShell actions={<ViewerActions />}>
      <div className="mx-auto flex max-w-3xl flex-col gap-6">
        <IconHeading icon={Bell} title={t("notificationsTitle")} lead={t("notificationsLead")} />

        <section aria-labelledby="notifications-inbox" className="flex flex-col gap-3">
          <div className="flex items-center justify-between gap-3">
            <div className="flex min-w-0 items-baseline gap-3">
              <h2 id="notifications-inbox" className="text-[17px] font-semibold">
                {t("notificationsInboxHeading")}
              </h2>
              <span className="text-sm text-[var(--text-muted)]">
                {t("notificationsUnreadCount", { count: unreadCount })}
              </span>
            </div>
            <Button variant="outline" size="sm" onClick={() => void refreshNotifications(true)}>
              {t("refresh")}
            </Button>
          </div>
          {error && (
            <p role="alert" className="text-sm text-[var(--state-danger)]">
              {error}
            </p>
          )}
          {loading ? (
            <p className="text-sm text-[var(--text-muted)]">{t("loading")}</p>
          ) : notifications.length === 0 ? (
            <div role="status" className="flex flex-col items-center gap-3 py-16 text-center">
              <Bell aria-hidden className="size-6 text-[var(--text-muted)]" />
              <p className="m-0 text-[15px] text-[var(--text-secondary)]">
                {t("notificationsEmpty")}
              </p>
            </div>
          ) : (
            <ul className="settings-group">
              {notifications.map((notification) => (
                <NotificationRow
                  key={notification.id}
                  notification={notification}
                  onMarkRead={onMarkRead}
                  joining={joiningID === notification.id}
                  onJoinParty={onJoinParty}
                  onDecideRequest={onDecideRequest}
                />
              ))}
            </ul>
          )}
        </section>
      </div>
    </AppShell>
  );
}

function NotificationRow({
  notification,
  onMarkRead,
  joining,
  onJoinParty,
  onDecideRequest,
}: {
  notification: NotificationInfo;
  onMarkRead: (id: string) => void;
  joining: boolean;
  onJoinParty: (notification: NotificationInfo) => void;
  onDecideRequest: (notification: NotificationInfo, approve: boolean) => void;
}) {
  const t = useT();
  const party = useWatchPartyState();
  // Requests filed before they had their own kind arrived as invites to the
  // host's own party; those are not invites.
  const joinable =
    notification.kind === "watch-party-invite" &&
    notification.ref !== "" &&
    !(party.isHost && party.partyID === notification.ref);
  // A request can be decided only while this viewer still hosts its party.
  const request =
    notification.kind === "watch-party-request" ? partyRequestRef(notification.ref) : null;
  const decidable =
    request !== null &&
    request.requestID !== "" &&
    party.status === "active" &&
    party.isHost &&
    party.partyID === request.partyID;
  return (
    <li className="settings-row flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
      <div className="flex min-w-0 gap-3">
        <span
          aria-hidden
          className={
            "mt-[7px] size-2 shrink-0 rounded-full " +
            (notification.read ? "bg-transparent" : "bg-[var(--accent)]")
          }
        />
        <div className="flex min-w-0 flex-col gap-1">
          <div className="flex flex-wrap items-center gap-x-2 text-xs text-[var(--text-muted)]">
            <span>{notificationKindLabel(notification.kind, t)}</span>
            {!notification.read && <span className="sr-only">{t("notificationsUnread")}</span>}
            <span aria-hidden>·</span>
            <span>{formatDateTime(notification.createdAt)}</span>
          </div>
          <span className="text-sm font-medium text-[var(--text-primary)]">
            {notification.title}
          </span>
          {notification.body && (
            <span className="text-sm text-[var(--text-secondary)]">{notification.body}</span>
          )}
        </div>
      </div>
      <div className="flex gap-2 self-start sm:self-center">
        {joinable && (
          <Button size="sm" disabled={joining} onClick={() => onJoinParty(notification)}>
            {joining ? t("watchPartyJoining") : t("watchPartyJoin")}
          </Button>
        )}
        {decidable && (
          <>
            <Button size="sm" onClick={() => onDecideRequest(notification, true)}>
              {t("watchPartyApprove")}
            </Button>
            <Button
              variant="outline"
              size="sm"
              onClick={() => onDecideRequest(notification, false)}
            >
              {t("watchPartyDecline")}
            </Button>
          </>
        )}
        {!notification.read && (
          <Button variant="outline" size="sm" onClick={() => onMarkRead(notification.id)}>
            {t("notificationsMarkRead")}
          </Button>
        )}
      </div>
    </li>
  );
}

function notificationKindLabel(kind: string, t: TFunction): string {
  switch (kind) {
    case "extension-request":
      return t("notificationKindExtensionRequest");
    case "watch-party-invite":
      return t("notificationKindWatchPartyInvite");
    case "watch-party-request":
      return t("notificationKindWatchPartyRequest");
    case "system":
      return t("notificationKindSystem");
    default:
      return kind || t("notificationKindSystem");
  }
}

/** Splits a watch-party-request ref, "<party id>:<request id>". */
function partyRequestRef(ref: string): { partyID: string; requestID: string } {
  const at = ref.lastIndexOf(":");
  return at < 0
    ? { partyID: ref, requestID: "" }
    : { partyID: ref.slice(0, at), requestID: ref.slice(at + 1) };
}

function formatDateTime(value: string): string {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return "";
  return date.toLocaleString();
}
