import { afterEach, describe, expect, it, vi } from "vitest";
import {
  ApiConfigurationError,
  ApiDeadlineError,
  ApiError,
  ApiMalformedResponseError,
  ApiResponseTooLargeError,
  fetchSetupStatus,
  getJSON,
  graphql,
  isAuthRejection,
  postJSON,
  postKeepalive,
  profileHeaders,
  setExpectedProfile,
  setIdentityEpochSignal,
  setProfileMismatchHandler,
} from "./api";

afterEach(() => {
  setIdentityEpochSignal();
  setExpectedProfile();
  setProfileMismatchHandler();
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

describe("authenticated JSON request bounds", () => {
  it("rejects a hung request at the default deadline with a typed error", async () => {
    vi.useFakeTimers();
    let transportSignal: AbortSignal | undefined;
    let liveTransports = 0;
    vi.stubGlobal(
      "fetch",
      vi.fn((_path: string, init?: RequestInit) => {
        transportSignal = init?.signal as AbortSignal;
        liveTransports += 1;
        return new Promise<Response>((_resolve, reject) => {
          transportSignal?.addEventListener(
            "abort",
            () => {
              liveTransports -= 1;
              reject(transportSignal?.reason);
            },
            { once: true },
          );
        });
      }),
    );

    const pending = getJSON("/api/hung");
    const rejected = expect(pending).rejects.toMatchObject({
      name: "ApiDeadlineError",
      deadlineMs: 30_000,
    });
    await vi.advanceTimersByTimeAsync(30_000);

    await rejected;
    await expect(pending).rejects.toBeInstanceOf(ApiDeadlineError);
    expect(transportSignal?.aborted).toBe(true);
    expect(transportSignal?.reason).toBeInstanceOf(ApiDeadlineError);
    expect(liveTransports).toBe(0);
  });

  it("rejects a streamed response as soon as it exceeds the configured ceiling", async () => {
    const encoder = new TextEncoder();
    let cancelled = false;
    const body = new ReadableStream<Uint8Array>({
      start(controller) {
        controller.enqueue(encoder.encode('{"value":"'));
        controller.enqueue(encoder.encode('too large"}'));
      },
      cancel() {
        cancelled = true;
      },
    });
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(body)));

    const pending = getJSON("/api/oversize", { maxResponseBytes: 12 });

    await expect(pending).rejects.toMatchObject({
      name: "ApiResponseTooLargeError",
      maxResponseBytes: 12,
    });
    await expect(pending).rejects.toBeInstanceOf(ApiResponseTooLargeError);
    expect(cancelled).toBe(true);
  });

  it("applies the 8 MiB default ceiling from Content-Length before reading", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(
        new Response("{}", {
          headers: { "Content-Length": String(8 * 1024 * 1024 + 1) },
        }),
      ),
    );

    await expect(getJSON("/api/oversize")).rejects.toMatchObject({
      name: "ApiResponseTooLargeError",
      maxResponseBytes: 8 * 1024 * 1024,
    });
  });

  it.each([
    [{ maxResponseBytes: 32 * 1024 * 1024 + 1 }, "maxResponseBytes"],
    [{ deadlineMs: 120_001 }, "deadlineMs"],
  ])("rejects configuration above a hard maximum", async (options, field) => {
    const fetchMock = vi.fn();
    vi.stubGlobal("fetch", fetchMock);

    const pending = getJSON("/api/config", options);

    await expect(pending).rejects.toMatchObject({
      name: "ApiConfigurationError",
      field,
    });
    await expect(pending).rejects.toBeInstanceOf(ApiConfigurationError);
    expect(fetchMock).not.toHaveBeenCalled();
  });
});

