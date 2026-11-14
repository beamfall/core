import type { useT } from "@/lib/i18n";

// A distinguishing default device name (CORE-1607): "Chrome on macOS" tells
// two browsers apart in Console › Devices where "Web Browser" did not. The
// browser and OS names are product names, so only the joining phrase is
// localized. Unknown agents fall back to the generic "Web Browser".

function browserName(ua: string): string | null {
  if (/\bEdg(e|A|iOS)?\//.test(ua)) return "Edge";
  if (/\b(OPR|Opera)\//.test(ua)) return "Opera";
  if (/\b(Firefox|FxiOS)\//.test(ua)) return "Firefox";
  if (/\b(Chrome|CriOS|Chromium)\//.test(ua)) return "Chrome";
  if (/\bSafari\//.test(ua)) return "Safari";
  return null;
}

function osName(ua: string, touchPoints: number): string | null {
  if (/\biPhone\b/.test(ua)) return "iPhone";
  // iPadOS asks for desktop sites and reports itself as a Mac with touch.
  if (/\biPad\b/.test(ua) || (/\bMacintosh\b/.test(ua) && touchPoints > 1)) return "iPad";
  if (/\bAndroid\b/.test(ua)) return "Android";
  if (/\bWindows\b/.test(ua)) return "Windows";
  if (/\bCrOS\b/.test(ua)) return "ChromeOS";
  if (/\bMac OS X\b|\bMacintosh\b/.test(ua)) return "macOS";
  if (/\bLinux\b/.test(ua)) return "Linux";
  return null;
}

export function defaultDeviceName(
  t: ReturnType<typeof useT>,
  ua: string = typeof navigator === "undefined" ? "" : navigator.userAgent,
  touchPoints: number = typeof navigator === "undefined" ? 0 : navigator.maxTouchPoints || 0,
): string {
  const browser = browserName(ua);
  const os = osName(ua, touchPoints);
  if (browser && os) return t("deviceNameDefault", { browser, os });
  return browser ?? t("webBrowser");
}
