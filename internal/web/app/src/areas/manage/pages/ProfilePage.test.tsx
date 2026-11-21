import "@testing-library/jest-dom/vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { I18nProvider } from "@/lib/i18n";
import { type SessionState, useSession } from "@/lib/session";
import { type Locale, STRINGS } from "@/strings";
import { ProfilePage } from "./ProfilePage";

vi.mock("@/lib/session", () => ({ useSession: vi.fn() }));

const session = {
  deviceRole: "manager",
  profileID: "adult-a",
  profile: "adult",
  revealed: false,
};
const refreshSession = vi.fn(async () => {});
const refreshProfiles = vi.fn(async () => {});
const switchProfile = vi.fn(async () => {});

function renderPage(locale: Locale = "en") {
  return render(
    <I18nProvider initialLocale={locale}>
      <ProfilePage onSwitch={switchProfile} error="" />
    </I18nProvider>,
  );
}

function server({
  active = true,
  rejectRosterRefresh = false,
  rejectSave = false,
  rejectSession = false,
  subtitleSave,
}: {
  active?: boolean;
  rejectRosterRefresh?: boolean;
  rejectSave?: boolean;
  rejectSession?: boolean;
  subtitleSave?: Promise<void>;
} = {}) {
  const profiles = [
    { id: "adult-a", name: "Alex", kind: "adult", entryPinSet: true },
    { id: "adult-b", name: "Sam", kind: "adult", entryPinSet: false },
    { id: "managed-a", name: "Jo", kind: "managed", entryPinSet: false },
  ];
  let languages = ["fr-CA", "en"];
  const writes: Record<string, unknown>[] = [];
  const fetcher = vi.fn<typeof fetch>(async (_input, init) => {
    const { query, variables } = JSON.parse(String(init?.body)) as {
      query: string;
      variables: Record<string, unknown>;
    };
    if (query.startsWith("query AdminSession")) {
      return Response.json({
        data: { adminSession: { active, expiresAt: new Date(Date.now() + 60_000).toISOString() } },
      });
    }
    if (query.startsWith("query SetupState")) {
      return Response.json({ data: { setupState: { adminPasswordSet: true } } });
    }
    if (query.startsWith("query Profiles") && query.includes("entryPinSet")) {
      if (rejectRosterRefresh && writes.length === 1) {
        return Response.json({ errors: [{ message: "Roster refresh failed" }] });
      }
      return Response.json({ data: { profiles } });
    }
    if (query.startsWith("query CurrentSession") && query.includes("subtitleLanguages")) {
      if (rejectSession) return Response.json({ errors: [{ message: "Server refusal" }] });
      return Response.json({
        data: { currentSession: { ...session, subtitleLanguages: languages } },
      });
    }
    if (query.startsWith("mutation UpdateProfile")) {
      writes.push(variables);
      if (query.includes("subtitleLanguages: $subtitleLanguages")) await subtitleSave;
      if (rejectSave) return Response.json({ errors: [{ message: "Server refusal" }] });
      const profile = profiles.find((p) => p.id === variables.profileID);
      if (!profile || !active)
        return Response.json({ errors: [{ message: "admin session required" }] });
      if (query.includes("entryPIN: $entryPIN")) profile.entryPinSet = true;
      if (query.includes("subtitleLanguages: $subtitleLanguages")) {
        languages = variables.subtitleLanguages as string[];
      }
      return Response.json({
        data: { updateProfile: { id: profile.id, subtitleLanguages: languages } },
      });
    }
    throw new Error("Unexpected Profile settings request");
  });
  vi.stubGlobal("fetch", fetcher);
  return { fetcher, writes };
}

async function enterPin(
  user: ReturnType<typeof userEvent.setup>,
  pin: string,
  locale: Locale = "en",
) {
  await user.type(screen.getByLabelText(STRINGS[locale].restrictionsNewPin), pin);
  await user.type(screen.getByLabelText(STRINGS[locale].restrictionsConfirmPin), pin);
}