describe("identity epoch API boundary", () => {
  it("aborts concurrent Console requests without caller-supplied signals", async () => {
    const identity = new AbortController();
    const signals: AbortSignal[] = [];
    vi.stubGlobal(
      "fetch",
      vi.fn((_path: string, init?: RequestInit) => {
        const signal = init?.signal as AbortSignal;
        signals.push(signal);
        return new Promise<Response>((_resolve, reject) => {
          signal.addEventListener(
            "abort",
            () => reject(signal.reason ?? new DOMException("Aborted", "AbortError")),
            { once: true },
          );
        });
      }),
    );
    setIdentityEpochSignal(identity.signal);

    const pending = [
      getJSON("/api/console-state"),
      postJSON("/api/console-action", { enabled: true }),
      graphql("query ConsoleState { consoleConfig { revision } }"),
    ];

    expect(signals).toHaveLength(3);
    expect(new Set(signals).size).toBe(3);
    expect(signals.every((signal) => !signal.aborted)).toBe(true);
    identity.abort();

    const results = await Promise.allSettled(pending);
    expect(results.every((result) => result.status === "rejected")).toBe(true);
    expect(signals.every((signal) => signal.aborted)).toBe(true);
  });

  it.each([
    ["GET", (signal: AbortSignal) => getJSON("/identity-scoped", signal)],
    ["POST", (signal: AbortSignal) => postJSON("/identity-scoped", { value: 1 }, signal)],
    [
      "GraphQL",
      (signal: AbortSignal) =>
        graphql("query IdentityScoped { currentSession { profileID } }", {}, signal),
    ],
  ])("aborts production %s transport when the identity departs", async (kind, requestCall) => {
    const identity = new AbortController();
    const request = new AbortController();
    let fired: AbortSignal | undefined;
    let capturedPath = "";
    let capturedInit: RequestInit | undefined;
    vi.stubGlobal(
      "fetch",
      vi.fn((path: string, init?: RequestInit) => {
        capturedPath = path;
        capturedInit = init;
        fired = init?.signal as AbortSignal;
        return new Promise<Response>((_resolve, reject) => {
          fired?.addEventListener("abort", () => reject(fired?.reason), { once: true });
        });
      }),
    );
    setIdentityEpochSignal(identity.signal);

    const pending = requestCall(request.signal);
    identity.abort();

    await expect(pending).rejects.toBeDefined();
    expect(fired?.aborted).toBe(true);
    expect(request.signal.aborted).toBe(false);
    if (kind === "GET") {
      expect(capturedPath).toBe("/identity-scoped");
      expect(capturedInit?.method).toBeUndefined();
    } else {
      expect(capturedInit?.method).toBe("POST");
      expect(capturedInit?.headers).toMatchObject({ "Content-Type": "application/json" });
    }
    if (kind === "GraphQL") {
      expect(capturedPath).toBe("/api/graphql");
      expect(JSON.parse(String(capturedInit?.body))).toMatchObject({
        query: "query IdentityScoped { currentSession { profileID } }",
      });
    }
  });
});

describe("response body and in-band authorization typing", () => {
  it("rejects an empty 2xx body with a typed ApiError instead of a bare SyntaxError", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(() => Promise.resolve(new Response("", { status: 200 }))),
    );

    const pending = getJSON("/api/empty");

    await expect(pending).rejects.toBeInstanceOf(ApiMalformedResponseError);
    await expect(pending).rejects.toBeInstanceOf(ApiError);
    await expect(pending).rejects.toMatchObject({ status: 200, path: "/api/empty" });
  });

  it("rejects a non-JSON 2xx body with the same typed error", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(() => Promise.resolve(new Response("<html>proxy page</html>", { status: 200 }))),
    );

    await expect(postJSON("/api/html", {})).rejects.toBeInstanceOf(ApiMalformedResponseError);
  });

  it.each([
    ["admin session required", 403],
    ["manager role required", 403],
    ["invalid session", 401],
  ])("maps the GraphQL %s error onto the auth-rejection path", async (message, status) => {
    vi.stubGlobal(
      "fetch",
      vi.fn(() =>
        Promise.resolve(
          new Response(JSON.stringify({ errors: [{ message }] }), {
            status: 200,
            headers: { "Content-Type": "application/json" },
          }),
        ),
      ),
    );

    const pending = graphql("query AdminSession { adminSession { active } }");

    await expect(pending).rejects.toMatchObject({ name: "ApiError", status, message });
    await expect(pending).rejects.toSatisfy(isAuthRejection);
  });

  it("keeps an ordinary GraphQL error off the auth-rejection path", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(() =>
        Promise.resolve(
          new Response(JSON.stringify({ errors: [{ message: "library not found" }] }), {
            status: 200,
            headers: { "Content-Type": "application/json" },
          }),
        ),
      ),
    );

    const pending = graphql('query Library { library(id: "x") { id } }');

    await expect(pending).rejects.toMatchObject({ name: "ApiError", status: 200 });
    await expect(pending).rejects.not.toSatisfy(isAuthRejection);
  });
});

describe("setup probe", () => {
  it("leaves the sign-in security mode unknown when the probe fails", async () => {
    vi.stubGlobal("fetch", vi.fn().mockRejectedValue(new TypeError("network down")));
    await expect(fetchSetupStatus()).resolves.toEqual({
      required: false,
      tokenRequired: true,
      securityMode: null,
    });
  });

  it("reads home for a server that predates the mode field", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(new Response(JSON.stringify({ required: false }))),
    );
    await expect(fetchSetupStatus()).resolves.toMatchObject({ securityMode: "home" });
  });
});

