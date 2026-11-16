import "@testing-library/jest-dom/vitest";
import { act, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { setAdminEpochSignal } from "@/areas/console/adminEpoch";
import { ApiError, setIdentityEpochSignal } from "@/lib/api";
import {
  acknowledgeBackupKey,
  fetchAdminSession,
  fetchBackupKeyStatus,
  revealBackupKey,
} from "@/lib/graphql";
import type { BackupKeyStatusInfo, RevealedBackupKeyInfo } from "@/lib/graphql/types";
import { I18nProvider } from "@/lib/i18n";
import { BACKUP_KEY_SESSION_RECHECK_MS, BackupKeySection } from "./BackupKeySection";

vi.mock("@/lib/graphql", () => ({
  fetchBackupKeyStatus: vi.fn(),
  revealBackupKey: vi.fn(),
  acknowledgeBackupKey: vi.fn(),
  fetchAdminSession: vi.fn(),
}));

const mockStatus = vi.mocked(fetchBackupKeyStatus);
const mockReveal = vi.mocked(revealBackupKey);
const mockAck = vi.mocked(acknowledgeBackupKey);
const mockAdminSession = vi.mocked(fetchAdminSession);

function sessionFor(ms: number) {
  return { active: true, expiresAt: new Date(Date.now() + ms).toISOString() };
}

// Not a real key: the section treats the value as opaque text.
const PLACEHOLDER_KEY = "placeholder-backup-key";
const PENDING: BackupKeyStatusInfo = {
  pending: true,
  expiresAt: "2026-12-15T12:00:00Z",
  daysLeft: 30,
};
const SAVED: BackupKeyStatusInfo = { pending: false, expiresAt: "", daysLeft: 0 };
const REVEALED: RevealedBackupKeyInfo = {
  escrowID: "escrow-1",
  backupKey: PLACEHOLDER_KEY,
  expiresAt: PENDING.expiresAt,
  daysLeft: 30,
};

function renderSection() {
  return render(
    <I18nProvider>
      <BackupKeySection />
    </I18nProvider>,
  );
}

async function revealKey() {
  const user = userEvent.setup();
  renderSection();
  await user.click(await screen.findByRole("button", { name: "Show backup key" }));
  await screen.findByText(PLACEHOLDER_KEY);
  return user;
}

beforeEach(() => {
  setAdminEpochSignal();
  setIdentityEpochSignal();
  mockAdminSession.mockReset().mockImplementation(async () => sessionFor(60 * 60_000));
  mockStatus.mockReset().mockResolvedValue(PENDING);
  mockReveal.mockReset().mockResolvedValue(REVEALED);
  mockAck.mockReset().mockResolvedValue(SAVED);
});

afterEach(() => {
  setAdminEpochSignal();
  setIdentityEpochSignal();
  vi.useRealTimers();
});

describe("BackupKeySection (CORE-2032)", () => {
  it("renders nothing once the backup key is saved", async () => {
    mockStatus.mockResolvedValue(SAVED);
    const { container } = renderSection();
    await waitFor(() => expect(mockStatus).toHaveBeenCalledTimes(1));
    expect(container).toBeEmptyDOMElement();
  });

  it("warns with the days left before the server replaces the key", async () => {
    mockStatus.mockResolvedValue({ ...PENDING, daysLeft: 1 });
    renderSection();
    expect(await screen.findByText(/1 day is left/)).toBeInTheDocument();
    expect(screen.queryByText(PLACEHOLDER_KEY)).not.toBeInTheDocument();
  });

  it("reveals the key only on request and keeps it out of storage and the URL", async () => {
    const user = userEvent.setup();
    renderSection();
    expect(await screen.findByText(/30 days are left/)).toBeInTheDocument();
    expect(mockReveal).not.toHaveBeenCalled();

    await user.click(screen.getByRole("button", { name: "Show backup key" }));
    expect(await screen.findByText(PLACEHOLDER_KEY)).toBeInTheDocument();
    expect(screen.getByText(/Backups taken before you confirm use a temporary key/)).toBeVisible();
    expect(window.location.href).not.toContain(PLACEHOLDER_KEY);
    expect(JSON.stringify({ ...window.localStorage })).not.toContain(PLACEHOLDER_KEY);
    expect(JSON.stringify({ ...window.sessionStorage })).not.toContain(PLACEHOLDER_KEY);
  });

  it("confirms with the escrow ID only after the owner ticks saved, then drops the key", async () => {
    const user = await revealKey();
    const confirm = screen.getByRole("button", { name: "Confirm key saved" });
    expect(confirm).toBeDisabled();

    await user.click(screen.getByLabelText("I have saved this backup key somewhere safe"));
    await user.click(confirm);

    expect(await screen.findByText(/Backup key confirmed\. Take a new backup/)).toBeInTheDocument();
    expect(mockAck).toHaveBeenCalledWith("escrow-1", expect.any(AbortSignal));
    expect(mockAck.mock.calls[0]).not.toContain(PLACEHOLDER_KEY);
    expect(screen.queryByText(PLACEHOLDER_KEY)).not.toBeInTheDocument();
  });

  it("drops the key and reloads the window when the server refuses the confirm", async () => {
    mockAck.mockRejectedValue(new ApiError("the backup key changed; reveal it again", 200));
    const user = await revealKey();
    mockStatus.mockResolvedValue({ ...PENDING, daysLeft: 30 });

    await user.click(screen.getByLabelText("I have saved this backup key somewhere safe"));
    await user.click(screen.getByRole("button", { name: "Confirm key saved" }));

    expect(await screen.findByText("the backup key changed; reveal it again")).toBeInTheDocument();
    expect(screen.queryByText(PLACEHOLDER_KEY)).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Show backup key" })).toBeInTheDocument();
    expect(mockStatus).toHaveBeenCalledTimes(2);
  });

  it("reveals nothing when the server refuses (no Admin Session)", async () => {
    mockReveal.mockRejectedValue(new ApiError("admin session required", 200));
    const user = userEvent.setup();
    renderSection();
    await user.click(await screen.findByRole("button", { name: "Show backup key" }));
    expect(await screen.findByText("admin session required")).toBeInTheDocument();
    expect(screen.queryByText(PLACEHOLDER_KEY)).not.toBeInTheDocument();
  });

  it("forgets the revealed key the moment the Admin Session ends", async () => {
    const adminEpoch = new AbortController();
    setAdminEpochSignal(adminEpoch.signal);
    await revealKey();
    expect(mockReveal.mock.calls[0][0]?.aborted).toBe(false);

    act(() => adminEpoch.abort());
    expect(screen.queryByText(PLACEHOLDER_KEY)).not.toBeInTheDocument();
  });

  it("drops a reveal that lands after the Admin Session ended", async () => {
    const adminEpoch = new AbortController();
    setAdminEpochSignal(adminEpoch.signal);
    let resolveReveal!: (value: RevealedBackupKeyInfo) => void;
    mockReveal.mockImplementation(
      () =>
        new Promise((resolve) => {
          resolveReveal = resolve;
        }),
    );
    const user = userEvent.setup();
    renderSection();
    await user.click(await screen.findByRole("button", { name: "Show backup key" }));

    act(() => adminEpoch.abort());
    await act(async () => resolveReveal(REVEALED));
    expect(screen.queryByText(PLACEHOLDER_KEY)).not.toBeInTheDocument();
  });
  // /manage/backups publishes no Admin epoch, so these run with none: the key
  // must follow the Admin Session on its own.
  describe("without a published Admin epoch (the Manage route)", () => {
    it("drops the key when the Admin Session window lapses", async () => {
      mockAdminSession.mockImplementation(async () => sessionFor(150));
      await revealKey();
      await waitFor(() => expect(screen.queryByText(PLACEHOLDER_KEY)).not.toBeInTheDocument());
      expect(screen.getByRole("button", { name: "Show backup key" })).toBeInTheDocument();
    });

    it("drops the key when the Admin Session is locked elsewhere", async () => {
      vi.useFakeTimers({ shouldAdvanceTime: true });
      await revealKey();
      await waitFor(() => expect(mockAdminSession).toHaveBeenCalledTimes(1));
      expect(screen.getByText(PLACEHOLDER_KEY)).toBeInTheDocument();

      mockAdminSession.mockResolvedValue({ active: false, expiresAt: "" });
      await act(async () => {
        await vi.advanceTimersByTimeAsync(BACKUP_KEY_SESSION_RECHECK_MS);
      });
      expect(screen.queryByText(PLACEHOLDER_KEY)).not.toBeInTheDocument();
    });

    it("drops the key when the session cannot be read", async () => {
      mockAdminSession.mockImplementation(
        () => new Promise((_, reject) => setTimeout(() => reject(new Error("offline")), 50)),
      );
      await revealKey();
      await waitFor(() => expect(screen.queryByText(PLACEHOLDER_KEY)).not.toBeInTheDocument());
    });

    it("drops the key on an identity change (Profile switch, relock)", async () => {
      const identity = new AbortController();
      setIdentityEpochSignal(identity.signal);
      await revealKey();
      act(() => identity.abort());
      expect(screen.queryByText(PLACEHOLDER_KEY)).not.toBeInTheDocument();
    });
  });
});
