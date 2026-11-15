import "@testing-library/jest-dom/vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { I18nProvider } from "@/lib/i18n";
import { formatDrift, WatchPartyPanel, WatchPartySyncIndicator } from "./WatchPartyPanel";
import { inviteToParty, type WatchPartyState } from "./watchPartyStore";

vi.mock("./watchPartyStore", () => ({ inviteToParty: vi.fn(async () => true) }));

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
};

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
    expect(onStart).toHaveBeenCalled();
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

  it("formats drift with a sign", () => {
    expect(formatDrift(120.4)).toBe("+120 ms");
    expect(formatDrift(-1234)).toBe("−1.2 s");
  });
});
