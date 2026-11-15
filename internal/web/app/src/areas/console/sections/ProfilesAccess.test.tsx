import "@testing-library/jest-dom/vitest";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { setAdvancedSettings } from "@/lib/advancedSettings";
import {
  accessGrantsForProfile,
  fetchAccessSchedule,
  fetchEnforcedFilters,
  fetchLibraries,
  revokeAccessGrant,
  setAccessSchedule,
  setCertificationCeiling,
  setExtensionPreauthorization,
  setProfileLibraries,
  setProfileRemoteBitrateCap,
  setProfileStreamCap,
} from "@/lib/graphql";
import { I18nProvider } from "@/lib/i18n";
import { useSession } from "@/lib/session";
import { ProfilesAccess } from "./ProfilesAccess";

vi.mock("@/lib/graphql", () => ({
  fetchEnforcedFilters: vi.fn(),
  fetchAccessSchedule: vi.fn(),
  fetchLibraries: vi.fn(),
  accessGrantsForProfile: vi.fn(),
  revokeAccessGrant: vi.fn(),
  setAccessSchedule: vi.fn(),
  setCertificationCeiling: vi.fn(),
  setProfileStreamCap: vi.fn(),
  setProfileRemoteBitrateCap: vi.fn(),
  setExtensionPreauthorization: vi.fn(),
  setProfileLibraries: vi.fn(),
}));
vi.mock("@/lib/session", () => ({ useSession: vi.fn() }));

beforeEach(() => {
  // The stream cap and extension pre-auth are Advanced (CORE-1117).
  setAdvancedSettings(true);
  if (!Element.prototype.hasPointerCapture) Element.prototype.hasPointerCapture = () => false;
  if (!Element.prototype.scrollIntoView) Element.prototype.scrollIntoView = () => {};
  vi.clearAllMocks();
  vi.mocked(useSession).mockReturnValue({
    revealed: false,
  } as unknown as ReturnType<typeof useSession>);
  vi.mocked(fetchEnforcedFilters).mockResolvedValue({
    profiles: [
      {
        id: "p1",
        name: "Owner",
        kind: "adult",
        libraryIDs: ["library-local"],
        remoteMaxBitrate: 12_500_000,
        certificationCeilings: [],
      },
      {
        id: "p2",
        name: "Kid",
        kind: "managed",
        libraryIDs: ["library-local"],
        certificationCeilings: [],
      },
    ],
    plugins: [],
  });
  vi.mocked(fetchLibraries).mockResolvedValue([
    { id: "library-local", title: "Local", pluginID: null, previewClips: false, restricted: false },
    {
      id: "library-restricted",
      title: "Restricted",
      pluginID: null,
      previewClips: false,
      restricted: true,
    },
  ]);
  vi.mocked(accessGrantsForProfile).mockResolvedValue([]);
  vi.mocked(revokeAccessGrant).mockResolvedValue({
    profileID: "p2",
    mediaID: "media-1",
  });
  vi.mocked(fetchAccessSchedule).mockResolvedValue({
    profileID: "p2",
    windows: [],
  });
  vi.mocked(setAccessSchedule).mockResolvedValue({
    profileID: "p2",
    windows: [{ weekday: 0, startMinute: 0, endMinute: 1440 }],
  });
  vi.mocked(setCertificationCeiling).mockResolvedValue({
    id: "p2",
    certificationCeilings: [{ pluginID: "filmtv", ceiling: "PG", allowUnrated: false }],
  });
  vi.mocked(setProfileStreamCap).mockResolvedValue({
    id: "p2",
    maxConcurrentStreams: 2,
  });
  vi.mocked(setProfileRemoteBitrateCap).mockResolvedValue({
    id: "p2",
    remoteMaxBitrate: 8_000_000,
  });
  vi.mocked(setExtensionPreauthorization).mockResolvedValue({
    profileID: "p2",
    enabled: true,
  });
  vi.mocked(setProfileLibraries).mockResolvedValue({
    id: "p2",
    libraryIDs: ["library-local"],
  });
});

