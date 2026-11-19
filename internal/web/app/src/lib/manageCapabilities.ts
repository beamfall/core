import { useEffect, useState } from "react";
import { fetchCapabilityStates } from "@/lib/graphql";
import type { CapabilityStateInfo } from "@/lib/graphql/types";

const WORKFLOW_PATHS = new Map([
  ["/manage/convert", "conversion"],
  ["/manage/rename", "rename"],
]);
const listeners = new Set<(states: CapabilityStateInfo[]) => void>();

export function manageWorkflow(path: string): string | undefined {
  return WORKFLOW_PATHS.get(path);
}

export function publishManageCapabilities(states: CapabilityStateInfo[]) {
  for (const update of listeners) update(states);
}

export function useManageCapabilities(refreshKey = 0) {
  const [capabilities, setCapabilities] = useState<CapabilityStateInfo[] | null>(null);
  useEffect(() => {
    let active: AbortController | undefined;
    function refresh() {
      active?.abort();
      const request = new AbortController();
      active = request;
      void fetchCapabilityStates(request.signal, { adminUnlock: false }).then(
        (states) => {
          if (!request.signal.aborted) setCapabilities(states);
        },
        () => {
          if (!request.signal.aborted) setCapabilities((previous) => previous ?? []);
        },
      );
    }
    function update(states: CapabilityStateInfo[]) {
      active?.abort();
      setCapabilities(states);
    }
    refresh();
    window.addEventListener("focus", refresh);
    listeners.add(update);
    return () => {
      window.removeEventListener("focus", refresh);
      listeners.delete(update);
      active?.abort();
    };
  }, [refreshKey]);

  // A missing capability read leaves page access to the server's permission checks.
  const enabled = (id: string | undefined) =>
    !capabilities?.some((state) => state.id === id && !state.enabled);
  const visibleManagePage = (path: string) => {
    const id = manageWorkflow(`/manage/${path}`);
    return id === undefined || (capabilities !== null && enabled(id));
  };
  return { loading: capabilities === null, enabled, visibleManagePage };
}
