import "@testing-library/jest-dom/vitest";
import { cleanup, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  type PartyScreen,
  type PartySession,
  partyScreenAddDisplay,
  partyScreenLead,
} from "@/lib/graphql";
import { I18nProvider } from "@/lib/i18n";
import { PartyScreens } from "./HostLive";

vi.mock("@/lib/graphql", () => ({
  partyScreenAddDisplay: vi.fn(),
  partyScreenLead: vi.fn(),
  partyScreenDrop: vi.fn(),
  partyScreenTicker: vi.fn(),
}));

const screenBase: PartyScreen = {
  id: "sc1",
  name: "Lounge TV",
  kind: "joined" as PartyScreen["kind"],
  carrier: "web" as PartyScreen["carrier"],
  lead: true,
  ticker: false,
  offsetMS: 0,
  rttMS: 0,
  reportedAt: "",
};

function session(over: Partial<PartySession> = {}): PartySession {
  return {
    id: "party1",
    screens: [
      { ...screenBase, playerDisplay: true },
      { ...screenBase, id: "sc2", name: "Phone", lead: false },
    ],
    availableDisplays: [
      {
        handle: "h-den",
        displayName: "Den TV",
        presence: "ready",
        joined: false,
        coversLibraries: false,
        missingLibraryIDs: ["l1", "l2"],
      },
      {
        handle: "h-garage",
        displayName: "Garage TV",
        presence: "offline",
        joined: false,
        coversLibraries: true,
        missingLibraryIDs: [],
      },
      {
        handle: "h-lounge",
        displayName: "Lounge TV",
        presence: "playing",
        joined: true,
        coversLibraries: true,
        missingLibraryIDs: [],
      },
    ],
    ...over,
  } as PartySession;
}

function renderScreens(s = session()) {
  const run = vi.fn(async (op: () => Promise<unknown>) => {
    await op();
  });
  render(
    <I18nProvider>
      <MemoryRouter>
        <PartyScreens session={s} run={run as never} />
      </MemoryRouter>
    </I18nProvider>,
  );
  return run;
}

beforeEach(() => {
  vi.mocked(partyScreenAddDisplay)
    .mockReset()
    .mockResolvedValue({ session: session() } as never);
  vi.mocked(partyScreenLead)
    .mockReset()
    .mockResolvedValue(session() as never);
});
afterEach(cleanup);

describe("PartyScreens player displays", () => {
  it("marks the lead and the player display, and offers Make lead for the others", async () => {
    renderScreens();
    expect(screen.getByText("Lead")).toBeInTheDocument();
    expect(screen.getAllByText(/Player display/).length).toBeGreaterThan(0);
    await userEvent.click(screen.getByRole("button", { name: "Make Phone the lead screen" }));
    expect(partyScreenLead).toHaveBeenCalledWith("sc2");
  });

  it("lists available displays with presence and library coverage", async () => {
    renderScreens();
    expect(screen.getByText(/missing 2 of this party’s Libraries/)).toBeInTheDocument();
    expect(screen.getByText("Open Beamfall on Garage TV")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Add Garage TV as a screen" })).toBeDisabled();
    expect(screen.getByText("Joined")).toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: "Add Lounge TV as a screen" }),
    ).not.toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Add Den TV as a screen" }));
    expect(partyScreenAddDisplay).toHaveBeenCalledWith("h-den");
  });

  it("hides the list when no display is available", () => {
    renderScreens(session({ availableDisplays: [] }));
    expect(screen.queryByText("Player displays")).not.toBeInTheDocument();
  });
});
