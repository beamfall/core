import type { ReactNode } from "react";
import "@testing-library/jest-dom/vitest";
import { act, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { setAdvancedSettings } from "@/lib/advancedSettings";
import { fetchClientPrefs, fetchMetadataCredits, setClientPref } from "@/lib/graphql";
import { I18nProvider } from "@/lib/i18n";
import { RegisterProvider } from "@/lib/register";
import { useSession } from "@/lib/session";
import { ProfileSettingsPage } from "./ProfileSettingsPage";

vi.mock("@/components/chrome/AppShell", () => ({
  AppShell: ({ children }: { children: ReactNode }) => <>{children}</>,
}));

vi.mock("@/lib/graphql", () => ({
  fetchClientPrefs: vi.fn(),
  fetchMetadataCredits: vi.fn(),
  setClientPref: vi.fn(),
}));

vi.mock("@/lib/session", () => ({
  useSession: vi.fn(),
}));

Element.prototype.hasPointerCapture ??= () => false;
Element.prototype.setPointerCapture ??= () => {};
Element.prototype.releasePointerCapture ??= () => {};
Element.prototype.scrollIntoView ??= () => {};

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((r) => {
    resolve = r;
  });
  return { promise, resolve };
}

function setProfileKind(kind: "adult" | "managed", profileID = "p1") {
  vi.mocked(useSession).mockReturnValue({
    session: { deviceRole: "manager", profileID, profile: kind, revealed: false },
    profiles: [{ id: profileID, name: "Robin", kind }],
    refreshProfiles: vi.fn().mockResolvedValue(undefined),
  } as unknown as ReturnType<typeof useSession>);
}

beforeEach(() => {
  vi.mocked(fetchClientPrefs).mockReset();
  vi.mocked(setClientPref).mockReset();
  vi.mocked(fetchClientPrefs).mockResolvedValue([{ key: "autoskip.intro", value: "on" }]);
  vi.mocked(setClientPref).mockResolvedValue({ key: "autoskip.recap" });
  vi.mocked(fetchMetadataCredits).mockReset();
  vi.mocked(fetchMetadataCredits).mockResolvedValue([]);
  setProfileKind("adult");
});

afterEach(() => {
  localStorage.clear();
  setAdvancedSettings(false);
});

async function showAdvanced(user: ReturnType<typeof userEvent.setup>) {
  await user.click(await screen.findByRole("switch", { name: "Show advanced settings" }));
}

function renderPage() {
  return render(
    <I18nProvider>
      <RegisterProvider>
        <ProfileSettingsPage />
      </RegisterProvider>
    </I18nProvider>,
  );
}

