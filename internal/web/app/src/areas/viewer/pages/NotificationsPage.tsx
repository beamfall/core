import { useCallback, useEffect, useState } from "react";
import { AppShell } from "@/components/chrome/AppShell";
import { Button } from "@/components/ui/button";
import { fetchNotifications, markNotificationRead } from "@/lib/graphql";
import type { NotificationInfo } from "@/lib/graphql/types";
import { useT } from "@/lib/i18n";
import { ViewerActions } from "../components/ViewerActions";

const NOTIFICATION_INBOX_LIMIT = 100;
const NOTIFICATIONS_CHANGED_EVENT = "beamfall:notifications-changed";

type TFunction = ReturnType<typeof useT>;

// ADR-0039 fallback inbox: durable, reveal-filtered, per-Profile notifications
// for clients that do not grant push or return to the app later.
export function NotificationsPage() {
  const t = useT();
  const [notifications, setNotifications] = useState<NotificationInfo[]>([]);
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
    window.addEventListener("focus", refreshIfForeground);
    document.addEventListener("visibilitychange", refreshIfForeground);
    return () => {
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

  const unreadCount = notifications.filter((row) => !row.read).length;

  return (
    <AppShell actions={<ViewerActions />}>
      <div className="mx-auto flex max-w-3xl flex-col gap-6">
        <header className="flex flex-col gap-2">
          <h1 className="bf-page-title">{t("notificationsTitle")}</h1>
          <p className="bf-lead">{t("notificationsLead")}</p>
        </header>

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
            <p className="text-sm text-[var(--text-muted)]">{t("notificationsEmpty")}</p>
          ) : (
            <ul className="settings-group">
              {notifications.map((notification) => (
                <NotificationRow
                  key={notification.id}
                  notification={notification}
                  onMarkRead={onMarkRead}
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
}: {
  notification: NotificationInfo;
  onMarkRead: (id: string) => void;
}) {
  const t = useT();
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
      {!notification.read && (
        <Button
          variant="outline"
          size="sm"
          className="self-start sm:self-center"
          onClick={() => onMarkRead(notification.id)}
        >
          {t("notificationsMarkRead")}
        </Button>
      )}
    </li>
  );
}

function notificationKindLabel(kind: string, t: TFunction): string {
  switch (kind) {
    case "extension-request":
      return t("notificationKindExtensionRequest");
    case "watch-party-invite":
      return t("notificationKindWatchPartyInvite");
    case "system":
      return t("notificationKindSystem");
    default:
      return kind || t("notificationKindSystem");
  }
}

function formatDateTime(value: string): string {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return "";
  return date.toLocaleString();
}
