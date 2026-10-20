import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const fetchMock = vi.fn();

function jsonResponse(body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status: 200,
    headers: { "Content-Type": "application/json" },
  });
}

async function loadCompat() {
  return import("./compat");
}

beforeEach(() => {
  vi.resetModules();
  fetchMock.mockReset();
  vi.stubGlobal("fetch", fetchMock);
});

afterEach(() => vi.unstubAllGlobals());

describe("compatibility boot handshake", () => {
  it("submits the compiled contract through the same-origin JSON boundary", async () => {
    fetchMock.mockResolvedValue(
      jsonResponse({
        decision: "compatible",
        decisionID: "decision-1",
        messageClass: "compat-compatible",
      }),
    );
    const compat = await loadCompat();

    await compat.performHandshake();

    expect(fetchMock).toHaveBeenCalledOnce();
    const [path, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(path).toBe("/api/compat/handshake");
    expect(init).toMatchObject({
      method: "POST",
      credentials: "same-origin",
      headers: {
        "Content-Type": "application/json",
        "X-Beamfall-Csrf": "1",
      },
      body: JSON.stringify({
        surface: "core-web-ui",
        appVersion: "embedded",
        buildID: "embedded-core-web-ui-v1",
        coreContractRevRange: [1, 1],
        renderingContractRevRange: [1, 1],
        renderingStamps: ["core-web-ui-v1"],
        fixedVocabularies: [],
        requestedCapabilities: [],
        developerAPIMajor: 0,
      }),
    });
    expect(init.signal).toBeInstanceOf(AbortSignal);
    expect(init.signal?.aborted).toBe(false);
  });

  it("coalesces concurrent and repeated boots for the same subject epoch", async () => {
    fetchMock.mockResolvedValue(
      jsonResponse({
        decision: "degraded",
        messageClass: "compat-degraded",
        disabledCapabilities: ["optional.preview"],
      }),
    );
    const compat = await loadCompat();

    const results = await Promise.all([
      compat.performHandshake(),
      compat.performHandshake(),
      compat.performHandshake(),
    ]);

    expect(fetchMock).toHaveBeenCalledTimes(1);
    expect(results.every((result) => result.decision === "degraded")).toBe(true);
  });

  it("allows one fresh handshake when the subject epoch changes", async () => {
    fetchMock
      .mockResolvedValueOnce(
        jsonResponse({
          decision: "compatible",
          messageClass: "compat-compatible",
        }),
      )
      .mockResolvedValueOnce(
        jsonResponse({
          decision: "degraded",
          messageClass: "compat-degraded",
        }),
      );
    const compat = await loadCompat();

    await compat.performHandshake();
    await compat.rebindCompatibility();
    await compat.performHandshake();

    expect(fetchMock).toHaveBeenCalledTimes(2);
  });

  it("rebinds after each server-side authentication change", async () => {
    fetchMock.mockImplementation(() =>
      jsonResponse({
        decision: "compatible",
        messageClass: "compat-compatible",
      }),
    );
    const compat = await loadCompat();

    await compat.rebindCompatibility();
    await compat.rebindCompatibility();

    expect(fetchMock).toHaveBeenCalledTimes(2);
  });

  it("ignores an obsolete terminal response after the subject epoch changes", async () => {
    let resolveObsolete: ((response: Response) => void) | undefined;
    fetchMock
      .mockImplementationOnce(
        () =>
          new Promise<Response>((resolve) => {
            resolveObsolete = resolve;
          }),
      )
      .mockResolvedValueOnce(
        jsonResponse({
          decision: "compatible",
          messageClass: "compat-compatible",
        }),
      );
    const compat = await loadCompat();

    const obsolete = compat.performHandshake();
    const rebound = compat.rebindCompatibility();
    await rebound;
    resolveObsolete?.(
      jsonResponse({
        decision: "client-too-old",
        messageClass: "compat-client-too-old",
      }),
    );

    expect((await obsolete).decision).toBe("compatible");
    expect(compat.isBlocked()).toBe(false);
    expect(fetchMock).toHaveBeenCalledTimes(2);
  });

  it("does not latch bootstrap or transport failures", async () => {
    fetchMock
      .mockResolvedValueOnce(
        jsonResponse({
          decision: "bootstrap",
          messageClass: "compat-bootstrap",
        }),
      )
      .mockRejectedValueOnce(new Error("offline"))
      .mockResolvedValueOnce(
        jsonResponse({
          decision: "compatible",
          messageClass: "compat-compatible",
        }),
      );
    const compat = await loadCompat();

    await compat.performHandshake();
    await expect(compat.performHandshake()).rejects.toThrow("offline");
    await compat.performHandshake();

    expect(fetchMock).toHaveBeenCalledTimes(3);
    expect(compat.isBlocked()).toBe(false);
  });

  it("recognizes compatibility refusal payloads without conflating ordinary 403s", async () => {
    fetchMock.mockResolvedValueOnce(
      new Response(
        JSON.stringify({
          decision: "client-too-old",
          messageClass: "compat-client-too-old",
        }),
        { status: 403, headers: { "Content-Type": "application/json" } },
      ),
    );
    const compat = await loadCompat();

    let refusal: unknown;
    try {
      await compat.performHandshake();
    } catch (error) {
      refusal = error;
    }

    expect(compat.isCompatibilityRefusal(refusal)).toBe(true);
    expect(
      compat.isCompatibilityRefusal({
        status: 403,
        payload: { messageClass: "compat-client-too-old" },
      }),
    ).toBe(false);
  });

  it.each([
    ["client-too-old", "compat-client-too-old"],
    ["server-too-old", "compat-server-too-old"],
    ["server-installation-inconsistent", "compat-server-installation-inconsistent"],
  ] as const)("permanently latches terminal decision %s", async (decision, messageClass) => {
    fetchMock.mockResolvedValue(jsonResponse({ decision, messageClass }));
    const compat = await loadCompat();

    const first = await compat.performHandshake();
    const later = await compat.rebindCompatibility().catch(() => compat.performHandshake());
    const api = await import("./api");

    expect(first.messageClass).toBe(messageClass);
    expect(later).toBe(first);
    expect(compat.isBlocked()).toBe(true);
    await expect(api.getJSON("/api/manifest")).rejects.toThrow(messageClass);
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });
});
