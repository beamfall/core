import "@testing-library/jest-dom/vitest";
import { act, cleanup, render, screen } from "@testing-library/react";
import type { ReactNode } from "react";
import { MemoryRouter, Route, Routes } from "react-router";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { I18nProvider } from "@/lib/i18n";
import { clearViewerCache } from "@/lib/viewerCache";
import { resetHomeNetworkProbeForTest } from "../lib/watchTogether";
import { MediaDetailPage } from "./MediaDetailPage";

// CORE-2509: opening a film's detail page sends a bounded set of GraphQL reads,
// once each, with nothing aborted and sent again. Every fetcher here is the
// real one; only the network answers are canned.

const mocks = vi.hoisted(() => ({
  identity: JSON.stringify(["viewer", "p1", "adult", false]),
}));

vi.mock("@/lib/session", () => ({
  useViewerIdentity: () => mocks.identity,
  useSession: () => ({ manifest: null, revealed: false, session: { profileID: "p1" } }),
}));
vi.mock("@/components/chrome/AppShell", () => ({
  AppShell: ({ children }: { children: ReactNode }) => <main>{children}</main>,
}));
vi.mock("../components/ViewerActions", () => ({ ViewerActions: () => null }));
vi.mock("../player/preload", () => ({ preloadPlayer: vi.fn() }));

const category = (entityID: string, title: string, kind: string) => ({
  entityID,
  title,
  kind,
  kindDisplayName: kind,
  archetype: "category",
  relation: kind,
});
const person = (entityID: string, title: string, relation: string, job?: string) => ({
  entityID,
  title,
  kind: "person",
  kindDisplayName: "Person",
  archetype: "person",
  relation,
  ...(job ? { attributes: { job } } : {}),
});

const film = {
  id: "m1",
  title: "A Film",
  summary: "Summary",
  year: 2001,
  durationSeconds: 8100,
  mediaClass: "video",
  libraryID: "lib-films",
  favorited: false,
  artworkSlots: { poster: "/api/media/artwork/m1/poster?v=1" },
  credits: [
    category("g1", "Drama", "genre"),
    category("g2", "Biography", "genre"),
    category("s1", "Universal Pictures", "studio"),
    category("s2", "DreamWorks Pictures", "studio"),
    category("s3", "Imagine Entertainment", "studio"),
    person("p1", "Lead Actor", "cast"),
    person("p2", "Second Actor", "cast"),
    person("d1", "The Director", "crew", "Director"),
    person("w1", "The Writer", "crew", "Screenplay"),
  ],
};

const card = (id: string) => ({ id, kind: "media", title: `Title ${id}` });

// The answer for each root field, by name; anything else gets null.
const answers: Record<string, unknown> = {
  media: film,
  mediaFields: [{ name: "tagline", type: "text", value: "A tagline", label: { en: "Tagline" } }],
  mediaRelations: { locked: false, media: [] },
  mediaTracks: {
    versions: [
      {
        id: "v1",
        label: "",
        videoCodec: "hevc",
        hdr: "HDR10",
        audioTracks: [
          { streamIndex: 1, codec: "eac3", language: "en", channels: 6, default: true },
        ],
        captionTracks: [],
      },
    ],
  },
  sourceItems: [card("a1"), card("a2")],
  relatedMedia: [card("r1"), card("r2")],
  itemState: { id: "m1", kind: "media", watched: false, watchlisted: false, rating: 0 },
};

function rootField(query: string): string {
  const body = query.slice(query.indexOf("{") + 1);
  return /\s*(\w+)/.exec(body)?.[1] ?? "";
}

let sent: string[] = [];
let aborted: string[] = [];

beforeEach(() => {
  sent = [];
  aborted = [];
  resetHomeNetworkProbeForTest();
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (!url.endsWith("/api/graphql")) return new Response("{}", { status: 404 });
      const { query, variables } = JSON.parse(String(init?.body)) as {
        query: string;
        variables: unknown;
      };
      const field = rootField(query);
      const read = `${field} ${JSON.stringify(variables)}`;
      sent.push(read);
      init?.signal?.addEventListener("abort", () => aborted.push(read));
      const data = { [field]: field in answers ? answers[field] : null };
      return new Response(JSON.stringify({ data }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      });
    }),
  );
});

afterEach(() => {
  cleanup();
  clearViewerCache();
  vi.unstubAllGlobals();
  window.localStorage.clear();
});

describe("MediaDetailPage requests (CORE-2509)", () => {
  it("opens a film with under 15 GraphQL reads, none repeated or aborted", async () => {
    render(
      <I18nProvider>
        <MemoryRouter initialEntries={["/media/m1"]}>
          <Routes>
            <Route path="/media/*" element={<MediaDetailPage />} />
          </Routes>
        </MemoryRouter>
      </I18nProvider>,
    );
    await screen.findByRole("heading", { level: 1, name: "A Film" });
    // Let every follow-on read settle.
    for (let i = 0; i < 10; i += 1) await act(async () => new Promise((r) => setTimeout(r, 5)));

    expect(sent.length).toBeLessThan(15);
    expect(aborted).toEqual([]);
    expect(sent.filter((read, i) => sent.indexOf(read) !== i)).toEqual([]);
    expect(sent.map((read) => read.split(" ")[0]).sort()).toEqual([
      "itemState",
      "media",
      "mediaFields",
      "mediaRelations",
      "mediaRelations",
      "mediaTracks",
      "relatedMedia",
      "sourceItems",
    ]);
  });
});
