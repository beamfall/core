import "@testing-library/jest-dom/vitest";
import { cleanup, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { clearDevicePlayerBinding, setDevicePlayerBinding } from "@/lib/graphql";
import type { DeviceInfo } from "@/lib/graphql/types";
import { I18nProvider } from "@/lib/i18n";
import { setAdminEpochSignal } from "../adminEpoch";
import { DevicePlayerBinding } from "./DevicePlayerBinding";

vi.mock("@/lib/graphql", () => ({
  setDevicePlayerBinding: vi.fn(),
  clearDevicePlayerBinding: vi.fn(),
}));

const tv: DeviceInfo = {
  deviceID: "d1",
  name: "Living room Apple TV",
  role: "viewer",
  revoked: false,
  current: false,
};
const players = [{ id: "pl1", name: "Lounge" }];

function renderBinding(device: DeviceInfo = tv, choices = players) {
  const onChanged = vi.fn();
  render(
    <I18nProvider>
      <DevicePlayerBinding device={device} players={choices} onChanged={onChanged} />
    </I18nProvider>,
  );
  return onChanged;
}

beforeEach(() => {
  setAdminEpochSignal(new AbortController().signal);
  vi.mocked(setDevicePlayerBinding)
    .mockReset()
    .mockResolvedValue(undefined as never);
  vi.mocked(clearDevicePlayerBinding)
    .mockReset()
    .mockResolvedValue(undefined as never);
});
afterEach(() => {
  cleanup();
  setAdminEpochSignal();
});

describe("DevicePlayerBinding", () => {
  it("binds a device to a Player Profile with a display name", async () => {
    const user = userEvent.setup();
    const onChanged = renderBinding();
    await user.click(
      screen.getByRole("button", { name: "Use Living room Apple TV as a player display" }),
    );
    const dialog = await screen.findByRole("dialog");
    const name = within(dialog).getByLabelText("Display name");
    await user.clear(name);
    await user.type(name, "Lounge TV");
    await user.click(within(dialog).getByRole("button", { name: "Save" }));
    expect(setDevicePlayerBinding).toHaveBeenCalledWith(
      "d1",
      "pl1",
      "Lounge TV",
      expect.anything(),
    );
    expect(onChanged).toHaveBeenCalled();
  });

  it("asks for a Player Profile first when there is none", async () => {
    const user = userEvent.setup();
    renderBinding(tv, []);
    await user.click(screen.getByRole("button", { name: /as a player display/ }));
    expect(
      await screen.findByText("Create a Player Profile in Profiles first."),
    ).toBeInTheDocument();
  });

  it("reports a refused binding", async () => {
    const user = userEvent.setup();
    vi.mocked(setDevicePlayerBinding).mockRejectedValueOnce(new Error("revoked"));
    renderBinding();
    await user.click(screen.getByRole("button", { name: /as a player display/ }));
    await user.click(
      within(await screen.findByRole("dialog")).getByRole("button", { name: "Save" }),
    );
    expect(await screen.findByRole("alert")).toHaveTextContent("couldn’t be saved");
  });

  it("clears a binding only after confirming", async () => {
    const user = userEvent.setup();
    const onChanged = renderBinding({
      ...tv,
      playerProfileID: "pl1",
      playerDisplayName: "Lounge TV",
    });
    await user.click(screen.getByRole("button", { name: "Stop using as a player display" }));
    const dialog = await screen.findByRole("alertdialog");
    expect(clearDevicePlayerBinding).not.toHaveBeenCalled();
    await user.click(
      within(dialog).getByRole("button", { name: "Stop using as a player display" }),
    );
    expect(clearDevicePlayerBinding).toHaveBeenCalledWith("d1", expect.anything());
    expect(onChanged).toHaveBeenCalled();
  });
});
