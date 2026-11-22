import "@testing-library/jest-dom/vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { I18nProvider } from "@/lib/i18n";
import { type RadioStation, startRadio } from "../lib/radio";
import { RadioTuning } from "./StartRadio";

vi.mock("../lib/radio", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../lib/radio")>()),
  startRadio: vi.fn(),
}));

vi.mock("@/lib/api", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/lib/api")>()),
  getExpectedProfile: () => "p1",
}));

vi.mock("@/lib/session", () => ({ useViewerIdentity: () => "viewer-1" }));

const station: RadioStation = {
  profileID: "p1",
  seedKind: "entity",
  seedID: "e1",
  rules: {},
  reasons: { a: "Why a", b: "Why b" },
};

beforeEach(() => {
  vi.mocked(startRadio).mockReset().mockResolvedValue({ status: "needs-confirm" });
});

afterEach(cleanup);

function tune(queuedIDs: readonly string[] | undefined) {
  render(
    <I18nProvider initialLocale="en">
      <MemoryRouter>
        <RadioTuning station={station} credits={[]} queuedIDs={queuedIDs} />
      </MemoryRouter>
    </I18nProvider>,
  );
  fireEvent.change(screen.getAllByRole("combobox")[0], { target: { value: "1980" } });
}

// Review finding P2: retuning follows Start radio's confirm-before-replace rule.
describe("RadioTuning", () => {
  it("replaces a queue holding only radio's picks at once", () => {
    tune(["a", "b"]);
    expect(startRadio).toHaveBeenCalledWith(
      expect.objectContaining({ rules: { decades: [1980] }, confirmReplace: true }),
    );
  });

  it("asks before replacing a queue the listener added to", () => {
    tune(["a", "mine"]);
    expect(startRadio).toHaveBeenCalledWith(expect.objectContaining({ confirmReplace: false }));
  });

  it("asks when the queue has not been read", () => {
    tune(undefined);
    expect(startRadio).toHaveBeenCalledWith(expect.objectContaining({ confirmReplace: false }));
  });
});
