import "@testing-library/jest-dom/vitest";
import { cleanup, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import { MemoryRouter, Route, Routes, useLocation } from "react-router";
import { afterEach, describe, expect, it, vi } from "vitest";
import { setAdvancedSettings, useAdvancedSettings } from "@/lib/advancedSettings";
import { I18nProvider } from "@/lib/i18n";
import { RevealSetting, SettingsMenuIntro } from "./SettingsMenuIntro";

const session = vi.hoisted(() => ({ isManager: true }));
vi.mock("@/lib/session", () => ({ useSession: () => session }));

afterEach(() => {
  cleanup();
  session.isManager = true;
  setAdvancedSettings(false);
});

function Where() {
  const { pathname } = useLocation();
  return <div data-testid="path">{pathname}</div>;
}

// A stand-in Playback & sign-in page: the codec control only shows with
// Advanced settings on, like the real one.
function ConfigPage() {
  const [advanced] = useAdvancedSettings();
  return (
    <div>
      <RevealSetting />
      <div data-setting="console.playback.maxTranscodes">
        <button type="button" data-help-tip="">
          help
        </button>
        <button type="button">cap</button>
      </div>
      {advanced && (
        <details>
          <summary>more</summary>
          <div data-setting="console.playback.codec">
            <button type="button">codec</button>
          </div>
        </details>
      )}
    </div>
  );
}

// A stand-in Webhooks page: the secret field only shows once Add opens the form.
function WebhooksPage() {
  const [open, setOpen] = useState(false);
  return (
    <div>
      <RevealSetting />
      <button type="button" data-setting="console.webhooks.add" onClick={() => setOpen(true)}>
        add webhook
      </button>
      {open && (
        <div data-setting="console.webhooks.secret">
          <input aria-label="secret" />
        </div>
      )}
    </div>
  );
}

// A stand-in page behind an unlock prompt: kept mounted but inert until unlocked.
function LockedPage() {
  const [locked, setLocked] = useState(true);
  return (
    <div>
      <button type="button" onClick={() => setLocked(false)}>
        unlock
      </button>
      <div inert={locked}>
        <RevealSetting />
        <div data-setting="console.playback.maxTranscodes">
          <button type="button">cap</button>
        </div>
      </div>
    </div>
  );
}

function renderIntro(area: "console" | "manage" = "console") {
  return render(
    <I18nProvider>
      <MemoryRouter initialEntries={[`/${area}`]}>
        <SettingsMenuIntro area={area} />
        <Where />
        <Routes>
          <Route path="/console/config" element={<ConfigPage />} />
          <Route path="/console/webhooks" element={<WebhooksPage />} />
          <Route path="*" element={null} />
        </Routes>
      </MemoryRouter>
    </I18nProvider>,
  );
}

describe("SettingsMenuIntro", () => {
  it("says what the area is for and links to the other one", () => {
    renderIntro("console");
    expect(screen.getByRole("link", { name: "Organizing media? Go to Manage" })).toHaveAttribute(
      "href",
      "/manage",
    );
    cleanup();
    renderIntro("manage");
    expect(
      screen.getByRole("link", { name: "Changing server settings? Go to the Console" }),
    ).toHaveAttribute("href", "/console");
  });

  it("opens a found setting and brings it into view with focus", async () => {
    const user = userEvent.setup();
    renderIntro();

    await user.type(screen.getByRole("searchbox", { name: "Search settings" }), "transcodes");
    const results = screen.getByRole("list", { name: "Search settings" });
    await user.click(within(results).getAllByRole("link")[0]);

    expect(screen.getByTestId("path")).toHaveTextContent("/console/config");
    await waitFor(() => expect(screen.getByRole("button", { name: "cap" })).toHaveFocus());
    expect(screen.getByRole("searchbox", { name: "Search settings" })).toHaveValue("");
  });

  it("turns Advanced on and unfolds the setting when it needs it", async () => {
    const user = userEvent.setup();
    renderIntro();

    await user.type(screen.getByRole("searchbox", { name: "Search settings" }), "codec{Enter}");

    await waitFor(() => expect(screen.getByRole("button", { name: "codec" })).toHaveFocus());
    expect(screen.getByRole("button", { name: "codec" }).closest("details")).toHaveAttribute(
      "open",
    );
  });

  it("shows the control that opens a setting that only appears after an action", async () => {
    const user = userEvent.setup();
    renderIntro();

    await user.type(screen.getByRole("searchbox", { name: "Search settings" }), "secret");
    const results = screen.getByRole("list", { name: "Search settings" });
    const webhook = within(results)
      .getAllByRole("link")
      .find((a) => a.getAttribute("href") === "/console/webhooks");
    expect(webhook).toBeDefined();
    await user.click(webhook as HTMLElement);

    await waitFor(() => expect(screen.getByRole("button", { name: "add webhook" })).toHaveFocus(), {
      timeout: 3000,
    });
    expect(screen.getByTestId("path")).toHaveTextContent("/console/webhooks");
  });

  it("waits for an unlock prompt to close before revealing the setting", async () => {
    const user = userEvent.setup();
    render(
      <MemoryRouter
        initialEntries={[
          {
            pathname: "/manage/libraries",
            state: { revealSetting: "console.playback.maxTranscodes" },
          },
        ]}
      >
        <LockedPage />
      </MemoryRouter>,
    );
    const cap = screen.getByRole("button", { name: "cap", hidden: true });

    // Several polls: an unlocked page would have revealed it on the first.
    await new Promise((resolve) => setTimeout(resolve, 600));
    expect(cap).not.toHaveFocus();
    expect(cap.parentElement).not.toHaveClass("bf-setting-found");

    await user.click(screen.getByRole("button", { name: "unlock" }));
    await waitFor(() => expect(cap).toHaveFocus());
    expect(cap.parentElement).toHaveClass("bf-setting-found");
  });

  it("says so when nothing matches and clears on Escape", async () => {
    const user = userEvent.setup();
    renderIntro();
    const box = screen.getByRole("searchbox", { name: "Search settings" });

    await user.type(box, "zzzqqq");
    expect(screen.getByRole("status")).toHaveTextContent("No matching settings");
    await user.keyboard("{Escape}");
    expect(box).toHaveValue("");
    expect(screen.getByRole("status")).toBeEmptyDOMElement();
  });

  it("announces how many results match while focus stays in the box", async () => {
    const user = userEvent.setup();
    renderIntro();
    const box = screen.getByRole("searchbox", { name: "Search settings" });

    await user.type(box, "transcodes");
    const count = within(screen.getByRole("list", { name: "Search settings" })).getAllByRole(
      "link",
    ).length;
    expect(screen.getByRole("status")).toHaveTextContent(
      count === 1 ? "1 result" : `${count} results`,
    );
    expect(box).toHaveFocus();
  });

  it("finds a Console page by name from the Manage menu", async () => {
    const user = userEvent.setup();
    renderIntro("manage");

    await user.type(screen.getByRole("searchbox", { name: "Search settings" }), "backups");
    const results = screen.getByRole("list", { name: "Search settings" });
    const link = within(results)
      .getAllByRole("link")
      .find((a) => a.getAttribute("href") === "/console/backups");
    expect(link).toHaveTextContent("Console");
  });

  it("leaves Manage results out for someone who cannot open Manage", async () => {
    const user = userEvent.setup();
    renderIntro();
    const box = screen.getByRole("searchbox", { name: "Search settings" });

    await user.type(box, "purge");
    expect(screen.getByRole("list", { name: "Search settings" })).toHaveTextContent("Manage");

    cleanup();
    session.isManager = false;
    renderIntro();
    await user.type(screen.getByRole("searchbox", { name: "Search settings" }), "purge");
    expect(screen.queryByRole("list", { name: "Search settings" })).toBeNull();
    expect(screen.getByRole("status")).toHaveTextContent("No matching settings");
  });
});
