// A kind-agnostic reader for a Server-Sent Events stream.
//
// The browser's EventSource hands a frame that carries an `event: <kind>` line
// only to a listener registered for that exact kind, and never to `onmessage`.
// Core names every event it sends and the set of kinds is open, so a consumer
// that wants "any event" cannot use EventSource. This reader parses the stream
// itself and delivers every frame, with the kind as the event's `type`.

/** The live state of an SSE subscription, surfaced to a connection indicator. */
export type EventStreamState = "live" | "reconnecting";

export interface EventStreamHandlers {
  onEvent: (event: MessageEvent) => void;
  onError?: (err: Event) => void;
  onState?: (state: EventStreamState) => void;
}

const FIRST_RETRY_MS = 1000;
const MAX_RETRY_MS = 30000;

/** Incremental SSE frame parser (WHATWG HTML §9.2.6); feed it decoded text. */
export function createEventStreamParser(
  onFrame: (frame: { type: string; data: string; lastEventId: string }) => void,
): (chunk: string) => void {
  let buffer = "";
  let type = "";
  let data: string[] = [];
  let lastEventId = "";

  const line = (text: string) => {
    if (text === "") {
      if (data.length > 0) onFrame({ type: type || "message", data: data.join("\n"), lastEventId });
      type = "";
      data = [];
      return;
    }
    if (text.startsWith(":")) return;
    const colon = text.indexOf(":");
    const field = colon === -1 ? text : text.slice(0, colon);
    let value = colon === -1 ? "" : text.slice(colon + 1);
    if (value.startsWith(" ")) value = value.slice(1);
    if (field === "event") type = value;
    else if (field === "data") data.push(value);
    else if (field === "id" && !value.includes("\0")) lastEventId = value;
  };

  return (chunk) => {
    buffer += chunk;
    for (;;) {
      const match = /\r\n|\n|\r/.exec(buffer);
      if (!match) return;
      // A lone trailing CR may be the first half of a CRLF split across chunks.
      if (match[0] === "\r" && match.index === buffer.length - 1) return;
      line(buffer.slice(0, match.index));
      buffer = buffer.slice(match.index + match[0].length);
    }
  };
}

/**
 * Open `url` as an SSE stream and deliver every frame, whatever its kind.
 * Reconnects with capped backoff, resuming from the last event id, until the
 * returned function is called.
 */
export function openEventStream(url: string, handlers: EventStreamHandlers): () => void {
  if (typeof fetch === "undefined" || typeof AbortController === "undefined") return () => {};
  const controller = new AbortController();
  const { signal } = controller;
  let lastEventId = "";
  let retryMs = FIRST_RETRY_MS;

  const readOnce = async () => {
    const headers: Record<string, string> = { Accept: "text/event-stream" };
    if (lastEventId) headers["Last-Event-ID"] = lastEventId;
    const response = await fetch(url, {
      credentials: "include",
      cache: "no-store",
      headers,
      signal,
    });
    const contentType = response.headers.get("content-type") ?? "";
    if (!response.ok || !response.body || !contentType.includes("text/event-stream")) {
      throw new Error(`event stream unavailable (${response.status})`);
    }
    handlers.onState?.("live");
    retryMs = FIRST_RETRY_MS;
    const feed = createEventStreamParser((frame) => {
      lastEventId = frame.lastEventId;
      handlers.onEvent(
        new MessageEvent(frame.type, { data: frame.data, lastEventId: frame.lastEventId }),
      );
    });
    const reader = response.body.getReader();
    const decoder = new TextDecoder();
    for (;;) {
      const { done, value } = await reader.read();
      if (done) return;
      feed(decoder.decode(value, { stream: true }));
    }
  };

  const run = async () => {
    while (!signal.aborted) {
      try {
        await readOnce();
      } catch {
        // Fall through to the reconnect below; an abort ends the loop.
      }
      if (signal.aborted) return;
      handlers.onError?.(new Event("error"));
      handlers.onState?.("reconnecting");
      await new Promise<void>((resolve) => {
        const wake = () => {
          clearTimeout(timer);
          signal.removeEventListener("abort", wake);
          resolve();
        };
        const timer = setTimeout(wake, retryMs);
        signal.addEventListener("abort", wake, { once: true });
      });
      retryMs = Math.min(retryMs * 2, MAX_RETRY_MS);
    }
  };
  void run();

  return () => controller.abort();
}
