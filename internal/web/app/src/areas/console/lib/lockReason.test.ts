import { describe, expect, it } from "vitest";
import { lockReason } from "./lockReason";

const base = {
  profileKind: "adult",
  adminActive: true,
  boundProfileID: "p1",
  currentProfileID: "p1",
  expiresAt: undefined as string | undefined,
  now: Date.parse("2026-06-20T12:00:00Z"),
};

describe("lockReason", () => {
  it("is null when an adult holds an active, current, unexpired session", () => {
    expect(lockReason(base)).toBeNull();
  });

  it("returns not-adult for a managed Profile regardless of session state", () => {
    expect(lockReason({ ...base, profileKind: "managed" })).toBe("not-adult");
    // not-adult wins even if a session is somehow active.
    expect(lockReason({ ...base, profileKind: "managed", adminActive: true })).toBe("not-adult");
  });

  it("returns no-session when no Admin Session is active", () => {
    expect(lockReason({ ...base, adminActive: false })).toBe("no-session");
  });

  it("returns profile-switched when the bound Profile differs from current", () => {
    expect(lockReason({ ...base, boundProfileID: "p1", currentProfileID: "p2" })).toBe(
      "profile-switched",
    );
  });

  it("returns expired when expiresAt is at or before now", () => {
    expect(lockReason({ ...base, expiresAt: "2026-06-20T11:59:59Z" })).toBe("expired");
  });

  it("is null when expiresAt is in the future", () => {
    expect(lockReason({ ...base, expiresAt: "2026-06-20T13:00:00Z" })).toBeNull();
  });

  it("ignores an unparseable expiresAt rather than locking", () => {
    expect(lockReason({ ...base, expiresAt: "not-a-date" })).toBeNull();
  });
});
