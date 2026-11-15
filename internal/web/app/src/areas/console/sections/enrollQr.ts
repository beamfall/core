import type { RemoteEndpointInfo } from "@/lib/graphql/types";

// Which address the "Add a device" QR encodes (CORE-1614). A phone can only
// open an address on the network, so a Console reached on loopback encodes the
// server's first LAN endpoint instead, and shows no QR when it has none. The
// pairing code never goes in the URL: it is a credential, and the person
// types it on the phone.

/** True when `hostname` (as `location.hostname` gives it) names this machine. */
export function isLoopbackHost(hostname: string): boolean {
  const host = hostname.toLowerCase().replace(/^\[|\]$/g, "");
  return (
    host === "localhost" ||
    host.endsWith(".localhost") ||
    host === "::1" ||
    /^127\.\d+\.\d+\.\d+$/.test(host)
  );
}

/** The enroll URL to encode, or null when there is no address a phone can open. */
export function enrollQrTarget(
  origin: string,
  hostname: string,
  base: string,
  endpoints: readonly RemoteEndpointInfo[] | null,
): string | null {
  const path = `${base.endsWith("/") ? base : `${base}/`}enroll`;
  if (!isLoopbackHost(hostname)) return `${origin}${path}`;
  const lan = endpoints?.find((endpoint) => endpoint.kind === "lan");
  if (!lan) return null;
  try {
    const url = new URL(lan.url);
    if (url.protocol !== "http:" && url.protocol !== "https:") return null;
    if (isLoopbackHost(url.hostname)) return null;
    return `${url.origin}${path}`;
  } catch {
    return null;
  }
}
