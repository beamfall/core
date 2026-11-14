import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { I18nProvider } from "@/lib/i18n";
import { SyncNudger } from "./SyncNudger";

function renderNudger(value: number, min = -5, supported = true) {
  const onNudge = vi.fn();
  const onReset = vi.fn();
  render(
    <I18nProvider initialLocale="en">
      <SyncNudger
        label="Audio sync"
        value={value}
        min={min}
        max={5}
        supported={supported}
        onNudge={onNudge}
        onReset={onReset}
      />
    </I18nProvider>,
  );
  return { onNudge, onReset };
}

describe("SyncNudger", () => {
  it("shows the value with its direction and steps both ways", () => {
    const { onNudge } = renderNudger(-1.2);
    expect(screen.getByText("−1.2 s")).toBeTruthy();
    expect(screen.getByText("earlier")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Audio sync +0.1 s" }));
    fireEvent.click(screen.getByRole("button", { name: "Audio sync −0.5 s" }));
    expect(onNudge.mock.calls).toEqual([[0.1], [-0.5]]);
  });

  it("disables steps that would leave the range", () => {
    renderNudger(0, 0);
    expect(
      (screen.getByRole("button", { name: "Audio sync −0.1 s" }) as HTMLButtonElement).disabled,
    ).toBe(true);
    expect(
      (screen.getByRole("button", { name: "Audio sync +0.1 s" }) as HTMLButtonElement).disabled,
    ).toBe(false);
    expect(
      (screen.getByRole("button", { name: "Audio sync: Reset" }) as HTMLButtonElement).disabled,
    ).toBe(true);
  });

  it("keeps reset available when the route cannot apply the value", () => {
    const { onReset } = renderNudger(0.4, 0, false);
    expect(
      (screen.getByRole("button", { name: "Audio sync +0.1 s" }) as HTMLButtonElement).disabled,
    ).toBe(true);
    fireEvent.click(screen.getByRole("button", { name: "Audio sync: Reset" }));
    expect(onReset).toHaveBeenCalledTimes(1);
    expect(screen.getByText("later")).toBeTruthy();
  });
});
