import "@testing-library/jest-dom/vitest";
import { cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { fetchImporters } from "@/lib/graphql";
import type { ImporterInfo } from "@/lib/graphql/types";
import { I18nProvider } from "@/lib/i18n";
import { MigrationOffer } from "./MigrationOffer";

vi.mock("@/lib/graphql", () => ({
  fetchImporters: vi.fn(),
}));

function importer(pluginID: string, id: string, name: string): ImporterInfo {
  return {
    pluginID,
    id,
    displayName: name,
    categories: ["watch-state"],
    input: "credentials",
    conflict: "skip",
    credentialRef: "",
    credentialRequired: false,
    credentialConfigured: false,
    credentialStatus: "not-required",
  };
}

function renderOffer(onSkip = vi.fn()) {
  render(
    <I18nProvider>
      <MigrationOffer onSkip={onSkip} />
    </I18nProvider>,
  );
  return { onSkip };
}

beforeEach(() => {
  vi.mocked(fetchImporters).mockReset();
});

afterEach(cleanup);

describe("MigrationOffer (Seam A)", () => {
  it("lists plugin-declared sources (never hardcoded) and shows additive-only trust copy", async () => {
    vi.mocked(fetchImporters).mockResolvedValue([
      importer("filmtv", "plex", "Plex"),
      importer("podcasts", "opml", "OPML subscriptions"),
    ]);

    renderOffer();

    // Sources come from the importer display names the plugins declare.
    expect(await screen.findByText("Plex")).toBeInTheDocument();
    expect(screen.getByText("OPML subscriptions")).toBeInTheDocument();
    // Additive-only trust copy (funnel §3.3) is present.
    expect(screen.getByText(/never overwrites or downgrades/i)).toBeInTheDocument();
    expect(screen.getByText(/report first/i)).toBeInTheDocument();
  });

  it("skip is first-class — it finishes setup without running an import", async () => {
    vi.mocked(fetchImporters).mockResolvedValue([importer("filmtv", "plex", "Plex")]);
    const { onSkip } = renderOffer();

    await screen.findByText("Plex");
    await userEvent.click(screen.getByRole("button", { name: /skip for now/i }));

    expect(onSkip).toHaveBeenCalledOnce();
  });

  it("still renders skip and continue when the source list fails, then retries", async () => {
    vi.mocked(fetchImporters)
      .mockRejectedValueOnce(new Error("offline"))
      .mockResolvedValueOnce([importer("filmtv", "plex", "Plex")]);
    renderOffer();

    await waitFor(() =>
      expect(screen.getByRole("button", { name: /skip for now/i })).toBeInTheDocument(),
    );
    expect(screen.getByRole("button", { name: /choose a source/i })).toBeInTheDocument();

    await userEvent.click(screen.getByRole("button", { name: "Retry" }));

    expect(await screen.findByText("Plex")).toBeInTheDocument();
    expect(fetchImporters).toHaveBeenCalledTimes(2);
  });
});
