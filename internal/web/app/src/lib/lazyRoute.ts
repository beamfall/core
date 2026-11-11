import { type ComponentType, createElement, lazy } from "react";

type AnyProps = Record<string, unknown>;

export type PreloadableLazy = ComponentType<AnyProps> & { preload: () => Promise<unknown> };

// React.lazy suspends on first read even when the import has already settled,
// and React 19 then holds the revealed content back for a throttle window, so
// effects of the route (data requests) start ~300 ms late. This wrapper keeps
// the resolved component and renders it directly once preload() (or the first
// suspended render) has settled; before that it behaves exactly like lazy().
export function lazyRoute(load: () => Promise<{ default: ComponentType<never> }>): PreloadableLazy {
  let resolved: ComponentType<AnyProps> | null = null;
  let pending: Promise<{ default: ComponentType<never> }> | null = null;
  const preload = () => {
    pending ??= load().then(
      (m) => {
        resolved = m.default as ComponentType<AnyProps>;
        return m;
      },
      (err: unknown) => {
        pending = null; // a failed chunk may be retried
        throw err;
      },
    );
    return pending;
  };
  const Lazy = lazy(preload as () => Promise<{ default: ComponentType<AnyProps> }>);
  const Route = ((props: AnyProps) =>
    resolved ? createElement(resolved, props) : createElement(Lazy, props)) as PreloadableLazy;
  Route.preload = preload;
  return Route;
}
