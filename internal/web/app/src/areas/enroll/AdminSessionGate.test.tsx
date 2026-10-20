import "@testing-library/jest-dom/vitest";
import { cleanup, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { fetchAdminSession, verifyAdminSession } from "@/lib/graphql";
import { I18nProvider } from "@/lib/i18n";
import { AdminSessionGate } from "./AdminSessionGate";

vi.mock("@/lib/graphql", () => ({
  endAdminSession: vi.fn(),
  fetchAdminSession: vi.fn(),
  verifyAdminSession: vi.fn(),
}));

beforeEach(() => {
  vi.mocked(fetchAdminSession).mockReset().mockResolvedValue({ active: false });
  vi.mocked(verifyAdminSession).mockReset();
});

afterEach(() => {
  cleanup();
});

async function unlock() {
  render(
    <I18nProvider>
      <AdminSessionGate>
        <p>Gated console content</p>
      </AdminSessionGate>
    </I18nProvider>,
  );
  await userEvent.type(await screen.findByLabelText("Admin password"), "correct horse");
  await userEvent.click(screen.getByRole("button", { name: "Unlock Console" }));
}

describe("AdminSessionGate", () => {
  it("unlocks without a notice when the password is accepted and the rebind succeeds", async () => {
    vi.mocked(verifyAdminSession).mockResolvedValue({ active: true, rebind: "bound" });

    await unlock();

    expect(await screen.findByText("Gated console content")).toBeInTheDocument();
    expect(screen.queryByRole("status")).toBeNull();
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("commits the unlock and surfaces a handshake notice when only the rebind failed", async () => {
    vi.mocked(verifyAdminSession).mockResolvedValue({ active: true, rebind: "rebind-failed" });

    await unlock();

    expect(await screen.findByText("Gated console content")).toBeInTheDocument();
    expect(screen.getByRole("status")).toHaveTextContent("server handshake did not complete");
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("keeps the form and reports the rejection when the password is refused", async () => {
    vi.mocked(verifyAdminSession).mockResolvedValue({ active: false, rebind: "bound" });

    await unlock();

    expect(await screen.findByRole("alert")).toHaveTextContent(
      "The admin password was not accepted.",
    );
    expect(screen.queryByText("Gated console content")).toBeNull();
  });
});
