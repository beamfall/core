import { render } from "@testing-library/react";
import { MemoryRouter } from "react-router";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { GenericMediaInfo } from "@/lib/graphql/types";
import { useCanonicalMediaAddress } from "./useCanonicalMediaAddress";

const session = vi.hoisted(() => ({ revealed: false }));
vi.mock("@/lib/session", () => ({
  useSession: () => ({
    revealed: session.revealed,
    manifest: {
      libraries: [{ id: "library-music", title: "Music", pluginID: "music", previewClips: false }],
    },
  }),
}));

const ID = "media-sha256-a371bff46385f7ded";
const media: GenericMediaInfo = {
  id: ID,
  title: "38 Planes",
  mediaClass: "audio",
  libraryID: "library-music",
  credits: [{ entityID: "p", title: "Company", kind: "k", archetype: "person", relation: "r" }],
} as GenericMediaInfo;

function Harness({ route, enabled }: { route: "watch" | "media"; enabled?: boolean }) {
  useCanonicalMediaAddress(route, media, enabled);
  return null;
}

function arriveAt(path: string, route: "watch" | "media" = "watch", enabled = true) {
  window.history.replaceState({ idx: 3 }, "", path);
  render(
    <MemoryRouter>
      <Harness route={route} enabled={enabled} />
    </MemoryRouter>,
  );
}

describe("useCanonicalMediaAddress (CORE-2329)", () => {
  afterEach(() => {
    session.revealed = false;
    window.history.replaceState(null, "", "/");
  });

  it("replaces a bare-ID or stale address with the readable one in place", () => {
    const length = window.history.length;
    arriveAt(`/watch/${ID}?t=5#x`);
    expect(window.location.pathname).toBe("/watch/music/company-38-planes-a371bff46385f7ded");
    expect(window.location.search).toBe("?t=5");
    expect(window.location.hash).toBe("#x");
    expect(window.history.state).toEqual({ idx: 3 });
    expect(window.history.length).toBe(length);
  });

  it("fixes an old slug on the detail route", () => {
    arriveAt("/media/old/renamed-a371bff46385f7ded", "media");
    expect(window.location.pathname).toBe("/media/music/company-38-planes-a371bff46385f7ded");
  });

  it("keeps the bare ID in a Revealed session", () => {
    session.revealed = true;
    arriveAt("/watch/music/company-38-planes-a371bff46385f7ded");
    expect(window.location.pathname).toBe(`/watch/${ID}`);
  });

  it("leaves another page's address alone (docked player or other item)", () => {
    arriveAt("/browse/library-music");
    expect(window.location.pathname).toBe("/browse/library-music");
    arriveAt("/watch/media-sha256-00000000000000000");
    expect(window.location.pathname).toBe("/watch/media-sha256-00000000000000000");
    arriveAt(`/watch/${ID}`, "watch", false);
    expect(window.location.pathname).toBe(`/watch/${ID}`);
  });
});
