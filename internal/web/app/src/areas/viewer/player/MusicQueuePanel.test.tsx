import "@testing-library/jest-dom/vitest";
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { useState } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { OFFERED_LOCALE_TAGS } from "@/l10n/locales";
import { I18nProvider } from "@/lib/i18n";
import { STRINGS } from "@/strings";
import { MusicQueuePanel } from "./MusicQueuePanel";

const viewer = vi.hoisted(() => ({
  profileID: "profile-a",
  revealed: true,
  refresh: vi.fn(async () => {}),
}));
vi.mock("@/lib/session", () => ({
  useSession: () => ({
    session: { profileID: viewer.profileID },
    revealed: viewer.revealed,
    refresh: viewer.refresh,
  }),
  useViewerIdentity: () => `${viewer.profileID}:${viewer.revealed}`,
}));

function QueueButton({ onPlay }: { onPlay: (id: string) => void }) {
  const [open, setOpen] = useState(false);
  return (
    <MusicQueuePanel open={open} onOpenChange={setOpen} currentID="track-202" onPlay={onPlay} />
  );
}

function renderQueuePanel(onPlay = vi.fn()) {
  render(
    <I18nProvider initialLocale="en">
      <QueueButton onPlay={onPlay} />
    </I18nProvider>,
  );
  fireEvent.click(screen.getByRole("button", { name: STRINGS.en.musicQueueTitle }));
  return onPlay;
}

function page(
  rows: object[],
  currentSession = { profileID: viewer.profileID, revealed: viewer.revealed },
) {
  return new Response(JSON.stringify({ data: { currentSession, sourceItems: rows } }));
}

afterEach(() => {
  vi.unstubAllGlobals();
  viewer.profileID = "profile-a";
  viewer.revealed = true;
  viewer.refresh.mockClear();
});

