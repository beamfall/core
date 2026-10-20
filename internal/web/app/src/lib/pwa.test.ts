import { describe, expect, it, vi } from "vitest";
import { reloadOnceOnControllerChange } from "./pwa";

type Host = {
  controller: ServiceWorker | null;
  addEventListener: (type: string, listener: () => void) => void;
  fire: () => void;
};

function host(controller: ServiceWorker | null): Host {
  const listeners: (() => void)[] = [];
  return {
    controller,
    addEventListener: (_type, listener) => {
      listeners.push(listener);
    },
    fire: () => {
      for (const listener of listeners) listener();
    },
  };
}

describe("service-worker upgrade handoff", () => {
  it("reloads exactly once when a live controller is replaced", () => {
    const reload = vi.fn();
    const container = host({} as ServiceWorker);

    reloadOnceOnControllerChange(container, reload);
    container.fire();
    container.fire();

    expect(reload).toHaveBeenCalledTimes(1);
  });

  it("never reloads on a first install with no previous controller", () => {
    const reload = vi.fn();
    const container = host(null);

    reloadOnceOnControllerChange(container, reload);
    container.fire();

    expect(reload).not.toHaveBeenCalled();
  });
});
