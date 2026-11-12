import "@testing-library/jest-dom/vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { profilePickedThisTab } from "@/components/beamfall/profile";
import { I18nProvider } from "@/lib/i18n";
import { useSession } from "@/lib/session";
import { ProfilePickerPage } from "./ProfilePickerPage";

const navigate = vi.fn();

vi.mock("react-router", async (importOriginal) => ({
  ...(await importOriginal<typeof import("react-router")>()),
  useNavigate: () => navigate,
  useLocation: () => ({ pathname: "/profiles", search: "", hash: "", state: null, key: "k" }),
}));

vi.mock("@/areas/enroll", () => ({
  SignOutButton: () => null,
}));

vi.mock("@/lib/session", () => ({
  useSession: vi.fn(),
}));

const switchProfile = vi.fn();

beforeEach(() => {
  navigate.mockReset();
  switchProfile.mockReset();
  window.sessionStorage.clear();
  vi.mocked(useSession).mockReturnValue({
    session: { deviceRole: "viewer", profileID: "p1", profile: "adult", revealed: false },
    profiles: [
      { id: "p1", name: "Robin", kind: "adult", avatar: "rocket.3" },
      { id: "p2", name: "Sam", kind: "adult" },
      { id: "p3", name: "Kid", kind: "managed" },
    ],
    switchProfile,
  } as unknown as ReturnType<typeof useSession>);
});

function renderPage() {
  return render(
    <I18nProvider>
      <ProfilePickerPage />
    </I18nProvider>,
  );
}

describe("ProfilePickerPage", () => {
  it("shows one avatar tile per Profile", () => {
    renderPage();
    expect(screen.getByRole("heading", { name: "Who's watching?" })).toBeInTheDocument();
    expect(screen.getAllByRole("listitem")).toHaveLength(3);
  });

  it("continues without a switch when the current Profile is picked", async () => {
    renderPage();
    await userEvent.click(screen.getByRole("button", { name: /Robin/ }));
    expect(switchProfile).not.toHaveBeenCalled();
    expect(navigate).toHaveBeenCalledWith("/", { replace: true });
    expect(profilePickedThisTab()).toBe(true);
  });

  it("switches to a managed Profile without asking for a PIN", async () => {
    switchProfile.mockResolvedValue(undefined);
    renderPage();
    await userEvent.click(screen.getByRole("button", { name: /Kid/ }));
    expect(switchProfile).toHaveBeenCalledWith("p3", undefined);
    await waitFor(() => expect(navigate).toHaveBeenCalledWith("/", { replace: true }));
  });

  it("asks for an adult Profile's PIN in place and switches with it", async () => {
    switchProfile.mockResolvedValue(undefined);
    renderPage();
    await userEvent.click(screen.getByRole("button", { name: /Sam/ }));
    expect(switchProfile).not.toHaveBeenCalled();
    expect(screen.getByRole("heading", { name: "Hi, Sam" })).toBeInTheDocument();
    await userEvent.type(screen.getByLabelText("PIN"), "2468");
    await userEvent.click(screen.getByRole("button", { name: "Continue" }));
    expect(switchProfile).toHaveBeenCalledWith("p2", "2468");
    await waitFor(() => expect(navigate).toHaveBeenCalledWith("/", { replace: true }));
  });

  it("keeps the PIN prompt open with the reason when the PIN is refused", async () => {
    switchProfile.mockRejectedValue(new Error("profile PIN required"));
    renderPage();
    await userEvent.click(screen.getByRole("button", { name: /Sam/ }));
    await userEvent.type(screen.getByLabelText("PIN"), "0000");
    await userEvent.click(screen.getByRole("button", { name: "Continue" }));
    expect(await screen.findByText(/That PIN didn.t work/)).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "Hi, Sam" })).toBeInTheDocument();
    expect(navigate).not.toHaveBeenCalled();
  });

  it("returns to the tiles from the PIN prompt", async () => {
    renderPage();
    await userEvent.click(screen.getByRole("button", { name: /Sam/ }));
    await userEvent.click(screen.getByRole("button", { name: "Choose another profile" }));
    expect(screen.getByRole("heading", { name: "Who's watching?" })).toBeInTheDocument();
  });
});
