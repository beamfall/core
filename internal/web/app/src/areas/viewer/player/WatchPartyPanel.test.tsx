import "@testing-library/jest-dom/vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { I18nProvider } from "@/lib/i18n";
import { MAX_DISPLAY_TRIM_MS, setDisplayTrimMS } from "./displayTrim";
import {
  formatDisplayTrim,
  formatDrift,
  WatchPartyPanel,
  WatchPartySyncIndicator,
} from "./WatchPartyPanel";
import {
  decidePartyRequest,
  endParty,
  followLeadParty,
  inviteToParty,
  type WatchPartyState,
} from "./watchPartyStore";

// The party the indicator reads from the store (the panel takes it as a prop).
let storeParty: WatchPartyState;

vi.mock("./watchPartyStore", () => ({
  inviteToParty: vi.fn(async () => true),
  endParty: vi.fn(async () => {}),
  decidePartyRequest: vi.fn(async () => true),
  clearWatchPartyNotice: vi.fn(),
  followLeadParty: vi.fn(async () => "m1"),
  useWatchPartyState: () => storeParty,
}));

const idle: WatchPartyState = {
  status: "idle",
  partyID: "",
  isHost: false,
  lead: false,
  leadOffer: null,
  timeline: null,
  members: [],
  invited: [],
  offsetMS: 0,
  error: "",
  errorDetail: "",
  notice: "",
  noticeName: "",
  requests: [],
};

beforeEach(() => {
  storeParty = idle;
  vi.mocked(endParty).mockClear();
  vi.mocked(decidePartyRequest).mockClear();
  vi.mocked(followLeadParty).mockClear();
  setDisplayTrimMS(0);
});

function renderPanel(party: WatchPartyState, onStart = vi.fn(async () => true)) {
  const onOpenChange = vi.fn();
  const onLeave = vi.fn(async () => {});
  render(
    <I18nProvider>
      <WatchPartyPanel
        party={party}
        open
        onOpenChange={onOpenChange}
        onStart={onStart}
        onLeave={onLeave}
      />
    </I18nProvider>,
  );
  return { onOpenChange, onLeave, onStart };
}

