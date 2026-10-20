import "@testing-library/jest-dom/vitest";
import { act, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { setAdminEpochSignal } from "@/areas/console/adminEpoch";
import { fetchBackupHistory, fetchMetadataExport, verifyBackup } from "@/lib/graphql";
import type { BackupHistoryEntry, MetadataExportInfo } from "@/lib/graphql/types";
import { I18nProvider } from "@/lib/i18n";
import { BackupExportPage } from "./BackupExportPage";

// Observation seam for the unmount boundary: every useState setter in the tree
// is wrapped (behavior-preserving, stable identity via useRef) so the unmount
// test can count state writes and prove none land after the tree is gone.
const setterLog = vi.hoisted(() => ({ writes: 0 }));

vi.mock("react", async (importOriginal) => {
  const actual = await importOriginal<typeof import("react")>();
  const useState = ((initial?: unknown) => {
    const [state, setState] = actual.useState(initial);
    const wrapped = actual.useRef<typeof setState | null>(null);
    if (wrapped.current === null) {
      wrapped.current = ((value: unknown) => {
        setterLog.writes += 1;
        (setState as (v: unknown) => void)(value);
      }) as typeof setState;
    }
    return [state, wrapped.current];
  }) as typeof actual.useState;
  return { ...actual, useState };
});

vi.mock("@/lib/graphql", () => ({
  runBackup: vi.fn(),
  fetchBackupHistory: vi.fn(),
  verifyBackup: vi.fn(),
  fetchMetadataExport: vi.fn(),
  // PathPickerField (used by the destination field) pulls these from the module.
  fetchPathPicker: vi.fn(),
  addPathPickerRoot: vi.fn(),
}));

const mockHistory = vi.mocked(fetchBackupHistory);
const mockVerify = vi.mocked(verifyBackup);
const mockExport = vi.mocked(fetchMetadataExport);

afterEach(() => {
  setAdminEpochSignal();
  vi.clearAllMocks();
});

// A history read parked until the test settles it, capturing the AbortSignal
// the page composed for the transport.
function pendingHistory() {
  let resolve!: (entries: BackupHistoryEntry[]) => void;
  const signals: (AbortSignal | undefined)[] = [];
  mockHistory.mockImplementation((_dest, signal) => {
    signals.push(signal);
    return new Promise((r) => {
      resolve = r;
    });
  });
  return { signals, resolve: (entries: BackupHistoryEntry[]) => resolve(entries) };
}

const EXPORT_PAYLOAD: MetadataExportInfo = {
  schema: "v3",
  exportedAt: "2026-08-10T00:00:00Z",
  libraries: [],
  media: [],
  entities: [],
  relations: [],
  mediaLinks: [],
  collections: [],
};

const HISTORY_ROW: BackupHistoryEntry = {
  directory: "/backups/beamfall-1f9a",
  createdAt: "2026-07-03T02:00:00Z",
  sizeBytes: 2_040_109_465,
  valid: true,
};

function renderPage() {
  return render(
    <I18nProvider>
      <BackupExportPage />
    </I18nProvider>,
  );
}

describe("BackupExportPage", () => {
  it("exposes the offline operator-run restore assistant", () => {
    const { container } = renderPage();

    expect(container.querySelector("#restore")).toBeInTheDocument();
    expect(screen.getByText("Guided restore")).toBeVisible();
    expect(screen.getByText(/beamfall restore --manifest/)).toBeVisible();
    expect(screen.getByText(/refuses a live server lock/)).toBeVisible();
    expect(screen.getByText(/only guides restore\. It never overwrites/)).toBeVisible();
  });

  it("loads history for a destination and renders per-set verify state", async () => {
    const user = userEvent.setup();
    mockHistory.mockResolvedValue([
      {
        directory: "/backups/beamfall-1f9a",
        createdAt: "2026-07-03T02:00:00Z",
        sizeBytes: 2_040_109_465,
        valid: true,
      },
      {
        directory: "/backups/beamfall-7e40",
        createdAt: "2026-06-30T02:00:00Z",
        sizeBytes: 1_932_735_283,
        valid: false,
        error: "content hash mismatch",
      },
    ]);

    renderPage();

    await user.type(screen.getByLabelText("Destination"), "/var/lib/beamfall/backups");
    await user.click(screen.getByRole("button", { name: "Load history" }));

    await waitFor(() =>
      expect(mockHistory).toHaveBeenCalledWith(
        "/var/lib/beamfall/backups",
        expect.any(AbortSignal),
      ),
    );
    expect(screen.getByText("beamfall-1f9a")).toBeVisible();
    expect(screen.getByText("Verified")).toBeVisible();
    expect(screen.getByText("Failed")).toBeVisible();
    // Size is rendered human-readable.
    expect(screen.getByText("1.9 GB")).toBeVisible();
  });

  it("selecting a set shows the manifest path and the Recovery Keys exclusion callout", async () => {
    const user = userEvent.setup();
    mockHistory.mockResolvedValue([
      {
        directory: "/backups/beamfall-1f9a",
        createdAt: "2026-07-03T02:00:00Z",
        sizeBytes: 2_040_109_465,
        valid: true,
      },
    ]);

    renderPage();
    await user.type(screen.getByLabelText("Destination"), "/backups");
    await user.click(screen.getByRole("button", { name: "Load history" }));
    await screen.findByText("beamfall-1f9a");

    // Manifest is empty until a set is selected.
    expect(screen.getByText(/Select a backup set to see its manifest/)).toBeVisible();

    await user.click(screen.getByText("beamfall-1f9a"));

    expect(screen.getByText("/backups/beamfall-1f9a/manifest.json")).toBeVisible();
    expect(screen.getByText("Recovery Keys are NOT in this backup")).toBeVisible();
    expect(screen.getByText(/Save your Recovery Keys separately/)).toBeVisible();
  });

  it("per-row Verify re-runs verifyBackup against the set manifest and shows the result", async () => {
    const user = userEvent.setup();
    mockHistory.mockResolvedValue([
      {
        directory: "/backups/beamfall-1f9a",
        createdAt: "2026-07-03T02:00:00Z",
        sizeBytes: 2_040_109_465,
        valid: true,
      },
    ]);
    mockVerify.mockResolvedValue({ ok: true });

    renderPage();
    await user.type(screen.getByLabelText("Destination"), "/backups");
    await user.click(screen.getByRole("button", { name: "Load history" }));
    const row = (await screen.findByText("beamfall-1f9a")).closest("tr")!;

    await user.click(within(row).getByRole("button", { name: "Verify" }));

    await waitFor(() =>
      expect(mockVerify).toHaveBeenCalledWith("/backups/beamfall-1f9a/manifest.json"),
    );
    expect(await screen.findByText(/Integrity check passed/)).toBeVisible();
  });

  // The three Admin-epoch boundaries (areas/console/adminEpoch): the page's
  // reads run at /console/backups, so an Admin Session expiry, an operator
  // lock, or an unmount must abort the in-flight transport signal AND drop the
  // response's state write — prior-epoch data never lands on screen.
  it("aborts the in-flight history read and drops its write when the Admin Session expires", async () => {
    const user = userEvent.setup();
    const adminEpoch = new AbortController();
    setAdminEpochSignal(adminEpoch.signal);
    const pending = pendingHistory();

    renderPage();
    await user.type(screen.getByLabelText("Destination"), "/backups");
    await user.click(screen.getByRole("button", { name: "Load history" }));
    await waitFor(() => expect(pending.signals).toHaveLength(1));
    expect(pending.signals[0]?.aborted).toBe(false);

    // Admin Session expiry: AdminGate aborts the published epoch signal.
    act(() => adminEpoch.abort());
    expect(pending.signals[0]?.aborted).toBe(true);

    // The response settles after the epoch ended — its write must not land.
    await act(async () => pending.resolve([HISTORY_ROW]));
    expect(screen.queryByText("beamfall-1f9a")).not.toBeInTheDocument();
  });

  it("aborts the in-flight metadata export and drops its write when the operator locks", async () => {
    const user = userEvent.setup();
    const adminEpoch = new AbortController();
    setAdminEpochSignal(adminEpoch.signal);
    let resolveExport!: (data: MetadataExportInfo) => void;
    const signals: (AbortSignal | undefined)[] = [];
    mockExport.mockImplementation((signal) => {
      signals.push(signal);
      return new Promise((r) => {
        resolveExport = r;
      });
    });

    renderPage();
    await user.click(screen.getByRole("button", { name: "Generate export" }));
    await waitFor(() => expect(signals).toHaveLength(1));
    expect(signals[0]?.aborted).toBe(false);

    // Operator lock: AdminGate aborts the published epoch signal.
    act(() => adminEpoch.abort());
    expect(signals[0]?.aborted).toBe(true);

    await act(async () => resolveExport(EXPORT_PAYLOAD));
    expect(screen.queryByText(/Schema/)).not.toBeInTheDocument();
  });

  it("aborts both in-flight reads on unmount so no write reaches the unmounted page", async () => {
    const user = userEvent.setup();
    const pending = pendingHistory();
    let resolveExport!: (data: MetadataExportInfo) => void;
    const exportSignals: (AbortSignal | undefined)[] = [];
    mockExport.mockImplementation((signal) => {
      exportSignals.push(signal);
      return new Promise((r) => {
        resolveExport = r;
      });
    });

    const { unmount } = renderPage();
    await user.type(screen.getByLabelText("Destination"), "/backups");
    await user.click(screen.getByRole("button", { name: "Load history" }));
    await user.click(screen.getByRole("button", { name: "Generate export" }));
    await waitFor(() => expect(pending.signals).toHaveLength(1));
    await waitFor(() => expect(exportSignals).toHaveLength(1));
    expect(pending.signals[0]?.aborted).toBe(false);
    expect(exportSignals[0]?.aborted).toBe(false);

    unmount();
    expect(pending.signals[0]?.aborted).toBe(true);
    expect(exportSignals[0]?.aborted).toBe(true);

    // Settling after unmount must be inert — the wrapped setters record every
    // state write, and zero may fire once the tree is unmounted.
    const writesBeforeSettle = setterLog.writes;
    await act(async () => {
      pending.resolve([HISTORY_ROW]);
      resolveExport(EXPORT_PAYLOAD);
    });
    expect(setterLog.writes - writesBeforeSettle).toBe(0);
  });
});
