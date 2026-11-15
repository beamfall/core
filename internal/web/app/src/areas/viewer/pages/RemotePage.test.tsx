import "@testing-library/jest-dom/vitest";
import { cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactNode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { controlSession, fetchProfileSessions } from "@/lib/graphql";
import { I18nProvider } from "@/lib/i18n";
import { RemotePage } from "./RemotePage";

vi.mock("@/components/chrome/AppShell", () => ({
  AppShell: ({ children }: { children: ReactNode }) => <>{children}</>,
}));
vi.mock("../components/ViewerActions", () => ({ ViewerActions: () => null }));
vi.mock("@/lib/graphql", () => ({
  fetchProfileSessions: vi.fn(),
  controlSession: vi.fn(),
}));

const lounge = {
  sessionID: "session-b",
  deviceID: "d2",
  deviceName: "Lounge",
  deviceRole: "viewer",
  current: false,
  mediaID: "m1",
  title: "Sample Feature",
  positionSeconds: 65,
  durationSeconds: 3600,
};

function renderPage() {
  return render(
    <I18nProvider>
      <RemotePage />
    </I18nProvider>,
  );
}

beforeEach(() => {
  vi.mocked(fetchProfileSessions)
    .mockReset()
    .mockResolvedValue([
      { ...lounge, sessionID: "session-a", deviceName: "This laptop", current: true },
      lounge,
    ]);
  vi.mocked(controlSession).mockReset().mockResolvedValue({ sessionID: "session-b", command: "" });
});

afterEach(cleanup);

describe("RemotePage", () => {
  it("lists the Profile's other sessions, not this one", async () => {
    renderPage();
    expect(await screen.findByText("Lounge")).toBeInTheDocument();
    expect(screen.queryByText("This laptop")).not.toBeInTheDocument();
    expect(screen.getByText(/Sample Feature/)).toHaveTextContent("1:05 of 1:00:00");
  });

  it("sends transport commands to the chosen session", async () => {
    renderPage();
    await userEvent.click(await screen.findByRole("button", { name: "Pause: Lounge" }));
    expect(controlSession).toHaveBeenCalledWith({ sessionID: "session-b", command: "pause" });
    await userEvent.click(screen.getByRole("button", { name: "Previous: Lounge" }));
    expect(controlSession).toHaveBeenLastCalledWith({
      sessionID: "session-b",
      command: "skip-media",
      direction: "previous",
    });
    expect(await screen.findByRole("status")).toHaveTextContent("Sent.");
  });

  it("sends a message and clears the field", async () => {
    renderPage();
    const field = await screen.findByRole("textbox", { name: "Message to show on Lounge" });
    await userEvent.type(field, "  Dinner is ready ");
    await userEvent.click(screen.getByRole("button", { name: "Send message" }));
    expect(controlSession).toHaveBeenCalledWith({
      sessionID: "session-b",
      command: "display-message",
      text: "Dinner is ready",
    });
    await waitFor(() => expect(field).toHaveValue(""));
  });

  it("reports a refused command", async () => {
    vi.mocked(controlSession).mockRejectedValueOnce(new Error("session not controllable"));
    renderPage();
    await userEvent.click(await screen.findByRole("button", { name: "Stop: Lounge" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("didn’t take the command");
  });

  it("hides a title the viewer cannot reveal and says when nothing else plays", async () => {
    vi.mocked(fetchProfileSessions).mockResolvedValueOnce([
      { ...lounge, title: undefined, mediaID: undefined, durationSeconds: 0 },
    ]);
    const { unmount } = renderPage();
    expect(await screen.findByText("Playing")).toBeInTheDocument();
    unmount();
    vi.mocked(fetchProfileSessions).mockResolvedValueOnce([]);
    renderPage();
    expect(await screen.findByText(/None of your other devices/)).toBeInTheDocument();
  });

  it("offers a retry when the sessions cannot load", async () => {
    vi.mocked(fetchProfileSessions).mockRejectedValueOnce(new Error("offline"));
    renderPage();
    expect(await screen.findByRole("alert")).toHaveTextContent("couldn’t be loaded");
    await userEvent.click(screen.getByRole("button", { name: "Refresh" }));
    expect(await screen.findByText("Lounge")).toBeInTheDocument();
  });
});
