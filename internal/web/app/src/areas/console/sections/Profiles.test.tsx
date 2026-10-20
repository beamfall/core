import "@testing-library/jest-dom/vitest";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { createProfile, disableProfile, fetchProfilesAdmin, updateProfile } from "@/lib/graphql";
import type { ProfileAdminInfo } from "@/lib/graphql/types";
import { I18nProvider } from "@/lib/i18n";
import { Profiles } from "./Profiles";

vi.mock("@/lib/graphql", () => ({
  fetchProfilesAdmin: vi.fn(),
  createProfile: vi.fn(),
  updateProfile: vi.fn(),
  disableProfile: vi.fn(),
}));

const rows: ProfileAdminInfo[] = [
  { id: "p1", name: "Owner", kind: "adult", libraryIDs: ["library-local"], disabled: false },
  { id: "p2", name: "Kid", kind: "managed", libraryIDs: ["library-local"], disabled: false },
];

beforeEach(() => {
  // Radix Select needs these jsdom shims.
  if (!Element.prototype.hasPointerCapture) Element.prototype.hasPointerCapture = () => false;
  if (!Element.prototype.scrollIntoView) Element.prototype.scrollIntoView = () => {};
  vi.mocked(fetchProfilesAdmin).mockReset();
  vi.mocked(createProfile).mockReset();
  vi.mocked(updateProfile).mockReset();
  vi.mocked(disableProfile).mockReset();
  vi.mocked(fetchProfilesAdmin).mockResolvedValue(rows);
  vi.mocked(createProfile).mockResolvedValue({
    id: "p3",
    name: "Salon",
    kind: "adult",
    libraryIDs: ["library-local"],
    disabled: false,
  });
  vi.mocked(updateProfile).mockResolvedValue({
    id: "p2",
    name: "Teen",
    kind: "managed",
    libraryIDs: ["library-local"],
    disabled: false,
  });
  vi.mocked(disableProfile).mockResolvedValue({ id: "p2", disabled: true });
});

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
    expect(screen.getAllByText("Managed").length).toBeGreaterThan(0);
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
    await user.click(within(row).getByRole("combobox", { name: "Kind" }));
    await user.click(await screen.findByRole("option", { name: "Adult" }));
    await user.click(within(row).getByRole("button", { name: "Save" }));
    expect(updateProfile).toHaveBeenCalledWith("p2", "Teen", "adult");
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
});
