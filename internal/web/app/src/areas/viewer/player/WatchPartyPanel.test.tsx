import "@testing-library/jest-dom/vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { I18nProvider } from "@/lib/i18n";
import { formatDrift, WatchPartyPanel, WatchPartySyncIndicator } from "./WatchPartyPanel";
import {
  decidePartyRequest,
  endParty,
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
  useWatchPartyState: () => storeParty,
}));

const idle: WatchPartyState = {
  status: "idle",
  partyID: "",
  isHost: false,
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
