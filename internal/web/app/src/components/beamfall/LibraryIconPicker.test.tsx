import "@testing-library/jest-dom/vitest";
import { cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { setLibraryIcon } from "@/lib/graphql";
import type { LibraryInfo } from "@/lib/graphql/types";
import { I18nProvider } from "@/lib/i18n";
import { LIBRARY_ICON_NAMES } from "@/lib/libraryIcons";
import { setManifestRefresher } from "@/lib/manifestRefresh";
import {
  LibraryIconPicker,
  resetLibraryIconChoices,
  useLibraryIconSymbol,
} from "./LibraryIconPicker";

vi.mock("@/lib/graphql", () => ({ setLibraryIcon: vi.fn() }));

const library: LibraryInfo = {
  id: "lib-1",
  title: "Films",
  pluginID: null,
  previewClips: false,
  restricted: false,
  icon: "film",
  defaultIcon: "film",
};

function HeaderSymbol({ library }: { library: LibraryInfo }) {
  const Symbol = useLibraryIconSymbol(library);
  return <Symbol data-testid="header-symbol" />;
}

function renderPicker(lib: LibraryInfo = library, onChanged = vi.fn()) {
  const view = render(
    <I18nProvider>
      <HeaderSymbol library={lib} />
      <LibraryIconPicker library={lib} onChanged={onChanged} />
    </I18nProvider>,
  );
  return { ...view, onChanged };
}

const radio = (name: string) => screen.getByRole("radio", { name });

beforeEach(() => {
  vi.mocked(setLibraryIcon).mockReset();
  vi.mocked(setLibraryIcon).mockResolvedValue(undefined);
});

afterEach(() => {
  cleanup();
  resetLibraryIconChoices();
  setManifestRefresher();
});

describe("LibraryIconPicker", () => {
  it("is one named radio group offering Default and every icon, with the current choice checked", () => {
    renderPicker({ ...library, icon: "headphones", iconChoice: "headphones" });

    const group = screen.getByRole("radiogroup", { name: "Icon: Films" });
    expect(group).toHaveAccessibleDescription("Shown beside this library in the sidebar.");
    const radios = screen.getAllByRole("radio");
    expect(radios).toHaveLength(LIBRARY_ICON_NAMES.length + 1);
    for (const option of radios) expect(option).toHaveAccessibleName();
    expect(radio("Headphones")).toBeChecked();
    expect(radio("Default")).not.toBeChecked();
    expect(screen.getByTestId("header-symbol")).toHaveClass("lucide-headphones");
  });

  it("checks Default while the library has no choice of its own", () => {
    renderPicker();
    expect(radio("Default")).toBeChecked();
    expect(screen.getByTestId("header-symbol")).toHaveClass("lucide-film");
  });

  it("shows a pick at once, saves it, then asks for the list and the manifest again", async () => {
    const refreshed = vi.fn();
    setManifestRefresher(refreshed);
    const user = userEvent.setup();
    const { onChanged } = renderPicker();

    await user.click(radio("Headphones"));
    // On screen before the server has been asked.
    expect(radio("Headphones")).toBeChecked();
    expect(screen.getByTestId("header-symbol")).toHaveClass("lucide-headphones");
    expect(setLibraryIcon).not.toHaveBeenCalled();

    await waitFor(() => expect(onChanged).toHaveBeenCalledTimes(1));
    expect(setLibraryIcon).toHaveBeenCalledExactlyOnceWith("lib-1", "headphones");
    expect(refreshed).toHaveBeenCalledTimes(1);
    expect(radio("Headphones")).toBeChecked();
  });

  it("moves the choice with the arrow keys and saves only where it comes to rest", async () => {
    const user = userEvent.setup();
    const { onChanged } = renderPicker();

    await user.tab();
    expect(radio("Default")).toHaveFocus();
    await user.keyboard("{ArrowRight}{ArrowRight}{ArrowRight}");
    expect(radio("Video camera")).toBeChecked();
    expect(radio("Video camera")).toHaveFocus();

    await waitFor(() => expect(onChanged).toHaveBeenCalledTimes(1));
    expect(setLibraryIcon).toHaveBeenCalledExactlyOnceWith("lib-1", "video");
  });

  it("returns to Default by saving the empty choice", async () => {
    const user = userEvent.setup();
    const { onChanged } = renderPicker({ ...library, icon: "star", iconChoice: "star" });

    await user.click(radio("Default"));
    expect(screen.getByTestId("header-symbol")).toHaveClass("lucide-film");
    await waitFor(() => expect(onChanged).toHaveBeenCalledTimes(1));
    expect(setLibraryIcon).toHaveBeenCalledExactlyOnceWith("lib-1", "");
  });

  it("puts the server's choice back and says so when the save fails, then recovers", async () => {
    vi.mocked(setLibraryIcon).mockRejectedValueOnce(new Error("refused"));
    const user = userEvent.setup();
    const { onChanged } = renderPicker();

    await user.click(radio("Star"));
    expect(radio("Star")).toBeChecked();
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Couldn't save the icon. Try again.",
    );
    expect(radio("Default")).toBeChecked();
    expect(screen.getByTestId("header-symbol")).toHaveClass("lucide-film");
    expect(onChanged).not.toHaveBeenCalled();

    await user.click(radio("Heart"));
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
    await waitFor(() => expect(onChanged).toHaveBeenCalledTimes(1));
    expect(setLibraryIcon).toHaveBeenLastCalledWith("lib-1", "heart");
  });

  it("sends a pick made during a save once that save returns", async () => {
    let finishFirst!: () => void;
    vi.mocked(setLibraryIcon).mockImplementationOnce(
      () =>
        new Promise<void>((resolve) => {
          finishFirst = resolve;
        }),
    );
    const user = userEvent.setup();
    const { onChanged } = renderPicker();

    await user.click(radio("Star"));
    await waitFor(() => expect(setLibraryIcon).toHaveBeenCalledTimes(1));
    await user.click(radio("Heart"));
    finishFirst();

    await waitFor(() => expect(onChanged).toHaveBeenCalledTimes(1));
    expect(vi.mocked(setLibraryIcon).mock.calls).toEqual([
      ["lib-1", "star"],
      ["lib-1", "heart"],
    ]);
    expect(radio("Heart")).toBeChecked();
  });

  it("offers nothing when the server publishes no default icon", () => {
    renderPicker({ ...library, icon: undefined, defaultIcon: undefined });
    expect(screen.queryByRole("radiogroup")).not.toBeInTheDocument();
  });
});
