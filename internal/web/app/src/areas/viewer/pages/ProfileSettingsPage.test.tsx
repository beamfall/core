import type { ReactNode } from "react";
import "@testing-library/jest-dom/vitest";
import { act, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
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
  } as unknown as ReturnType<typeof useSession>);
}

beforeEach(() => {
  vi.mocked(fetchClientPrefs).mockReset();
  vi.mocked(setClientPref).mockReset();
  vi.mocked(fetchClientPrefs).mockResolvedValue([{ key: "autoskip.intro", value: "on" }]);
  vi.mocked(setClientPref).mockResolvedValue({ key: "autoskip.recap" });
  setProfileKind("adult");
});

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

    await user.click(await screen.findByLabelText("Audio output"));
    await user.click(await screen.findByRole("option", { name: "Surround" }));

    expect(setClientPref).toHaveBeenCalledWith("playback.downstream-audio", "surround");
  });

  it("persists the global skip interval preference", async () => {
    const user = userEvent.setup();
    renderPage();

    await user.click(await screen.findByLabelText("Skip buttons"));
    await user.click(await screen.findByRole("option", { name: "30s" }));

    expect(setClientPref).toHaveBeenCalledWith("playback.skip-interval", "30");
  });

  it("labels trim silence as long-pause removal and persists the toggle", async () => {
    const user = userEvent.setup();
    renderPage();

    await user.click(await screen.findByRole("switch", { name: "Remove long pauses" }));

    expect(screen.getByText(/Smart speed is planned separately/i)).toBeInTheDocument();
    expect(setClientPref).toHaveBeenCalledWith("playback.trim-silence", "on");
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
