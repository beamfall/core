import "@testing-library/jest-dom/vitest";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
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
  setExtensionPreauthorization: vi.fn(),
  setProfileLibraries: vi.fn(),
}));
vi.mock("@/lib/session", () => ({ useSession: vi.fn() }));

beforeEach(() => {
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

  it("applies a stream cap through setProfileStreamCap", async () => {
    const user = await pickProfile("Kid");
    const capInput = await screen.findByLabelText("Concurrent streams");
    await user.type(capInput, "2");
    await user.click(screen.getByRole("button", { name: "Apply" }));
    expect(setProfileStreamCap).toHaveBeenCalledWith("p2", 2);
    expect(await screen.findByText("Applied.")).toBeInTheDocument();
  });

  it("clears the cap by sending 0 when the input is empty", async () => {
    const user = await pickProfile("Kid");
    await screen.findByLabelText("Concurrent streams");
    await user.click(screen.getByRole("button", { name: "Apply" }));
    expect(setProfileStreamCap).toHaveBeenCalledWith("p2", 0);
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
