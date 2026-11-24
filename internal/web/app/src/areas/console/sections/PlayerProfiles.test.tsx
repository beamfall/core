import "@testing-library/jest-dom/vitest";
import { cleanup, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { type PlayerProfileAdminInfo, setPlayerControllers, setPlayerWalkUp } from "@/lib/graphql";
import { I18nProvider } from "@/lib/i18n";
import { setAdminEpochSignal } from "../adminEpoch";
import type { AsyncState } from "../useAsync";
import { PlayerProfiles } from "./PlayerProfiles";

vi.mock("@/lib/graphql", () => ({
  disableProfile: vi.fn(),
  setPlayerWalkUp: vi.fn(),
  setPlayerControllers: vi.fn(),
}));

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

  it("reports a failed save", async () => {
    const user = userEvent.setup();
    vi.mocked(setPlayerControllers).mockRejectedValueOnce(new Error("FORBIDDEN"));
    renderSection();
    await user.click(screen.getByRole("button", { name: "Save controllers" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("couldn’t be saved");
  });
});
