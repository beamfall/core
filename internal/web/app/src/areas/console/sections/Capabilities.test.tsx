import "@testing-library/jest-dom/vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { fetchCapabilityStates, setCapabilityEnabled } from "@/lib/graphql";
import type { CapabilityStateInfo } from "@/lib/graphql/types";
import { I18nProvider } from "@/lib/i18n";
import { Capabilities } from "./Capabilities";

vi.mock("@/lib/graphql", () => ({
  fetchCapabilityStates: vi.fn(),
  setCapabilityEnabled: vi.fn(),
}));

const states: CapabilityStateInfo[] = [
  { id: "conversion", kind: "workflow", enabled: true, flippable: true },
  { id: "rename", kind: "workflow", enabled: false, flippable: true },
  {
    id: "share-mount",
    kind: "probe",
    enabled: false,
    flippable: false,
    reason: "probe-absent",
  },
];

beforeEach(() => {
  vi.mocked(fetchCapabilityStates).mockReset();
  vi.mocked(setCapabilityEnabled).mockReset();
  vi.mocked(fetchCapabilityStates).mockResolvedValue(states);
  vi.mocked(setCapabilityEnabled).mockResolvedValue(
    states.map((s) => (s.id === "conversion" ? { ...s, enabled: false } : s)),
  );
});

function renderPanel() {
  return render(
    <I18nProvider>
      <Capabilities />
    </I18nProvider>,
  );
}

describe("Capabilities", () => {
  it("renders workflow capabilities with toggles and the probe group read-only", async () => {
    renderPanel();

    expect(await screen.findByText("conversion")).toBeVisible();
    expect(screen.getByText("rename")).toBeVisible();
    expect(screen.getByText("share-mount")).toBeVisible();
    // The probe-absent capability shows its degraded reason and NO toggle.
    expect(screen.getByText("Not detected on this host")).toBeVisible();
    // Two flippable workflow capabilities → exactly two switches (probe has none).
    expect(screen.getAllByRole("switch")).toHaveLength(2);
    expect(fetchCapabilityStates).toHaveBeenCalled();
  });

  it("flips a workflow capability through the audited mutation", async () => {
    const user = userEvent.setup();
    renderPanel();
    await screen.findByText("conversion");

    await user.click(screen.getByLabelText("conversion"));

    expect(setCapabilityEnabled).toHaveBeenCalledWith("conversion", false);
  });

  it("renders the unavailable state when the query fails", async () => {
    vi.mocked(fetchCapabilityStates).mockRejectedValue(new Error("boom"));
    renderPanel();

    expect(await screen.findByText("Capabilities are unavailable right now.")).toBeVisible();
  });
});
