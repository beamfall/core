import { describe, expect, it } from "vitest";
import { defaultDeviceName } from "./deviceName";

const t = ((key: string, vars?: Record<string, string>) =>
  key === "deviceNameDefault" ? `${vars?.browser} on ${vars?.os}` : "Web Browser") as Parameters<
  typeof defaultDeviceName
>[0];

describe("defaultDeviceName", () => {
  it.each([
    [
      "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0 Safari/537.36",
      0,
      "Chrome on macOS",
    ],
    [
      "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.1 Safari/605.1.15",
      0,
      "Safari on macOS",
    ],
    [
      "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.1 Safari/605.1.15",
      5,
      "Safari on iPad",
    ],
    [
      "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0 Safari/537.36 Edg/131.0",
      0,
      "Edge on Windows",
    ],
    [
      "Mozilla/5.0 (X11; Linux x86_64; rv:133.0) Gecko/20100101 Firefox/133.0",
      0,
      "Firefox on Linux",
    ],
    [
      "Mozilla/5.0 (iPhone; CPU iPhone OS 18_1 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) CriOS/131.0 Mobile/15E148 Safari/604.1",
      5,
      "Chrome on iPhone",
    ],
    [
      "Mozilla/5.0 (Linux; Android 15; Pixel 9) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0 Mobile Safari/537.36",
      5,
      "Chrome on Android",
    ],
  ])("names %s", (ua, touch, want) => {
    expect(defaultDeviceName(t, ua, touch)).toBe(want);
  });

  it("falls back to the generic name for an unknown agent", () => {
    expect(defaultDeviceName(t, "curl/8.0", 0)).toBe("Web Browser");
  });
});
