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
});
