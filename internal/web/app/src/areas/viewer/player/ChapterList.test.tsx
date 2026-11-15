import "@testing-library/jest-dom/vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { I18nProvider } from "@/lib/i18n";
import type { ChapterEntry, TrickplayManifest } from "../lib/transport";
import { ChapterList } from "./ChapterList";

const chapters: ChapterEntry[] = [
  { startSeconds: 0, title: "Opening", thumbnailURL: "/assets/t/marker-0.jpg", current: false },
  { startSeconds: 95, title: "", thumbnailURL: "/assets/t/marker-95000.jpg", current: true },
];

const trickplay: TrickplayManifest = {
  interval: 10,
  tileWidth: 320,
  tileHeight: 180,
  columns: 10,
  rows: 10,
  sheets: ["sheet-0.jpg"],
};

function renderList(props: Partial<Parameters<typeof ChapterList>[0]> = {}) {
  const onSeek = vi.fn();
  const onOpenChange = vi.fn();
  render(
    <I18nProvider>
      <ChapterList
        chapters={chapters}
        trickplay={null}
        trickplayBaseURL=""
        onSeek={onSeek}
        onOpenChange={onOpenChange}
        {...props}
      />
    </I18nProvider>,
  );
  return { onSeek, onOpenChange };
}

describe("ChapterList", () => {
  it("renders nothing without chapters", () => {
    renderList({ chapters: [] });
    expect(screen.queryByRole("button", { name: "Chapters" })).not.toBeInTheDocument();
  });

  it("lists chapters with titles, start times and the current one, and seeks on select", async () => {
    const user = userEvent.setup();
    const { onSeek, onOpenChange } = renderList();

    await user.click(screen.getByRole("button", { name: "Chapters" }));
    const items = await screen.findAllByRole("menuitem");
    expect(onOpenChange).toHaveBeenLastCalledWith(true);
    expect(items).toHaveLength(2);
    expect(items[0]).toHaveTextContent("Opening");
    expect(items[0]).toHaveTextContent("0:00");
    expect(items[1]).toHaveTextContent("Chapter 2");
    expect(items[1]).toHaveTextContent("1:35");
    expect(items[1]).toHaveAttribute("aria-current", "true");
    expect(items[0]).not.toHaveAttribute("aria-current");

    await user.click(items[1]);
    expect(onSeek).toHaveBeenCalledWith(95);
    expect(onOpenChange).toHaveBeenLastCalledWith(false);
  });

  it("falls back from a missing marker thumbnail to the trickplay tile", async () => {
    const user = userEvent.setup();
    renderList({ trickplay, trickplayBaseURL: "/api/media/assets/t/trickplay.json" });

    await user.click(screen.getByRole("button", { name: "Chapters" }));
    const items = await screen.findAllByRole("menuitem");
    const img = items[1].querySelector("img");
    expect(img).toHaveAttribute("src", "/assets/t/marker-95000.jpg");
    if (!img) throw new Error("thumbnail missing");

    fireEvent.error(img);
    expect(items[1].querySelector("img")).toBeNull();
    const tile = items[1].querySelector<HTMLElement>(".bf-chapter-thumb.is-trickplay");
    expect(tile?.style.backgroundImage).toContain(
      `${window.location.origin}/api/media/assets/t/sheet-0.jpg`,
    );
  });

  it("shows no image when neither a thumbnail nor trickplay exists", async () => {
    const user = userEvent.setup();
    renderList({
      chapters: chapters.map((chapter) => ({ ...chapter, thumbnailURL: null })),
    });
    await user.click(screen.getByRole("button", { name: "Chapters" }));
    const items = await screen.findAllByRole("menuitem");
    expect(items[0].querySelector(".bf-chapter-thumb")).toBeNull();
  });

  it("closes on Escape without seeking", async () => {
    const user = userEvent.setup();
    const { onSeek, onOpenChange } = renderList();
    await user.click(screen.getByRole("button", { name: "Chapters" }));
    await screen.findAllByRole("menuitem");
    await user.keyboard("{Escape}");
    expect(onOpenChange).toHaveBeenLastCalledWith(false);
    expect(onSeek).not.toHaveBeenCalled();
  });
});
