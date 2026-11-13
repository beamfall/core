import type { ReactNode } from "react";
import "@testing-library/jest-dom/vitest";
import { act, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { setAdvancedSettings } from "@/lib/advancedSettings";
import { fetchClientPrefs, setClientPref } from "@/lib/graphql";
import { I18nProvider } from "@/lib/i18n";
import { useSession } from "@/lib/session";
import { ProfileSettingsPage } from "./ProfileSettingsPage";

vi.mock("@/components/chrome/AppShell", () => ({
  AppShell: ({ children }: { children: ReactNode }) => <>{children}</>,
}));

vi.mock("@/lib/graphql", () => ({
  fetchClientPrefs: vi.fn(),
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
      <ProfileSettingsPage />
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

    expect(screen.getByText(/Smart speed is planned separately/i)).toBeInTheDocument();
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
    expect(screen.getByText(/managed by your operator/i)).toBeInTheDocument();
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
        <ProfileSettingsPage />
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
});
