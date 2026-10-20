import { beforeEach, describe, expect, it, vi } from "vitest";
import { graphql } from "@/lib/api";
import { rebindCompatibility } from "@/lib/compat";
import { relock, reveal, switchProfile } from "./viewer";

vi.mock("@/lib/api", () => ({
  getManifest: vi.fn(),
  graphql: vi.fn(),
}));

vi.mock("@/lib/compat", () => ({
  rebindCompatibility: vi.fn(),
}));

beforeEach(() => {
  vi.mocked(graphql).mockReset();
  vi.mocked(rebindCompatibility).mockReset();
  vi.mocked(rebindCompatibility).mockResolvedValue({
    decision: "compatible",
    messageClass: "compat-compatible",
  });
});

describe("viewer authorization compatibility epochs", () => {
  it.each([
    [
      "profile switch",
      () => switchProfile("profile-2"),
      { switchProfile: { profileID: "profile-2", revealed: false } },
    ],
    ["successful Reveal", () => reveal("1234"), { reveal: { revealed: true } }],
    ["Relock", () => relock(), { relock: { revealed: false } }],
  ] as const)("rebinds after %s changes the auth fingerprint", async (_name, mutate, response) => {
    vi.mocked(graphql).mockResolvedValue(response);

    await mutate();

    expect(rebindCompatibility).toHaveBeenCalledOnce();
    expect(vi.mocked(graphql).mock.invocationCallOrder[0]).toBeLessThan(
      vi.mocked(rebindCompatibility).mock.invocationCallOrder[0],
    );
  });

  it("does not rebind when Reveal is refused", async () => {
    vi.mocked(graphql).mockResolvedValue({ reveal: { revealed: false } });

    await reveal("wrong");

    expect(rebindCompatibility).not.toHaveBeenCalled();
  });

  it.each([
    [
      "Profile switch",
      (onCommitted: () => void) => switchProfile("profile-2", onCommitted),
      { switchProfile: { profileID: "profile-2", revealed: false } },
    ],
    ["Relock", (onCommitted: () => void) => relock(onCommitted), { relock: { revealed: false } }],
  ] as const)("reports the %s commit before a rejected rebind", async (_name, mutate, response) => {
    const onCommitted = vi.fn();
    vi.mocked(graphql).mockResolvedValue(response);
    vi.mocked(rebindCompatibility).mockRejectedValue(new Error("offline"));

    await expect(mutate(onCommitted)).rejects.toThrow("offline");

    expect(onCommitted).toHaveBeenCalledOnce();
    expect(vi.mocked(graphql).mock.invocationCallOrder[0]).toBeLessThan(
      onCommitted.mock.invocationCallOrder[0],
    );
    expect(onCommitted.mock.invocationCallOrder[0]).toBeLessThan(
      vi.mocked(rebindCompatibility).mock.invocationCallOrder[0],
    );
  });
});
