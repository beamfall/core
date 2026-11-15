import "@testing-library/jest-dom/vitest";
import { cleanup, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { fetchServerLogs } from "@/lib/graphql";
import { I18nProvider } from "@/lib/i18n";
import { ServerLogs, serverLogBundle } from "./ServerLogs";

vi.mock("@/lib/graphql", () => ({ fetchServerLogs: vi.fn() }));

const lines = [
  "time=2026-11-14T10:00:00Z level=INFO msg=started addr=[IP]",
  "time=2026-11-14T10:00:05Z level=WARN msg=denied token=[REDACTED] path=/home/[USER]/media",
];

// jsdom's Blob has no text(); FileReader reads it the same way.
function readBlob(blob: Blob): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => resolve(String(reader.result));
    reader.onerror = () => reject(reader.error);
    reader.readAsText(blob);
  });
}

function renderLogs() {
  return render(
    <I18nProvider>
      <ServerLogs />
    </I18nProvider>,
  );
}

beforeEach(() => {
  vi.mocked(fetchServerLogs).mockResolvedValue({ available: true, capacity: 2000, lines });
});

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

describe("Console server logs (CORE-1716)", () => {
  it("shows the recent redacted lines oldest first", async () => {
    renderLogs();
    const log = await screen.findByLabelText("Recent server log lines");
    expect(log.textContent).toBe(lines.join("\n"));
    expect(screen.getByText("Showing the last 2 of up to 2000 lines.")).toBeInTheDocument();
  });

  it("downloads a bundle with a header and the same lines", async () => {
    const created: Blob[] = [];
    const createObjectURL = vi.fn((blob: Blob) => {
      created.push(blob);
      return "blob:server-log";
    });
    Object.assign(URL, { createObjectURL, revokeObjectURL: vi.fn() });
    const click = vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(() => {});

    renderLogs();
    await userEvent.click(await screen.findByRole("button", { name: "Download log bundle" }));

    expect(click).toHaveBeenCalledTimes(1);
    expect(created).toHaveLength(1);
    const text = await readBlob(created[0]);
    expect(text).toContain("# Beamfall server log bundle");
    expect(text).toContain("# Lines: 2 (server keeps up to 2000)");
    expect(text.trimEnd().endsWith(lines.join("\n"))).toBe(true);
  });

  it("explains when the server keeps no log tail and disables the download", async () => {
    vi.mocked(fetchServerLogs).mockResolvedValue({ available: false, capacity: 0, lines: [] });
    renderLogs();
    expect(
      await screen.findByText(
        "This server does not keep recent log lines. Read its log on the host instead.",
      ),
    ).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Download log bundle" })).toBeDisabled();
  });

  it("shows an error when the read fails", async () => {
    vi.mocked(fetchServerLogs).mockRejectedValue(new Error("admin session required"));
    renderLogs();
    expect(await screen.findByText("Server logs could not be loaded.")).toBeInTheDocument();
  });

  it("builds the bundle text without touching the lines", () => {
    const text = serverLogBundle(
      { available: true, capacity: 5, lines: ["a", "b"] },
      new Date("2026-11-14T00:00:00Z"),
    );
    expect(text).toBe(
      [
        "# Beamfall server log bundle",
        "# Generated: 2026-11-14T00:00:00.000Z",
        "# Lines: 2 (server keeps up to 5)",
        "# Redacted on the server: tokens, secrets, IP addresses, home folder user names",
        "",
        "a",
        "b",
        "",
      ].join("\n"),
    );
  });
});
