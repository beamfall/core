import { afterEach, describe, expect, it, vi } from "vitest";
import { readVersionChoice, versionLabel, writeVersionChoice } from "./versionChoice";

afterEach(() => {
  vi.restoreAllMocks();
  window.localStorage.clear();
});

describe("versionChoice (CORE-1676)", () => {
  it("remembers a pick per profile and Media", () => {
    writeVersionChoice("profile-a", "media-1", "v1");
    writeVersionChoice("profile-b", "media-1", "v2");

    expect(readVersionChoice("profile-a", "media-1")).toBe("v1");
    expect(readVersionChoice("profile-b", "media-1")).toBe("v2");
    expect(readVersionChoice("profile-a", "media-2")).toBe("");
  });

  it("reads and writes nothing without a profile or Media", () => {
    writeVersionChoice(undefined, "media-1", "v1");
    writeVersionChoice("profile-a", "", "v1");

    expect(window.localStorage.length).toBe(0);
    expect(readVersionChoice(undefined, "media-1")).toBe("");
    expect(readVersionChoice("profile-a", "")).toBe("");
  });

  it("survives storage that throws", () => {
    vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => {
      throw new Error("blocked");
    });
    vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
      throw new Error("blocked");
    });

    expect(() => writeVersionChoice("profile-a", "media-1", "v1")).not.toThrow();
    expect(readVersionChoice("profile-a", "media-1")).toBe("");
  });

  it("names the default Version with the client's own label", () => {
    expect(versionLabel({ id: "v0", label: "", selected: true }, "Default")).toBe("Default");
    expect(versionLabel({ id: "v1", label: "4K", selected: false }, "Default")).toBe("4K");
  });
});
