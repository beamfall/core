import "@testing-library/jest-dom/vitest";
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { ItemStateInfo } from "@/lib/graphql/types";
import { I18nProvider } from "@/lib/i18n";
import { isEngagementInvalidation, notifyEngagementChanged } from "./engagementSync";
import { ItemActions } from "./ItemActions";
import { itemTargetFor } from "./useItemActions";

const server = vi.hoisted(() => ({
  state: { id: "entity-1", kind: "entity", watched: false, watchlisted: false, rating: 0 },
  calls: [] as Array<[string, unknown, unknown]>,
}));

vi.mock("@/lib/graphql", () => {
  const apply = (name: string, patch: (value: never) => Partial<ItemStateInfo>) =>
    vi.fn(async (target: unknown, value: never) => {
      server.calls.push([name, target, value]);
      server.state = { ...server.state, ...patch(value) };
      return server.state;
    });
  return {
    fetchItemState: vi.fn(async () => server.state),
    setWatched: apply("setWatched", (watched) => ({ watched })),
    setWatchlisted: apply("setWatchlisted", (watchlisted) => ({ watchlisted })),
    setRating: apply("setRating", (rating) => ({ rating })),
  };
});

afterEach(() => {
  cleanup();
  server.state = { id: "entity-1", kind: "entity", watched: false, watchlisted: false, rating: 0 };
  server.calls = [];
});

function renderActions() {
  return render(
    <I18nProvider>
      <ItemActions target={{ entityID: "entity-1" }} />
    </I18nProvider>,
  );
}

describe("ItemActions", () => {
  it("marks watched, adds to the watchlist, and sets then clears a rating", async () => {
    renderActions();
    const watched = await screen.findByRole("button", { name: "Mark watched" });
    await waitFor(() => expect(watched).toBeEnabled());

    fireEvent.click(watched);
    await screen.findByRole("button", { name: "Mark unwatched", pressed: true });

    fireEvent.click(screen.getByRole("button", { name: "Add to watchlist" }));
    await screen.findByRole("button", { name: "Remove from watchlist", pressed: true });

    fireEvent.click(screen.getByRole("button", { name: "Rate 4 out of 5" }));
    const clear = await screen.findByRole("button", { name: "Clear your rating" });
    await waitFor(() => expect(clear).toBeEnabled());
    fireEvent.click(clear);
    await screen.findByRole("button", { name: "Rate 4 out of 5", pressed: false });

    expect(server.calls).toEqual([
      ["setWatched", { entityID: "entity-1" }, true],
      ["setWatchlisted", { entityID: "entity-1" }, true],
      ["setRating", { entityID: "entity-1" }, 8],
      ["setRating", { entityID: "entity-1" }, 0],
    ]);
  });

  it("follows a change made on another client", async () => {
    renderActions();
    await screen.findByRole("button", { name: "Mark watched" });
    server.state = { ...server.state, watched: true };
    act(() => notifyEngagementChanged());
    await screen.findByRole("button", { name: "Mark unwatched", pressed: true });
  });

  it("offers a retry when the first load fails, and recovers", async () => {
    const { fetchItemState } = await import("@/lib/graphql");
    vi.mocked(fetchItemState).mockRejectedValueOnce(new Error("offline"));
    renderActions();
    const retry = await screen.findByRole("button", { name: "Try again" });
    expect(screen.getByRole("button", { name: "Mark watched" })).toBeDisabled();
    fireEvent.click(retry);
    await waitFor(() => expect(screen.getByRole("button", { name: "Mark watched" })).toBeEnabled());
    expect(screen.queryByRole("button", { name: "Try again" })).toBeNull();
  });
});

describe("item action helpers", () => {
  it("targets a media item as Media and anything else as an Entity", () => {
    expect(itemTargetFor({ id: "m1", kind: "media" })).toEqual({ mediaID: "m1" });
    expect(itemTargetFor({ id: "e1", kind: "series" })).toEqual({ entityID: "e1" });
  });

  it("recognises only the engagement-changed invalidation", () => {
    const frame = (reason: string) =>
      JSON.stringify({ kind: "client.invalidate", payload: { profileID: "p", reason } });
    expect(isEngagementInvalidation(frame("engagement-changed"))).toBe(true);
    expect(isEngagementInvalidation(frame("access-changed"))).toBe(false);
    expect(isEngagementInvalidation("not json")).toBe(false);
  });
});
