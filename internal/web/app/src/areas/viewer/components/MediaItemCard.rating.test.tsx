import "@testing-library/jest-dom/vitest";
import { cleanup, render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { SourceItem } from "@/lib/graphql/types";
import { I18nProvider } from "@/lib/i18n";
import { MediaItemCard } from "./MediaItemCard";

vi.mock("@/lib/session", () => ({ useViewerIdentity: () => "viewer-1" }));

afterEach(cleanup);

function renderCard(overrides: Partial<SourceItem> = {}) {
  const item: SourceItem = { id: "m1", kind: "media", title: "Alpha", ...overrides };
  return render(
    <I18nProvider initialLocale="en">
      <MemoryRouter>
        <MediaItemCard item={item} />
      </MemoryRouter>
    </I18nProvider>,
  );
}

describe("MediaItemCard content rating and audience score (CORE-2400)", () => {
  it("shows the rating and score and names both", () => {
    const { container } = renderCard({ certification: "PG-13", audienceScore: 7.8 });
    expect(container.querySelector(".bf-card-rating")).toHaveTextContent("PG-13★ 7.8");
    expect(
      screen.getByRole("link", { name: "Alpha, Rated PG-13, audience score 7.8" }),
    ).toBeInTheDocument();
  });

  it("formats the score with one decimal", () => {
    const { container } = renderCard({ audienceScore: 7 });
    expect(container.querySelector(".bf-card-rating")).toHaveTextContent("★ 7.0");
  });

  it("shows only the rating when there is no score", () => {
    const { container } = renderCard({ certification: "TV-MA" });
    const line = container.querySelector(".bf-card-rating");
    expect(line).toHaveTextContent("TV-MA");
    expect(line).not.toHaveTextContent("★");
    expect(screen.getByRole("link", { name: "Alpha, Rated TV-MA" })).toBeInTheDocument();
  });

  it("shows only the score when there is no rating", () => {
    const { container } = renderCard({ audienceScore: 6.5 });
    const line = container.querySelector(".bf-card-rating");
    expect(line).toHaveTextContent("★ 6.5");
    expect(container.querySelector(".bf-card-cert")).toBeNull();
    expect(screen.getByRole("link", { name: "Alpha, Audience score 6.5" })).toBeInTheDocument();
  });

  it("renders nothing when both are absent, blank or off the scale", () => {
    for (const overrides of [{}, { certification: "  " }, { audienceScore: 73 }]) {
      const { container } = renderCard(overrides);
      expect(container.querySelector(".bf-card-rating")).toBeNull();
      expect(screen.getByRole("link", { name: "Alpha" })).toBeInTheDocument();
      cleanup();
    }
  });

  it("leaves entity cards without a rating line", () => {
    const { container } = renderCard({ kind: "entity", certification: "PG" });
    expect(container.querySelector(".bf-card-rating")).toBeNull();
  });
});
