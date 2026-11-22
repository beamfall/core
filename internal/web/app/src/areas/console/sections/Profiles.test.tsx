import "@testing-library/jest-dom/vitest";
import { act, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  createProfile,
  disableProfile,
  fetchProfilesAdmin,
  reorderProfiles,
  setProfileAvatar,
  setProfileEntryPin,
  updateProfile,
} from "@/lib/graphql";
import type { ProfileAdminInfo } from "@/lib/graphql/types";
import { I18nProvider } from "@/lib/i18n";
import { setAdminEpochSignal } from "../adminEpoch";
import { Profiles } from "./Profiles";

vi.mock("@/lib/graphql", () => ({
  fetchProfilesAdmin: vi.fn(),
  createProfile: vi.fn(),
  updateProfile: vi.fn(),
  disableProfile: vi.fn(),
  reorderProfiles: vi.fn(),
  setProfileAvatar: vi.fn(),
  setProfileEntryPin: vi.fn(),
  uploadProfilePicture: vi.fn(),
  removeProfilePicture: vi.fn(),
}));

const rows: ProfileAdminInfo[] = [
  { id: "p1", name: "Owner", kind: "adult", libraryIDs: ["library-local"], disabled: false },
  { id: "p2", name: "Kid", kind: "managed", libraryIDs: ["library-local"], disabled: false },
];
const salon: ProfileAdminInfo = {
  id: "p3",
  name: "Salon",
  kind: "adult",
  libraryIDs: ["library-local"],
  disabled: false,
};

beforeEach(() => {
  // Radix Select needs these jsdom shims.
  if (!Element.prototype.hasPointerCapture) Element.prototype.hasPointerCapture = () => false;
  if (!Element.prototype.scrollIntoView) Element.prototype.scrollIntoView = () => {};
  vi.mocked(fetchProfilesAdmin).mockReset();
  vi.mocked(createProfile).mockReset();
  vi.mocked(updateProfile).mockReset();
  vi.mocked(disableProfile).mockReset();
  vi.mocked(fetchProfilesAdmin).mockResolvedValue(rows);
  vi.mocked(createProfile).mockResolvedValue(salon);
  vi.mocked(updateProfile).mockResolvedValue({
    id: "p2",
    name: "Teen",
    kind: "managed",
    libraryIDs: ["library-local"],
    disabled: false,
  });
  vi.mocked(disableProfile).mockResolvedValue({ id: "p2", disabled: true });
  vi.mocked(reorderProfiles).mockReset();
  vi.mocked(reorderProfiles).mockResolvedValue(["p2", "p1"]);
  vi.mocked(setProfileAvatar).mockReset();
  vi.mocked(setProfileAvatar).mockResolvedValue(undefined as never);
  vi.mocked(setProfileEntryPin).mockReset().mockResolvedValue(undefined);
});

afterEach(() => setAdminEpochSignal());

function rowOrder() {
  return screen
    .getAllByLabelText("Profile name")
    .slice(1)
    .map((input) => (input as HTMLInputElement).value);
}

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((res) => {
    resolve = res;
  });
  return { promise, resolve };
}

function renderSection() {
  return render(
    <I18nProvider>
      <Profiles />
    </I18nProvider>,
  );
}

