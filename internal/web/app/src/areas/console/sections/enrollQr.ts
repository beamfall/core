import type { RemoteEndpointInfo } from "@/lib/graphql/types";

// Which address the "Add a device" QR encodes (CORE-1614). A phone can only
// open an address on the network, so a Console reached on loopback encodes the
// server's first LAN endpoint instead, and shows no QR when it has none. The
// pairing code never goes in the URL: it is a credential, and the person
// types it on the phone.

/** True when `hostname` (as `location.hostname` gives it) names this machine. */
export function isLoopbackHost(hostname: string): boolean {
  // A trailing dot names the same host ("localhost."), and the URL parser
  // writes an IPv4-mapped address in hex ("[::ffff:7f00:1]").
  const host = hostname
    .toLowerCase()
    .replace(/^\[|\]$/g, "")
    .replace(/\.$/, "");
  const mapped = /^::ffff:(?:0:)?(.+)$/.exec(host)?.[1];
  if (mapped !== undefined)
    return isLoopbackIPv4(mapped) || /^7f[0-9a-f]{2}:[0-9a-f]{1,4}$/.test(mapped);
  return (
    host === "localhost" ||
    host.endsWith(".localhost") ||
    host === "::1" ||
    host === "0:0:0:0:0:0:0:1" ||
    host === "0.0.0.0" ||
    host === "::" ||
    isLoopbackIPv4(host)
  );
}

function isLoopbackIPv4(host: string): boolean {
  return /^127\.\d+\.\d+\.\d+$/.test(host);
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
