import "@testing-library/jest-dom/vitest";
import { cleanup, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  fetchLibraries,
  type PlayerProfileAdminInfo,
  setPlayerControllers,
  setPlayerWalkUp,
  setProfileLibraries,
} from "@/lib/graphql";
import { I18nProvider } from "@/lib/i18n";
import { setAdminEpochSignal } from "../adminEpoch";
import type { AsyncState } from "../useAsync";
import { PlayerProfiles } from "./PlayerProfiles";

vi.mock("@/lib/graphql", () => ({
  fetchLibraries: vi.fn(async () => []),
  setProfileLibraries: vi.fn(),
  disableProfile: vi.fn(),
  setPlayerWalkUp: vi.fn(),
  setPlayerControllers: vi.fn(),
}));
vi.mock("@/lib/session", () => ({ useSession: () => ({ revealed: true }) }));

const household = [
  { id: "a1", name: "Alex", kind: "adult" as const },
  { id: "a2", name: "Sam", kind: "adult" as const },
  { id: "k1", name: "Kid", kind: "managed" as const },
];

const lounge: PlayerProfileAdminInfo = {
  id: "pl1",
  name: "Lounge",
  kind: "player",
  libraryIDs: [],
  disabled: false,
  walkUpEnabled: false,
  playerControllers: { profileID: "pl1", allAdults: true, include: [], exclude: [] },
};

function state(data: PlayerProfileAdminInfo[] | null, failed = false) {
  return {
    data,
    loading: false,
    failed,
    refresh: vi.fn(async () => {}),
  } as unknown as AsyncState<PlayerProfileAdminInfo[]>;
}

function renderSection(
  players = state([lounge]),
  onChanged = vi.fn(),
  profiles: typeof household | null = household,
) {
  render(
    <I18nProvider>
      <PlayerProfiles players={players} household={profiles} onChanged={onChanged} />
    </I18nProvider>,
  );
  return onChanged;
}

beforeEach(() => {
  setAdminEpochSignal(new AbortController().signal);
  vi.mocked(setPlayerWalkUp)
    .mockReset()
    .mockResolvedValue(undefined as never);
  vi.mocked(setPlayerControllers)
    .mockReset()
    .mockResolvedValue(undefined as never);
});
afterEach(() => {
  cleanup();
  setAdminEpochSignal();
});

