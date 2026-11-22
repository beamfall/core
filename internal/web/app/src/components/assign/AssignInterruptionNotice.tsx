import { createContext, type ReactNode, useContext, useState } from "react";
import { Button } from "@/components/ui/button";
import { useT } from "@/lib/i18n";

type Counts = { done: number; total: number };
const AssignInterruptionContext = createContext<((counts: Counts) => void) | null>(null);

export function useAssignInterruptionNotice() {
  return useContext(AssignInterruptionContext);
}

// The session spine unmounts routes during identity transitions. Only the
// counts survive so the person can see the result after their panel closes.
export function AssignInterruptionNotice({ children }: { children: ReactNode }) {
  const t = useT();
  const [counts, setCounts] = useState<Counts | null>(null);
  return (
    <AssignInterruptionContext.Provider value={setCounts}>
      {children}
      {counts && (
        <div className="fixed right-4 bottom-4 left-4 z-50 flex flex-wrap items-center gap-2 rounded-md border bg-card p-3 shadow-lg sm:left-auto">
          <p role="status" className="text-sm">
            {t("assignInterrupted", counts)}
          </p>
          <Button size="sm" variant="ghost" onClick={() => setCounts(null)}>
            {t("dismiss")}
          </Button>
        </div>
      )}
    </AssignInterruptionContext.Provider>
  );
}