describe("WatchPartyPanel", () => {
  it("starts a party from the title playing", async () => {
    const { onStart } = renderPanel(idle);
    await userEvent.click(screen.getByRole("button", { name: "Start a Watch Party" }));
    expect(onStart).toHaveBeenCalledWith(true);
  });

  it("offers host-only control at start (CORE-1765)", async () => {
    const { onStart } = renderPanel(idle);
    expect(screen.getByRole("radio", { name: "Everyone" })).toBeChecked();
    await userEvent.click(screen.getByRole("radio", { name: "Only me" }));
    await userEvent.click(screen.getByRole("button", { name: "Start a Watch Party" }));
    expect(onStart).toHaveBeenCalledWith(false);
  });

  it("lets the host approve or decline a member's request (CORE-1765)", async () => {
    renderPanel({
      ...idle,
      status: "active",
      partyID: "p1",
      isHost: true,
      members: ["Host", "Guest"],
      requests: [
        { id: "r1", from: "Guest", item: "m1", positionMS: 83000, paused: false },
        { id: "r2", from: "Guest", item: "m1", positionMS: 5000, paused: true },
      ],
    });
    expect(screen.getByText("Guest asks to play from 1:23")).toBeVisible();
    expect(screen.getByText("Guest asks to pause at 0:05")).toBeVisible();
    await userEvent.click(screen.getAllByRole("button", { name: "Approve" })[0]);
    expect(decidePartyRequest).toHaveBeenCalledWith("r1", true);
    await userEvent.click(screen.getAllByRole("button", { name: "Decline" })[1]);
    expect(decidePartyRequest).toHaveBeenCalledWith("r2", false);
  });

  it("shows no requests to a member", () => {
    renderPanel({
      ...idle,
      status: "active",
      partyID: "p1",
      members: ["Host", "Guest"],
      requests: [{ id: "r1", from: "Guest", item: "m1", positionMS: 0, paused: false }],
    });
    expect(screen.queryByRole("button", { name: "Approve" })).toBeNull();
  });

  it("shows members, invites a profile by name and leaves", async () => {
    const { onLeave } = renderPanel({
      ...idle,
      status: "active",
      partyID: "p1",
      members: ["Host", "Guest"],
      invited: ["Third"],
    });

    expect(screen.getByRole("button", { name: /Watch Party · 2/ })).toBeVisible();
    expect(screen.getByText("Guest")).toBeVisible();
    expect(screen.getByText("Third (invited)")).toBeVisible();

    await userEvent.type(screen.getByLabelText("Invite a profile"), "Fourth");
    await userEvent.click(screen.getByRole("button", { name: "Invite" }));
    expect(inviteToParty).toHaveBeenCalledWith("Fourth");

    await userEvent.click(screen.getByRole("button", { name: "Leave party" }));
    expect(onLeave).toHaveBeenCalled();
    // Only the host can end the party for everyone.
    expect(screen.queryByRole("button", { name: "End for everyone" })).toBeNull();
  });

  it("lets the host end the party for everyone", async () => {
    renderPanel({ ...idle, status: "active", partyID: "p1", isHost: true, members: ["Host"] });
    await userEvent.click(screen.getByRole("button", { name: "End for everyone" }));
    expect(endParty).toHaveBeenCalled();
  });

  describe("lead and follower screens (CORE-2245)", () => {
    const leadParty: WatchPartyState = {
      ...idle,
      status: "active",
      partyID: "p1",
      lead: true,
      members: ["Den", "Den", "Den"],
    };

    it("starts a lead party, saying what leading means", async () => {
      const { onStart } = renderPanel(idle);
      expect(screen.queryByText(/This screen plays the sound/)).toBeNull();
      await userEvent.click(screen.getByRole("radio", { name: "Lead for other screens" }));
      expect(
        screen.getByText(
          "This screen plays the sound. Other screens follow it, muted and in step.",
        ),
      ).toBeVisible();
      await userEvent.click(screen.getByRole("button", { name: "Start a Watch Party" }));
      expect(onStart).toHaveBeenCalledWith(false, true);
    });

    it("offers nothing when no other screen on this profile leads", () => {
      renderPanel(idle);
      expect(screen.queryByRole("button", { name: "Follow it" })).toBeNull();
    });

    it("follows the offered lead screen, also one it left before", async () => {
      renderPanel({ ...idle, leadOffer: { partyID: "p9", item: "m2", declined: true } });
      expect(screen.getByText("Another screen on this profile is leading.")).toBeVisible();
      await userEvent.click(screen.getByRole("button", { name: "Follow it" }));
      expect(followLeadParty).toHaveBeenCalledTimes(1);
    });

    it("tells the lead that it leads and how many screens follow", () => {
      renderPanel({ ...leadParty, isHost: true });
      expect(screen.getByText("This screen leads")).toBeVisible();
      expect(screen.getByText("2 screens following")).toBeVisible();
      // The lead still invites and ends the party.
      expect(screen.getByLabelText("Invite a profile")).toBeVisible();
      expect(screen.getByRole("button", { name: "End for everyone" })).toBeVisible();
      expect(screen.queryByRole("group", { name: "Display delay" })).toBeNull();
    });

    it.each([
      [["Den"], "No screens following"],
      [["Den", "Den"], "1 screen following"],
    ])("counts %j as %s", (members, label) => {
      renderPanel({ ...leadParty, isHost: true, members });
      expect(screen.getByText(label)).toBeVisible();
    });

    it("tells a follower whom it follows, that it is muted, and lets it leave", async () => {
      const { onLeave } = renderPanel(leadParty);
      expect(screen.getByRole("heading", { name: "Following Den" })).toBeVisible();
      expect(
        screen.getByText("Sound plays from the lead screen. This screen is muted."),
      ).toBeVisible();
      // A follower neither invites nor ends the party, and is not told it leads.
      expect(screen.queryByLabelText("Invite a profile")).toBeNull();
      expect(screen.queryByRole("button", { name: "End for everyone" })).toBeNull();
      expect(screen.queryByText("This screen leads")).toBeNull();
      await userEvent.click(screen.getByRole("button", { name: "Leave party" }));
      expect(onLeave).toHaveBeenCalled();
    });

    it("names no lead when the members carry different names", () => {
      renderPanel({ ...leadParty, members: ["Den", "Guest"] });
      expect(screen.getByRole("heading", { name: "Following the lead screen" })).toBeVisible();
    });

    it("steps a follower's display delay 10 ms at a time and resets it", async () => {
      renderPanel(leadParty);
      const trim = screen.getByRole("group", { name: "Display delay" });
      expect(trim).toHaveAccessibleDescription("Raise it if this screen's picture lags the sound.");
      const readout = screen.getByRole("status");
      const more = screen.getByRole("button", { name: "Increase display delay" });
      const less = screen.getByRole("button", { name: "Decrease display delay" });
      const reset = screen.getByRole("button", { name: "Reset" });
      expect(readout).toHaveTextContent("0 ms");
      expect(reset).toHaveAttribute("aria-disabled", "true");

      await userEvent.click(more);
      await userEvent.click(more);
      expect(readout).toHaveTextContent("+20 ms");
      expect(readout).toHaveAttribute("data-display-trim", "20");
      expect(reset).not.toHaveAttribute("aria-disabled");

      await userEvent.click(reset);
      expect(readout).toHaveTextContent("0 ms");
      await userEvent.click(less);
      expect(readout).toHaveTextContent("−10 ms");
      expect(window.localStorage.getItem("beamfall.followerDisplayTrimMS")).toBe("-10");
    });

    it("stops at the end of the range without dropping the control", async () => {
      setDisplayTrimMS(MAX_DISPLAY_TRIM_MS);
      renderPanel(leadParty);
      const more = screen.getByRole("button", { name: "Increase display delay" });
      expect(more).toHaveAttribute("aria-disabled", "true");
      expect(more).toBeEnabled();
      await userEvent.click(more);
      expect(screen.getByRole("status")).toHaveAttribute("data-display-trim", "500");
      expect(screen.getByRole("button", { name: "Decrease display delay" })).not.toHaveAttribute(
        "aria-disabled",
      );
    });

    it("formats the display delay", () => {
      expect(formatDisplayTrim(0)).toBe("0 ms");
      expect(formatDisplayTrim(20)).toBe("+20 ms");
      expect(formatDisplayTrim(-10)).toBe("−10 ms");
    });
  });

  it("explains the home-network limit", () => {
    renderPanel({ ...idle, error: "home-network" });
    expect(screen.getByRole("alert")).toHaveTextContent(
      "Watch Party works on your home network only.",
    );
  });
});

