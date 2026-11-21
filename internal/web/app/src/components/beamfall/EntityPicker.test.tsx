import "@testing-library/jest-dom/vitest";
import { cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import { MemoryRouter } from "react-router";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { searchResult } from "@/lib/graphql";
import type { SearchResultItem, SearchResultResponse } from "@/lib/graphql/types";
import { I18nProvider } from "@/lib/i18n";
import { EntityMultiPicker, EntityPicker, splitIDs } from "./EntityPicker";

// A failing search bypasses vi.fn: its result tracking leaves the rejection unhandled.
const searchFailure = vi.hoisted(() => ({ on: false }));
vi.mock("@/lib/graphql", () => {
  const tracked = vi.fn();
  const searchResult = (...args: unknown[]) =>
    searchFailure.on ? Promise.reject(new Error("down")) : tracked(...args);
  return { searchResult: Object.assign(searchResult, { tracked }) };
});

const mediaRow = {
  id: "m-1",
  kind: "media",
  title: "Harbor Lights",
  libraryID: "lib-1",
  libraryTitle: "Home Video",
  year: 2021,
  mediaClass: "video",
  artworkSlots: [],
  mode: "keyword",
  score: 1,
} as unknown as SearchResultItem;

const entityRow = {
  id: "e-1",
  kind: "entity",
  title: "Harbor Series",
  libraryID: "lib-1",
  libraryTitle: "Home Video",
  entityKind: "container",
  artworkSlots: [],
  mode: "keyword",
  score: 1,
} as unknown as SearchResultItem;

function tracked() {
  return (searchResult as unknown as { tracked: ReturnType<typeof vi.fn> }).tracked;
}

function respond(items: SearchResultItem[]) {
  tracked().mockResolvedValue({
    mode: "keyword",
    items,
    hasMore: false,
    restrictedResultsPresent: false,
  } as SearchResultResponse);
}

function Single({
  onChange,
  kind,
}: {
  onChange: (id: string) => void;
  kind?: "media" | "entity" | "any";
}) {
  const [value, setValue] = useState("");
  return (
    <>
      <label htmlFor="pick">Item</label>
      <EntityPicker
        id="pick"
        kind={kind}
        value={value}
        onChange={(id) => {
          setValue(id);
          onChange(id);
        }}
      />
    </>
  );
}

function Multi({ onChange }: { onChange: (ids: string[]) => void }) {
  const [value, setValue] = useState<string[]>([]);
  return (
    <EntityMultiPicker
      ariaLabel="Items"
      value={value}
      onChange={(ids) => {
        setValue(ids);
        onChange(ids);
      }}
    />
  );
}

function wrap(node: React.ReactNode) {
  return render(
    <I18nProvider>
      <MemoryRouter>{node}</MemoryRouter>
    </I18nProvider>,
  );
}

beforeEach(() => {
  searchFailure.on = false;
  tracked().mockReset();
});
afterEach(cleanup);

describe("EntityPicker", () => {
  it("is a labelled ARIA combobox", () => {
    wrap(<Single onChange={vi.fn()} />);
    expect(screen.getByRole("combobox", { name: "Item" })).toBeInTheDocument();
  });

  it("searches by title and picks a Media row, then links to open and edit it", async () => {
    respond([mediaRow, entityRow]);
    const onChange = vi.fn();
    wrap(<Single onChange={onChange} />);
    await userEvent.type(screen.getByRole("combobox"), "harbor");
    const option = await screen.findByRole("option", { name: /Harbor Lights/ });
    // Media-only picker hides the Entity row.
    expect(screen.queryByRole("option", { name: /Harbor Series/ })).toBeNull();
    expect(option).toHaveTextContent("2021 · Home Video");
    await userEvent.click(option);
    expect(onChange).toHaveBeenCalledWith("m-1");
    expect(tracked().mock.calls[0]?.[0]).toBe("harbor");
    const selected = screen.getByTestId("entity-picker-selected");
    expect(selected).toHaveTextContent("Harbor Lights");
    expect(screen.getByRole("link", { name: "Edit details" })).toHaveAttribute(
      "href",
      "/manage/items/m-1",
    );
    // Open goes to the detail page, not the player.
    expect(screen.getByRole("link", { name: "Open" })).toHaveAttribute("href", "/media/m-1");
  });

  it("drops the previous query's rows while the next search loads", async () => {
    respond([mediaRow]);
    wrap(<Single onChange={vi.fn()} />);
    const input = screen.getByRole("combobox");
    await userEvent.type(input, "harbor");
    await screen.findByRole("option", { name: /Harbor Lights/ });
    tracked().mockReturnValue(new Promise(() => {}));
    await userEvent.type(input, "x");
    expect(await screen.findByText("Searching…")).toBeInTheDocument();
    expect(screen.queryByRole("option", { name: /Harbor Lights/ })).toBeNull();
  });

  it("pages past rows of the wrong kind to find matches", async () => {
    const media = Array.from({ length: 16 }, (_, i) => ({ ...mediaRow, id: `m-${i}` }));
    tracked()
      .mockResolvedValueOnce({ mode: "keyword", items: media, hasMore: true })
      .mockResolvedValueOnce({ mode: "keyword", items: [entityRow], hasMore: false });
    wrap(<Single onChange={vi.fn()} kind="entity" />);
    await userEvent.type(screen.getByRole("combobox"), "harbor");
    expect(await screen.findByRole("option", { name: /Harbor Series/ })).toBeInTheDocument();
    expect(tracked().mock.calls[1]?.[3]).toEqual({ offset: 16, limit: 16 });
  });

  it("asks for a longer title when the scan cap is hit with no match", async () => {
    const media = Array.from({ length: 16 }, (_, i) => ({ ...mediaRow, id: `m-${i}` }));
    tracked().mockResolvedValue({ mode: "keyword", items: media, hasMore: true });
    wrap(<Single onChange={vi.fn()} kind="entity" />);
    await userEvent.type(screen.getByRole("combobox"), "harbor");
    expect(
      await screen.findByText("No matches in the first results. Try a longer title."),
    ).toBeInTheDocument();
    expect(tracked()).toHaveBeenCalledTimes(4);
  });

  it("offers Entity rows when kind is any, without an edit link", async () => {
    respond([entityRow]);
    const onChange = vi.fn();
    wrap(<Single onChange={onChange} kind="any" />);
    await userEvent.type(screen.getByRole("combobox"), "harbor");
    await userEvent.click(await screen.findByRole("option", { name: /Harbor Series/ }));
    expect(onChange).toHaveBeenCalledWith("e-1");
    expect(screen.getByRole("link", { name: "Open" })).toHaveAttribute("href", "/entity/e-1");
    expect(screen.queryByRole("link", { name: "Edit details" })).toBeNull();
  });

  it("takes a pasted ID with Enter when nothing matches", async () => {
    respond([]);
    const onChange = vi.fn();
    wrap(<Single onChange={onChange} />);
    await userEvent.type(screen.getByRole("combobox"), "abc123");
    expect(await screen.findByText("No items match.")).toBeInTheDocument();
    await screen.findByRole("option", { name: "Use this ID: abc123" });
    await userEvent.keyboard("{Enter}");
    expect(onChange).toHaveBeenCalledWith("abc123");
    expect(screen.getByTestId("entity-picker-selected")).toHaveTextContent("abc123");
  });

  it("says search is unavailable and still offers the pasted ID", async () => {
    searchFailure.on = true;
    wrap(<Single onChange={vi.fn()} />);
    await userEvent.type(screen.getByRole("combobox"), "abc123");
    expect(
      await screen.findByText("Search is unavailable. You can still paste an ID."),
    ).toBeInTheDocument();
    expect(screen.getByRole("option", { name: "Use this ID: abc123" })).toBeInTheDocument();
  });

  it("clears the picked ID", async () => {
    respond([]);
    const onChange = vi.fn();
    wrap(<Single onChange={onChange} />);
    await userEvent.type(screen.getByRole("combobox"), "abc123");
    await screen.findByRole("option", { name: "Use this ID: abc123" });
    await userEvent.keyboard("{Enter}");
    await userEvent.click(screen.getByRole("button", { name: "Clear" }));
    expect(onChange).toHaveBeenLastCalledWith("");
    expect(screen.queryByTestId("entity-picker-selected")).toBeNull();
    expect(screen.getByRole("combobox")).toHaveFocus();
  });
});

describe("EntityMultiPicker", () => {
  it("adds picked rows and pasted lists as removable chips", async () => {
    respond([mediaRow]);
    const onChange = vi.fn();
    wrap(<Multi onChange={onChange} />);
    const input = screen.getByRole("combobox", { name: "Items" });
    await userEvent.type(input, "harbor");
    await userEvent.click(await screen.findByRole("option", { name: /Harbor Lights/ }));
    expect(onChange).toHaveBeenLastCalledWith(["m-1"]);

    respond([]);
    await userEvent.type(input, "a1, b2 m-1");
    await userEvent.click(await screen.findByRole("option", { name: "Use these IDs: 3" }));
    expect(onChange).toHaveBeenLastCalledWith(["m-1", "a1", "b2"]);

    await userEvent.click(screen.getByRole("button", { name: "Remove Harbor Lights" }));
    await waitFor(() => expect(onChange).toHaveBeenLastCalledWith(["a1", "b2"]));
  });
});

describe("splitIDs", () => {
  it("splits on commas and whitespace", () => {
    expect(splitIDs(" a, b\nc  ,,d ")).toEqual(["a", "b", "c", "d"]);
  });
});