describe("ProfileSettingsPage", () => {
  it("hydrates toggles from clientPrefs and persists a self-service change", async () => {
    const user = userEvent.setup();
    renderPage();

    const intro = await screen.findByRole("switch", { name: "Skip intros" });
    expect(intro).toBeChecked();

    await user.click(screen.getByRole("switch", { name: "Skip recaps" }));
    expect(setClientPref).toHaveBeenCalledWith("autoskip.recap", "on");
  });

  it("clears a boolean preference with an empty value", async () => {
    const user = userEvent.setup();
    renderPage();

    await user.click(await screen.findByRole("switch", { name: "Skip intros" }));
    expect(setClientPref).toHaveBeenCalledWith("autoskip.intro", "");
  });

  it("turns on pre-play and then offers the suggested-item count (CORE-1701)", async () => {
    vi.mocked(setClientPref).mockResolvedValue({ key: "preplay.enabled" });
    const user = userEvent.setup();
    renderPage();

    const toggle = await screen.findByRole("switch", { name: "Play intros and previews first" });
    expect(toggle).not.toBeChecked();
    expect(screen.queryByLabelText("Previews before a title")).toBeNull();

    await user.click(toggle);
    expect(setClientPref).toHaveBeenCalledWith("preplay.enabled", "on");

    await user.click(await screen.findByLabelText("Previews before a title"));
    await user.click(await screen.findByRole("option", { name: "4" }));
    expect(setClientPref).toHaveBeenCalledWith("preplay.extras", "4");
  });

  it("re-skins with a theme preset, saves it to the Profile and keeps the scheme on the device (CORE-1179)", async () => {
    const user = userEvent.setup();
    renderPage();

    await user.click(await screen.findByLabelText("Theme"));
    await user.click(await screen.findByRole("option", { name: "Moss" }));
    expect(setClientPref).toHaveBeenCalledWith("theme.preset", "moss");
    expect(document.documentElement.style.getPropertyValue("--theme-dark-accent")).not.toBe("");

    await user.click(screen.getByLabelText("Color scheme"));
    await user.click(await screen.findByRole("option", { name: "Light" }));
    expect(document.documentElement.dataset.theme).toBe("light");
    expect(setClientPref).toHaveBeenCalledTimes(1);
    expect(JSON.parse(localStorage.getItem("beamfall.web.appearance.v1") ?? "{}")).toEqual({
      scheme: "light",
      preset: "moss",
    });
    localStorage.clear();
    delete document.documentElement.dataset.theme;
  });

  it("rolls the theme preset back when the save fails (CORE-1179)", async () => {
    const user = userEvent.setup();
    vi.mocked(setClientPref).mockRejectedValue(new Error("offline"));
    renderPage();

    await user.click(await screen.findByLabelText("Theme"));
    await user.click(await screen.findByRole("option", { name: "Moss" }));
    expect(setClientPref).toHaveBeenCalledWith("theme.preset", "moss");
    await waitFor(() =>
      expect(JSON.parse(localStorage.getItem("beamfall.web.appearance.v1") ?? "{}").preset).toBe(
        "beamfall",
      ),
    );
    expect(screen.getByLabelText("Theme")).not.toHaveTextContent("Moss");
    localStorage.clear();
  });

  it("keeps a newer saved theme when an older save fails afterwards (CORE-1179)", async () => {
    const user = userEvent.setup();
    let failMoss: (error: Error) => void = () => {};
    vi.mocked(setClientPref)
      .mockImplementationOnce(
        () =>
          new Promise((_, reject) => {
            failMoss = reject;
          }),
      )
      .mockResolvedValueOnce(undefined as never);
    renderPage();

    await user.click(await screen.findByLabelText("Theme"));
    await user.click(await screen.findByRole("option", { name: "Moss" }));
    await user.click(screen.getByLabelText("Theme"));
    await user.click(await screen.findByRole("option", { name: "Tide" }));
    expect(setClientPref).toHaveBeenLastCalledWith("theme.preset", "tide");
    await waitFor(() => expect(screen.getByLabelText("Theme")).toHaveTextContent("Tide"));

    failMoss(new Error("offline"));
    await waitFor(() => expect(screen.getByRole("alert")).toBeTruthy());
    expect(screen.getByLabelText("Theme")).toHaveTextContent("Tide");
    expect(JSON.parse(localStorage.getItem("beamfall.web.appearance.v1") ?? "{}").preset).toBe(
      "tide",
    );
    localStorage.clear();
  });

  it("shows an older theme save that succeeds after the newest one failed (CORE-1179)", async () => {
    const user = userEvent.setup();
    let saveMoss: () => void = () => {};
    vi.mocked(setClientPref)
      .mockImplementationOnce(
        () =>
          new Promise((resolve) => {
            saveMoss = () => resolve(undefined as never);
          }),
      )
      .mockRejectedValueOnce(new Error("offline"));
    renderPage();

    await user.click(await screen.findByLabelText("Theme"));
    await user.click(await screen.findByRole("option", { name: "Moss" }));
    await user.click(screen.getByLabelText("Theme"));
    await user.click(await screen.findByRole("option", { name: "Tide" }));
    await waitFor(() => expect(screen.getByRole("alert")).toBeTruthy());
    await waitFor(() => expect(screen.getByLabelText("Theme")).not.toHaveTextContent("Tide"));

    saveMoss();
    await waitFor(() => expect(screen.getByLabelText("Theme")).toHaveTextContent("Moss"));
    expect(JSON.parse(localStorage.getItem("beamfall.web.appearance.v1") ?? "{}").preset).toBe(
      "moss",
    );
    localStorage.clear();
  });

  it("persists the downstream audio preference", async () => {
    const user = userEvent.setup();
    renderPage();
    await showAdvanced(user);

    await user.click(await screen.findByLabelText("Audio output"));
    await user.click(await screen.findByRole("option", { name: "Surround" }));

    expect(setClientPref).toHaveBeenCalledWith("playback.downstream-audio", "surround");
  });

  it("persists the global skip interval preference", async () => {
    const user = userEvent.setup();
    renderPage();
    await showAdvanced(user);

    await user.click(await screen.findByLabelText("Skip buttons"));
    await user.click(await screen.findByRole("option", { name: "30s" }));

    expect(setClientPref).toHaveBeenCalledWith("playback.skip-interval", "30");
  });

  it("labels trim silence as long-pause removal and persists the toggle", async () => {
    const user = userEvent.setup();
    renderPage();
    await showAdvanced(user);

    await user.click(await screen.findByRole("switch", { name: "Remove long pauses" }));

    expect(screen.getByText(/Skips long silent gaps/i)).toBeInTheDocument();
    expect(setClientPref).toHaveBeenCalledWith("playback.trim-silence", "on");
  });

  it("leaves trim silence, skip interval and audio output to Advanced at their defaults", async () => {
    const user = userEvent.setup();
    renderPage();

    await screen.findByRole("switch", { name: "Skip intros" });
    expect(screen.queryByRole("switch", { name: "Remove long pauses" })).toBeNull();
    expect(screen.queryByLabelText("Skip buttons")).toBeNull();
    expect(screen.queryByLabelText("Audio output")).toBeNull();

    await showAdvanced(user);
    expect(screen.getByRole("switch", { name: "Remove long pauses" })).toBeVisible();
    expect(screen.getByLabelText("Skip buttons")).toBeVisible();
    expect(screen.getByLabelText("Audio output")).toBeVisible();
  });

  it("keeps an Advanced setting in view while it is not at its default", async () => {
    const user = userEvent.setup();
    vi.mocked(fetchClientPrefs).mockResolvedValue([
      { key: "playback.trim-silence", value: "on" },
      { key: "playback.skip-interval", value: "30" },
    ]);
    renderPage();

    const trim = await screen.findByRole("switch", { name: "Remove long pauses" });
    expect(screen.getByLabelText("Skip buttons")).toBeVisible();
    expect(screen.queryByLabelText("Audio output")).toBeNull();

    // Turning it back to the default does not make it vanish mid-edit.
    await user.click(trim);
    expect(setClientPref).toHaveBeenCalledWith("playback.trim-silence", "");
    expect(screen.getByRole("switch", { name: "Remove long pauses" })).not.toBeChecked();
  });

  it("shows the recommendations opt-in for an adult Profile", async () => {
    renderPage();
    expect(
      await screen.findByRole("switch", { name: "Personalized recommendations" }),
    ).toBeInTheDocument();
  });

  it("hides recommendations opt-in for a managed Profile and shows the managed note", async () => {
    setProfileKind("managed");
    renderPage();

    await screen.findByRole("switch", { name: "Skip intros" });
    expect(screen.queryByRole("switch", { name: "Personalized recommendations" })).toBeNull();
    expect(screen.getByText(/set by the person who runs the server/i)).toBeInTheDocument();
  });

  it("surfaces a save error and reloads when a write fails", async () => {
    const user = userEvent.setup();
    vi.mocked(setClientPref).mockRejectedValueOnce(new Error("boom"));
    renderPage();

    await user.click(await screen.findByRole("switch", { name: "Skip recaps" }));
    await waitFor(() => {
      expect(screen.getByRole("alert")).toBeInTheDocument();
    });
    // Reloaded canonical state after the failed write.
    expect(fetchClientPrefs).toHaveBeenCalledTimes(2);
  });

  function tones() {
    const group = screen.getByRole("group", { name: "Colour" });
    return Array.from(group.querySelectorAll<HTMLInputElement>("input[type=radio]"));
  }

  it("keeps a saved avatar when only the Profile list refresh fails", async () => {
    const user = userEvent.setup();
    const refreshProfiles = vi.fn().mockRejectedValue(new Error("offline"));
    vi.mocked(useSession).mockReturnValue({
      session: { deviceRole: "manager", profileID: "p1", profile: "adult", revealed: false },
      profiles: [{ id: "p1", name: "Robin", kind: "adult" }],
      refreshProfiles,
    } as unknown as ReturnType<typeof useSession>);
    renderPage();
    await screen.findByRole("group", { name: "Colour" });
    const target = tones().findIndex((tone) => !tone.checked);

    await user.click(tones()[target]);

    await waitFor(() => expect(refreshProfiles).toHaveBeenCalledTimes(1));
    expect(setClientPref).toHaveBeenCalledWith("profile.avatar", expect.any(String));
    expect(tones()[target]).toBeChecked();
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });

  it("undoes the avatar choice and says so when the write fails", async () => {
    const user = userEvent.setup();
    vi.mocked(setClientPref).mockRejectedValueOnce(new Error("boom"));
    renderPage();
    await screen.findByRole("group", { name: "Colour" });
    const before = tones().findIndex((tone) => tone.checked);
    const target = tones().findIndex((tone) => !tone.checked);

    await user.click(tones()[target]);

    expect(await screen.findByRole("alert")).toBeInTheDocument();
    await waitFor(() => expect(tones()[before]).toBeChecked());
  });

  it("does not let a stale Profile preference load overwrite the current Profile", async () => {
    const p1Load = deferred<{ key: string; value: string }[]>();
    const p2Load = deferred<{ key: string; value: string }[]>();
    vi.mocked(fetchClientPrefs)
      .mockReturnValueOnce(p1Load.promise)
      .mockReturnValueOnce(p2Load.promise);

    const view = renderPage();

    setProfileKind("adult", "p2");
    view.rerender(
      <I18nProvider>
        <RegisterProvider>
          <ProfileSettingsPage />
        </RegisterProvider>
      </I18nProvider>,
    );

    await act(async () => {
      p2Load.resolve([{ key: "autoskip.recap", value: "on" }]);
      await p2Load.promise;
    });

    expect(await screen.findByRole("switch", { name: "Skip recaps" })).toBeChecked();
    expect(screen.getByRole("switch", { name: "Skip intros" })).not.toBeChecked();

    await act(async () => {
      p1Load.resolve([{ key: "autoskip.intro", value: "on" }]);
      await p1Load.promise;
    });

    expect(screen.getByRole("switch", { name: "Skip recaps" })).toBeChecked();
    expect(screen.getByRole("switch", { name: "Skip intros" })).not.toBeChecked();
  });

  it("lists metadata credits as safe external links (CORE-1126)", async () => {
    vi.mocked(fetchMetadataCredits).mockResolvedValue([
      { text: { en: "Metadata provided by Fixture" }, url: "https://fixture.example.test/" },
      { text: { en: "Data from Another fixture" }, url: "http://another.example.test/" },
    ]);
    renderPage();

    const section = await screen.findByRole("region", { name: "Credits" });
    const link = within(section).getByRole("link", { name: "Metadata provided by Fixture" });
    expect(link).toHaveAttribute("href", "https://fixture.example.test/");
    expect(link).toHaveAttribute("target", "_blank");
    expect(link).toHaveAttribute("rel", "noopener noreferrer");
    expect(within(section).getByText("Data from Another fixture")).toBeInTheDocument();
    expect(within(section).getAllByRole("link")).toHaveLength(1);
  });

  it("omits the Credits section when no plugin declares an attribution", async () => {
    renderPage();

    await screen.findByRole("switch", { name: "Skip intros" });
    await waitFor(() => expect(fetchMetadataCredits).toHaveBeenCalled());
    expect(screen.queryByRole("region", { name: "Credits" })).toBeNull();
  });
});
