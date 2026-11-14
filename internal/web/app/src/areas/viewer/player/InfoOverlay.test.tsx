import "@testing-library/jest-dom/vitest";
import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import type { MetadataAttribution } from "@/lib/graphql";
import { I18nProvider } from "@/lib/i18n";
import { InfoOverlay } from "./InfoOverlay";

function renderOverlay(metadataAttribution?: MetadataAttribution) {
  return render(
    <I18nProvider initialLocale="en">
      <InfoOverlay
        mediaTitle="Long Weekend"
        info={null}
        stream={null}
        manualRenditionID=""
        hlsActive={false}
        open
        onOpenChange={() => {}}
        metadataAttribution={metadataAttribution}
      />
    </I18nProvider>,
  );
}

describe("InfoOverlay metadata attribution (CORE-1126)", () => {
  it("renders an https attribution as a safe external link", () => {
    renderOverlay({
      providerName: { en: "Fixture source" },
      text: { en: "Metadata provided by Fixture", fr: "Données fournies par Fixture" },
      url: "https://fixture.example.test/",
    });

    const link = screen.getByRole("link", { name: "Metadata provided by Fixture" });
    expect(link).toHaveAttribute("href", "https://fixture.example.test/");
    expect(link).toHaveAttribute("target", "_blank");
    expect(link).toHaveAttribute("rel", "noopener noreferrer");
  });

  it("renders a non-https attribution as plain text without a link", () => {
    renderOverlay({ text: { en: "Metadata provided by Fixture" }, url: "javascript:alert(1)" });

    expect(screen.getByText("Metadata provided by Fixture")).toBeInTheDocument();
    expect(screen.queryByRole("link")).not.toBeInTheDocument();
  });

  it("renders nothing for an item without attribution", () => {
    renderOverlay();

    expect(screen.queryByText(/Metadata provided by/)).not.toBeInTheDocument();
    expect(screen.queryByRole("link")).not.toBeInTheDocument();
  });
});
