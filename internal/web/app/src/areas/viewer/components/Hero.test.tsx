import "@testing-library/jest-dom/vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { MemoryRouter, useLocation } from "react-router";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { Section, SourceItem } from "@/lib/graphql/types";
import { I18nProvider } from "@/lib/i18n";
import { readEntityPreview } from "../lib/entityPreview";
import { Hero } from "./Hero";

vi.mock("@/lib/session", () => ({
  useViewerIdentity: () => "viewer-1",
}));

afterEach(cleanup);

function LocationState() {
  const location = useLocation();
  return (
    <>
      <span data-testid="path">{location.pathname}</span>
      <span data-testid="state">{JSON.stringify(location.state)}</span>
    </>
  );
}

describe("Hero entity navigation", () => {
  it("carries the identity-bound entity preview into the entity page", () => {
    const section = { title: "Featured" } as unknown as Section;
    const item = { id: "e1", kind: "series", title: "Show" } as SourceItem;
    render(
      <I18nProvider>
        <MemoryRouter>
          <Hero section={section} item={item} />
          <LocationState />
        </MemoryRouter>
      </I18nProvider>,
    );

    fireEvent.click(screen.getByRole("button"));

    expect(screen.getByTestId("path")).toHaveTextContent("/entity/e1");
    const state = JSON.parse(screen.getByTestId("state").textContent || "null");
    expect(readEntityPreview(state, "viewer-1", "e1")).toEqual({ title: "Show", kind: "series" });
  });

  it("prioritises the hero artwork with a high-DPI srcset", () => {
    const section = { title: "Featured" } as unknown as Section;
    const item = {
      id: "m1",
      kind: "media",
      title: "Film",
      artworkSlots: { backdrop: "/art/backdrop.jpg" },
    } as unknown as SourceItem;
    const { container } = render(
      <I18nProvider>
        <MemoryRouter>
          <Hero section={section} item={item} />
        </MemoryRouter>
      </I18nProvider>,
    );

    const img = container.querySelector("img");
    expect(img).toHaveAttribute("src", "/art/backdrop.jpg?bucket=w1280");
    expect(img).toHaveAttribute(
      "srcset",
      "/art/backdrop.jpg?bucket=w640 640w, /art/backdrop.jpg?bucket=w1280 1280w",
    );
    expect(img).toHaveAttribute("sizes", "100vw");
    expect(img).toHaveAttribute("fetchpriority", "high");
  });

  it("names an episode's show and shows its details once", () => {
    const section = { title: "Featured" } as unknown as Section;
    const item = {
      id: "m2",
      kind: "media",
      title: "S34E09 · Episode 9",
      summary: "49 min",
      year: 2026,
      certification: "TV-14",
      audienceScore: 7.4,
      durationSeconds: 2930,
      subtitle: { en: "S34 · E9 · Episode 9" },
      container: { id: "e2", title: "The Graham Norton Show" },
    } as unknown as SourceItem;
    const { container } = render(
      <I18nProvider>
        <MemoryRouter>
          <Hero section={section} item={item} />
          <LocationState />
        </MemoryRouter>
      </I18nProvider>,
    );

    expect(screen.getByRole("heading", { level: 2 })).toHaveTextContent("The Graham Norton Show");
    expect(screen.getByText("S34 · E9 · Episode 9")).toBeInTheDocument();
    const facts = [...(container.querySelector(".bf-hero-facts")?.children ?? [])].map(
      (fact) => fact.textContent,
    );
    expect(facts).toEqual(["2026", "TV-14", "49 min", "★ 7.4Audience score 7.4"]);
    // The server's runtime summary repeats a fact, so it is not shown twice.
    expect(container.querySelector(".bf-hero-summary")).toBeNull();

    fireEvent.click(screen.getByRole("button"));
    const state = JSON.parse(screen.getByTestId("state").textContent || "null");
    expect(state).toEqual({ title: "The Graham Norton Show · S34 · E9 · Episode 9" });
  });

  it("shows a runtime once whatever the server's rounding, and a clip's seconds", () => {
    const section = { title: "Featured" } as unknown as Section;
    const facts = (item: Partial<SourceItem>) => {
      const { container, unmount } = render(
        <I18nProvider>
          <MemoryRouter>
            <Hero
              section={section}
              item={{ id: "m", kind: "media", title: "Film", ...item } as SourceItem}
            />
          </MemoryRouter>
        </I18nProvider>,
      );
      const shown = {
        facts: [...(container.querySelector(".bf-hero-facts")?.children ?? [])].map(
          (f) => f.textContent,
        ),
        summary: container.querySelector(".bf-hero-summary")?.textContent ?? null,
      };
      unmount();
      return shown;
    };
    expect(facts({ durationSeconds: 5400, summary: "90 min" })).toEqual({
      facts: ["1 h 30 min"],
      summary: null,
    });
    expect(facts({ durationSeconds: 61, summary: "2 min" })).toEqual({
      facts: ["1 min"],
      summary: null,
    });
    expect(facts({ durationSeconds: 44, summary: "44 sec" })).toEqual({
      facts: ["44 sec"],
      summary: null,
    });
  });
});