describe("MusicQueuePanel", () => {
  it("reads all pages in server order and sends zero mutations when opened", async () => {
    const firstPage = Array.from({ length: 200 }, (_, i) => ({
      id: `track-${i + 1}`,
      title: `Track ${i + 1}`,
      mediaClass: "audio",
    }));
    firstPage[0] = { ...firstPage[0], title: "Film", mediaClass: "video" };
    const requests = vi
      .fn()
      .mockImplementationOnce(async () => page(firstPage))
      .mockImplementationOnce(async () =>
        page([
          { id: "track-201", title: "Private title", mediaClass: "audio", locked: true },
          { id: "track-202", title: "Track 202", mediaClass: "audio" },
        ]),
      );
    vi.stubGlobal("fetch", requests);
    const onPlay = renderQueuePanel();
    const panel = screen.getByRole("dialog", { name: STRINGS.en.musicQueueTitle });
    expect(await within(panel).findByText("Track 202")).toHaveAttribute("aria-current", "true");
    const displayed = within(panel).getAllByRole("listitem");
    expect(displayed).toHaveLength(202);
    expect(displayed[0]).toHaveTextContent("Film");
    expect(displayed[199]).toHaveTextContent("Track 200");
    expect(displayed[200]).toHaveTextContent(STRINGS.en.musicQueueLockedItem);
    expect(screen.queryByText("Private title")).toBeNull();
    expect(requests.mock.calls).toHaveLength(2);
    for (const [index, [path, request]] of requests.mock.calls.entries()) {
      const body = JSON.parse(request.body);
      expect(path).toBe("/api/graphql");
      expect(body.query).toMatch(/^query MusicQueue\b/);
      expect(body.query).toContain('sourceItems(kind: "queue"');
      expect(body.query).toContain("currentSession { profileID revealed");
      expect(body.variables).toEqual({ query: "queue", limit: 200, offset: index * 200 });
    }
    expect(onPlay).not.toHaveBeenCalled();
  });

  it("starts the first playable row from the refreshed pages", async () => {
    const firstPage = Array.from({ length: 200 }, (_, i) => ({
      id: `film-${i + 1}`,
      title: `Film ${i + 1}`,
      mediaClass: "video",
    }));
    const requests = vi
      .fn()
      .mockImplementationOnce(async () =>
        page([{ id: "old-first", title: "Removed track", mediaClass: "audio" }]),
      )
      .mockImplementationOnce(async () => page(firstPage))
      .mockImplementationOnce(async () =>
        page([{ id: "new-first", title: "New first track", mediaClass: "audio" }]),
      );
    vi.stubGlobal("fetch", requests);
    const onPlay = renderQueuePanel();
    fireEvent.click(await screen.findByRole("button", { name: STRINGS.en.musicQueuePlay }));
    await waitFor(() => expect(onPlay).toHaveBeenCalledExactlyOnceWith("new-first"));
    expect(
      requests.mock.calls.map(([, request]) => JSON.parse(request.body).variables.offset),
    ).toEqual([0, 0, 200]);
  });

  it("leaves the panel open when another device clears the queue before Play", async () => {
    const requests = vi
      .fn()
      .mockImplementationOnce(async () =>
        page([{ id: "old-first", title: "Removed track", mediaClass: "audio" }]),
      )
      .mockImplementationOnce(async () => page([]));
    vi.stubGlobal("fetch", requests);
    const onPlay = renderQueuePanel();
    fireEvent.click(await screen.findByRole("button", { name: STRINGS.en.musicQueuePlay }));
    expect(await screen.findByText(STRINGS.en.musicQueueEmpty)).toBeInTheDocument();
    expect(onPlay).not.toHaveBeenCalled();
  });

  it.each([
    ["profile switch", { profileID: "profile-b", revealed: true }],
    ["relock", { profileID: "profile-a", revealed: false }],
  ])("discards every page when a remote %s happens between pages", async (_, changedSession) => {
    const requests = vi
      .fn()
      .mockImplementationOnce(async () =>
        page(
          Array.from({ length: 200 }, (_, i) => ({
            id: `track-${i}`,
            title: `First page ${i}`,
            mediaClass: "audio",
          })),
        ),
      )
      .mockImplementationOnce(async () =>
        page([{ id: "other", title: "Other identity track", mediaClass: "audio" }], changedSession),
      );
    vi.stubGlobal("fetch", requests);
    const onPlay = renderQueuePanel();
    await waitFor(() => expect(viewer.refresh).toHaveBeenCalledOnce());
    expect(requests).toHaveBeenCalledTimes(2);
    expect(screen.queryByText("First page 0")).toBeNull();
    expect(screen.queryByText("Other identity track")).toBeNull();
    expect(screen.queryByRole("button", { name: STRINGS.en.musicQueuePlay })).toBeNull();
    expect(onPlay).not.toHaveBeenCalled();
  });

  it("shows the empty state and no Play action", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => page([])),
    );
    renderQueuePanel();
    expect(await screen.findByText(STRINGS.en.musicQueueEmpty)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: STRINGS.en.musicQueuePlay })).toBeNull();
  });

  it.each(["relock", "profile switch"])("drops the open list immediately on %s", async (change) => {
    const request = vi
      .fn()
      .mockImplementationOnce(async () =>
        page([{ id: "private", title: "Restricted track", mediaClass: "audio" }]),
      )
      .mockImplementationOnce(async () => page([]));
    vi.stubGlobal("fetch", request);
    const onPlay = vi.fn();
    const tree = () => (
      <I18nProvider initialLocale="en">
        <QueueButton onPlay={onPlay} />
      </I18nProvider>
    );
    const view = render(tree());
    fireEvent.click(screen.getByRole("button", { name: STRINGS.en.musicQueueTitle }));
    expect(await screen.findByText("Restricted track")).toBeInTheDocument();

    if (change === "relock") viewer.revealed = false;
    else viewer.profileID = "profile-b";
    view.rerender(tree());
    expect(screen.queryByText("Restricted track")).toBeNull();
    expect(screen.queryByRole("button", { name: STRINGS.en.musicQueuePlay })).toBeNull();
    expect(await screen.findByText(STRINGS.en.musicQueueEmpty)).toBeInTheDocument();
    expect(request).toHaveBeenCalledTimes(2);
  });

  it("localizes read failures and retries the read", async () => {
    const request = vi
      .fn()
      .mockRejectedValueOnce(new Error("network"))
      .mockImplementationOnce(async () => page([]));
    vi.stubGlobal("fetch", request);
    renderQueuePanel();
    expect(await screen.findByRole("alert")).toHaveTextContent(STRINGS.en.musicQueueReadFailed);
    fireEvent.click(screen.getByRole("button", { name: STRINGS.en.tryAgain }));
    await waitFor(() =>
      expect(screen.getByRole("status")).toHaveTextContent(STRINGS.en.musicQueueEmpty),
    );
  });

  it("provides all queue strings in every offered locale", () => {
    for (const locale of OFFERED_LOCALE_TAGS) {
      const strings = STRINGS[locale as keyof typeof STRINGS];
      for (const key of [
        "musicQueueTitle",
        "musicQueuePlay",
        "musicQueueEmpty",
        "musicQueueReadFailed",
        "musicQueueLockedItem",
      ] as const) {
        expect(strings[key], `${locale}: ${key}`).toBeTruthy();
        if (locale !== "en") expect(strings[key], `${locale}: ${key}`).not.toBe(STRINGS.en[key]);
      }
    }
  });
});
