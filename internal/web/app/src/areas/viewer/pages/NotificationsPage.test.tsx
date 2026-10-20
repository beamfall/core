import type { ReactNode } from "react";
import "@testing-library/jest-dom/vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { fetchNotifications, markNotificationRead } from "@/lib/graphql";
import { I18nProvider } from "@/lib/i18n";
import { NotificationsPage } from "./NotificationsPage";

vi.mock("@/components/chrome/AppShell", () => ({
  AppShell: ({ children }: { children: ReactNode }) => <>{children}</>,
}));

vi.mock("../components/ViewerActions", () => ({
  ViewerActions: () => null,
}));

vi.mock("@/lib/graphql", () => ({
  fetchNotifications: vi.fn(),
  markNotificationRead: vi.fn(),
}));

const unreadNotification = {
  id: "notification-1",
  kind: "extension-request",
  title: "More time requested",
  body: "A profile requested more time.",
  ref: "request-1",
  createdAt: "2026-06-17T12:00:00Z",
  read: false,
};

beforeEach(() => {
  vi.mocked(fetchNotifications).mockReset();
  vi.mocked(markNotificationRead).mockReset();
  vi.mocked(fetchNotifications).mockResolvedValue([unreadNotification]);
  vi.mocked(markNotificationRead).mockResolvedValue({
    id: "notification-1",
    found: true,
  });
});

describe("NotificationsPage", () => {
  it("renders the inbox and unread count from the notification query", async () => {
    renderNotificationsPage();

    expect(await screen.findByText("More time requested")).toBeVisible();
    expect(screen.getByText("Extension request")).toBeVisible();
    expect(screen.getByText("1 unread")).toBeVisible();
    expect(fetchNotifications).toHaveBeenCalledWith({ limit: 100 });
  });

  it("marks an unread notification read in place", async () => {
    const user = userEvent.setup();
    renderNotificationsPage();

    await user.click(await screen.findByRole("button", { name: "Mark read" }));

    expect(markNotificationRead).toHaveBeenCalledWith("notification-1");
    await waitFor(() => {
      expect(screen.queryByRole("button", { name: "Mark read" })).toBeNull();
    });
    expect(screen.getByText("0 unread")).toBeVisible();
  });

  it("refreshes when the browser returns to the foreground", async () => {
    renderNotificationsPage();
    await screen.findByText("More time requested");

    fireEvent.focus(window);

    await waitFor(() => {
      expect(fetchNotifications).toHaveBeenCalledTimes(2);
    });
  });
});

function renderNotificationsPage() {
  return render(
    <I18nProvider>
      <NotificationsPage />
    </I18nProvider>,
  );
}
