import { runInNewContext } from "node:vm";
import { describe, expect, it, vi } from "vitest";
import { renderShellServiceWorker } from "./sw-plugin";

type ExtendableEvent = {
  waitUntil(promise: Promise<unknown>): void;
};

type FetchEvent = {
  request: Request;
  respondWith(promise: Promise<unknown>): void;
};

type WorkerHandlers = {
  activate?: (event: ExtendableEvent) => void;
  fetch?: (event: FetchEvent) => void;
  install?: (event: ExtendableEvent) => void;
};

function cacheName(source: string): string {
  const match = source.match(/const CACHE = CACHE_PREFIX \+ "([^"]+)"/);
  if (!match?.[1]) throw new Error("generated worker has no cache version");
  return `beamfall-shell-${match[1]}`;
}

function workerSource(indexSource: string): string {
  return renderShellServiceWorker("/web/", [
    { fileName: "assets/app.js", source: "app bytes" },
    { fileName: "index.html", source: indexSource },
  ]);
}

describe("Beamfall shell service worker", () => {
  it("changes cache names when bytes change behind the same filenames", () => {
    expect(cacheName(workerSource("first bytes"))).not.toBe(
      cacheName(workerSource("changed bytes")),
    );
  });

  it("deletes only stale owned caches during activation", async () => {
    const source = workerSource("index bytes");
    const current = cacheName(source);
    const stale = "beamfall-shell-stale";
    const unrelated = "another-app-cache";
    const similarlyNamed = "beamfall-shells-unrelated";
    const deleted: string[] = [];
    const handlers: WorkerHandlers = {};
    let activation: Promise<unknown> | undefined;

    runInNewContext(source, {
      URL,
      caches: {
        delete: async (key: string) => {
          deleted.push(key);
          return true;
        },
        keys: async () => [current, stale, unrelated, similarlyNamed],
      },
      fetch: vi.fn(),
      self: {
        addEventListener: (type: keyof WorkerHandlers, handler: WorkerHandlers[typeof type]) => {
          handlers[type] = handler;
        },
        clients: { claim: vi.fn().mockResolvedValue(undefined) },
        location: { origin: "https://beamfall.test" },
        skipWaiting: vi.fn(),
      },
    });

    handlers.activate?.({
      waitUntil(promise) {
        activation = promise;
      },
    });
    await activation;

    expect(deleted).toEqual([stale]);
  });

  it("serves a navigation from the network ahead of a cached shell", async () => {
    const source = workerSource("index bytes");
    const handlers: WorkerHandlers = {};
    const staleShell = new Response("previous build shell");
    const freshShell = new Response("current build shell");
    const match = vi.fn().mockResolvedValue(staleShell);
    const networkFetch = vi.fn().mockResolvedValue(freshShell);
    let response: Promise<unknown> | undefined;

    runInNewContext(source, {
      Response,
      URL,
      caches: { match },
      fetch: networkFetch,
      self: {
        addEventListener: (type: keyof WorkerHandlers, handler: WorkerHandlers[typeof type]) => {
          handlers[type] = handler;
        },
        clients: { claim: vi.fn() },
        location: { origin: "https://beamfall.test" },
        skipWaiting: vi.fn(),
      },
    });

    const request = new Request("https://beamfall.test/web/manage/libraries");
    Object.defineProperty(request, "mode", { value: "navigate" });
    handlers.fetch?.({
      request,
      respondWith(promise) {
        response = promise;
      },
    });

    await expect(response).resolves.toBe(freshShell);
    expect(networkFetch).toHaveBeenCalledWith(request);
    expect(match).not.toHaveBeenCalled();
  });

  it("precaches current assets and serves the app shell offline", async () => {
    const source = workerSource("index bytes");
    const handlers: WorkerHandlers = {};
    const cachedShell = new Response("offline shell");
    const addAll = vi.fn().mockResolvedValue(undefined);
    const match = vi.fn().mockResolvedValue(cachedShell);
    const networkFetch = vi.fn().mockRejectedValue(new Error("offline"));
    let installation: Promise<unknown> | undefined;
    let response: Promise<unknown> | undefined;

    runInNewContext(source, {
      Response,
      URL,
      caches: {
        match,
        open: vi.fn().mockResolvedValue({ addAll }),
      },
      fetch: networkFetch,
      self: {
        addEventListener: (type: keyof WorkerHandlers, handler: WorkerHandlers[typeof type]) => {
          handlers[type] = handler;
        },
        clients: { claim: vi.fn() },
        location: { origin: "https://beamfall.test" },
        skipWaiting: vi.fn().mockResolvedValue(undefined),
      },
    });

    handlers.install?.({
      waitUntil(promise) {
        installation = promise;
      },
    });
    await installation;
    expect(addAll).toHaveBeenCalledWith(["/web/assets/app.js", "/web/index.html"]);

    const request = new Request("https://beamfall.test/web/deep/link");
    Object.defineProperty(request, "mode", { value: "navigate" });
    handlers.fetch?.({
      request,
      respondWith(promise) {
        response = promise;
      },
    });

    await expect(response).resolves.toBe(cachedShell);
    expect(networkFetch).toHaveBeenCalledWith(request);
    expect(match).toHaveBeenCalledWith("/web/index.html");
  });

  it("stores fetched lazy /assets/ chunks and skips other responses", async () => {
    const source = workerSource("index bytes");
    const handlers: WorkerHandlers = {};
    const put = vi.fn().mockResolvedValue(undefined);
    const open = vi.fn().mockResolvedValue({ put });
    const basic = (status: number, type = "basic") => {
      const res = new Response("chunk", { status });
      Object.defineProperty(res, "type", { value: type });
      return res;
    };
    const networkFetch = vi.fn();

    runInNewContext(source, {
      Response,
      URL,
      caches: { match: vi.fn().mockResolvedValue(undefined), open },
      fetch: networkFetch,
      self: {
        addEventListener: (type: keyof WorkerHandlers, handler: WorkerHandlers[typeof type]) => {
          handlers[type] = handler;
        },
        clients: { claim: vi.fn() },
        location: { origin: "https://beamfall.test" },
        skipWaiting: vi.fn(),
      },
    });

    const run = async (path: string, res: Response) => {
      networkFetch.mockResolvedValueOnce(res);
      const request = new Request(`https://beamfall.test${path}`);
      let response: Promise<unknown> | undefined;
      handlers.fetch?.({
        request,
        respondWith(promise) {
          response = promise;
        },
      });
      await expect(response).resolves.toBe(res);
      await new Promise((resolve) => setTimeout(resolve, 0));
      return request;
    };

    const request = await run("/web/assets/lazy-Ab3dEf9_.js", basic(200));
    expect(put).toHaveBeenCalledTimes(1);
    expect(put.mock.calls[0]?.[0]).toBe(request);

    await run("/web/assets/missing-Ab3dEf9_.js", basic(404));
    await run("/web/assets/redirected-Ab3dEf9_.js", basic(200, "opaque"));
    await run("/web/other.json", basic(200));
    expect(put).toHaveBeenCalledTimes(1);
  });
});
