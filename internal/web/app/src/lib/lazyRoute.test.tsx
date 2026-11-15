import "@testing-library/jest-dom/vitest";
import { act, cleanup, fireEvent, render, screen } from "@testing-library/react";
import { Suspense, useEffect, useState } from "react";
import { afterEach, describe, expect, it } from "vitest";
import { lazyRoute } from "./lazyRoute";

function deferred() {
  let resolve!: () => void;
  const promise = new Promise<{ default: () => React.ReactElement }>((res) => {
    resolve = () => res({ default: () => <p>loaded</p> });
  });
  return { promise, resolve };
}

describe("lazyRoute", () => {
  afterEach(cleanup);

  it("renders the fallback until the chunk resolves", async () => {
    const d = deferred();
    const Route = lazyRoute(() => d.promise);
    render(
      <Suspense fallback={<p>fallback</p>}>
        <Route />
      </Suspense>,
    );
    expect(screen.getByText("fallback")).toBeInTheDocument();
    await act(async () => {
      d.resolve();
      await d.promise;
    });
    expect(await screen.findByText("loaded")).toBeInTheDocument();
  });

  it("renders a preloaded route on the first pass without the fallback", async () => {
    const d = deferred();
    const Route = lazyRoute(() => d.promise);
    d.resolve();
    await Route.preload();
    render(
      <Suspense fallback={<p>fallback</p>}>
        <Route />
      </Suspense>,
    );
    expect(screen.getByText("loaded")).toBeInTheDocument();
    expect(screen.queryByText("fallback")).not.toBeInTheDocument();
  });

  it("keeps one mount and its local state across parent re-renders after a cold load", async () => {
    let mounts = 0;
    function Page() {
      const [count, setCount] = useState(0);
      useEffect(() => {
        mounts++;
      }, []);
      return (
        <button type="button" onClick={() => setCount((c) => c + 1)}>
          count {count}
        </button>
      );
    }
    let resolve!: () => void;
    const chunk = new Promise<{ default: () => React.ReactElement }>((res) => {
      resolve = () => res({ default: Page });
    });
    const Route = lazyRoute(() => chunk);
    let rerenderParent!: () => void;
    function Parent() {
      const [tick, setTick] = useState(0);
      rerenderParent = () => setTick((t) => t + 1);
      return (
        <div data-tick={tick}>
          <Suspense fallback={<p>fallback</p>}>
            <Route />
          </Suspense>
        </div>
      );
    }
    render(<Parent />);
    await act(async () => {
      resolve();
      await chunk;
    });
    fireEvent.click(await screen.findByRole("button", { name: "count 0" }));
    await act(async () => rerenderParent());
    await act(async () => rerenderParent());
    expect(screen.getByRole("button", { name: "count 1" })).toBeInTheDocument();
    expect(mounts).toBe(1);
  });

  it("retries after a failed load", async () => {
    let calls = 0;
    const Route = lazyRoute(() => {
      calls++;
      return calls === 1
        ? Promise.reject(new Error("chunk"))
        : Promise.resolve({ default: () => <p>ok</p> });
    });
    await expect(Route.preload()).rejects.toThrow("chunk");
    await Route.preload();
    expect(calls).toBe(2);
  });
});
