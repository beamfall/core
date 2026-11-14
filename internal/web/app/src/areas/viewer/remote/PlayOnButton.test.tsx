import "@testing-library/jest-dom/vitest";
import { cleanup, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { controlSession, fetchMySessions } from "@/lib/graphql";
import { I18nProvider } from "@/lib/i18n";
import { PlayOnButton } from "./PlayOnButton";

vi.mock("@/lib/graphql", () => ({ fetchMySessions: vi.fn(), controlSession: vi.fn() }));

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
});
