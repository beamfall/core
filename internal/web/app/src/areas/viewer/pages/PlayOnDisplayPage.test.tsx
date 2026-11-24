import "@testing-library/jest-dom/vitest";
import { cleanup, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactNode } from "react";
import { MemoryRouter } from "react-router";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { commandPlayerDisplay, fetchPlayerDisplays } from "@/lib/graphql";
import type { PlayerDisplayInfo } from "@/lib/graphql/types";
import { I18nProvider } from "@/lib/i18n";
import { PlayOnDisplayPage } from "./PlayOnDisplayPage";

vi.mock("@/components/chrome/AppShell", () => ({
  AppShell: ({ children }: { children: ReactNode }) => <main>{children}</main>,
}));
vi.mock("../components/ViewerActions", () => ({ ViewerActions: () => null }));
vi.mock("@/lib/graphql", () => ({
  fetchPlayerDisplays: vi.fn(),
  commandPlayerDisplay: vi.fn(),
}));

const LOUNGE: PlayerDisplayInfo = {
  handle: "h-lounge",
  sessionID: "s1",
  playerProfileID: "p1",
  displayName: "Lounge TV",
  presence: "playing",
  currentItem: {
    mediaID: "m1",
    title: "Sample Feature",
    positionSeconds: 65,
    durationSeconds: 600,
  },
};

function renderAt(search: string) {
  return render(
    <I18nProvider>
      <MemoryRouter initialEntries={[`/play-on${search}`]}>
        <PlayOnDisplayPage />
      </MemoryRouter>
    </I18nProvider>,
  );
}

beforeEach(() => {
  vi.mocked(fetchPlayerDisplays).mockReset().mockResolvedValue([LOUNGE]);
  vi.mocked(commandPlayerDisplay)
    .mockReset()
    .mockResolvedValue({ status: "applied", sessionID: "s1", command: "pause" } as never);
});

afterEach(cleanup);

describe("PlayOnDisplayPage", () => {
  it("shows the same message for a missing handle without loading displays", async () => {
    renderAt("");
    expect(await screen.findByRole("alert")).toHaveTextContent("This display isn’t available.");
    expect(fetchPlayerDisplays).not.toHaveBeenCalled();
  });

  it("shows the same message for a handle the caller does not control", async () => {
    renderAt("?display=h-other");
    expect(await screen.findByRole("alert")).toHaveTextContent("This display isn’t available.");
    expect(screen.queryByText("Lounge TV")).not.toBeInTheDocument();
  });

  it("shows the same message when the lookup fails", async () => {
    vi.mocked(fetchPlayerDisplays).mockRejectedValueOnce(new Error("forbidden"));
    renderAt("?display=h-lounge");
    expect(await screen.findByRole("alert")).toHaveTextContent("This display isn’t available.");
  });

  it("resolves a controlled display and sends commands to its Session", async () => {
    renderAt("?display=h-lounge");
    expect(await screen.findByRole("heading", { name: "Lounge TV" })).toBeInTheDocument();
    expect(screen.getByText("Playing Sample Feature")).toBeInTheDocument();
    expect(screen.getByText("1:05 of 10:00")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: /Pause/ }));
    expect(commandPlayerDisplay).toHaveBeenCalledWith({ sessionID: "s1", command: "pause" });
  });

  it("offers no controls for an offline display", async () => {
    vi.mocked(fetchPlayerDisplays).mockResolvedValue([
      { ...LOUNGE, presence: "offline", sessionID: undefined, currentItem: null },
    ]);
    renderAt("?display=h-lounge");
    expect(await screen.findByText("Open Beamfall on Lounge TV")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /Pause/ })).not.toBeInTheDocument();
  });

  it("explains a display that is showing a party", async () => {
    vi.mocked(commandPlayerDisplay).mockRejectedValueOnce(new Error("display in party"));
    renderAt("?display=h-lounge");
    await userEvent.click(await screen.findByRole("button", { name: /Stop/ }));
    expect(await screen.findByText("Lounge TV is showing a party right now.")).toBeInTheDocument();
  });
});