async function pickProfile(name: string) {
  const user = userEvent.setup();
  render(
    <I18nProvider>
      <ProfilesAccess />
    </I18nProvider>,
  );
  await waitFor(() => expect(fetchEnforcedFilters).toHaveBeenCalled());
  // Open the profile picker and select.
  const triggers = screen.getAllByRole("combobox");
  await user.click(triggers[0]);
  await user.click(await screen.findByRole("option", { name }));
  return user;
}

afterEach(() => {
  localStorage.clear();
  setAdvancedSettings(false);
});

describe("ProfilesAccess managed-Profile policy", () => {
  it("saves access windows through setAccessSchedule", async () => {
    const user = await pickProfile("Kid");
    await user.click(await screen.findByRole("button", { name: "Add window" }));
    await user.click(screen.getByRole("button", { name: "Save schedule" }));
    expect(setAccessSchedule).toHaveBeenCalledWith("p2", [
      { weekday: 0, startMinute: 0, endMinute: 1440 },
    ]);
  });

  it("applies certification ceilings through setCertificationCeiling", async () => {
    vi.mocked(fetchEnforcedFilters).mockResolvedValue({
      profiles: [
        {
          id: "p2",
          name: "Kid",
          kind: "managed",
          libraryIDs: ["library-local"],
          certificationCeilings: [],
        },
      ],
      plugins: [
        { id: "filmtv", displayName: { en: "Film & TV" }, certificationScale: ["G", "PG"] },
      ],
    });
    const user = await pickProfile("Kid");
    await user.click((await screen.findAllByRole("combobox"))[1]);
    await user.click(await screen.findByRole("option", { name: "PG" }));
    expect(setCertificationCeiling).toHaveBeenCalledWith("p2", "filmtv", "PG");
  });

  // Both caps carry an "Apply" button, so each test scopes to its own form.
  function formOf(input: HTMLElement) {
    const form = input.closest("form");
    if (!form) throw new Error("input is not inside a form");
    return within(form);
  }

  it("applies a stream cap through setProfileStreamCap", async () => {
    const user = await pickProfile("Kid");
    const capInput = await screen.findByLabelText("Concurrent streams");
    await user.type(capInput, "2");
    await user.click(formOf(capInput).getByRole("button", { name: "Apply" }));
    expect(setProfileStreamCap).toHaveBeenCalledWith("p2", 2);
    expect(await formOf(capInput).findByText("Applied.")).toBeInTheDocument();
  });

  it("clears the cap by sending 0 when the input is empty", async () => {
    const user = await pickProfile("Kid");
    const capInput = await screen.findByLabelText("Concurrent streams");
    await user.click(formOf(capInput).getByRole("button", { name: "Apply" }));
    expect(setProfileStreamCap).toHaveBeenCalledWith("p2", 0);
  });

  it("applies a remote bitrate cap in Mbit/s as bits per second (CORE-1717)", async () => {
    const user = await pickProfile("Kid");
    const capInput = await screen.findByLabelText("Remote bitrate cap (Mbit/s)");
    await user.type(capInput, "8");
    await user.click(formOf(capInput).getByRole("button", { name: "Apply" }));
    expect(setProfileRemoteBitrateCap).toHaveBeenCalledWith("p2", 8_000_000);
    expect(await formOf(capInput).findByText("Applied.")).toBeInTheDocument();
    expect(setProfileStreamCap).not.toHaveBeenCalled();
  });

  it("shows the Profile's stored remote bitrate cap in Mbit/s (CORE-1853)", async () => {
    await pickProfile("Owner");
    expect(await screen.findByLabelText("Remote bitrate cap (Mbit/s)")).toHaveValue(12.5);
  });

  it("leaves the remote cap input empty when the Profile has no cap", async () => {
    await pickProfile("Kid");
    expect(await screen.findByLabelText("Remote bitrate cap (Mbit/s)")).toHaveValue(null);
  });

  it("shows the saved remote cap after Apply and rereads the profiles (CORE-1853)", async () => {
    vi.mocked(setProfileRemoteBitrateCap).mockResolvedValue({
      id: "p1",
      remoteMaxBitrate: 4_000_000,
    });
    const user = await pickProfile("Owner");
    const capInput = await screen.findByLabelText("Remote bitrate cap (Mbit/s)");
    await user.clear(capInput);
    await user.type(capInput, "4");
    await user.click(formOf(capInput).getByRole("button", { name: "Apply" }));
    expect(setProfileRemoteBitrateCap).toHaveBeenCalledWith("p1", 4_000_000);
    expect(await formOf(capInput).findByText("Applied.")).toBeInTheDocument();
    expect(capInput).toHaveValue(4);
    await waitFor(() => expect(fetchEnforcedFilters).toHaveBeenCalledTimes(2));
  });

  it("shows the reread remote cap when the profile is reselected mid-Apply (CORE-1853)", async () => {
    let finishSave: (value: { id: string; remoteMaxBitrate: number }) => void = () => {};
    vi.mocked(setProfileRemoteBitrateCap).mockReturnValue(
      new Promise((resolve) => {
        finishSave = resolve;
      }),
    );
    const user = await pickProfile("Owner");
    const capInput = await screen.findByLabelText("Remote bitrate cap (Mbit/s)");
    await user.clear(capInput);
    await user.type(capInput, "4");
    await user.click(formOf(capInput).getByRole("button", { name: "Apply" }));
    // The input is locked while the save is in flight, so no edit is lost.
    expect(capInput).toBeDisabled();
    // Switch away and back before the save returns: the control remounts with
    // the pre-save profiles payload.
    for (const name of ["Kid", "Owner"]) {
      await user.click(screen.getAllByRole("combobox")[0]);
      await user.click(await screen.findByRole("option", { name }));
    }
    expect(await screen.findByLabelText("Remote bitrate cap (Mbit/s)")).toHaveValue(12.5);
    const before = await fetchEnforcedFilters();
    vi.mocked(fetchEnforcedFilters).mockResolvedValue({
      ...before,
      profiles: before.profiles.map((p) =>
        p.id === "p1" ? { ...p, remoteMaxBitrate: 4_000_000 } : p,
      ),
    });
    finishSave({ id: "p1", remoteMaxBitrate: 4_000_000 });
    await waitFor(() =>
      expect(screen.getByLabelText("Remote bitrate cap (Mbit/s)")).toHaveValue(4),
    );
  });

  it("clears the remote bitrate cap by sending 0 when the input is empty", async () => {
    const user = await pickProfile("Kid");
    const capInput = await screen.findByLabelText("Remote bitrate cap (Mbit/s)");
    await user.click(formOf(capInput).getByRole("button", { name: "Apply" }));
    expect(setProfileRemoteBitrateCap).toHaveBeenCalledWith("p2", 0);
  });

  it("shows the extension pre-auth toggle for a managed Profile and wires it", async () => {
    const user = await pickProfile("Kid");
    const toggle = await screen.findByRole("switch", {
      name: "Pre-authorize extension requests",
    });
    await user.click(toggle);
    expect(setExtensionPreauthorization).toHaveBeenCalledWith("p2", true);
  });

  it("hides the extension pre-auth toggle for an adult Profile", async () => {
    await pickProfile("Owner");
    await screen.findByLabelText("Concurrent streams");
    expect(
      screen.queryByRole("switch", {
        name: "Pre-authorize extension requests",
      }),
    ).toBeNull();
  });

  it("blocks restricted Library grants until Reveal and saves visible grants", async () => {
    const user = await pickProfile("Kid");
    await screen.findByLabelText("Concurrent streams");
    const restricted = screen.getAllByText("Reveal required")[0].closest("label");
    if (!restricted) throw new Error("expected restricted library row");
    expect(within(restricted).getByRole("checkbox")).toBeDisabled();
    await user.click(screen.getByRole("button", { name: "Save library access" }));
    expect(setProfileLibraries).toHaveBeenCalledWith("p2", ["library-local"]);
  });

  it("allows a revealed owner/admin session to grant a restricted Library to a managed Profile", async () => {
    vi.mocked(useSession).mockReturnValue({
      revealed: true,
    } as unknown as ReturnType<typeof useSession>);
    const user = await pickProfile("Kid");
    await screen.findByLabelText("Concurrent streams");
    const restricted = screen.getAllByText("Restricted")[0].closest("label");
    if (!restricted) throw new Error("expected restricted library row");
    await user.click(within(restricted).getByRole("checkbox"));
    await user.click(screen.getByRole("button", { name: "Save library access" }));
    expect(setProfileLibraries).toHaveBeenCalledWith("p2", ["library-local", "library-restricted"]);
  });

  it("revokes managed-profile access exceptions without showing titles", async () => {
    vi.mocked(accessGrantsForProfile).mockResolvedValue([
      {
        profileID: "p2",
        mediaID: "media-1",
        requestID: "req-1",
        actorProfileID: "p1",
        createdAt: "2026-06-20T00:00:00Z",
        updatedAt: "2026-06-20T00:00:00Z",
      },
    ]);
    const user = await pickProfile("Kid");
    expect(await screen.findByText("media-1")).toBeInTheDocument();
    expect(screen.queryByText("p1")).toBeNull();
    expect(screen.queryByText("req-1")).toBeNull();
    await user.click(screen.getByRole("button", { name: "Revoke" }));
    expect(
      await screen.findByText(/will lose this title-specific exception immediately/i),
    ).toBeInTheDocument();
    await user.click(
      within(await screen.findByRole("alertdialog")).getByRole("button", { name: "Revoke" }),
    );
    expect(revokeAccessGrant).toHaveBeenCalledWith("p2", "media-1");
  });

  it("clears managed grant rows when switching to another Profile", async () => {
    vi.mocked(accessGrantsForProfile).mockImplementation(async (profileID) =>
      profileID === "p2"
        ? [
            {
              profileID: "p2",
              mediaID: "media-1",
              requestID: "req-1",
              actorProfileID: "p1",
              createdAt: "2026-06-20T00:00:00Z",
              updatedAt: "2026-06-20T00:00:00Z",
            },
          ]
        : [],
    );
    const user = await pickProfile("Kid");
    expect(await screen.findByText("media-1")).toBeInTheDocument();

    await user.click(screen.getAllByRole("combobox")[0]);
    await user.click(await screen.findByRole("option", { name: "Owner" }));

    await waitFor(() => expect(screen.queryByText("media-1")).not.toBeInTheDocument());
    // The Console fetch path threads its abort signal through to the fetcher,
    // so the query carries (profileID, signal) — not the profileID alone.
    expect(accessGrantsForProfile).toHaveBeenCalledWith("p2", expect.any(AbortSignal));
  });
});

describe("ProfilesAccess in the Normal view (CORE-1117)", () => {
  beforeEach(() => setAdvancedSettings(false));

  it("keeps a managed Profile's Library access but leaves the stream cap to Advanced", async () => {
    await pickProfile("Kid");

    expect(await screen.findByRole("button", { name: "Save library access" })).toBeVisible();
    expect(screen.queryByLabelText("Concurrent streams")).toBeNull();
    expect(screen.queryByRole("switch", { name: "Pre-authorize extension requests" })).toBeNull();
  });

  it("shows an adult Profile's Library access without the Advanced stream cap", async () => {
    await pickProfile("Owner");

    expect(await screen.findByRole("button", { name: "Save library access" })).toBeVisible();
    expect(screen.queryByLabelText("Concurrent streams")).toBeNull();
  });
});
