import "@testing-library/jest-dom/vitest";
import { act, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { endAdminSession, fetchAdminSession, verifyAdminSession } from "@/lib/graphql";
import { I18nProvider } from "@/lib/i18n";
import { useSession } from "@/lib/session";
import { AdminGate } from "./AdminGate";
import { getAdminEpochSignal } from "./adminEpoch";
import { useAsync } from "./useAsync";

const postUnmountWrites = vi.hoisted(() => ({ armed: false, count: 0 }));

vi.mock("react", async (importOriginal) => {
  const react = await importOriginal<typeof import("react")>();
  return {
    ...react,
    useState<T>(initial: T | (() => T)) {
      const [value, setValue] = react.useState(initial);
      const trackedSetValue: typeof setValue = (next) => {
        if (postUnmountWrites.armed) postUnmountWrites.count++;
        setValue(next);
      };
      return [value, trackedSetValue] as const;
    },
  };
});

vi.mock("@/lib/graphql", () => ({
  fetchAdminSession: vi.fn(),
  verifyAdminSession: vi.fn(),
  endAdminSession: vi.fn(),
}));
vi.mock("@/lib/session", () => ({ useSession: vi.fn() }));

function setSession(profile: "adult" | "managed", profileID = "p1") {
  vi.mocked(useSession).mockReturnValue({
    session: { deviceRole: "manager", profileID, profile, revealed: false },
  } as unknown as ReturnType<typeof useSession>);
}

beforeEach(() => {
  postUnmountWrites.armed = false;
  postUnmountWrites.count = 0;
  vi.mocked(fetchAdminSession).mockReset();
  vi.mocked(verifyAdminSession).mockReset();
  vi.mocked(endAdminSession).mockReset();
  vi.mocked(fetchAdminSession).mockResolvedValue({ active: false });
  vi.mocked(endAdminSession).mockResolvedValue({ active: false });
  setSession("adult");
  delete window.beamfallHost;
});

afterEach(() => {
  vi.useRealTimers();
  delete window.beamfallHost;
});

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (err: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

function renderGate() {
  return render(
    <I18nProvider>
      <AdminGate>
        <div>UNLOCKED CONTENT</div>
      </AdminGate>
    </I18nProvider>,
  );
}

function gateNode() {
  return (
    <I18nProvider>
      <AdminGate>
        <div>UNLOCKED CONTENT</div>
      </AdminGate>
    </I18nProvider>
  );
}

describe("AdminGate reason-coded locks", () => {
  it("shows the not-adult reason with NO password form for a managed Profile", async () => {
    setSession("managed");
    renderGate();
    expect(await screen.findByText("Console unavailable")).toBeInTheDocument();
    expect(screen.queryByLabelText("Admin password")).toBeNull();
    // The Admin/Reveal distinction is always stated.
    expect(screen.getByText(/separate from Reveal/i)).toBeInTheDocument();
  });

  it("shows the no-session reason with the re-auth form when locked", async () => {
    renderGate();
    expect(await screen.findByText("Console locked")).toBeInTheDocument();
    expect(screen.getByLabelText("Admin password")).toBeInTheDocument();
  });

  it("keeps Console locked until the server Admin Session is active", async () => {
    Object.defineProperty(window, "beamfallHost", {
      configurable: true,
      value: {
        kind: "apple-manager",
        platform: "macos",
        clientClass: "apple-manager-macos-direct",
        takeInitialMessages: () => [
          { name: "session.handoff", payload: { sessionID: "native-session" } },
          {
            name: "state.profileRevealMode",
            payload: { profileID: "p1", revealed: true, mode: "manager" },
          },
        ],
      },
    });

    renderGate();

    expect(await screen.findByText("Console locked")).toBeInTheDocument();
    expect(screen.queryByText("UNLOCKED CONTENT")).toBeNull();
    expect(verifyAdminSession).not.toHaveBeenCalled();
  });

  it("unlocks on a successful verify and renders the Console", async () => {
    vi.mocked(verifyAdminSession).mockResolvedValue({
      active: true,
      expiresAt: "2999-01-01T00:00:00Z",
    });
    const user = userEvent.setup();
    renderGate();
    await user.type(await screen.findByLabelText("Admin password"), "pw");
    await user.click(screen.getByRole("button", { name: "Unlock Console" }));
    expect(await screen.findByText("UNLOCKED CONTENT")).toBeInTheDocument();
  });

  it("uses the initial adminSession expiry after a page reload", async () => {
    vi.mocked(fetchAdminSession).mockResolvedValue({
      active: true,
      expiresAt: "2000-01-01T00:00:00Z",
    });
    renderGate();
    expect(await screen.findByText("Admin session expired")).toBeInTheDocument();
    expect(screen.queryByText("UNLOCKED CONTENT")).toBeNull();
  });

  it("locks and clears Console content when an active Admin Session expires", async () => {
    vi.useFakeTimers();
    vi.mocked(fetchAdminSession).mockResolvedValue({
      active: true,
      expiresAt: new Date(Date.now() + 1_000).toISOString(),
    });
    renderGate();
    await act(async () => {});
    expect(screen.getByText("UNLOCKED CONTENT")).toBeInTheDocument();

    act(() => vi.advanceTimersByTime(1_000));

    expect(screen.getByText("Admin session expired")).toBeInTheDocument();
    expect(screen.queryByText("UNLOCKED CONTENT")).toBeNull();
  });

  // The Admin-Session epoch reaches the COMMON Console fetch path, not just the
  // gate's own request: an expiry must abort the transport of the Console
  // screens rendered under it, whose fetchers never see the gate at all.
  it("aborts a Console screen's in-flight fetch when the Admin Session expires", async () => {
    vi.useFakeTimers();
    vi.mocked(fetchAdminSession).mockResolvedValue({
      active: true,
      expiresAt: new Date(Date.now() + 1_000).toISOString(),
    });
    const pending = deferred<string>();
    const fetcher = vi
      .fn<(signal?: AbortSignal) => Promise<string>>()
      .mockReturnValue(pending.promise);
    function ConsoleScreen() {
      const { data } = useAsync(fetcher);
      return <div>{data ?? "CONSOLE ROWS PENDING"}</div>;
    }
    render(
      <I18nProvider>
        <AdminGate>
          <ConsoleScreen />
        </AdminGate>
      </I18nProvider>,
    );
    await act(async () => {});
    expect(screen.getByText("CONSOLE ROWS PENDING")).toBeInTheDocument();
    // The published epoch is the load-bearing assertion. The child's own signal
    // would also abort on unmount, so asserting only that would keep passing
    // with no epoch wiring at all.
    const epoch = getAdminEpochSignal();
    expect(epoch).toBeInstanceOf(AbortSignal);
    expect(epoch?.aborted).toBe(false);
    const signal = fetcher.mock.calls[0]?.[0];
    expect(signal).toBeInstanceOf(AbortSignal);
    expect(signal).not.toBe(epoch);
    expect(signal?.aborted).toBe(false);

    act(() => vi.advanceTimersByTime(1_000));

    expect(epoch?.aborted).toBe(true);
    expect(signal?.aborted).toBe(true);
    expect(screen.getByText("Admin session expired")).toBeInTheDocument();
  });

  it("clears Console content before the lock mutation settles", async () => {
    const pending = deferred<{ active: boolean }>();
    vi.mocked(fetchAdminSession).mockResolvedValue({
      active: true,
      expiresAt: "2999-01-01T00:00:00Z",
    });
    vi.mocked(endAdminSession).mockReturnValueOnce(pending.promise);
    const user = userEvent.setup();
    render(
      <I18nProvider>
        <AdminGate sessionBar={(lock) => <button onClick={lock}>Lock now</button>}>
          <div>UNLOCKED CONTENT</div>
        </AdminGate>
      </I18nProvider>,
    );
    await screen.findByText("UNLOCKED CONTENT");

    await user.click(screen.getByRole("button", { name: "Lock now" }));

    expect(screen.queryByText("UNLOCKED CONTENT")).toBeNull();
    expect(screen.getByText("Console locked")).toBeInTheDocument();
    const signal = vi.mocked(endAdminSession).mock.calls[0]?.[0];
    expect(signal?.aborted).toBe(false);
    pending.resolve({ active: false });
    await pending.promise;
  });

  it("drops to the expired reason when the unlock window has passed", async () => {
    vi.mocked(verifyAdminSession).mockResolvedValue({
      active: true,
      expiresAt: "2000-01-01T00:00:00Z", // already past
    });
    const user = userEvent.setup();
    renderGate();
    await user.type(await screen.findByLabelText("Admin password"), "pw");
    await user.click(screen.getByRole("button", { name: "Unlock Console" }));
    expect(await screen.findByText("Admin session expired")).toBeInTheDocument();
    expect(screen.queryByText("UNLOCKED CONTENT")).toBeNull();
  });

  it("surfaces the generic rejection without inferring a server reason", async () => {
    vi.mocked(verifyAdminSession).mockRejectedValue(new Error("nope"));
    const user = userEvent.setup();
    renderGate();
    await user.type(await screen.findByLabelText("Admin password"), "bad");
    await user.click(screen.getByRole("button", { name: "Unlock Console" }));
    await waitFor(() =>
      expect(screen.getByRole("alert")).toHaveTextContent("The admin password was not accepted."),
    );
  });

  it("aborts and drops a stale AdminSession resolve when the Profile identity changes", async () => {
    const first = deferred<{ active: boolean; expiresAt?: string }>();
    vi.mocked(fetchAdminSession)
      .mockReturnValueOnce(first.promise)
      .mockResolvedValueOnce({ active: false });

    const view = renderGate();
    const firstSignal = vi.mocked(fetchAdminSession).mock.calls[0]?.[0] as AbortSignal;
    setSession("adult", "p2");
    view.rerender(gateNode());

    expect(firstSignal.aborted).toBe(true);
    expect(await screen.findByText("Console locked")).toBeInTheDocument();

    first.resolve({ active: true, expiresAt: "2999-01-01T00:00:00Z" });
    await first.promise;

    expect(screen.queryByText("UNLOCKED CONTENT")).toBeNull();
    expect(screen.getByText("Console locked")).toBeInTheDocument();
  });

  it("aborts and drops a stale AdminSession rejection when the Profile identity changes", async () => {
    const first = deferred<{ active: boolean; expiresAt?: string }>();
    vi.mocked(fetchAdminSession)
      .mockReturnValueOnce(first.promise)
      .mockResolvedValueOnce({ active: true, expiresAt: "2999-01-01T00:00:00Z" });

    const view = renderGate();
    const firstSignal = vi.mocked(fetchAdminSession).mock.calls[0]?.[0] as AbortSignal;
    setSession("adult", "p2");
    view.rerender(gateNode());

    expect(firstSignal.aborted).toBe(true);
    expect(await screen.findByText("UNLOCKED CONTENT")).toBeInTheDocument();

    first.reject(new Error("departed identity failed"));
    await first.promise.catch(() => {});

    expect(screen.getByText("UNLOCKED CONTENT")).toBeInTheDocument();
    expect(screen.queryByText("Console locked")).toBeNull();
  });

  it("clears form state and aborts an in-flight unlock when the Profile identity changes", async () => {
    const pending = deferred<{ active: boolean; expiresAt?: string }>();
    vi.mocked(verifyAdminSession).mockReturnValueOnce(pending.promise);
    const user = userEvent.setup();
    const view = renderGate();

    const input = await screen.findByLabelText("Admin password");
    await user.type(input, "pw");
    await user.click(screen.getByRole("button", { name: "Unlock Console" }));
    const verifySignal = vi.mocked(verifyAdminSession).mock.calls[0]?.[1] as AbortSignal;
    expect(verifySignal.aborted).toBe(false);

    setSession("adult", "p2");
    view.rerender(gateNode());

    expect(verifySignal.aborted).toBe(true);
    const replacementInput = await screen.findByLabelText("Admin password");
    expect(replacementInput).toHaveValue("");
    expect(screen.getByRole("button", { name: "Unlock Console" })).toBeDisabled();
    expect(screen.queryByRole("alert")).toBeNull();
    await user.type(replacementInput, "new password");
    expect(screen.getByRole("button", { name: "Unlock Console" })).toBeEnabled();

    pending.resolve({ active: true, expiresAt: "2999-01-01T00:00:00Z" });
    await pending.promise;
    expect(screen.queryByText("UNLOCKED CONTENT")).toBeNull();
  });

  it("aborts the AdminSession fetch on unmount and never writes to unmounted state", async () => {
    const pending = deferred<{ active: boolean; expiresAt?: string }>();
    vi.mocked(fetchAdminSession).mockReturnValueOnce(pending.promise);

    const { unmount } = renderGate();
    const signal = vi.mocked(fetchAdminSession).mock.calls[0]?.[0] as AbortSignal;
    expect(signal.aborted).toBe(false);

    unmount();
    expect(signal.aborted).toBe(true);

    postUnmountWrites.armed = true;
    pending.resolve({ active: true, expiresAt: "2999-01-01T00:00:00Z" });
    await pending.promise;
    await act(async () => {});
    postUnmountWrites.armed = false;
    expect(postUnmountWrites.count).toBe(0);
  });

  it("aborts a deferred verifyAdminSession on unmount and never writes to unmounted state", async () => {
    const pending = deferred<{ active: boolean; expiresAt?: string }>();
    vi.mocked(verifyAdminSession).mockReturnValueOnce(pending.promise);
    const user = userEvent.setup();

    const { unmount } = renderGate();
    await user.type(await screen.findByLabelText("Admin password"), "pw");
    await user.click(screen.getByRole("button", { name: "Unlock Console" }));
    const signal = vi.mocked(verifyAdminSession).mock.calls[0]?.[1] as AbortSignal;
    expect(signal.aborted).toBe(false);

    unmount();

    postUnmountWrites.armed = true;
    await act(async () => {
      pending.resolve({ active: true, expiresAt: "2999-01-01T00:00:00Z" });
      await pending.promise;
    });
    postUnmountWrites.armed = false;
    expect({ signalAborted: signal.aborted, postUnmountWrites: postUnmountWrites.count }).toEqual({
      signalAborted: true,
      postUnmountWrites: 0,
    });
  });
});
