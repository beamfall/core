import "@testing-library/jest-dom/vitest";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import {
  deleteWebhook,
  fetchWebhookDeliveries,
  fetchWebhookSettings,
  fetchWebhooks,
  saveWebhook,
  setWebhookSettings,
  testWebhook,
} from "@/lib/graphql";
import type { WebhookEndpointInfo } from "@/lib/graphql/types";
import { I18nProvider } from "@/lib/i18n";
import { Webhooks } from "./Webhooks";

// jsdom has no ResizeObserver; the Radix Checkbox and Switch need one.
class ResizeObserverStub {
  observe() {}
  unobserve() {}
  disconnect() {}
}
globalThis.ResizeObserver ??= ResizeObserverStub as unknown as typeof ResizeObserver;

vi.mock("@/lib/graphql", () => ({
  fetchWebhooks: vi.fn(),
  fetchWebhookDeliveries: vi.fn(),
  fetchWebhookSettings: vi.fn(),
  saveWebhook: vi.fn(),
  deleteWebhook: vi.fn(),
  testWebhook: vi.fn(),
  setWebhookSettings: vi.fn(),
}));

const chat: WebhookEndpointInfo = {
  id: "hook-1",
  name: "Chat alerts",
  format: "discord",
  url: "https://chat.example",
  eventKinds: [],
  disabled: false,
  allowPrivateNetwork: false,
  secretSet: false,
  from: "",
  to: [],
  username: "",
  problem: "",
};

const phone: WebhookEndpointInfo = {
  ...chat,
  id: "hook-2",
  name: "Phone",
  format: "ntfy",
  url: "https://push.example",
  eventKinds: ["playback.started"],
  disabled: true,
  secretSet: true,
};

beforeEach(() => {
  vi.mocked(fetchWebhooks).mockReset().mockResolvedValue([chat, phone]);
  vi.mocked(fetchWebhookDeliveries)
    .mockReset()
    .mockResolvedValue([
      {
        endpointID: "hook-1",
        eventKind: "media.added",
        outcome: "failed",
        statusClass: "5xx",
        attempts: 3,
        durationMs: 80,
        at: "2026-11-14T12:00:00Z",
        test: false,
      },
    ]);
  vi.mocked(fetchWebhookSettings)
    .mockReset()
    .mockResolvedValue({
      publicBaseURL: "https://media.example.net",
      formats: ["discord", "slack", "ntfy", "gotify", "email", "generic"],
      eventKinds: ["media.added", "playback.started", "playback.stopped"],
    });
  vi.mocked(saveWebhook).mockReset().mockResolvedValue(chat);
  vi.mocked(deleteWebhook).mockReset().mockResolvedValue(true);
  vi.mocked(testWebhook).mockReset();
  vi.mocked(setWebhookSettings).mockReset();
});

function renderPage() {
  return render(
    <I18nProvider>
      <Webhooks />
    </I18nProvider>,
  );
}

function rowFor(name: string) {
  // The endpoint table renders before the delivery log, which repeats names.
  const row = screen.getAllByText(name)[0].closest("tr");
  if (!row) throw new Error(`no row for ${name}`);
  return row;
}

// A long user-event flow: it runs in well under a second alone but can pass 5s
// when the machine is oversubscribed (two vitest runs at once), so it gets its
// own budget rather than a raised global timeout (CORE-2890).
const SLOW_FLOW_TIMEOUT = 15_000;

