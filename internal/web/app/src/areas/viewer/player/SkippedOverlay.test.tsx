import "@testing-library/jest-dom/vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { I18nProvider } from "@/lib/i18n";
import { SkippedOverlay } from "./SkippedOverlay";

const segment = { segmentKind: "intro", startSeconds: 0, endSeconds: 30, title: "Intro" };

afterEach(() => {
  vi.useRealTimers();
  vi.restoreAllMocks();
});

describe("SkippedOverlay", () => {
  it("announces the skip and offers a focusable, non-modal Undo", () => {
    render(
      <I18nProvider>
        <button type="button">Elsewhere</button>
        <SkippedOverlay segment={segment} onUndo={() => {}} onDismiss={() => {}} />
      </I18nProvider>,
    );

    const status = screen.getByRole("status");
    expect(status).toHaveTextContent("Skipped intro");
    // No dialog/aria-modal — nothing here claims the modal focus-trap role
    // (ADR-0014 §4: overlays never trap VO focus).
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    expect(status).not.toHaveAttribute("aria-modal");

    const undoButton = screen.getByRole("button", { name: "Undo" });
    undoButton.focus();
    expect(undoButton).toHaveFocus();
    // An ordinary tabbable button — focus can move freely away to another
    // element (nothing re-grabs it, unlike a trapping modal dialog).
    screen.getByRole("button", { name: "Elsewhere" }).focus();
    expect(undoButton).not.toHaveFocus();
  });

  it("calls onUndo when the Undo control is activated", () => {
    const onUndo = vi.fn();
    render(
      <I18nProvider>
        <SkippedOverlay segment={segment} onUndo={onUndo} onDismiss={() => {}} />
      </I18nProvider>,
    );
    fireEvent.click(screen.getByRole("button", { name: "Undo" }));
    expect(onUndo).toHaveBeenCalledTimes(1);
  });

  it("auto-dismisses itself after a brief delay", () => {
    vi.useFakeTimers();
    const onDismiss = vi.fn();
    render(
      <I18nProvider>
        <SkippedOverlay segment={segment} onUndo={() => {}} onDismiss={onDismiss} />
      </I18nProvider>,
    );
    expect(onDismiss).not.toHaveBeenCalled();
    vi.runAllTimers();
    expect(onDismiss).toHaveBeenCalledTimes(1);
  });

  it("labels credits and recap distinctly from intro", () => {
    const { rerender } = render(
      <I18nProvider>
        <SkippedOverlay
          segment={{ ...segment, segmentKind: "credits" }}
          onUndo={() => {}}
          onDismiss={() => {}}
        />
      </I18nProvider>,
    );
    expect(screen.getByRole("status")).toHaveTextContent("Skipped credits");

    rerender(
      <I18nProvider>
        <SkippedOverlay
          segment={{ ...segment, segmentKind: "recap" }}
          onUndo={() => {}}
          onDismiss={() => {}}
        />
      </I18nProvider>,
    );
    expect(screen.getByRole("status")).toHaveTextContent("Skipped recap");
  });
});