describe("Manage profile settings", () => {
  beforeEach(() => {
    refreshSession.mockClear();
    refreshProfiles.mockClear();
    switchProfile.mockClear();
    vi.mocked(useSession).mockReturnValue({
      isManager: true,
      session,
      profiles: [
        { id: "adult-a", name: "Alex", kind: "adult" },
        { id: "managed-a", name: "Jo", kind: "managed" },
      ],
      refreshSession,
      refreshProfiles,
    } as unknown as SessionState);
  });

  afterEach(() => vi.unstubAllGlobals());

  it("keeps the current profile selected and switches to a managed profile without a PIN", async () => {
    const user = userEvent.setup();
    renderPage();

    await user.click(screen.getByRole("button", { name: /^Alex/ }));
    expect(switchProfile).not.toHaveBeenCalled();
    await user.click(screen.getByRole("button", { name: /^Jo/ }));
    expect(switchProfile).toHaveBeenCalledExactlyOnceWith("managed-a", undefined);
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });

  it("requires an Admin Session before reading or displaying settings", async () => {
    const user = userEvent.setup();
    const { fetcher, writes } = server({ active: false });
    renderPage();
    await user.click(screen.getByRole("button", { name: "Open settings" }));

    expect(await screen.findByText("Admin password needed")).toBeInTheDocument();
    expect(screen.queryByLabelText("New PIN")).not.toBeInTheDocument();
    expect(
      screen.queryByLabelText(STRINGS.en.manageProfileSubtitleLanguages),
    ).not.toBeInTheDocument();
    expect(fetcher).toHaveBeenCalledTimes(2);
    expect(writes).toEqual([]);
  });

  it("shows the stored PIN status and writes set and change to the selected adult profile", async () => {
    const user = userEvent.setup();
    const { writes } = server();
    let completeRosterRefresh = () => {};
    const rosterRefresh = new Promise<void>((resolve) => {
      completeRosterRefresh = resolve;
    });
    refreshProfiles.mockReturnValueOnce(rosterRefresh);
    renderPage();
    await user.click(screen.getByRole("button", { name: "Open settings" }));

    expect(await screen.findByRole("button", { name: "Change PIN" })).toBeInTheDocument();
    expect(screen.getByLabelText("New PIN")).toHaveValue("");
    expect(screen.queryByRole("option", { name: "Jo" })).not.toBeInTheDocument();
    await user.selectOptions(screen.getByLabelText("Profile"), "adult-b");
    await user.type(screen.getByLabelText("New PIN"), "1234");
    const confirmation = screen.getByLabelText("Confirm PIN");
    await user.type(confirmation, "1235");
    await user.click(screen.getByRole("button", { name: "Set PIN" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("The two PINs don't match.");
    expect(writes).toEqual([]);
    await user.clear(screen.getByLabelText("New PIN"));
    await user.clear(confirmation);
    await enterPin(user, "1234");
    await user.click(screen.getByRole("button", { name: "Set PIN" }));
    await waitFor(() => expect(refreshProfiles).toHaveBeenCalledTimes(1));
    expect(screen.getByRole("button", { name: /^Jo/ })).toBeDisabled();
    expect(screen.getByText("PIN saved.")).toBeVisible();
    completeRosterRefresh();
    expect(await screen.findByRole("button", { name: "Change PIN" })).toBeInTheDocument();
    await waitFor(() => expect(screen.getByRole("button", { name: /^Jo/ })).toBeEnabled());
    expect(screen.getByLabelText("Profile")).toBeEnabled();
    expect(screen.getByLabelText("New PIN")).toHaveValue("");
    expect(screen.getByLabelText("Confirm PIN")).toHaveValue("");
    await enterPin(user, "5678");
    await user.click(screen.getByRole("button", { name: "Change PIN" }));
    expect(await screen.findByText("PIN saved.")).toBeInTheDocument();
    expect(refreshProfiles).toHaveBeenCalledTimes(2);
    expect(writes).toEqual([
      { profileID: "adult-b", entryPIN: "1234" },
      { profileID: "adult-b", entryPIN: "5678" },
    ]);
  });

  it("shows a confirmed PIN save when the editor roster refresh fails", async () => {
    const user = userEvent.setup();
    const { writes } = server({ rejectRosterRefresh: true });
    const copy = STRINGS.fr;
    renderPage("fr");
    await user.click(screen.getByRole("button", { name: copy.manageProfileSettingsOpen }));
    await user.selectOptions(await screen.findByLabelText(copy.profile), "adult-b");
    await enterPin(user, "1234", "fr");
    await user.click(screen.getByRole("button", { name: copy.restrictionsSetPin }));

    expect(await screen.findByRole("alert")).toHaveTextContent(copy.consoleProfilesUnavailable);
    expect(screen.getByText(copy.restrictionsPinSaved)).toBeVisible();
    expect(screen.queryByText(copy.restrictionsPinNotSaved)).not.toBeInTheDocument();
    expect(writes).toEqual([{ profileID: "adult-b", entryPIN: "1234" }]);
  });

  it("keeps the PIN form available when the subtitle languages read fails", async () => {
    const user = userEvent.setup();
    server({ rejectSession: true });
    renderPage("fr");
    await user.click(screen.getByRole("button", { name: STRINGS.fr.manageProfileSettingsOpen }));

    expect(await screen.findByRole("alert")).toHaveTextContent(
      STRINGS.fr.manageProfileSubtitleLanguagesUnavailable,
    );
    expect(
      screen.queryByLabelText(STRINGS.fr.manageProfileSubtitleLanguages),
    ).not.toBeInTheDocument();
    expect(await screen.findByLabelText(STRINGS.fr.restrictionsNewPin)).toBeEnabled();
  });

  it("reads, saves and clears subtitle languages in order for the current profile", async () => {
    const user = userEvent.setup();
    const { writes } = server();
    renderPage();
    await user.click(screen.getByRole("button", { name: "Open settings" }));
    const label = "Preferred subtitle languages for your current profile";
    const field = await screen.findByRole("textbox", { name: label });
    expect(field).toHaveValue("fr-CA, en");
    await user.selectOptions(await screen.findByLabelText("Profile"), "adult-b");
    expect(field).toHaveAccessibleName(label);
    await user.clear(field);
    await user.type(field, "es, fr-CA , ja");
    await user.click(screen.getByRole("button", { name: "Save" }));
    expect(await screen.findByText("Subtitle languages saved.")).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Done" }));
    await user.click(screen.getByRole("button", { name: "Open settings" }));
    const reopened = await screen.findByRole("textbox", { name: label });
    expect(reopened).toHaveValue("es, fr-CA, ja");
    await user.clear(reopened);
    await user.click(screen.getByRole("button", { name: "Save" }));
    expect(await screen.findByText("Subtitle languages saved.")).toBeInTheDocument();
    expect(refreshSession.mock.calls).toEqual([
      [{ profileID: "adult-a", subtitleLanguages: ["es", "fr-CA", "ja"] }],
      [{ profileID: "adult-a", subtitleLanguages: [] }],
    ]);
    expect(writes).toEqual([
      { profileID: "adult-a", subtitleLanguages: ["es", "fr-CA", "ja"] },
      { profileID: "adult-a", subtitleLanguages: [] },
    ]);
  });

  it.each([false, true])(
    "keeps settings open until a delayed subtitle save finishes (save rejected: %s)",
    async (rejectSave) => {
      const user = userEvent.setup();
      let finishSave = () => {};
      const subtitleSave = new Promise<void>((resolve) => {
        finishSave = resolve;
      });
      const { writes } = server({ rejectSave, subtitleSave });
      renderPage();
      await user.click(screen.getByRole("button", { name: "Open settings" }));
      const field = await screen.findByLabelText(STRINGS.en.manageProfileSubtitleLanguages);
      await user.clear(field);
      await user.type(field, "es");
      await user.click(screen.getByRole("button", { name: "Save" }));
      expect(writes).toEqual([{ profileID: "adult-a", subtitleLanguages: ["es"] }]);

      const done = screen.getByRole("button", { name: "Done" });
      const otherProfile = screen.getByRole("button", { name: /^Jo/ });
      expect(done).toBeDisabled();
      expect(otherProfile).toBeDisabled();
      await user.click(done);
      await user.click(otherProfile);
      expect(field).toBeVisible();
      expect(field).toHaveValue("es");
      expect(switchProfile).not.toHaveBeenCalled();

      finishSave();
      const result = rejectSave
        ? STRINGS.en.manageProfileSubtitleLanguagesFailed
        : STRINGS.en.manageProfileSubtitleLanguagesSaved;
      expect(await screen.findByText(result)).toHaveAttribute(
        "role",
        rejectSave ? "alert" : "status",
      );
      expect(field).toHaveValue("es");
      expect(done).toBeEnabled();
      expect(otherProfile).toBeEnabled();
    },
  );

  it.each(Object.keys(STRINGS) as Locale[])(
    "localizes both save errors in %s and retains the subtitle draft",
    async (locale) => {
      const user = userEvent.setup();
      const { writes } = server({ rejectSave: true });
      const copy = STRINGS[locale];
      renderPage(locale);
      await user.click(screen.getByRole("button", { name: copy.manageProfileSettingsOpen }));
      await screen.findByRole("button", { name: copy.restrictionsChangePin });
      await enterPin(user, "1234", locale);
      await user.click(screen.getByRole("button", { name: copy.restrictionsChangePin }));
      expect(await screen.findByText(copy.restrictionsPinNotSaved)).toHaveAttribute(
        "role",
        "alert",
      );
      expect(screen.getByLabelText(copy.restrictionsNewPin)).toHaveValue("");
      expect(screen.queryByRole("status")).not.toBeInTheDocument();

      const field = await screen.findByLabelText(copy.manageProfileSubtitleLanguages);
      await user.clear(field);
      await user.type(field, "es");
      await user.click(screen.getByRole("button", { name: copy.save }));
      expect(await screen.findByText(copy.manageProfileSubtitleLanguagesFailed)).toHaveAttribute(
        "role",
        "alert",
      );
      expect(field).toHaveValue("es");
      expect(screen.queryByRole("status")).not.toBeInTheDocument();
      expect(refreshSession).not.toHaveBeenCalled();
      expect(writes).toHaveLength(2);
    },
  );
});
