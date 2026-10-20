import "@testing-library/jest-dom/vitest";
import { cleanup, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { lazy, Suspense } from "react";
import { MemoryRouter } from "react-router";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { I18nProvider } from "@/lib/i18n";
import { AreaErrorBoundary, ShellErrorBoundary } from "./ErrorBoundary";

function Throwing(): never {
  throw new Error("render fault");
}

const reload = vi.fn();

beforeEach(() => {
  reload.mockReset();
  Object.defineProperty(window, "location", {
    configurable: true,
    value: { ...window.location, reload },
  });
  // React re-reports caught render errors on console.error; keep the run quiet.
  vi.spyOn(console, "error").mockImplementation(() => {});
});

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

describe("ShellErrorBoundary", () => {
  it("renders the reload panel with a working Reload control when a child throws", async () => {
    render(
      <I18nProvider>
        <ShellErrorBoundary>
          <Throwing />
        </ShellErrorBoundary>
      </I18nProvider>,
    );

    expect(screen.getByRole("alert")).toHaveTextContent("Something went wrong.");
    await userEvent.click(screen.getByRole("button", { name: "Reload" }));
    expect(reload).toHaveBeenCalledTimes(1);
  });

  it("keeps rendering its children while nothing throws", () => {
    render(
      <I18nProvider>
        <ShellErrorBoundary>
          <p>Healthy content</p>
        </ShellErrorBoundary>
      </I18nProvider>,
    );

    expect(screen.getByText("Healthy content")).toBeInTheDocument();
    expect(screen.queryByRole("alert")).toBeNull();
  });
});

describe("AreaErrorBoundary", () => {
  it("renders the area panel, not a blank root, when a lazy import rejects", async () => {
    const MissingChunk = lazy(() => Promise.reject(new Error("chunk 404")));

    render(
      <I18nProvider>
        <MemoryRouter>
          <ShellErrorBoundary>
            <header>Shell chrome</header>
            <Suspense fallback={<p>loading</p>}>
              <AreaErrorBoundary resetKey="/manage">
                <MissingChunk />
              </AreaErrorBoundary>
            </Suspense>
          </ShellErrorBoundary>
        </MemoryRouter>
      </I18nProvider>,
    );

    const panel = await screen.findByRole("alert");
    expect(panel).toHaveTextContent("Something went wrong.");
    expect(screen.getByText("Shell chrome")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Reload" }));
    expect(reload).toHaveBeenCalledTimes(1);
  });

  it("retries its children when the route key changes after a failure", () => {
    let faulty = true;
    function Flaky() {
      if (faulty) throw new Error("first render fault");
      return <p>Area recovered</p>;
    }
    const view = render(
      <I18nProvider>
        <AreaErrorBoundary resetKey="/manage/libraries">
          <Flaky />
        </AreaErrorBoundary>
      </I18nProvider>,
    );
    expect(screen.getByRole("alert")).toBeInTheDocument();

    faulty = false;
    view.rerender(
      <I18nProvider>
        <AreaErrorBoundary resetKey="/manage/jobs">
          <Flaky />
        </AreaErrorBoundary>
      </I18nProvider>,
    );

    expect(screen.getByText("Area recovered")).toBeInTheDocument();
    expect(screen.queryByRole("alert")).toBeNull();
  });
});
