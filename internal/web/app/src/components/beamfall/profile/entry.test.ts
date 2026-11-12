import { afterEach, describe, expect, it, vi } from "vitest";

afterEach(() => {
  vi.restoreAllMocks();
  vi.resetModules();
  window.sessionStorage.clear();
});

describe("who's-watching entry flag", () => {
  it("remembers the choice for the tab", async () => {
    const { markProfilePicked, profilePickedThisTab } = await import("./entry");
    expect(profilePickedThisTab()).toBe(false);
    markProfilePicked();
    expect(profilePickedThisTab()).toBe(true);
  });

  it("lets the visit through when storage cannot be read", async () => {
    vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => {
      throw new Error("blocked");
    });
    const { profilePickedThisTab } = await import("./entry");
    expect(profilePickedThisTab()).toBe(true);
  });

  it("lets the visit through when the choice cannot be written", async () => {
    vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
      throw new Error("quota");
    });
    const { markProfilePicked, profilePickedThisTab } = await import("./entry");
    expect(profilePickedThisTab()).toBe(false);
    markProfilePicked();
    expect(window.sessionStorage.getItem("beamfall.profile-picked")).toBeNull();
    expect(profilePickedThisTab()).toBe(true);
  });
});