describe("PlayerProfiles", () => {
  it("edits Library grants and never offers a restricted Library", async () => {
    const user = userEvent.setup();
    vi.mocked(fetchLibraries).mockResolvedValue([
      { id: "lib-films", title: "Films", pluginID: null, previewClips: false, restricted: false },
      {
        id: "lib-private",
        title: "Private",
        pluginID: null,
        previewClips: false,
        restricted: true,
      },
    ]);
    vi.mocked(setProfileLibraries)
      .mockReset()
      .mockResolvedValue(undefined as never);
    const onChanged = renderSection();
    const films = await screen.findByRole("checkbox", { name: /Films/ });
    expect(screen.queryByRole("checkbox", { name: /Private/ })).not.toBeInTheDocument();
    await user.click(films);
    await user.click(screen.getByRole("button", { name: "Save library access" }));
    expect(setProfileLibraries).toHaveBeenCalledWith("pl1", ["lib-films"]);
    expect(onChanged).toHaveBeenCalled();
  });

  it("drops restricted and hidden grants on the next save and says so", async () => {
    const user = userEvent.setup();
    vi.mocked(fetchLibraries).mockResolvedValue([
      { id: "lib-films", title: "Films", pluginID: null, previewClips: false, restricted: false },
      { id: "lib-music", title: "Music", pluginID: null, previewClips: false, restricted: false },
      {
        id: "lib-private",
        title: "Private",
        pluginID: null,
        previewClips: false,
        restricted: true,
      },
    ]);
    vi.mocked(setProfileLibraries)
      .mockReset()
      .mockResolvedValue(undefined as never);
    // lib-hidden is a restricted Library this owner cannot see until Reveal.
    renderSection(state([{ ...lounge, libraryIDs: ["lib-films", "lib-private", "lib-hidden"] }]));
    expect(await screen.findByRole("checkbox", { name: /Films/ })).toBeChecked();
    expect(screen.queryByRole("checkbox", { name: /Private/ })).not.toBeInTheDocument();
    expect(
      screen.getByText("Restricted Libraries are removed from this Player Profile when you save."),
    ).toBeInTheDocument();
    await user.click(screen.getByRole("checkbox", { name: /Music/ }));
    await user.click(screen.getByRole("button", { name: "Save library access" }));
    expect(setProfileLibraries).toHaveBeenCalledWith("pl1", ["lib-films", "lib-music"]);
  });

  it("says so when a grant save fails", async () => {
    const user = userEvent.setup();
    vi.mocked(fetchLibraries).mockResolvedValue([
      { id: "lib-films", title: "Films", pluginID: null, previewClips: false, restricted: false },
    ]);
    vi.mocked(setProfileLibraries).mockReset().mockRejectedValue(new Error("boom"));
    const onChanged = renderSection();
    await user.click(await screen.findByRole("checkbox", { name: /Films/ }));
    await user.click(screen.getByRole("button", { name: "Save library access" }));
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Library access was not saved. Try again.",
    );
    expect(onChanged).not.toHaveBeenCalled();
  });

  it("shows the kind as fixed and the empty state", () => {
    renderSection(state([]));
    expect(screen.getByText(/No Player Profiles yet/)).toBeInTheDocument();
    cleanup();
    renderSection();
    expect(screen.getByText("Player")).toBeInTheDocument();
    expect(screen.queryByRole("combobox")).not.toBeInTheDocument();
  });

  it("turns walk-up on only after the warning is confirmed", async () => {
    const user = userEvent.setup();
    const onChanged = renderSection();
    await user.click(screen.getByRole("button", { name: "Turn on walk-up" }));
    const dialog = await screen.findByRole("alertdialog");
    expect(within(dialog).getByText(/Anyone holding Lounge’s remote/)).toBeInTheDocument();
    expect(setPlayerWalkUp).not.toHaveBeenCalled();
    await user.click(within(dialog).getByRole("button", { name: "Turn on walk-up" }));
    expect(setPlayerWalkUp).toHaveBeenCalledWith("pl1", true, expect.anything());
    expect(onChanged).toHaveBeenCalled();
  });

  it("turns walk-up off without a confirmation", async () => {
    const user = userEvent.setup();
    renderSection(state([{ ...lounge, walkUpEnabled: true }]));
    await user.click(screen.getByRole("button", { name: "Turn off walk-up" }));
    expect(setPlayerWalkUp).toHaveBeenCalledWith("pl1", false, expect.anything());
  });

  it("saves controllers with adult exceptions and added Profiles", async () => {
    const user = userEvent.setup();
    renderSection();
    await user.click(screen.getByRole("checkbox", { name: "Sam" }));
    await user.click(screen.getByRole("checkbox", { name: "Kid" }));
    await user.click(screen.getByRole("button", { name: "Save controllers" }));
    expect(setPlayerControllers).toHaveBeenCalledWith(
      "pl1",
      { allAdults: true, include: ["k1"], exclude: ["a2"] },
      expect.anything(),
    );
    expect(await screen.findByRole("status")).toHaveTextContent("Controllers saved.");
  });

  it("drops exceptions when All adults is off", async () => {
    const user = userEvent.setup();
    renderSection(
      state([
        {
          ...lounge,
          playerControllers: { profileID: "pl1", allAdults: true, include: [], exclude: ["a2"] },
        },
      ]),
    );
    await user.click(screen.getByRole("checkbox", { name: "All adults" }));
    await user.click(screen.getByRole("checkbox", { name: "Alex" }));
    await user.click(screen.getByRole("button", { name: "Save controllers" }));
    expect(setPlayerControllers).toHaveBeenCalledWith(
      "pl1",
      { allAdults: false, include: ["a1"], exclude: [] },
      expect.anything(),
    );
  });

  it("waits for the household before saving", () => {
    renderSection(state([lounge]), vi.fn(), null);
    expect(screen.getByRole("button", { name: "Save controllers" })).toBeDisabled();
  });

  it("keeps stored rules for Profiles the checklists don't offer", async () => {
    const user = userEvent.setup();
    renderSection(
      state([
        {
          ...lounge,
          playerControllers: {
            profileID: "pl1",
            allAdults: true,
            include: ["gone"],
            exclude: ["a2", "hidden"],
          },
        },
      ]),
    );
    await user.click(screen.getByRole("checkbox", { name: "Kid" }));
    await user.click(screen.getByRole("button", { name: "Save controllers" }));
    expect(setPlayerControllers).toHaveBeenCalledWith(
      "pl1",
      { allAdults: true, include: ["gone", "k1"], exclude: ["a2", "hidden"] },
      expect.anything(),
    );
  });

  it("moves a Profile out of the other list when it is chosen", async () => {
    const user = userEvent.setup();
    renderSection(
      state([
        {
          ...lounge,
          playerControllers: { profileID: "pl1", allAdults: false, include: ["a2"], exclude: [] },
        },
      ]),
    );
    await user.click(screen.getByRole("checkbox", { name: "All adults" }));
    const exceptions = screen.getAllByRole("checkbox", { name: "Sam" })[0];
    await user.click(exceptions);
    await user.click(screen.getByRole("button", { name: "Save controllers" }));
    expect(setPlayerControllers).toHaveBeenCalledWith(
      "pl1",
      { allAdults: true, include: [], exclude: ["a2"] },
      expect.anything(),
    );
  });

  it("reports a failed save", async () => {
    const user = userEvent.setup();
    vi.mocked(setPlayerControllers).mockRejectedValueOnce(new Error("FORBIDDEN"));
    renderSection();
    await user.click(screen.getByRole("button", { name: "Save controllers" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("couldn’t be saved");
  });
});
