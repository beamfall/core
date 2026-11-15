import "@testing-library/jest-dom/vitest";
import { act, cleanup, fireEvent, render, screen } from "@testing-library/react";
import { MemoryRouter, Route, Routes, useLocation } from "react-router";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { calibrateWatchPartyClock } from "@/lib/graphql";
import { I18nProvider } from "@/lib/i18n";
import { readWatchPartyIntent, resetHomeNetworkProbeForTest } from "../lib/watchTogether";
import { getWatchParty, startWatchParty } from "../player/watchPartyStore";
import { WatchTogetherButton } from "./WatchTogetherButton";

const party = vi.hoisted(() => ({ status: "idle", error: "" }));

vi.mock("@/lib/graphql", () => ({ calibrateWatchPartyClock: vi.fn() }));
vi.mock("../player/watchPartyStore", () => ({
  startWatchParty: vi.fn(),
  getWatchParty: vi.fn(() => party),
  useWatchPartyState: () => party,
}));

function PlayerProbe() {
  const location = useLocation();
  return <output aria-label="player">{JSON.stringify(location.state)}</output>;
}

async function renderButton(positionSeconds?: number) {
  render(
    <I18nProvider>
      <MemoryRouter initialEntries={["/media/m1"]}>
        <Routes>
          <Route
            path="/media/:id"
            element={
              <WatchTogetherButton
                mediaID="m1"
                title="Lake Day"
                positionSeconds={positionSeconds}
              />
            }
          />
          <Route path="/watch/:id" element={<PlayerProbe />} />
        </Routes>
      </MemoryRouter>
    </I18nProvider>,
  );
  // Let the home-network probe settle.
  await act(async () => {});
}

beforeEach(() => {
  resetHomeNetworkProbeForTest();
  party.status = "idle";
  party.error = "";
  vi.mocked(calibrateWatchPartyClock)
    .mockReset()
    .mockResolvedValue({ offsetMS: 0, roundTripMS: 3 });
  vi.mocked(startWatchParty).mockReset();
  vi.mocked(getWatchParty).mockClear();
});

afterEach(cleanup);

describe("WatchTogetherButton", () => {
  it("is not offered off the home network", async () => {
    vi.mocked(calibrateWatchPartyClock).mockRejectedValue(new Error("home network only"));
    await renderButton();
    expect(screen.queryByRole("button", { name: "Watch Lake Day together" })).toBeNull();
  });

  it("is a ghost secondary on the home network, probed once per tab", async () => {
    await renderButton();
    const button = screen.getByRole("button", { name: "Watch Lake Day together" });
    expect(button).toHaveTextContent("Watch together");
    expect(button.className).not.toContain("bg-primary");
    expect(button.className).toContain("h-11");
    cleanup();
    await renderButton();
    expect(screen.getByRole("button", { name: "Watch Lake Day together" })).toBeInTheDocument();
    expect(calibrateWatchPartyClock).toHaveBeenCalledTimes(1);
  });

  it("is not offered while a party is already running", async () => {
    party.status = "active";
    await renderButton();
    expect(screen.queryByRole("button", { name: "Watch Lake Day together" })).toBeNull();
  });

  it("starts the party at the viewer's position and opens the player with the invite panel", async () => {
    vi.mocked(startWatchParty).mockResolvedValue(true);
    await renderButton(42.5);
    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: "Watch Lake Day together" }));
    });
    expect(startWatchParty).toHaveBeenCalledWith("m1", 42_500);
    const state = JSON.parse(screen.getByRole("status", { name: "player" }).textContent ?? "null");
    expect(state).toMatchObject({ title: "Lake Day", skipPrePlay: true });
    expect(readWatchPartyIntent(state)).toBe(true);
  });

  it("starts from the beginning when there is no saved position", async () => {
    vi.mocked(startWatchParty).mockResolvedValue(true);
    await renderButton();
    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: "Watch Lake Day together" }));
    });
    expect(startWatchParty).toHaveBeenCalledWith("m1", 0);
  });

  it("stays on the page with a home-network notice when the server refuses", async () => {
    vi.mocked(startWatchParty).mockImplementation(async () => {
      party.error = "home-network";
      return false;
    });
    await renderButton();
    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: "Watch Lake Day together" }));
    });
    expect(screen.getByRole("alert")).toHaveTextContent(
      "Watch Party works only on your home network.",
    );
    expect(screen.queryByRole("status", { name: "player" })).toBeNull();
  });

  it("says so when the party could not be started for another reason", async () => {
    vi.mocked(startWatchParty).mockImplementation(async () => {
      party.error = "failed";
      return false;
    });
    await renderButton();
    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: "Watch Lake Day together" }));
    });
    expect(screen.getByRole("alert")).toHaveTextContent(
      "The Watch Party couldn’t be started. Try again.",
    );
  });
});
