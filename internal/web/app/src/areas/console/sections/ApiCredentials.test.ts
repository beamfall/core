import { describe, expect, it } from "vitest";
import { apiScopeLabel } from "./ApiCredentials";

const labels = {
  scopeReadCatalog: "Read catalog",
  scopeReadSessions: "Read sessions",
  scopeIngest: "Ingest",
  scopeEventsSubscribe: "Subscribe to events",
  scopePlaybackControl: "Control playback",
};

describe("apiScopeLabel", () => {
  it("maps known API scopes through catalog string keys", () => {
    expect(apiScopeLabel("read-catalog", labels)).toBe("Read catalog");
    expect(apiScopeLabel("read-sessions", labels)).toBe("Read sessions");
    expect(apiScopeLabel("ingest", labels)).toBe("Ingest");
    expect(apiScopeLabel("events-subscribe", labels)).toBe("Subscribe to events");
    expect(apiScopeLabel("playback-control", labels)).toBe("Control playback");
  });

  it("humanizes unknown scope IDs without rendering raw slug punctuation", () => {
    expect(apiScopeLabel("custom-scope_name", labels)).toBe("Custom scope name");
  });
});
