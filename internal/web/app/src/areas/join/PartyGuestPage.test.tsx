import "@testing-library/jest-dom/vitest";
import { cleanup, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes } from "react-router";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { I18nProvider } from "@/lib/i18n";
import { PARTY_GUEST_HEADER, type PartyGuestState } from "@/lib/partyGuest";
import { expectNoAxeViolations } from "@/test/axe";
import { PartyGuestPage } from "./PartyGuestPage";

// Fictional party content only.
const GUEST_CODE = "bfg_test-token-not-real";
const STORAGE_KEY = "beamfall.partyGuest.LARK42";

function guestState(overrides: Partial<PartyGuestState> = {}): PartyGuestState {
  return {
    sessionID: "s1",
    joinCode: "LARK42",
    autoAdd: false,
    expiresAt: "2026-11-22T06:00:00Z",
    maxWaiting: 3,
    current: {
      id: "e0",
      title: "Moonlit Harbour",
      subtitle: "The Paper Kites of Elm Street",
      participantName: "Quill",
      state: "current",
      position: 0,
      etaSeconds: 0,
      pitchSemitones: 0,
    },
    entries: [
      {
        id: "e1",
        title: "Velvet Static",
        subtitle: "Nova Rook",
        participantName: "Bramble",
        state: "waiting",
        position: 1,
        etaSeconds: 30,
        pitchSemitones: 0,
      },
      {
        id: "e2",
        title: "Paper Lantern Parade",
        subtitle: "Juniper Vale",
        participantName: "Wren",
        state: "waiting",
        position: 2,
        etaSeconds: 280,
        pitchSemitones: 0,
      },
    ],
    mine: [
      {
        id: "e2",
        mediaID: "m2",
        title: "Paper Lantern Parade",
        subtitle: "Juniper Vale",
        participantName: "Wren",
        state: "waiting",
        position: 2,
        etaSeconds: 280,
        pitchSemitones: 0,
      },
      {
        id: "e9",
        mediaID: "m9",
        title: "Glasshouse Weather",
        subtitle: "Juniper Vale",
        participantName: "Wren",
        state: "pending",
        position: 0,
        etaSeconds: 0,
        pitchSemitones: 0,
      },
    ],
    ...overrides,
  };
}

type Handler = (init: RequestInit) => Response | Promise<Response>;
const json = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

let routes: Record<string, Handler>;
const fetchMock = vi.fn(async (input: RequestInfo | URL, init: RequestInit = {}) => {
  const url = String(input);
  const key = `${init.method ?? "GET"} ${url.split("?")[0]}`;
  const handler = routes[key];
  if (!handler) throw new Error(`unexpected ${key}`);
  return handler(init);
});

function calls(key: string) {
  return fetchMock.mock.calls.filter(
    ([input, init]) => `${init?.method ?? "GET"} ${String(input).split("?")[0]}` === key,
  );
}

