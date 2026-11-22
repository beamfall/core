import "@testing-library/jest-dom/vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { I18nProvider } from "@/lib/i18n";
import { useShareLink } from "./useShareLink";

const LINK = "http://192.168.4.3:8800/web/watch/abc?t=42";

function Harness() {
  const { share, node } = useShareLink("Title");
  return (
    <>
      <button type="button" onClick={() => share(LINK)}>
        go
      </button>
      {node}
    </>
  );
}

function stubNav(share: unknown) {
  Object.defineProperty(navigator, "share", { value: share, configurable: true });
  Object.defineProperty(navigator, "clipboard", { value: undefined, configurable: true });
}

function setup() {
  render(
    <I18nProvider>
      <Harness />
    </I18nProvider>,
  );
  fireEvent.click(screen.getByText("go"));
}

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

describe("useShareLink", () => {
  it("falls back to execCommand copy with a toast when the clipboard API is missing", async () => {
    stubNav(undefined);
    const exec = vi.fn(() => true);
    Object.defineProperty(document, "execCommand", { value: exec, configurable: true });
    setup();
    expect(await screen.findByText("Link copied")).toBeInTheDocument();
    expect(exec).toHaveBeenCalledWith("copy");
  });

  it("shows a manual-copy dialog with the link when every copy path fails", async () => {
    stubNav(undefined);
    Object.defineProperty(document, "execCommand", { value: () => false, configurable: true });
    setup();
    const input = await screen.findByDisplayValue(LINK);
    expect(input).toHaveAttribute("readonly");
    await waitFor(() => expect(screen.queryByText("Link copied")).not.toBeInTheDocument());
  });

  it("stays silent when the user cancels the share sheet", async () => {
    const share = vi.fn().mockRejectedValue(new DOMException("x", "AbortError"));
    stubNav(share);
    setup();
    await waitFor(() => expect(share).toHaveBeenCalled());
    expect(screen.queryByText("Link copied")).not.toBeInTheDocument();
    expect(screen.queryByDisplayValue(LINK)).not.toBeInTheDocument();
  });
});
