import "@testing-library/jest-dom/vitest";
import { cleanup, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  commandPlayerDisplay,
  controlSession,
  fetchMySessions,
  fetchPlayerDisplays,
} from "@/lib/graphql";
import { I18nProvider } from "@/lib/i18n";
import { PlayOnButton } from "./PlayOnButton";

vi.mock("@/lib/graphql", () => ({
  fetchMySessions: vi.fn(),
  controlSession: vi.fn(),
  fetchPlayerDisplays: vi.fn(),
  commandPlayerDisplay: vi.fn(),
}));

const FUTURE = "2999-01-01T00:00:00Z";

function renderButton(positionSeconds?: number) {
  return render(
    <I18nProvider>
      <PlayOnButton mediaID="m1" title="Sample Feature" positionSeconds={positionSeconds} />
    </I18nProvider>,
  );
}

beforeEach(() => {
  vi.mocked(fetchMySessions)
    .mockReset()
    .mockResolvedValue({
      sessions: [
        { id: "a", deviceID: "d1", deviceName: "This laptop", deviceRole: "viewer", current: true },
        {
          id: "b",
          deviceID: "d2",
          deviceName: "Lounge",
          deviceRole: "viewer",
          current: false,
          expiresAt: FUTURE,
        },
        {
          id: "c",
          deviceID: "d3",
          deviceName: "Old tablet",
          deviceRole: "viewer",
          current: false,
          expiresAt: "2000-01-01T00:00:00Z",
        },
      ],
      streamTokens: [],
    });
  vi.mocked(controlSession)
    .mockReset()
    .mockResolvedValue({ sessionID: "b", command: "play-media" });
  vi.mocked(fetchPlayerDisplays).mockReset().mockResolvedValue([]);
  vi.mocked(commandPlayerDisplay).mockReset();
});

afterEach(cleanup);

describe("PlayOnButton", () => {
  it("loads devices only when opened and lists the live other ones", async () => {
    renderButton();
    expect(fetchMySessions).not.toHaveBeenCalled();
    const toggle = screen.getByRole("button", { name: "Play Sample Feature on another device" });
    await userEvent.click(toggle);
    expect(toggle).toHaveAttribute("aria-expanded", "true");
    expect(await screen.findByRole("button", { name: "Lounge" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "This laptop" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Old tablet" })).not.toBeInTheDocument();
  });

  it("returns focus to the trigger when the list closes", async () => {
    renderButton();
    const toggle = screen.getByRole("button", { name: /on another device/ });
    await userEvent.click(toggle);
    (await screen.findByRole("button", { name: "Lounge" })).focus();
    await userEvent.keyboard("{Escape}");
    expect(screen.queryByRole("button", { name: "Lounge" })).not.toBeInTheDocument();
    expect(toggle).toHaveFocus();

    await userEvent.click(toggle);
    await userEvent.click(await screen.findByRole("button", { name: "Lounge" }));
    expect(toggle).toHaveFocus();
  });

  it("sends play-media from the resume point and reports it", async () => {
    renderButton(42.5);
    await userEvent.click(screen.getByRole("button", { name: /on another device/ }));
    await userEvent.click(await screen.findByRole("button", { name: "Lounge" }));
    expect(controlSession).toHaveBeenCalledWith({
      sessionID: "b",
      command: "play-media",
      mediaID: "m1",
      positionMs: 42_500,
    });
    expect(await screen.findByRole("status")).toHaveTextContent("Sent to Lounge.");
    expect(screen.queryByRole("button", { name: "Lounge" })).not.toBeInTheDocument();
  });

  it("says when no other device is signed in", async () => {
    vi.mocked(fetchMySessions).mockResolvedValueOnce({
      sessions: [{ id: "a", deviceID: "d1", deviceName: "", deviceRole: "viewer", current: true }],
      streamTokens: [],
    });
    renderButton();
    await userEvent.click(screen.getByRole("button", { name: /on another device/ }));
    expect(await screen.findByRole("status")).toHaveTextContent("No other device");
  });

  it("reports a device that could not start", async () => {
    vi.mocked(controlSession).mockRejectedValueOnce(new Error("unknown session"));
    renderButton();
    await userEvent.click(screen.getByRole("button", { name: /on another device/ }));
    await userEvent.click(await screen.findByRole("button", { name: "Lounge" }));
    expect(await screen.findByRole("status")).toHaveTextContent("couldn’t start playing");
  });

  describe("player displays", () => {
    const base = {
      playerProfileID: "p1",
      currentItem: null,
    } as const;

    beforeEach(() => {
      vi.mocked(fetchMySessions).mockResolvedValue({ sessions: [], streamTokens: [] });
      vi.mocked(fetchPlayerDisplays).mockResolvedValue([
        { ...base, handle: "h1", sessionID: "s1", displayName: "Den TV", presence: "ready" },
        {
          ...base,
          handle: "h2",
          sessionID: "s2",
          displayName: "Kids TV",
          presence: "playing",
          currentItem: { privateItem: true, positionSeconds: 10, durationSeconds: 100 },
        },
        { ...base, handle: "h3", displayName: "Garage TV", presence: "offline" },
      ]);
      vi.mocked(commandPlayerDisplay).mockResolvedValue({
        status: "applied",
        sessionID: "s1",
        command: "play-media",
      } as never);
    });

    it("lists presence, hides private items and offers no action offline", async () => {
      renderButton();
      await userEvent.click(screen.getByRole("button", { name: /on another device/ }));
      expect(await screen.findByRole("button", { name: /Den TV/ })).toHaveTextContent("Ready");
      expect(screen.getByRole("button", { name: /Kids TV/ })).toHaveTextContent("Private item");
      expect(screen.getByText("Open Beamfall on Garage TV")).toBeInTheDocument();
      expect(screen.queryByRole("button", { name: /Garage TV/ })).not.toBeInTheDocument();
    });

    it("plays on a ready display through its Session", async () => {
      renderButton(42.5);
      await userEvent.click(screen.getByRole("button", { name: /on another device/ }));
      await userEvent.click(await screen.findByRole("button", { name: /Den TV/ }));
      expect(commandPlayerDisplay).toHaveBeenCalledWith({
        sessionID: "s1",
        command: "play-media",
        mediaID: "m1",
        positionMs: 42_500,
      });
      expect(await screen.findByRole("status")).toHaveTextContent("Sent to Den TV.");
    });

    it("says when a display went offline", async () => {
      vi.mocked(commandPlayerDisplay).mockRejectedValueOnce(new Error("display offline"));
      renderButton();
      await userEvent.click(screen.getByRole("button", { name: /on another device/ }));
      await userEvent.click(await screen.findByRole("button", { name: /Den TV/ }));
      expect(await screen.findByRole("status")).toHaveTextContent("Den TV went offline.");
    });
  });
});