describe("Console Profiles lane", () => {
  it("lists the household profiles with their kind", async () => {
    renderSection();
    expect(await screen.findByDisplayValue("Owner")).toBeInTheDocument();
    expect(screen.getByDisplayValue("Kid")).toBeInTheDocument();
    expect(screen.getAllByText("Child (managed)").length).toBeGreaterThan(0);
  });

  it("creates an adult profile and refreshes the list", async () => {
    const user = userEvent.setup();
    renderSection();
    await screen.findByDisplayValue("Owner");
    const create = screen.getByRole("button", { name: "Create profile" });
    expect(create).toBeDisabled();
    await user.type(screen.getAllByLabelText("Profile name")[0], "Salon");
    expect(create).toBeEnabled();
    await user.click(create);
    expect(createProfile).toHaveBeenCalledWith("Salon", "adult");
    await waitFor(() => expect(fetchProfilesAdmin).toHaveBeenCalledTimes(2));
  });

  it("edits a profile name and kind", async () => {
    const user = userEvent.setup();
    renderSection();
    const kidName = await screen.findByDisplayValue("Kid");
    const row = kidName.closest("li");
    if (!row) throw new Error("expected profile row");
    await user.clear(kidName);
    await user.type(kidName, "Teen");
    await user.click(within(row).getByRole("combobox", { name: "Profile type" }));
    await user.click(await screen.findByRole("option", { name: "Adult" }));
    await user.click(within(row).getByRole("button", { name: "Save" }));
    expect(updateProfile).toHaveBeenCalledWith("p2", "Teen", "adult");
  });

  it("sets an adult Profile's entry PIN with the matching keypad digits", async () => {
    const user = userEvent.setup();
    renderSection();

    const entryPin = await screen.findByRole("button", { name: "Entry PIN for Owner" });
    expect(screen.queryByRole("button", { name: "Entry PIN for Kid" })).not.toBeInTheDocument();
    await user.click(entryPin);
    const dialog = await screen.findByRole("dialog", { name: "Entry PIN for Owner" });
    const setup = within(dialog);
    await user.click(setup.getByRole("button", { name: "Directions" }));
    await user.keyboard("{ArrowDown}{ArrowRight}{ArrowLeft}{ArrowUp}{ArrowDown}{ArrowRight}");
    const confirm = within(setup.getByRole("group", { name: "Confirm PIN" }));
    for (const direction of ["Down", "Right", "Left", "Up", "Down", "Right"]) {
      await user.click(confirm.getByRole("button", { name: direction }));
    }
    await user.click(setup.getByRole("button", { name: "Set PIN" }));

    expect(setProfileEntryPin).toHaveBeenCalledWith("p1", "864286", undefined);
    expect(await screen.findByRole("status")).toHaveTextContent("PIN saved.");
  });

  it("offboards a profile only after the confirm dialog explains the effect", async () => {
    const user = userEvent.setup();
    renderSection();
    await screen.findByDisplayValue("Kid");
    // Open the confirm for the managed profile row (second row).
    const disableButtons = screen.getAllByRole("button", { name: "Disable" });
    await user.click(disableButtons[1]);
    // The effect explanation is shown before confirming.
    const dialog = await screen.findByRole("alertdialog");
    expect(within(dialog).getByText(/hides the profile from pickers/i)).toBeInTheDocument();
    // Confirm via the dialog's action button.
    await user.click(within(dialog).getByRole("button", { name: "Disable" }));
    expect(disableProfile).toHaveBeenCalledWith("p2");
  });

  it("moves a profile with the arrow buttons, saves the order and announces it (CORE-2477)", async () => {
    const user = userEvent.setup();
    vi.mocked(fetchProfilesAdmin)
      .mockResolvedValueOnce(rows)
      .mockResolvedValueOnce([rows[1], rows[0]]);
    renderSection();
    await screen.findByDisplayValue("Kid");
    expect(screen.getByRole("button", { name: "Move Owner up" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Move Kid down" })).toBeDisabled();
    await user.click(screen.getByRole("button", { name: "Move Kid up" }));
    expect(reorderProfiles).toHaveBeenCalledWith(["p2", "p1"], undefined);
    expect(rowOrder()).toEqual(["Kid", "Owner"]);
    expect(screen.getByText("Kid moved to position 1 of 2.")).toBeInTheDocument();
  });

  it("moves a profile with Alt+Arrow from its row", async () => {
    const user = userEvent.setup();
    vi.mocked(fetchProfilesAdmin)
      .mockResolvedValueOnce(rows)
      .mockResolvedValueOnce([rows[1], rows[0]]);
    renderSection();
    await screen.findByDisplayValue("Owner");
    await user.click(screen.getByRole("button", { name: "Move Kid up" }));
    await waitFor(() => expect(rowOrder()).toEqual(["Kid", "Owner"]));
    screen.getByRole("button", { name: "Move Kid down" }).focus();
    await user.keyboard("{Alt>}{ArrowDown}{/Alt}");
    expect(reorderProfiles).toHaveBeenLastCalledWith(["p1", "p2"], undefined);
  });

  it("keeps a saved reorder when an older create refresh returns and reconciles with the server", async () => {
    const user = userEvent.setup();
    const older = deferred<ProfileAdminInfo[]>();
    const saved = deferred<string[]>();
    const reconciled = deferred<ProfileAdminInfo[]>();
    vi.mocked(fetchProfilesAdmin)
      .mockResolvedValueOnce(rows)
      .mockReturnValueOnce(older.promise)
      .mockReturnValueOnce(reconciled.promise);
    vi.mocked(reorderProfiles).mockReturnValueOnce(saved.promise);
    renderSection();
    await screen.findByDisplayValue("Kid");

    await user.type(screen.getAllByLabelText("Profile name")[0], "Salon");
    await user.click(screen.getByRole("button", { name: "Create profile" }));
    await waitFor(() => expect(fetchProfilesAdmin).toHaveBeenCalledTimes(2));
    const olderSignal = vi.mocked(fetchProfilesAdmin).mock.calls[1]?.[0];

    await user.click(screen.getByRole("button", { name: "Move Kid up" }));
    expect(reorderProfiles).toHaveBeenCalledWith(["p2", "p1"], undefined);
    expect(rowOrder()).toEqual(["Kid", "Owner"]);
    const olderWasAbortedBeforeSave = olderSignal?.aborted;
    await act(async () => saved.resolve(["p2", "p1", "p3"]));

    // Model a response that was already in flight and arrives despite abort.
    await act(async () => older.resolve([...rows, salon]));
    expect(rowOrder()).toEqual(["Kid", "Owner"]);
    expect(olderWasAbortedBeforeSave).toBe(true);
    expect(fetchProfilesAdmin).toHaveBeenCalledTimes(3);

    await act(async () => reconciled.resolve([rows[1], rows[0], salon]));
    expect(rowOrder()).toEqual(["Kid", "Owner", "Salon"]);
  });

  it("preserves an unsaved name through a failed reorder refresh and retry (CORE-2628)", async () => {
    const user = userEvent.setup();
    vi.mocked(fetchProfilesAdmin)
      .mockResolvedValueOnce(rows)
      .mockRejectedValueOnce(new Error("temporary read failure"))
      .mockResolvedValueOnce([rows[1], rows[0], salon]);
    renderSection();
    const ownerName = await screen.findByDisplayValue("Owner");
    await user.type(ownerName, " draft");
    await user.click(screen.getByRole("button", { name: "Move Kid up" }));

    expect(reorderProfiles).toHaveBeenCalledWith(["p2", "p1"], undefined);
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Profiles are unavailable right now.",
    );
    expect(rowOrder()).toEqual(["Kid", "Owner draft"]);
    expect(screen.getByDisplayValue("Owner draft")).toBe(ownerName);

    await user.click(screen.getByRole("button", { name: "Try again" }));
    await screen.findByDisplayValue("Salon");
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
    expect(rowOrder()).toEqual(["Kid", "Owner draft", "Salon"]);
    expect(screen.getByDisplayValue("Owner draft")).toBe(ownerName);
    expect(reorderProfiles).toHaveBeenCalledTimes(1);
  });

  it("saves one order at a time, ending on the newest one", async () => {
    const user = userEvent.setup();
    vi.mocked(fetchProfilesAdmin)
      .mockResolvedValueOnce(rows)
      .mockResolvedValueOnce([rows[1], rows[0]]);
    let release = () => {};
    vi.mocked(reorderProfiles).mockImplementationOnce(
      (ids) =>
        new Promise((resolve) => {
          release = () => resolve(ids);
        }),
    );
    renderSection();
    await screen.findByDisplayValue("Kid");
    await user.click(screen.getByRole("button", { name: "Move Kid up" }));
    await user.click(screen.getByRole("button", { name: "Move Kid down" }));
    await user.click(screen.getByRole("button", { name: "Move Owner down" }));
    // The later moves wait for the first save rather than racing it.
    expect(reorderProfiles).toHaveBeenCalledTimes(1);
    expect(fetchProfilesAdmin).toHaveBeenCalledTimes(1);
    release();
    await waitFor(() => expect(reorderProfiles).toHaveBeenCalledTimes(2));
    expect(reorderProfiles).toHaveBeenLastCalledWith(["p2", "p1"], undefined);
    expect(fetchProfilesAdmin).toHaveBeenCalledTimes(2);
    expect(rowOrder()).toEqual(["Kid", "Owner"]);
  });

  it("aborts the active reorder and discards queued moves when the Console locks", async () => {
    const user = userEvent.setup();
    const session = new AbortController();
    setAdminEpochSignal(session.signal);
    const aborted = vi.fn();
    vi.mocked(reorderProfiles).mockImplementationOnce(
      (_ids, signal) =>
        new Promise((_resolve, reject) => {
          signal?.addEventListener("abort", () => {
            aborted();
            reject(signal.reason);
          });
        }),
    );
    const view = renderSection();
    await screen.findByDisplayValue("Kid");
    await user.click(screen.getByRole("button", { name: "Move Kid up" }));
    await user.click(screen.getByRole("button", { name: "Move Kid down" }));
    expect(reorderProfiles).toHaveBeenCalledTimes(1);

    await act(async () => {
      session.abort();
      setAdminEpochSignal();
    });
    view.unmount();

    expect(aborted).toHaveBeenCalledOnce();
    expect(reorderProfiles).toHaveBeenCalledTimes(1);
    expect(fetchProfilesAdmin).toHaveBeenCalledTimes(1);
  });

  it("holds moves until a create refresh that replaces reorder recovery settles", async () => {
    const user = userEvent.setup();
    const replacement = deferred<ProfileAdminInfo[]>();
    const aborted = vi.fn();
    vi.mocked(fetchProfilesAdmin)
      .mockResolvedValueOnce(rows)
      .mockImplementationOnce(
        (signal) =>
          new Promise((_resolve, reject) => {
            signal?.addEventListener("abort", () => {
              aborted();
              reject(signal.reason);
            });
          }),
      )
      .mockReturnValueOnce(replacement.promise);
    vi.mocked(reorderProfiles).mockRejectedValueOnce(new Error("nope"));
    renderSection();
    await screen.findByDisplayValue("Kid");

    await user.click(screen.getByRole("button", { name: "Move Kid up" }));
    await screen.findByText("Could not save the new order.");
    expect(screen.getByRole("button", { name: "Move Kid down" })).toBeDisabled();

    await user.type(screen.getAllByLabelText("Profile name")[0], "Salon");
    await user.click(screen.getByRole("button", { name: "Create profile" }));
    expect(fetchProfilesAdmin).toHaveBeenCalledTimes(3);
    expect(aborted).toHaveBeenCalledOnce();
    expect(screen.getByRole("button", { name: "Move Kid down" })).toBeDisabled();

    await act(async () => replacement.resolve([...rows, salon]));
    expect(rowOrder()).toEqual(["Owner", "Kid", "Salon"]);
    expect(screen.getByRole("button", { name: "Move Kid up" })).toBeEnabled();
    expect(reorderProfiles).toHaveBeenCalledTimes(1);
  });

  it("holds moves while the list reloads after a failed save", async () => {
    const user = userEvent.setup();
    vi.mocked(reorderProfiles).mockRejectedValueOnce(new Error("nope"));
    renderSection();
    await screen.findByDisplayValue("Kid");
    let reload = () => {};
    vi.mocked(fetchProfilesAdmin).mockImplementationOnce(
      () =>
        new Promise((resolve) => {
          reload = () => resolve(rows);
        }),
    );
    await user.click(screen.getByRole("button", { name: "Move Kid up" }));
    await screen.findByText("Could not save the new order.");
    expect(screen.getByRole("button", { name: "Move Kid down" })).toBeDisabled();
    reload();
    await waitFor(() => expect(rowOrder()).toEqual(["Owner", "Kid"]));
    await waitFor(() => expect(screen.getByRole("button", { name: "Move Kid up" })).toBeEnabled());
    await user.click(screen.getByRole("button", { name: "Move Kid up" }));
    expect(reorderProfiles).toHaveBeenCalledTimes(2);
    expect(reorderProfiles).toHaveBeenLastCalledWith(["p2", "p1"], undefined);
  });

  it("keeps moves held until a recovery read succeeds after the save and reload both fail", async () => {
    const user = userEvent.setup();
    vi.mocked(reorderProfiles).mockRejectedValueOnce(new Error("nope"));
    renderSection();
    await screen.findByDisplayValue("Kid");
    vi.mocked(fetchProfilesAdmin).mockRejectedValueOnce(new Error("temporary read failure"));
    await user.click(screen.getByRole("button", { name: "Move Kid up" }));
    await screen.findByText("Profiles are unavailable right now.");
    expect(screen.getByRole("button", { name: "Move Kid down" })).toBeDisabled();

    const retry = deferred<ProfileAdminInfo[]>();
    vi.mocked(fetchProfilesAdmin).mockReturnValueOnce(retry.promise);
    await user.click(screen.getByRole("button", { name: "Try again" }));
    expect(screen.getByRole("button", { name: "Move Kid down" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Move Owner up" })).toBeDisabled();

    await act(async () => retry.resolve(rows));
    await waitFor(() => expect(screen.getByRole("button", { name: "Move Kid up" })).toBeEnabled());
    expect(rowOrder()).toEqual(["Owner", "Kid"]);
    expect(reorderProfiles).toHaveBeenCalledTimes(1);
  });

  it("restores the server order and says so when saving the order fails", async () => {
    const user = userEvent.setup();
    vi.mocked(reorderProfiles).mockRejectedValue(new Error("nope"));
    renderSection();
    await screen.findByDisplayValue("Kid");
    await user.click(screen.getByRole("button", { name: "Move Kid up" }));
    expect(await screen.findByText("Could not save the new order.")).toBeInTheDocument();
    await waitFor(() => expect(rowOrder()).toEqual(["Owner", "Kid"]));
  });

  it("customizes a profile's icon and colour from its row (CORE-1241)", async () => {
    const user = userEvent.setup();
    renderSection();
    await screen.findByDisplayValue("Kid");
    await user.click(screen.getByRole("button", { name: "Customize Kid" }));
    const dialog = await screen.findByRole("dialog", { name: "Customize Kid" });
    expect(within(dialog).getByRole("button", { name: "Upload picture" })).toBeInTheDocument();
    await user.click(within(dialog).getByRole("radio", { name: "Star" }));
    await waitFor(() =>
      expect(setProfileAvatar).toHaveBeenCalledWith("p2", expect.stringMatching(/^star/)),
    );
    await user.click(within(dialog).getByRole("button", { name: "Done" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
  });
});
