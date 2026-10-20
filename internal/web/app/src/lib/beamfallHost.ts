/// The web→native bridge surface the Apple host injects (MGRWEB-4). Used by
/// host-aware affordances to deep-link OUT to native — e.g. the Console preview
/// opening the native player instead of an in-web player (MGRWEB-7).
export interface BeamfallHostBridge {
  postMessage?: (message: { name: string; payload: Record<string, unknown> }) => boolean;
  webToNativeMessages?: readonly string[];
}

export interface BeamfallHost {
  kind: "apple-manager";
  platform: "macos" | "ipad";
  clientClass: "apple-manager-macos-direct" | "ipad-manager-app-store";
  takeInitialMessages?: () => unknown;
  bridge?: BeamfallHostBridge;
}

export type BeamfallHostMessage =
  | {
      name: "session.handoff";
      payload: { sessionID: string };
    }
  | {
      name: "state.profileRevealMode";
      payload: { profileID: string; revealed: boolean; mode: string };
    };

function isObject(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null;
}

function parseHostMessage(message: unknown): BeamfallHostMessage | null {
  if (!isObject(message) || typeof message.name !== "string") return null;
  if (!isObject(message.payload)) return null;
  if (message.name === "session.handoff") {
    const sessionID = message.payload.sessionID;
    return typeof sessionID === "string" && sessionID.length > 0
      ? { name: message.name, payload: { sessionID } }
      : null;
  }
  if (message.name === "state.profileRevealMode") {
    const { profileID, revealed, mode } = message.payload;
    return typeof profileID === "string" &&
      profileID.length > 0 &&
      typeof revealed === "boolean" &&
      typeof mode === "string" &&
      mode.length > 0
      ? { name: message.name, payload: { profileID, revealed, mode } }
      : null;
  }
  return null;
}

declare global {
  interface Window {
    beamfallHost?: BeamfallHost;
  }
}

export function getBeamfallHost(): BeamfallHost | null {
  const host = window.beamfallHost;
  if (host?.kind !== "apple-manager") return null;
  if (host.platform === "macos" && host.clientClass === "apple-manager-macos-direct") {
    return host;
  }
  if (host.platform === "ipad" && host.clientClass === "ipad-manager-app-store") {
    return host;
  }
  return null;
}

export function isBeamfallAppStoreHost(): boolean {
  return getBeamfallHost()?.clientClass === "ipad-manager-app-store";
}

/// The web→native deep-link that opens the native player (MGRWEB-7). Mirrors
/// the Apple host's `deepLink.openInNativePlayer` bridge message.
const OPEN_IN_NATIVE_PLAYER = "deepLink.openInNativePlayer";

/// Asks the Apple host to open `mediaID` in the **native** player. Returns
/// `true` only when running inside the Apple host AND the bridge accepted the
/// message; otherwise `false`, so callers fall back to the in-web player.
/// Inside the Apple host all playback is native (apple ADR-0002 §2), so the
/// embedded Console's preview affordance uses this to deep-link OUT.
export function requestOpenInNativePlayer(mediaID: string): boolean {
  const host = getBeamfallHost();
  const post = host?.bridge?.postMessage;
  if (typeof post !== "function" || mediaID.length === 0) return false;
  return post({ name: OPEN_IN_NATIVE_PLAYER, payload: { mediaID } }) === true;
}

export function takeBeamfallHostInitialMessages(): BeamfallHostMessage[] {
  const host = getBeamfallHost();
  if (!host || typeof host.takeInitialMessages !== "function") return [];
  try {
    const messages = host.takeInitialMessages();
    if (!Array.isArray(messages)) return [];
    return messages.flatMap((message) => {
      const parsed = parseHostMessage(message);
      return parsed ? [parsed] : [];
    });
  } catch {
    return [];
  }
}
