import "@testing-library/jest-dom/vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { MemoryRouter, useLocation } from "react-router";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { SourceItem } from "@/lib/graphql/types";
import { I18nProvider } from "@/lib/i18n";
import { readPlayerNavState } from "../lib/watchQueue";
import { MediaItemCard } from "./MediaItemCard";

vi.mock("@/lib/session", () => ({
  useViewerIdentity: () => "viewer-1",
}));

vi.mock("../player/preload", () => ({
  preloadPlayer: vi.fn(),
}));

afterEach(cleanup);

function item(overrides: Partial<SourceItem> = {}): SourceItem {
  return {
    id: "media-alpha",
    kind: "media",
    title: "Alpha",
    artwork: { poster: "/art/poster-alpha.jpg" },
    ...overrides,
  };
}

function LocationState() {
  const location = useLocation();
  return (
    <>
      <span data-testid="path">{location.pathname}</span>
      <span data-testid="state">{JSON.stringify(location.state)}</span>
    </>
  );
}

function renderCard(props: Partial<Parameters<typeof MediaItemCard>[0]> = {}) {
  return render(
    <I18nProvider initialLocale="en">
      <MemoryRouter>
        <ul>
          <MediaItemCard item={item()} {...props} />
        </ul>
        <LocationState />
      </MemoryRouter>
    </I18nProvider>,
  );
}

// CORE-2826: a poster card starts playback in one click, keyboard included.
describe("MediaItemCard one-click Play", () => {
  it("plays a poster card's Media with its queue while the card link still opens detail", () => {
    const queue = [
      { id: "media-alpha", title: "Alpha" },
      { id: "media-beta", title: "Beta" },
    ];
    renderCard({ queue });

    const link = screen.getByRole("link", { name: /^Alpha/ });
    expect(link).toHaveAttribute("href", "/media/media-alpha");
    const play = screen.getByRole("button", { name: "Play Alpha" });
    expect(link.contains(play)).toBe(false);
    expect(screen.getByRole("button", { name: "More actions for Alpha" })).toBeInTheDocument();

    fireEvent.click(play);

    expect(screen.getByTestId("path")).toHaveTextContent("/watch/media-alpha");
    const state = JSON.parse(screen.getByTestId("state").textContent || "null");
    expect(readPlayerNavState(state, "viewer-1").queue).toEqual(queue);
  });

  it("is a focusable button after the card link, named Resume when the item has progress", () => {
    renderCard({
      item: item({ engagement: { positionSeconds: 600, durationSeconds: 3600 } }),
      display: "thumbnail",
    });

    const play = screen.getByRole("button", { name: "Resume Alpha" });
    expect(play.tagName).toBe("BUTTON");
    expect(play).not.toHaveAttribute("tabindex", "-1");
    play.focus();
    expect(play).toHaveFocus();
    // It follows the card link in document (tab) order.
    const link = screen.getByRole("link", { name: /^Alpha/ });
    expect(link.compareDocumentPosition(play) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    fireEvent.click(play);
    expect(screen.getByTestId("path")).toHaveTextContent("/watch/media-alpha");
  });

  it("is left off rows, play-on-activate cards, locked items and entities", () => {
    const { unmount } = renderCard({ display: "list" });
    expect(screen.queryByRole("button", { name: "Play Alpha" })).toBeNull();
    unmount();

    const second = renderCard({ playOnActivate: true });
    expect(screen.queryByRole("button", { name: "Play Alpha" })).toBeNull();
    second.unmount();

    const third = renderCard({ item: item({ locked: true }) });
    expect(screen.queryByRole("button", { name: "Play Alpha" })).toBeNull();
    third.unmount();

    renderCard({ item: item({ kind: "entity", id: "e1" }) });
    expect(screen.queryByRole("button", { name: /^Play/ })).toBeNull();
  });

  it("keeps a child card's own Play button as the only one", () => {
    renderCard({ showPlayButton: true });
    expect(screen.getAllByRole("button", { name: /^Play Alpha$/ })).toHaveLength(1);
  });
});
