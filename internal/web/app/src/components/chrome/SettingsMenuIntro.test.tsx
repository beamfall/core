import "@testing-library/jest-dom/vitest";
import { cleanup, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes, useLocation } from "react-router";
import { afterEach, describe, expect, it, vi } from "vitest";
import { setAdvancedSettings, useAdvancedSettings } from "@/lib/advancedSettings";
import { I18nProvider } from "@/lib/i18n";
import type { SearchablePage } from "@/lib/settingsIndex";
import { RevealSetting, SettingsMenuIntro } from "./SettingsMenuIntro";

const session = vi.hoisted(() => ({ isManager: true }));
vi.mock("@/lib/session", () => ({ useSession: () => session }));

afterEach(() => {
  cleanup();
  session.isManager = true;
  setAdvancedSettings(false);
});

const CONSOLE_PAGES: SearchablePage[] = [
  { area: "console", path: "config", label: "navPlaybackSignIn" },
];

function Where() {
  const { pathname } = useLocation();
  return <div data-testid="path">{pathname}</div>;
}

// A stand-in Playback & sign-in page: the codec control only shows with
// Advanced settings on, like the real one.
function ConfigPage() {
  const advanced = useAdvancedSettings();
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

function renderIntro(area: "console" | "manage" = "console") {
  return render(
    <I18nProvider>
      <MemoryRouter initialEntries={[`/${area}`]}>
        <SettingsMenuIntro area={area} pages={CONSOLE_PAGES} />
        <Where />
        <Routes>
          <Route path="/console/config" element={<ConfigPage />} />
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

  it("says so when nothing matches and clears on Escape", async () => {
    const user = userEvent.setup();
    renderIntro();
    const box = screen.getByRole("searchbox", { name: "Search settings" });

    await user.type(box, "zzzqqq");
    expect(screen.getByRole("status")).toHaveTextContent("No matching settings");
    await user.keyboard("{Escape}");
    expect(box).toHaveValue("");
    expect(screen.queryByRole("status")).toBeNull();
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
