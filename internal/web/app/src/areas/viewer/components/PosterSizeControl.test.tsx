import "@testing-library/jest-dom/vitest";
import { cleanup, render, renderHook, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import {
  POSTER_SIZE_KEY,
  readPosterSize,
  useCardImageSizes,
  usePosterSizeRoot,
} from "@/components/chrome/posterSize";
import { I18nProvider } from "@/lib/i18n";
import { PosterSizeControl } from "./PosterSizeControl";

function renderControl() {
  return render(
    <I18nProvider>
      <PosterSizeControl />
    </I18nProvider>,
  );
}

describe("PosterSizeControl (CORE-2468)", () => {
  beforeEach(() => {
    localStorage.clear();
    delete document.documentElement.dataset.posterSize;
  });
  afterEach(cleanup);

  it("defaults to large, the grids' size before the control existed", () => {
    renderControl();
    expect(screen.getByRole("group", { name: "Poster size" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Large posters" })).toHaveAttribute(
      "aria-pressed",
      "true",
    );
    expect(screen.getByRole("button", { name: "Small posters" })).toHaveAttribute(
      "aria-pressed",
      "false",
    );
    expect(readPosterSize()).toBe("large");
  });

  it("stores the choice, marks the root element and shrinks the image hint", async () => {
    renderControl();
    renderHook(() => usePosterSizeRoot(null));
    const hint = renderHook(() => useCardImageSizes());
    expect(hint.result.current).toBe("300px");

    await userEvent.click(screen.getByRole("button", { name: "Small posters" }));

    expect(localStorage.getItem(POSTER_SIZE_KEY)).toBe("small");
    expect(screen.getByRole("button", { name: "Small posters" })).toHaveAttribute(
      "aria-pressed",
      "true",
    );
    expect(document.documentElement.dataset.posterSize).toBe("small");
    expect(hint.result.current).toBe("160px");
  });

  it("reads a stored size and ignores an unknown one", () => {
    localStorage.setItem(POSTER_SIZE_KEY, "medium");
    expect(readPosterSize()).toBe("medium");
    localStorage.setItem(POSTER_SIZE_KEY, "huge");
    expect(readPosterSize()).toBe("large");
  });
});
