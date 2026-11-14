import "@testing-library/jest-dom/vitest";
import { act, cleanup, render, renderHook, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactNode } from "react";
import { MemoryRouter, useLocation } from "react-router";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { fetchMySessions } from "@/lib/graphql";
import { I18nProvider } from "@/lib/i18n";
import { RemoteMessage, useRemoteReceiver } from "./RemoteReceiver";
import { resetAppliedControlEvents, useRemotePlayerTarget } from "./remoteControl";

vi.mock("@/lib/graphql", () => ({ fetchMySessions: vi.fn() }));

function control(target: string, payload: Record<string, unknown>, lastEventId = "") {
  return new MessageEvent("session.control", {
    lastEventId,
    data: JSON.stringify({
      kind: "session.control",
      aggregateType: "session",
      aggregateID: target,
      occurredAt: new Date().toISOString(),
      payload,
    }),
  });
}

let path = "";
function LocationProbe() {
  const location = useLocation();
  path = location.pathname + location.search;
  return null;
}

function wrapper({ children }: { children: ReactNode }) {
  return (
    <MemoryRouter initialEntries={["/"]}>
      <LocationProbe />
      {children}
    </MemoryRouter>
  );
}

beforeEach(() => {
  path = "";
  resetAppliedControlEvents();
  vi.mocked(fetchMySessions)
    .mockReset()
    .mockResolvedValue({
      sessions: [
        {
          id: "session-a",
          deviceID: "d1",
          deviceName: "Laptop",
          deviceRole: "viewer",
          current: true,
        },
        {
          id: "session-b",
          deviceID: "d2",
          deviceName: "Lounge",
          deviceRole: "viewer",
          current: false,
        },
      ],
      streamTokens: [],
    });
});

afterEach(cleanup);

describe("useRemoteReceiver", () => {
  it("applies only commands addressed to its own session", async () => {
    const pause = vi.fn();
    const video = { pause } as unknown as HTMLVideoElement;
    renderHook(() => useRemotePlayerTarget({ video: () => video }), { wrapper });
    const { result } = renderHook(() => useRemoteReceiver("p1"), { wrapper });

    act(() => result.current.onEvent(control("session-b", { command: "pause" })));
    act(() => result.current.onEvent(new MessageEvent("client.invalidate", { data: "{}" })));
    await waitFor(() => expect(fetchMySessions).toHaveBeenCalledOnce());
    expect(pause).not.toHaveBeenCalled();

    act(() => result.current.onEvent(control("session-a", { command: "pause" })));
    await waitFor(() => expect(pause).toHaveBeenCalledOnce());
    // The own session id is looked up once.
    expect(fetchMySessions).toHaveBeenCalledOnce();
  });

  it("opens the player for play-media at the requested position", async () => {
    const { result } = renderHook(() => useRemoteReceiver("p1"), { wrapper });
    act(() =>
      result.current.onEvent(
        control("session-a", { command: "play-media", mediaID: "m/1", positionMs: 5_500 }),
      ),
    );
    await waitFor(() => expect(path).toBe("/watch/m%2F1?t=5"));
  });

  it("starts from an explicit zero instead of saved progress", async () => {
    const { result } = renderHook(() => useRemoteReceiver("p1"), { wrapper });
    act(() =>
      result.current.onEvent(
        control("session-a", { command: "play-media", mediaID: "m1", positionMs: 0 }),
      ),
    );
    await waitFor(() => expect(path).toBe("/watch/m1?t=0"));
  });

  it("restarts in place when the player is already on that Media", async () => {
    const play = vi.fn(() => Promise.resolve());
    const video = { play, currentTime: 120 } as unknown as HTMLVideoElement;
    renderHook(() => useRemotePlayerTarget({ video: () => video, mediaID: "m1" }), { wrapper });
    const { result } = renderHook(() => useRemoteReceiver("p1"), { wrapper });
    act(() =>
      result.current.onEvent(
        control("session-a", { command: "play-media", mediaID: "m1", positionMs: 5_000 }),
      ),
    );
    await waitFor(() => expect(play).toHaveBeenCalledOnce());
    expect(video.currentTime).toBe(5);
    expect(path).toBe("/");
  });

  it("does not replay an event it already applied", async () => {
    const first = renderHook(() => useRemoteReceiver("p1"), { wrapper });
    act(() =>
      first.result.current.onEvent(
        control("session-a", { command: "display-message", text: "Once" }, "7"),
      ),
    );
    await waitFor(() => expect(first.result.current.message).toBe("Once"));
    first.unmount();

    // A remounted viewer is sent the same event again: it is dropped.
    const second = renderHook(() => useRemoteReceiver("p1"), { wrapper });
    act(() =>
      second.result.current.onEvent(
        control("session-a", { command: "display-message", text: "Once" }, "7"),
      ),
    );
    await waitFor(() => expect(fetchMySessions).toHaveBeenCalledTimes(2));
    await act(async () => {
      await Promise.resolve();
    });
    expect(second.result.current.message).toBeNull();

    act(() =>
      second.result.current.onEvent(
        control("session-a", { command: "display-message", text: "Next" }, "8"),
      ),
    );
    await waitFor(() => expect(second.result.current.message).toBe("Next"));
  });

  it("shows a display-message until it is dismissed", async () => {
    const { result } = renderHook(() => useRemoteReceiver("p1"), { wrapper });
    act(() =>
      result.current.onEvent(control("session-a", { command: "display-message", text: "Dinner" })),
    );
    await waitFor(() => expect(result.current.message).toBe("Dinner"));
    act(() => result.current.dismissMessage());
    expect(result.current.message).toBeNull();
  });

  it("retries the session lookup after a failure", async () => {
    vi.mocked(fetchMySessions).mockRejectedValueOnce(new Error("offline"));
    const { result } = renderHook(() => useRemoteReceiver("p1"), { wrapper });
    act(() =>
      result.current.onEvent(control("session-a", { command: "display-message", text: "One" })),
    );
    await waitFor(() => expect(fetchMySessions).toHaveBeenCalledOnce());
    act(() =>
      result.current.onEvent(control("session-a", { command: "display-message", text: "Two" })),
    );
    await waitFor(() => expect(result.current.message).toBe("Two"));
  });
});

describe("RemoteMessage", () => {
  it("renders the message as a status with a dismiss control", async () => {
    const onDismiss = vi.fn();
    render(
      <I18nProvider>
        <RemoteMessage message="Dinner" onDismiss={onDismiss} />
      </I18nProvider>,
    );
    expect(screen.getByRole("status")).toHaveTextContent("Message from another device");
    expect(screen.getByRole("status")).toHaveTextContent("Dinner");
    await userEvent.click(screen.getByRole("button", { name: "Dismiss" }));
    expect(onDismiss).toHaveBeenCalledOnce();
  });

  it("renders nothing without a message", () => {
    const { container } = render(
      <I18nProvider>
        <RemoteMessage message={null} onDismiss={() => {}} />
      </I18nProvider>,
    );
    expect(container).toBeEmptyDOMElement();
  });
});
