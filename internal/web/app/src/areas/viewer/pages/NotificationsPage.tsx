import { useCallback, useEffect, useState } from "react";
import { AppShell } from "@/components/chrome/AppShell";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
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

        <Card>
          <CardHeader className="flex flex-row items-center justify-between gap-3">
            <div className="flex min-w-0 items-center gap-3">
              <CardTitle>{t("notificationsInboxHeading")}</CardTitle>
              <Badge variant="outline">
                {t("notificationsUnreadCount", { count: unreadCount })}
              </Badge>
            </div>
            <Button variant="outline" size="sm" onClick={() => void refreshNotifications(true)}>
              {t("refresh")}
            </Button>
          </CardHeader>
          <CardContent className="flex flex-col gap-3">
            {error && <p className="text-sm text-[var(--red-500)]">{error}</p>}
            {loading ? (
              <p className="text-sm text-[var(--text-muted)]">{t("loading")}</p>
            ) : notifications.length === 0 ? (
              <p className="text-sm text-[var(--text-muted)]">{t("notificationsEmpty")}</p>
            ) : (
              <ul className="flex flex-col gap-2">
                {notifications.map((notification) => (
                  <NotificationRow
                    key={notification.id}
                    notification={notification}
                    onMarkRead={onMarkRead}
                  />
                ))}
              </ul>
            )}
          </CardContent>
        </Card>
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
    <li
      className={
        "flex flex-col gap-3 rounded-[var(--radius-control)] border px-3 py-3 sm:flex-row sm:items-center sm:justify-between " +
        (notification.read ? "border-[var(--border-subtle)]" : "border-[var(--accent)]")
      }
    >
      <div className="flex min-w-0 flex-col gap-1">
        <div className="flex flex-wrap items-center gap-2">
          <Badge variant="secondary">{notificationKindLabel(notification.kind, t)}</Badge>
          {!notification.read && <Badge variant="outline">{t("notificationsUnread")}</Badge>}
          <span className="text-xs text-[var(--text-muted)]">
            {formatDateTime(notification.createdAt)}
          </span>
        </div>
        <span className="text-sm font-medium text-[var(--text-primary)]">{notification.title}</span>
        {notification.body && (
          <span className="text-sm text-[var(--text-secondary)]">{notification.body}</span>
        )}
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