describe("WatchPartySyncIndicator", () => {
  function renderIndicator(props: Partial<Parameters<typeof WatchPartySyncIndicator>[0]>) {
    return render(
      <I18nProvider>
        <WatchPartySyncIndicator
          playback="playing"
          driftMS={0}
          needsTap={false}
          onResume={() => {}}
          {...props}
        />
      </I18nProvider>,
    );
  }

  it("shows the drift level while playing", () => {
    renderIndicator({ driftMS: 20 });
    expect(screen.getByRole("status")).toHaveTextContent("In sync");
    expect(screen.getByRole("status")).toHaveAttribute("data-drift-level", "in-sync");
  });

  it("shows an adjusting and a resyncing drift with its size", () => {
    const { unmount } = renderIndicator({ driftMS: -240 });
    expect(screen.getByRole("status")).toHaveTextContent("Adjusting −240 ms");
    unmount();
    renderIndicator({ driftMS: 1500 });
    expect(screen.getByRole("status")).toHaveTextContent("Catching up +1.5 s");
    expect(screen.getByRole("status")).toHaveAttribute("data-drift-level", "resyncing");
  });

  it("holds a follower screen to the tighter lead thresholds (CORE-2245)", () => {
    storeParty = { ...idle, status: "active", partyID: "p1", lead: true, members: ["Den", "Den"] };
    // 60 ms is in sync for an ordinary party, not for a screen under the lead's sound.
    const { unmount } = renderIndicator({ driftMS: 60 });
    expect(screen.getByRole("status")).toHaveTextContent("Adjusting +60 ms");
    unmount();
    const locked = renderIndicator({ driftMS: -40 });
    expect(screen.getByRole("status")).toHaveTextContent("In sync");
    locked.unmount();
    renderIndicator({ driftMS: 300 });
    expect(screen.getByRole("status")).toHaveAttribute("data-drift-level", "resyncing");
  });

  it("shows the lead how many screens follow instead of a drift (CORE-2245)", () => {
    storeParty = {
      ...idle,
      status: "active",
      partyID: "p1",
      lead: true,
      isHost: true,
      members: ["Den", "Den", "Den"],
    };
    renderIndicator({ driftMS: null, playback: "paused" });
    expect(screen.getByRole("status")).toHaveTextContent("2 screens following");
    expect(screen.getByRole("status")).toHaveAttribute("data-drift-level", "in-sync");
  });

  it("asks for a tap when the browser blocks playback", async () => {
    const onResume = vi.fn();
    renderIndicator({ needsTap: true, onResume });
    await userEvent.click(screen.getByRole("button", { name: "Tap to join playback" }));
    expect(onResume).toHaveBeenCalled();
  });

  it("renders nothing outside a party", () => {
    const { container } = renderIndicator({ playback: "off" });
    expect(container).toBeEmptyDOMElement();
  });

  it("tells a member who took over as host, and the new host that it is them", () => {
    storeParty = { ...idle, status: "active", notice: "handoff", noticeName: "Guest" };
    const { unmount } = renderIndicator({});
    expect(screen.getByRole("status")).toHaveTextContent("Guest is now hosting");
    unmount();
    storeParty = { ...storeParty, isHost: true };
    renderIndicator({});
    expect(screen.getByRole("status")).toHaveTextContent("You're now hosting");
  });

  it("tells every member that the host ended the party, even with playback off", () => {
    storeParty = { ...idle, notice: "ended", noticeName: "Host" };
    renderIndicator({ playback: "off" });
    expect(screen.getByRole("status")).toHaveTextContent("Host ended the Watch Party");
    expect(screen.getByRole("status")).toHaveAttribute("data-party-notice", "ended");
  });

  it("formats drift with a sign", () => {
    expect(formatDrift(120.4)).toBe("+120 ms");
    expect(formatDrift(-1234)).toBe("−1.2 s");
  });
});
