import { Component, type ReactNode } from "react";
import { Button } from "@/components/ui/button";
import { useT } from "@/lib/i18n";

// The shell's only error boundaries. A render throw or a rejected
// `lazy(() => import(...))` (the first load after a server upgrade, when the
// previous build's chunk hashes no longer exist) would otherwise unmount the
// React root to a blank page with no way back but a manual reload.
//
// Two scopes share one implementation: the ROOT boundary wraps the whole app
// (a fault anywhere leaves a register-neutral panel with a Reload control);
// each AREA boundary wraps one lazily-loaded area so a failed area chunk
// surfaces inside the shell instead of blanking the root. React caches a
// rejected lazy import permanently, so recovery is a real page reload — the
// worker serves navigations network-first, so the reload fetches the current
// build. An area boundary also clears itself when the route changes
// (`resetKey`), so a failed area never pins the user to its panel.
//
// Nothing about the error is logged or rendered: the thrown value can carry
// request paths or server messages, and the panel is deliberately generic.

interface ErrorBoundaryProps {
  children: ReactNode;
  fallback: ReactNode;
  /** When this changes while failed, the boundary retries its children. */
  resetKey?: string;
}

interface ErrorBoundaryState {
  failed: boolean;
}

class ErrorBoundary extends Component<ErrorBoundaryProps, ErrorBoundaryState> {
  state: ErrorBoundaryState = { failed: false };

  static getDerivedStateFromError(): ErrorBoundaryState {
    return { failed: true };
  }

  componentDidUpdate(previous: ErrorBoundaryProps): void {
    if (this.state.failed && previous.resetKey !== this.props.resetKey) {
      this.setState({ failed: false });
    }
  }

  render(): ReactNode {
    return this.state.failed ? this.props.fallback : this.props.children;
  }
}

function reloadPage(): void {
  window.location.reload();
}

function ErrorPanel({ scope }: { scope: "root" | "area" }) {
  const t = useT();
  const layout = scope === "root" ? "min-h-screen" : "min-h-[50vh]";
  return (
    <div role="alert" className={`flex ${layout} items-center justify-center px-4`}>
      <div className="max-w-md space-y-4 text-center">
        <h1 className="text-xl font-semibold">{t("shellErrorTitle")}</h1>
        <p className="text-sm text-muted-foreground">{t("shellErrorLead")}</p>
        <Button type="button" onClick={reloadPage}>
          {t("reloadPage")}
        </Button>
      </div>
    </div>
  );
}

/** The top-level boundary around the router. */
export function ShellErrorBoundary({ children }: { children: ReactNode }) {
  return <ErrorBoundary fallback={<ErrorPanel scope="root" />}>{children}</ErrorBoundary>;
}

/** A per-area boundary; `resetKey` is the current route path. */
export function AreaErrorBoundary({
  children,
  resetKey,
}: {
  children: ReactNode;
  resetKey?: string;
}) {
  return (
    <ErrorBoundary fallback={<ErrorPanel scope="area" />} resetKey={resetKey}>
      {children}
    </ErrorBoundary>
  );
}
