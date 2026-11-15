// The Profile's theme preset roams through profile_client_prefs (theme.preset, CORE-1179).
import { render, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { ThemePrefsSync } from "./ThemePrefsSync";

const mocks = vi.hoisted(() => ({
  fetchClientPrefs: vi.fn(),
  setPreset: vi.fn(),
  session: { profileID: "p1" } as { profileID: string } | null,
}));

vi.mock("@/lib/graphql/viewer", () => ({ fetchClientPrefs: mocks.fetchClientPrefs }));
vi.mock("@/lib/register", () => ({ useRegister: () => ({ setPreset: mocks.setPreset }) }));
vi.mock("@/lib/session", () => ({ useSession: () => ({ session: mocks.session }) }));

beforeEach(() => {
  mocks.fetchClientPrefs.mockReset();
  mocks.setPreset.mockReset();
  mocks.session = { profileID: "p1" };
});

describe("ThemePrefsSync", () => {
  it("applies the Profile's stored preset", async () => {
    mocks.fetchClientPrefs.mockResolvedValue([{ key: "theme.preset", value: "moss" }]);
    render(<ThemePrefsSync />);
    await waitFor(() => expect(mocks.setPreset).toHaveBeenCalledWith("moss"));
  });

  it("falls back to the default preset when none or an unknown one is stored", async () => {
    mocks.fetchClientPrefs.mockResolvedValue([{ key: "theme.preset", value: "retired" }]);
    render(<ThemePrefsSync />);
    await waitFor(() => expect(mocks.setPreset).toHaveBeenCalledWith("beamfall"));
  });

  it("does nothing without a session or when the read fails", async () => {
    mocks.session = null;
    render(<ThemePrefsSync />);
    expect(mocks.fetchClientPrefs).not.toHaveBeenCalled();

    mocks.session = { profileID: "p2" };
    mocks.fetchClientPrefs.mockRejectedValue(new Error("offline"));
    render(<ThemePrefsSync />);
    await waitFor(() => expect(mocks.fetchClientPrefs).toHaveBeenCalled());
    expect(mocks.setPreset).not.toHaveBeenCalled();
  });
});