beforeEach(() => {
  window.sessionStorage.clear();
  fetchMock.mockClear();
  routes = { "GET /api/party/guest": () => json(200, guestState()) };
  vi.stubGlobal("fetch", fetchMock);
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

function renderPage(path = "/join/LARK42") {
  return render(
    <I18nProvider initialLocale="en">
      <MemoryRouter initialEntries={[path]}>
        <Routes>
          <Route path="/join/:code" element={<PartyGuestPage />} />
        </Routes>
      </MemoryRouter>
    </I18nProvider>,
  );
}

function storeToken() {
  window.sessionStorage.setItem(STORAGE_KEY, JSON.stringify({ token: GUEST_CODE, name: "Wren" }));
}

describe("PartyGuestPage join", () => {
  it("prefills the code, joins with a name, stores the token and shows the party", async () => {
    routes["POST /api/party/join"] = () =>
      json(200, {
        token: GUEST_CODE,
        sessionID: "s1",
        participantID: "p1",
        name: "Wren",
        expiresAt: "2026-11-22T06:00:00Z",
      });
    const { container } = renderPage("/join/lark42");
    expect(screen.getByLabelText("Party code")).toHaveValue("LARK42");
    await expectNoAxeViolations(container);

    await userEvent.type(screen.getByLabelText("Your name"), "Wren");
    await userEvent.click(screen.getByRole("button", { name: "Join" }));

    expect(await screen.findByText("Singing as Wren")).toBeInTheDocument();
    const [, init] = calls("POST /api/party/join")[0];
    expect(JSON.parse(String(init?.body))).toEqual({ code: "LARK42", name: "Wren" });
    expect(init?.credentials).toBe("omit");
    expect(JSON.parse(window.sessionStorage.getItem(STORAGE_KEY) ?? "{}").token).toBe(GUEST_CODE);

    expect(await screen.findByText("Moonlit Harbour")).toBeInTheDocument();
    const stateCall = calls("GET /api/party/guest")[0];
    expect((stateCall[1]?.headers as Record<string, string>)[PARTY_GUEST_HEADER]).toBe(GUEST_CODE);
  });

  it.each([
    [404, "join code not recognised", "That code didn't work. Check it and try again."],
    [429, "rate-limited", "Too many tries. Wait a minute, then try again."],
    [409, "full", "This party is full. Ask the host to make room."],
  ])("explains a %i join refusal", async (status, reason, message) => {
    routes["POST /api/party/join"] = () => json(status, { error: reason });
    renderPage();
    await userEvent.type(screen.getByLabelText("Your name"), "Wren");
    await userEvent.click(screen.getByRole("button", { name: "Join" }));
    expect(await screen.findByRole("alert")).toHaveTextContent(message);
    expect(window.sessionStorage.getItem(STORAGE_KEY)).toBeNull();
  });

  it("asks for a name before calling the server", async () => {
    renderPage();
    await userEvent.click(screen.getByRole("button", { name: "Join" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("Enter your name.");
    expect(calls("POST /api/party/join")).toHaveLength(0);
  });
});

describe("PartyGuestPage joined", () => {
  it("shows my songs with position, ETA and pending state, and the rotation", async () => {
    storeToken();
    const { container } = renderPage();
    const mine = await screen.findByRole("heading", { name: "Your songs" });
    const section = mine.parentElement as HTMLElement;
    expect(await within(section).findByText("Number 2 in line")).toBeInTheDocument();
    expect(within(section).getByText("about 5 min")).toBeInTheDocument();
    expect(within(section).getByText("Waiting for the host to approve")).toBeInTheDocument();
    const rotation = screen.getByRole("region", { name: "Coming up" });
    expect(within(rotation).getByText("Bramble")).toBeInTheDocument();
    expect(within(rotation).getByText("Up next")).toBeInTheDocument();
    expect(within(rotation).getByText("Wren (You)")).toBeInTheDocument();
    await expectNoAxeViolations(container);
  });

  it("drops back to the join form when the token is refused", async () => {
    storeToken();
    routes["GET /api/party/guest"] = () => json(401, { error: "party session ended" });
    renderPage();
    expect(await screen.findByRole("alert")).toHaveTextContent("This party has ended");
    expect(screen.getByLabelText("Party code")).toHaveValue("LARK42");
    expect(window.sessionStorage.getItem(STORAGE_KEY)).toBeNull();
  });

  it("cancels a request", async () => {
    storeToken();
    routes["POST /api/party/guest/cancel"] = () => json(200, { ok: true });
    renderPage();
    await userEvent.click(await screen.findByRole("button", { name: "Cancel Glasshouse Weather" }));
    const [, init] = calls("POST /api/party/guest/cancel")[0];
    expect(JSON.parse(String(init?.body))).toEqual({ entryID: "e9" });
  });

  it("searches, marks songs sung earlier, and requests one", async () => {
    storeToken();
    routes["GET /api/party/guest/search"] = () =>
      json(200, {
        items: [
          {
            mediaID: "m1",
            title: "Copper Skyline",
            subtitle: "Nova Rook",
            durationSeconds: 215,
            artworkURL: "",
            lastPlayedAt: "2026-11-21T21:10:00Z",
          },
        ],
      });
    const requestedEntry = {
      id: "e10",
      mediaID: "m1",
      title: "Copper Skyline",
      subtitle: "Nova Rook",
      participantName: "Wren",
      state: "pending",
      position: 0,
      etaSeconds: 0,
      pitchSemitones: -2,
    };
    routes["POST /api/party/guest/request"] = () => {
      const base = guestState();
      routes["GET /api/party/guest"] = () =>
        json(200, { ...base, mine: [...base.mine, requestedEntry] });
      return json(200, { entry: requestedEntry });
    };
    const { container } = renderPage();
    await screen.findByText("Number 2 in line");
    await userEvent.type(screen.getByRole("searchbox", { name: "Find a song" }), "copper");
    await userEvent.click(screen.getByRole("button", { name: "Search" }));
    expect(await screen.findByText("Copper Skyline")).toBeInTheDocument();
    expect(screen.getByText("Nova Rook · 3:35")).toBeInTheDocument();
    expect(screen.getByText("Sung earlier tonight")).toBeInTheDocument();
    expect(String(calls("GET /api/party/guest/search")[0][0])).toContain("q=copper");
    await expectNoAxeViolations(container);

    const keyDown = screen.getByRole("button", { name: "Lower the key" });
    await userEvent.click(keyDown);
    await userEvent.click(keyDown);
    expect(screen.getByRole("group", { name: "Your key" })).toHaveTextContent("−2");
    await userEvent.click(screen.getByRole("button", { name: "Request Copper Skyline" }));
    expect(await screen.findByText("Sent to the host for approval.")).toBeInTheDocument();
    expect(await screen.findByText("Key −2")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Requested" })).toBeDisabled();
    const [, init] = calls("POST /api/party/guest/request")[0];
    expect(JSON.parse(String(init?.body))).toEqual({ mediaID: "m1", pitchSemitones: -2 });
  });

  it("disables Request at the waiting limit and says why", async () => {
    storeToken();
    routes["GET /api/party/guest"] = () => json(200, guestState({ maxWaiting: 2 }));
    routes["GET /api/party/guest/search"] = () =>
      json(200, {
        items: [
          {
            mediaID: "m4",
            title: "Harbour Lights",
            subtitle: "",
            durationSeconds: 180,
            artworkURL: "",
            lastPlayedAt: "",
          },
        ],
      });
    renderPage();
    await screen.findByText("Number 2 in line");
    await userEvent.type(screen.getByRole("searchbox", { name: "Find a song" }), "harbour");
    await userEvent.click(screen.getByRole("button", { name: "Search" }));
    const request = await screen.findByRole("button", { name: "Request Harbour Lights" });
    expect(request).toBeDisabled();
    expect(request).toHaveAccessibleDescription(/You have 2 songs in line, the most allowed/);
  });

  it("marks results already in my songs as Requested", async () => {
    storeToken();
    routes["GET /api/party/guest/search"] = () =>
      json(200, {
        items: [searchItem("m9", "Glasshouse Weather"), searchItem("m3", "Tin Roof Rain")],
      });
    renderPage();
    await screen.findByText("Number 2 in line");
    await search("glass");
    expect(await screen.findByRole("button", { name: "Requested" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Request Tin Roof Rain" })).toBeEnabled();
  });

  it("explains a waiting-limit refusal from the server", async () => {
    storeToken();
    routes["GET /api/party/guest/search"] = () =>
      json(200, { items: [searchItem("m3", "Tin Roof Rain")] });
    routes["POST /api/party/guest/request"] = () => json(409, { error: "waiting-limit" });
    renderPage();
    await screen.findByText("Number 2 in line");
    await search("tin");
    await userEvent.click(await screen.findByRole("button", { name: "Request Tin Roof Rain" }));
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "You have 3 songs in line, the most allowed.",
    );
  });

  it("holds Request until Retry-After has passed after a 429", async () => {
    storeToken();
    routes["GET /api/party/guest/search"] = () =>
      json(200, { items: [searchItem("m3", "Tin Roof Rain")] });
    routes["POST /api/party/guest/request"] = () =>
      new Response(JSON.stringify({ error: "rate-limited" }), {
        status: 429,
        headers: { "Content-Type": "application/json", "Retry-After": "1" },
      });
    renderPage();
    await screen.findByText("Number 2 in line");
    await search("tin");
    const request = await screen.findByRole("button", { name: "Request Tin Roof Rain" });
    await userEvent.click(request);
    expect(await screen.findByRole("alert")).toHaveTextContent("One moment");
    expect(request).toBeDisabled();
    await waitFor(() => expect(request).toBeEnabled(), { timeout: 2500 });
  });
});

function searchItem(mediaID: string, title: string) {
  return { mediaID, title, subtitle: "", durationSeconds: 200, artworkURL: "", lastPlayedAt: "" };
}

async function search(q: string) {
  await userEvent.type(screen.getByRole("searchbox", { name: "Find a song" }), q);
  await userEvent.click(screen.getByRole("button", { name: "Search" }));
}
