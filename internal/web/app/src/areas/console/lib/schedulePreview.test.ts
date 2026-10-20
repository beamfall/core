import { describe, expect, it } from "vitest";
import { type AccessWindow, formatMinute, isAllowedAt, nextTransition } from "./schedulePreview";

const DAY = 24 * 60;
// Helper: minute-of-week for a weekday + HH:MM.
const wm = (weekday: number, h: number, m = 0) => weekday * DAY + h * 60 + m;

describe("isAllowedAt", () => {
  it("treats an empty schedule as always allowed", () => {
    expect(isAllowedAt([], wm(1, 3))).toBe(true);
  });

  it("allows inside a window and blocks outside it", () => {
    const windows: AccessWindow[] = [
      { weekday: 1, startMinute: 9 * 60, endMinute: 17 * 60 }, // Mon 09:00–17:00
    ];
    expect(isAllowedAt(windows, wm(1, 12))).toBe(true);
    expect(isAllowedAt(windows, wm(1, 8))).toBe(false);
    expect(isAllowedAt(windows, wm(2, 12))).toBe(false); // Tuesday
  });

  it("handles an overnight window that wraps past midnight", () => {
    const windows: AccessWindow[] = [
      { weekday: 5, startMinute: 22 * 60, endMinute: 2 * 60 }, // Fri 22:00–02:00
    ];
    expect(isAllowedAt(windows, wm(5, 23))).toBe(true);
    expect(isAllowedAt(windows, wm(6, 1))).toBe(true); // Sat 01:00 (wrapped)
    expect(isAllowedAt(windows, wm(6, 3))).toBe(false);
  });
});

describe("nextTransition", () => {
  it("returns null for an always-allowed (empty) schedule", () => {
    expect(nextTransition([], 1, 0)).toBeNull();
  });

  it("finds the next block boundary from inside a window", () => {
    const windows: AccessWindow[] = [{ weekday: 1, startMinute: 9 * 60, endMinute: 17 * 60 }];
    const next = nextTransition(windows, 1, 12 * 60); // Monday noon
    expect(next).not.toBeNull();
    expect(next!.allowed).toBe(false); // next boundary is a block
    expect(next!.weekday).toBe(1);
    expect(next!.minute).toBe(17 * 60);
    expect(next!.inMinutes).toBe(5 * 60);
  });

  it("finds the next allow boundary from outside all windows", () => {
    const windows: AccessWindow[] = [{ weekday: 1, startMinute: 9 * 60, endMinute: 17 * 60 }];
    const next = nextTransition(windows, 1, 8 * 60); // Monday 08:00
    expect(next!.allowed).toBe(true);
    expect(next!.minute).toBe(9 * 60);
    expect(next!.inMinutes).toBe(60);
  });
});

describe("formatMinute", () => {
  it("zero-pads to HH:MM", () => {
    expect(formatMinute(9 * 60)).toBe("09:00");
    expect(formatMinute(17 * 60 + 5)).toBe("17:05");
    expect(formatMinute(0)).toBe("00:00");
  });
});