describe("Webhooks", () => {
  it("lists endpoints with their state and the delivery log by name", async () => {
    renderPage();

    expect((await screen.findAllByText("Chat alerts"))[0]).toBeVisible();
    expect(within(rowFor("Chat alerts")).getByText("Active")).toBeVisible();
    expect(within(rowFor("Phone")).getByText("Paused")).toBeVisible();
    expect(within(rowFor("Phone")).getByText("ntfy")).toBeVisible();

    const log = await screen.findByText("Media added");
    const logRow = log.closest("tr");
    expect(logRow).not.toBeNull();
    expect(within(logRow as HTMLElement).getByText("Chat alerts")).toBeVisible();
    expect(within(logRow as HTMLElement).getByText("Failed · 5xx")).toBeVisible();
  });

  it(
    "adds a webhook with the chosen events and no secret for Discord",
    async () => {
      const user = userEvent.setup();
      renderPage();
      await screen.findAllByText("Chat alerts");

      await user.click(screen.getByRole("button", { name: "Add webhook" }));
      expect(screen.queryByLabelText("Secret")).toBeNull();
      await user.type(screen.getByLabelText("Name"), "Releases");
      await user.type(screen.getByLabelText("URL"), "https://chat.example/hook/token");
      await user.click(screen.getByRole("checkbox", { name: "Media added" }));
      await user.click(screen.getByRole("button", { name: "Save webhook" }));

      await waitFor(() =>
        expect(saveWebhook).toHaveBeenCalledWith({
          name: "Releases",
          format: "discord",
          url: "https://chat.example/hook/token",
          eventKinds: ["media.added"],
          disabled: false,
          allowPrivateNetwork: false,
        }),
      );
      await waitFor(() => expect(fetchWebhooks).toHaveBeenCalledTimes(2));
      expect(screen.queryByRole("button", { name: "Save webhook" })).toBeNull();
    },
    SLOW_FLOW_TIMEOUT,
  );

  it("keeps the saved URL and secret when an edit leaves them blank", async () => {
    const user = userEvent.setup();
    renderPage();
    await screen.findByText("Phone");

    await user.click(within(rowFor("Phone")).getByRole("button", { name: "Edit" }));
    expect(screen.getByText("Leave blank to keep the saved URL.")).toBeVisible();
    expect(screen.getByText("A secret is stored. Leave blank to keep it.")).toBeVisible();
    await user.click(screen.getByRole("button", { name: "Save webhook" }));

    await waitFor(() => expect(saveWebhook).toHaveBeenCalled());
    const sent = vi.mocked(saveWebhook).mock.calls[0][0];
    expect(sent).toEqual({
      id: "hook-2",
      name: "Phone",
      format: "ntfy",
      eventKinds: ["playback.started"],
      disabled: true,
      allowPrivateNetwork: false,
    });
    expect(sent).not.toHaveProperty("url");
    expect(sent).not.toHaveProperty("secret");
  });

  it("shows the server's reason when a save is refused", async () => {
    vi.mocked(saveWebhook).mockRejectedValue(new Error("webhook configuration is invalid"));
    const user = userEvent.setup();
    renderPage();
    await screen.findAllByText("Chat alerts");

    await user.click(screen.getByRole("button", { name: "Add webhook" }));
    await user.type(screen.getByLabelText("Name"), "Broken");
    await user.type(screen.getByLabelText("URL"), "https://chat.example/x");
    await user.click(screen.getByRole("button", { name: "Save webhook" }));

    expect(await screen.findByRole("alert")).toHaveTextContent(
      "The webhook could not be saved: webhook configuration is invalid",
    );
  });

  it("sends a test and reports the outcome inline", async () => {
    vi.mocked(testWebhook).mockResolvedValue({
      ok: false,
      statusClass: "4xx",
      diagnostic: "webhook endpoint rejected delivery",
      durationMs: 12,
    });
    const user = userEvent.setup();
    renderPage();
    await screen.findAllByText("Chat alerts");

    await user.click(within(rowFor("Chat alerts")).getByRole("button", { name: "Send test" }));

    expect(
      await screen.findByText("Test failed: webhook endpoint rejected delivery"),
    ).toBeVisible();
    expect(testWebhook).toHaveBeenCalledWith("hook-1");
    await waitFor(() => expect(fetchWebhookDeliveries).toHaveBeenCalledTimes(2));

    vi.mocked(testWebhook).mockResolvedValue({
      ok: true,
      statusClass: "2xx",
      diagnostic: "",
      durationMs: 40,
    });
    await user.click(within(rowFor("Chat alerts")).getByRole("button", { name: "Send test" }));
    expect(await screen.findByText("Test delivered in 40 ms.")).toBeVisible();
  });

  it("deletes a webhook only after confirmation", async () => {
    const user = userEvent.setup();
    renderPage();
    await screen.findAllByText("Chat alerts");

    await user.click(within(rowFor("Chat alerts")).getByRole("button", { name: "Delete webhook" }));
    const dialog = await screen.findByRole("alertdialog");
    expect(
      within(dialog).getByRole("heading", { name: "Delete this webhook and its stored secret?" }),
    ).toBeVisible();
    expect(deleteWebhook).not.toHaveBeenCalled();
    await user.click(within(dialog).getByRole("button", { name: "Delete webhook" }));

    await waitFor(() => expect(deleteWebhook).toHaveBeenCalledWith("hook-1"));
    await waitFor(() => expect(fetchWebhooks).toHaveBeenCalledTimes(2));
  });

  it("saves the public server URL", async () => {
    vi.mocked(setWebhookSettings).mockResolvedValue({
      publicBaseURL: "https://watch.example.net",
      formats: [],
      eventKinds: [],
    });
    const user = userEvent.setup();
    renderPage();

    const field = await screen.findByLabelText("Public server URL");
    await waitFor(() => expect(field).toHaveValue("https://media.example.net"));
    await user.clear(field);
    await user.type(field, "https://watch.example.net");
    await user.click(screen.getByRole("button", { name: "Save URL" }));

    await waitFor(() =>
      expect(setWebhookSettings).toHaveBeenCalledWith("https://watch.example.net"),
    );
  });
});
