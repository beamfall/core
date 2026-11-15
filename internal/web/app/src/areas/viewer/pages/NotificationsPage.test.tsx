import type { ReactNode } from "react";
import "@testing-library/jest-dom/vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes, useParams } from "react-router";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { fetchNotifications, markNotificationRead } from "@/lib/graphql";
import { I18nProvider } from "@/lib/i18n";
import {
  decidePartyRequest,
  getWatchParty,
  joinWatchParty,
  type WatchPartyState,
} from "../player/watchPartyStore";
import { NotificationsPage } from "./NotificationsPage";

vi.mock("@/components/chrome/AppShell", () => ({
  AppShell: ({ children }: { children: ReactNode }) => <>{children}</>,
}));

vi.mock("../components/ViewerActions", () => ({
  ViewerActions: () => null,
}));

vi.mock("../player/watchPartyStore", () => ({
  getWatchParty: vi.fn(),
  joinWatchParty: vi.fn(),
  decidePartyRequest: vi.fn(),
  useWatchPartyState: () => vi.mocked(getWatchParty)(),
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

const inviteNotification = {
  id: "notification-2",
  kind: "watch-party-invite",
  title: "Join the party",
  body: "A profile invited you to a Watch Party.",
  ref: "party-1",
  createdAt: "2026-06-17T12:05:00Z",
  read: false,
};

const idleParty: WatchPartyState = {
  status: "idle",
  partyID: "",
  isHost: false,
  timeline: null,
  members: [],
  invited: [],
  offsetMS: 0,
  error: "",
  errorDetail: "",
  notice: "",
  noticeName: "",
  requests: [],
};

const requestNotification = {
  id: "notification-3",
  kind: "watch-party-request",
  title: "Watch Party seek request",
  body: "Guest",
  ref: "party-1:req-9",
  createdAt: "2026-06-17T12:10:00Z",
  read: false,
};

const hostingParty: WatchPartyState = {
  ...idleParty,
  status: "active",
  partyID: "party-1",
  isHost: true,
};

beforeEach(() => {
  vi.mocked(getWatchParty).mockReset();
  vi.mocked(joinWatchParty).mockReset();
  vi.mocked(decidePartyRequest).mockReset();
  vi.mocked(getWatchParty).mockReturnValue(idleParty);
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

  it("offers Join only on a Watch Party invite", async () => {
    vi.mocked(fetchNotifications).mockResolvedValue([unreadNotification, inviteNotification]);
    renderNotificationsPage();
    await screen.findByText("Join the party");

    expect(screen.getAllByRole("button", { name: "Join" })).toHaveLength(1);
  });

  it("joins a Watch Party from its invite and opens the player on the party's title (CORE-1677)", async () => {
    const user = userEvent.setup();
    vi.mocked(fetchNotifications).mockResolvedValue([inviteNotification]);
    vi.mocked(joinWatchParty).mockResolvedValue("media-7");
    renderNotificationsPage();

    await user.click(await screen.findByRole("button", { name: "Join" }));

    expect(joinWatchParty).toHaveBeenCalledWith("party-1");
    expect(await screen.findByText("player media-7")).toBeVisible();
    expect(markNotificationRead).toHaveBeenCalledWith("notification-2");
  });

  it("says Watch Party is home-network only when the join is refused", async () => {
    const user = userEvent.setup();
    vi.mocked(fetchNotifications).mockResolvedValue([inviteNotification]);
    vi.mocked(joinWatchParty).mockResolvedValue(null);
    vi.mocked(getWatchParty).mockReturnValue({ ...idleParty, error: "home-network" });
    renderNotificationsPage();

    await user.click(await screen.findByRole("button", { name: "Join" }));

    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Watch Party works on your home network only.",
    );
    expect(screen.queryByText(/^player /)).toBeNull();
  });

  it("does not offer a host Join on its own party's requests", async () => {
    vi.mocked(fetchNotifications).mockResolvedValue([inviteNotification]);
    vi.mocked(getWatchParty).mockReturnValue({
      ...idleParty,
      status: "active",
      partyID: "party-1",
      isHost: true,
    });
    renderNotificationsPage();
    await screen.findByText("Join the party");

    expect(screen.queryByRole("button", { name: "Join" })).toBeNull();
  });

  it("labels a request with its own kind and lets the hosting viewer approve it (CORE-1765)", async () => {
    const user = userEvent.setup();
    vi.mocked(fetchNotifications).mockResolvedValue([requestNotification]);
    vi.mocked(getWatchParty).mockReturnValue(hostingParty);
    vi.mocked(decidePartyRequest).mockResolvedValue(true);
    vi.mocked(markNotificationRead).mockResolvedValue({ id: "notification-3", found: true });
    renderNotificationsPage();

    expect(await screen.findByText("Watch Party request")).toBeVisible();
    expect(screen.queryByRole("button", { name: "Join" })).toBeNull();
    await user.click(screen.getByRole("button", { name: "Approve" }));

    expect(decidePartyRequest).toHaveBeenCalledWith("req-9", true);
    await waitFor(() => expect(markNotificationRead).toHaveBeenCalledWith("notification-3"));
  });

  it("declines a request from its notification", async () => {
    const user = userEvent.setup();
    vi.mocked(fetchNotifications).mockResolvedValue([requestNotification]);
    vi.mocked(getWatchParty).mockReturnValue(hostingParty);
    vi.mocked(decidePartyRequest).mockResolvedValue(true);
    renderNotificationsPage();

    await user.click(await screen.findByRole("button", { name: "Decline" }));
    expect(decidePartyRequest).toHaveBeenCalledWith("req-9", false);
  });

  it("offers no decision once this viewer no longer hosts the party", async () => {
    vi.mocked(fetchNotifications).mockResolvedValue([requestNotification]);
    vi.mocked(getWatchParty).mockReturnValue({ ...hostingParty, isHost: false });
    renderNotificationsPage();
    await screen.findByText("Watch Party seek request");

    expect(screen.queryByRole("button", { name: "Approve" })).toBeNull();
  });
});

function WatchStub() {
  const { id } = useParams();
  return <p>player {id}</p>;
}

function renderNotificationsPage() {
  return render(
    <MemoryRouter initialEntries={["/notifications"]}>
      <I18nProvider>
        <Routes>
          <Route path="/notifications" element={<NotificationsPage />} />
          <Route path="/watch/:id" element={<WatchStub />} />
        </Routes>
      </I18nProvider>
    </MemoryRouter>,
  );
}
