import { describe, expect, it } from "vitest";
import { readWatchPartyIntent, watchPartyInviteState } from "./watchTogether";

describe("readWatchPartyIntent", () => {
  it("reads the invite flag the detail page sets", () => {
    expect(readWatchPartyIntent({ title: "x", ...watchPartyInviteState() })).toBe(true);
  });

  it("ignores missing, foreign or malformed state", () => {
    expect(readWatchPartyIntent(undefined)).toBe(false);
    expect(readWatchPartyIntent(null)).toBe(false);
    expect(readWatchPartyIntent("invite")).toBe(false);
    expect(readWatchPartyIntent({ title: "x", skipPrePlay: true })).toBe(false);
    expect(readWatchPartyIntent({ watchPartyIntent: true })).toBe(false);
  });
});