describe("keepalive POST", () => {
  it("sends a same-origin CSRF-marked request that outlives the page", () => {
    const fetchMock = vi.fn(() => Promise.resolve(new Response("{}")));
    vi.stubGlobal("fetch", fetchMock);
    postKeepalive("/api/graphql", { query: "q", variables: { partyID: "p1" } });
    expect(fetchMock).toHaveBeenCalledWith("/api/graphql", {
      method: "POST",
      credentials: "same-origin",
      keepalive: true,
      headers: { "Content-Type": "application/json", "X-Beamfall-Csrf": "1" },
      body: JSON.stringify({ query: "q", variables: { partyID: "p1" } }),
    });
  });

  it("never throws, even when fetch rejects or is missing", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(() => Promise.reject(new Error("offline"))),
    );
    expect(() => postKeepalive("/api/graphql", {})).not.toThrow();
    vi.stubGlobal("fetch", undefined);
    expect(() => postKeepalive("/api/graphql", {})).not.toThrow();
  });
});

describe("request Profile precondition (CORE-2923)", () => {
  const graphMismatch = {
    errors: [
      {
        message: "the session is bound to another profile",
        extensions: { code: "CONFLICT", reason: "PROFILE_MISMATCH" },
      },
    ],
  };

  function headersOf(fetchMock: ReturnType<typeof vi.fn>, call = 0): Record<string, string> {
    return (fetchMock.mock.calls[call]?.[1] as RequestInit | undefined)?.headers as Record<
      string,
      string
    >;
  }

  it("names the expected Profile on every request, and none before one is known", async () => {
    const fetchMock = vi.fn(async () => Response.json({ data: { ok: true } }));
    vi.stubGlobal("fetch", fetchMock);

    await getJSON("/api/manifest");
    expect(headersOf(fetchMock)?.["X-Beamfall-Profile"]).toBeUndefined();
    expect(profileHeaders()).toEqual({});

    setExpectedProfile("profile-a");
    await getJSON("/api/manifest");
    await postJSON("/api/media/subtitle-pin", {});
    await graphql("mutation { setClientPref }");
    postKeepalive("/api/sessions/leave", {});
    for (const call of [1, 2, 3, 4]) {
      expect(headersOf(fetchMock, call)?.["X-Beamfall-Profile"]).toBe("profile-a");
    }
    expect(headersOf(fetchMock, 2)?.["X-Beamfall-Csrf"]).toBe("1");
    expect(profileHeaders()).toEqual({ "X-Beamfall-Profile": "profile-a" });
  });

  it("throws a typed 409 and asks the spine to reload when the shown Profile is refused", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => Response.json(graphMismatch, { status: 409 })),
    );
    const onMismatch = vi.fn();
    setProfileMismatchHandler(onMismatch);
    setExpectedProfile("profile-a");

    const refused = await graphql("mutation { setClientPref }").catch((error: unknown) => error);
    expect(refused).toBeInstanceOf(ApiError);
    expect(refused).toMatchObject({ status: 409 });
    expect(isAuthRejection(refused)).toBe(false);
    expect(onMismatch).toHaveBeenCalledTimes(1);
  });

  it("reads the REST refusal shape too", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () =>
        Response.json(
          { error: "profile mismatch", code: "CONFLICT", reason: "PROFILE_MISMATCH" },
          { status: 409 },
        ),
      ),
    );
    const onMismatch = vi.fn();
    setProfileMismatchHandler(onMismatch);
    setExpectedProfile("profile-a");

    await expect(postJSON("/api/media/subtitle-pin", {})).rejects.toMatchObject({ status: 409 });
    expect(onMismatch).toHaveBeenCalledTimes(1);
  });

  it("ignores a refusal for a Profile the tab no longer shows", async () => {
    let answer!: (response: Response) => void;
    vi.stubGlobal(
      "fetch",
      vi.fn(() => new Promise<Response>((resolve) => (answer = resolve))),
    );
    const onMismatch = vi.fn();
    setProfileMismatchHandler(onMismatch);
    setExpectedProfile("profile-a");

    const pending = postJSON("/api/media/subtitle-pin", {}).catch(() => undefined);
    // The tab moved on to another identity before the old request's refusal arrived.
    setExpectedProfile("profile-b");
    answer(Response.json(graphMismatch, { status: 409 }));
    await pending;
    expect(onMismatch).not.toHaveBeenCalled();
  });

  it("ignores other conflicts", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => Response.json({ error: "conflict", code: "CONFLICT" }, { status: 409 })),
    );
    const onMismatch = vi.fn();
    setProfileMismatchHandler(onMismatch);
    setExpectedProfile("profile-a");

    await expect(postJSON("/api/x", {})).rejects.toMatchObject({ status: 409 });
    expect(onMismatch).not.toHaveBeenCalled();
  });
});
