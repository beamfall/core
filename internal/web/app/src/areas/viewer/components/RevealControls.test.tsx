import "@testing-library/jest-dom/vitest";
import { act, cleanup, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useEffect } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { I18nProvider } from "@/lib/i18n";
import { useSession } from "@/lib/session";
import { PlayerHost, usePlayerHost } from "../player/PlayerHost";
import { RevealControls } from "./RevealControls";

vi.mock("@/lib/session", () => ({ useSession: vi.fn() }));
vi.mock("../lib/renderFocus", () => ({ prepareRenderFocusRestore: vi.fn() }));
vi.mock("../player/LazyPlayerPage", () => ({
  LazyHostedPlayer: ({
    docked,
    onAudioChange,
  }: {
    docked: boolean;
    onAudioChange: (audio: boolean) => void;
  }) => {
    useEffect(() => onAudioChange(true), [onAudioChange]);
    return <div data-testid="hosted-player" data-docked={docked} />;
  },
}));

afterEach(() => {
  cleanup();
  vi.mocked(useSession).mockReset();
});

describe("RevealControls relock failure handling", () => {
  it("surfaces an error and avoids an unhandled rejection when relock() rejects", async () => {
    const unhandled: unknown[] = [];
    const onUnhandled = (event: PromiseRejectionEvent) => {
      event.preventDefault();
      unhandled.push(event.reason);
    };
    window.addEventListener("unhandledrejection", onUnhandled);
    const onNodeUnhandled = (reason: unknown) => {
      unhandled.push(reason);
    };
    process.on("unhandledRejection", onNodeUnhandled);

    const relock = vi.fn().mockRejectedValue(new Error("network down"));
    vi.mocked(useSession).mockReturnValue({
      session: { revealPinSet: true },
      revealed: true,
      reveal: vi.fn(),
      relock,
    } as unknown as ReturnType<typeof useSession>);

    const user = userEvent.setup();
    try {
      render(
        <I18nProvider>
          <RevealControls />
        </I18nProvider>,
      );

      const button = screen.getByRole("button", { name: /relock/i });
      await user.click(button);

      expect(relock).toHaveBeenCalledOnce();
      expect(await screen.findByRole("alert")).toBeInTheDocument();
      expect(screen.getByRole("button", { name: /relock/i })).toBeEnabled();
      expect(unhandled).toEqual([]);
    } finally {
      window.removeEventListener("unhandledrejection", onUnhandled);
      process.off("unhandledRejection", onNodeUnhandled);
    }
  });
});

describe("RevealControls relock ends playback", () => {
  it("stops a docked track before the relock request settles", async () => {
    let settle: () => void = () => {};
    const relock = vi.fn(() => new Promise<void>((resolve) => (settle = resolve)));
    vi.mocked(useSession).mockReturnValue({
      session: { revealPinSet: true },
      revealed: true,
      reveal: vi.fn(),
      relock,
    } as unknown as ReturnType<typeof useSession>);

    let host: ReturnType<typeof usePlayerHost> = null;
    function Probe() {
      host = usePlayerHost();
      return null;
    }
    render(
      <I18nProvider>
        <PlayerHost>
          <Probe />
          <RevealControls />
        </PlayerHost>
      </I18nProvider>,
    );

    // A track plays, then the watch route closes: audio docks in the mini player.
    act(() => host?.attach({ mediaId: "track-1", state: {}, search: "" }));
    await screen.findByTestId("hosted-player");
    act(() => host?.detach());
    expect(screen.getByTestId("hosted-player")).toHaveAttribute("data-docked", "true");

    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: /relock/i }));

    // The player is gone while the relock request is still in flight.
    expect(relock).toHaveBeenCalledOnce();
    expect(screen.queryByTestId("hosted-player")).toBeNull();
    await act(async () => settle());
  });
});
