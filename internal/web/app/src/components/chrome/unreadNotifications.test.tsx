import { act, cleanup, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { fetchNotifications } from "@/lib/graphql";
import type { NotificationInfo } from "@/lib/graphql/types";
import {
  resetUnreadNotificationsForTest,
  UNREAD_NOTIFICATIONS_STALE_MS,
  useUnreadNotifications,
} from "./unreadNotifications";

vi.mock("@/lib/graphql", () => ({ fetchNotifications: vi.fn() }));

const rows = (count: number) =>
  Array.from({ length: count }, (_, index) => ({ id: String(index) })) as NotificationInfo[];

function Bell({ scope }: { scope: string | null }) {
  return <output>{useUnreadNotifications(scope)}</output>;
}

const count = () => screen.getByRole("status").textContent;

describe("useUnreadNotifications (CORE-1164)", () => {
  beforeEach(() => {
    resetUnreadNotificationsForTest();
    vi.mocked(fetchNotifications).mockReset().mockResolvedValue(rows(3));
  });

  afterEach(() => {
    cleanup();
    vi.useRealTimers();
  });

  it("fetches once and serves a remount from the cache at first paint", async () => {
    const first = render(<Bell scope="p1:locked" />);
    await waitFor(() => expect(count()).toBe("3"));
    first.unmount();

    render(<Bell scope="p1:locked" />);
    expect(count()).toBe("3");
    expect(fetchNotifications).toHaveBeenCalledTimes(1);
  });

  it("shares one request between shells mounted together", async () => {
    render(
      <>
        <Bell scope="p1:locked" />
        <Bell scope="p1:locked" />
      </>,
    );
    await waitFor(() => expect(screen.getAllByRole("status")[1]?.textContent).toBe("3"));
    expect(fetchNotifications).toHaveBeenCalledTimes(1);
  });

  it("revalidates on remount and on refocus only once the count is stale", async () => {
    vi.useFakeTimers({ toFake: ["Date"] });
    const first = render(<Bell scope="p1:locked" />);
    await waitFor(() => expect(count()).toBe("3"));
    act(() => void window.dispatchEvent(new Event("focus")));
    expect(fetchNotifications).toHaveBeenCalledTimes(1);
    first.unmount();

    vi.setSystemTime(Date.now() + UNREAD_NOTIFICATIONS_STALE_MS + 1);
    vi.mocked(fetchNotifications).mockResolvedValue(rows(5));
    render(<Bell scope="p1:locked" />);
    expect(count()).toBe("3");
    await waitFor(() => expect(count()).toBe("5"));
    expect(fetchNotifications).toHaveBeenCalledTimes(2);

    vi.setSystemTime(Date.now() + UNREAD_NOTIFICATIONS_STALE_MS + 1);
    vi.mocked(fetchNotifications).mockResolvedValue(rows(6));
    act(() => void window.dispatchEvent(new Event("focus")));
    await waitFor(() => expect(count()).toBe("6"));
  });

  it("refetches at once when the inbox reports a change", async () => {
    render(<Bell scope="p1:locked" />);
    await waitFor(() => expect(count()).toBe("3"));

    vi.mocked(fetchNotifications).mockResolvedValue(rows(2));
    act(() => void window.dispatchEvent(new Event("beamfall:notifications-changed")));

    await waitFor(() => expect(count()).toBe("2"));
    expect(fetchNotifications).toHaveBeenCalledTimes(2);
  });

  it("never shows one scope's count to another", async () => {
    const view = render(<Bell scope="p1:locked" />);
    await waitFor(() => expect(count()).toBe("3"));

    vi.mocked(fetchNotifications).mockResolvedValue(rows(1));
    view.rerender(<Bell scope="p2:locked" />);
    expect(count()).toBe("0");
    await waitFor(() => expect(count()).toBe("1"));

    view.rerender(<Bell scope={null} />);
    expect(count()).toBe("0");
    expect(fetchNotifications).toHaveBeenCalledTimes(2);
  });

  it("keeps the last known count when a refresh fails", async () => {
    render(<Bell scope="p1:locked" />);
    await waitFor(() => expect(count()).toBe("3"));

    vi.mocked(fetchNotifications).mockRejectedValue(new Error("offline"));
    act(() => void window.dispatchEvent(new Event("beamfall:notifications-changed")));
    await waitFor(() => expect(fetchNotifications).toHaveBeenCalledTimes(2));

    expect(count()).toBe("3");
  });
});
