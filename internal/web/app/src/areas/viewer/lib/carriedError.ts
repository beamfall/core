import { useSyncExternalStore } from "react";
import type { StringKey } from "@/strings";

// A Reveal or Profile switch is an identity epoch: SessionProvider clears the
// bound session before the mutation runs (ADR-0082), so ViewerArea unmounts the
// control that started it while the request is in flight. A failure that
// control reports must outlive that instance, so it is parked here and handed
// to whichever instance is mounted (the same one, or its remount once the old
// identity reloads). The consumer takes it with `carry(null)` once shown.
export function createCarriedError() {
  let value: StringKey | null = null;
  const listeners = new Set<() => void>();
  const subscribe = (listener: () => void) => {
    listeners.add(listener);
    return () => {
      listeners.delete(listener);
    };
  };
  const read = () => value;
  return {
    carry(next: StringKey | null) {
      value = next;
      for (const listener of listeners) listener();
    },
    usePending(): StringKey | null {
      return useSyncExternalStore(subscribe, read, read);
    },
  };
}
