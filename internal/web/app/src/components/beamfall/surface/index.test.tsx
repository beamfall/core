import "@testing-library/jest-dom/vitest";
import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { I18nProvider } from "@/lib/i18n";
import {
  FieldButton,
  FieldInput,
  FieldLabel,
  GlassCard,
  OptionRadio,
  OptionRow,
  PageShell,
  SurfaceHeading,
  SurfaceSection,
} from ".";

afterEach(() => {
  cleanup();
});

describe("surface primitives", () => {
  it("renders the brand bar, an optional suffix and the column", () => {
    render(
      <I18nProvider>
        <PageShell suffix="Server setup">
          <p>body</p>
        </PageShell>
      </I18nProvider>,
    );
    expect(screen.getByRole("main")).toHaveClass("theme-cinema");
    expect(screen.getByRole("img", { name: "Beamfall" })).toBeInTheDocument();
    expect(screen.getByText("Server setup")).toBeInTheDocument();
    expect(screen.getByText("body").parentElement).toHaveClass("max-w-[440px]");
  });

  it("lets a form be the glass card and keeps its own attributes", () => {
    render(
      <GlassCard asChild>
        <form aria-label="card" noValidate />
      </GlassCard>,
    );
    const form = screen.getByRole("form", { name: "card" });
    expect(form).toHaveAttribute("data-slot", "glass-card");
    expect(form).toHaveClass("rounded-[14px]", "bg-card");
  });

  it("makes a focus-target heading programmatically focusable", () => {
    render(<SurfaceHeading focusTarget title="Title" lead="Lead" />);
    const heading = screen.getByRole("heading", { level: 1, name: "Title" });
    expect(heading).toHaveAttribute("tabindex", "-1");
    expect(heading).toHaveAttribute("data-step-heading");
  });

  it("leaves a plain heading out of the focus order", () => {
    render(<SurfaceHeading title="Title" lead="Lead" />);
    const heading = screen.getByRole("heading", { level: 1, name: "Title" });
    expect(heading).not.toHaveAttribute("tabindex");
    expect(heading).not.toHaveAttribute("data-step-heading");
  });

  it("lets callers override field classes through the merge", () => {
    render(
      <>
        <FieldLabel htmlFor="f">Name</FieldLabel>
        <FieldInput id="f" className="font-mono" />
        <FieldButton className="px-3">Show</FieldButton>
      </>,
    );
    const input = screen.getByLabelText("Name");
    expect(input).toHaveClass("h-11", "rounded-[10px]", "font-mono");
    const button = screen.getByRole("button", { name: "Show" });
    expect(button).toHaveAttribute("type", "button");
    expect(button).toHaveClass("px-3");
    expect(button).not.toHaveClass("px-4");
  });

  it("wraps a radio so the whole row selects it", () => {
    render(
      <OptionRow>
        <OptionRadio name="mode" value="home" />
        Home
      </OptionRow>,
    );
    expect(screen.getByRole("radio", { name: "Home" })).toHaveAttribute("type", "radio");
  });

  it("keeps the caller's element for a section", () => {
    render(
      <SurfaceSection asChild>
        <section aria-label="more" />
      </SurfaceSection>,
    );
    expect(screen.getByRole("region", { name: "more" })).toHaveClass("border-t", "pt-5");
  });
});
