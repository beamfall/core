import "@testing-library/jest-dom/vitest";
import { cleanup, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { I18nProvider } from "@/lib/i18n";
import { useSession } from "@/lib/session";
import { prepareRenderFocusRestore } from "../lib/renderFocus";
import { RevealControls } from "./RevealControls";

vi.mock("@/lib/session", () => ({ useSession: vi.fn() }));
vi.mock("../lib/renderFocus", () => ({ prepareRenderFocusRestore: vi.fn() }));

afterEach(() => {
  cleanup();
  vi.mocked(useSession).mockReset();
  vi.mocked(prepareRenderFocusRestore).mockReset();
});

describe("Reveal focus transition", () => {
  it("captures the render anchor before a successful Reveal reload", async () => {
    const reveal = vi.fn().mockResolvedValue(true);
    vi.mocked(useSession).mockReturnValue({
      revealed: false,
      reveal,
      relock: vi.fn(),
    } as unknown as ReturnType<typeof useSession>);
    const user = userEvent.setup();
    render(
      <I18nProvider>
        <RevealControls />
      </I18nProvider>,
    );

    await user.click(screen.getByRole("button", { name: /restrictions/i }));
    await user.type(screen.getByLabelText(/pin/i), "1234");
    await user.click(screen.getByRole("button", { name: /^reveal$/i }));

    expect(prepareRenderFocusRestore).toHaveBeenCalledOnce();
    expect(vi.mocked(prepareRenderFocusRestore).mock.invocationCallOrder[0]).toBeLessThan(
      reveal.mock.invocationCallOrder[0],
    );
  });

  it("captures the render anchor before a relock reload", async () => {
    const relock = vi.fn().mockResolvedValue(undefined);
    vi.mocked(useSession).mockReturnValue({
      revealed: true,
      reveal: vi.fn(),
      relock,
    } as unknown as ReturnType<typeof useSession>);
    const user = userEvent.setup();
    render(
      <I18nProvider>
        <RevealControls />
      </I18nProvider>,
    );

    await user.click(screen.getByRole("button", { name: /relock/i }));

    expect(prepareRenderFocusRestore).toHaveBeenCalledOnce();
    expect(vi.mocked(prepareRenderFocusRestore).mock.invocationCallOrder[0]).toBeLessThan(
      relock.mock.invocationCallOrder[0],
    );
  });
});
