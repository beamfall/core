import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { BrowserRouter } from "react-router";
import "./styles/global.css";
import { ShellErrorBoundary } from "@/components/chrome/ErrorBoundary";
import { I18nProvider } from "@/lib/i18n";
import { registerServiceWorker } from "@/lib/pwa";
import { RegisterProvider } from "@/lib/register";
import { SessionProvider } from "@/lib/session";
import { App, preloadInitialRoute } from "./App";

const container = document.getElementById("root");
if (!container) throw new Error("missing #root");

// BASE_URL is "/web/" (the cutover mount; vite base) — the router basename
// tracks it so client routing works under whichever mount point it is built for.
const basename = import.meta.env.BASE_URL.replace(/\/$/, "");

// Wait briefly for the matched area chunk so a fast load renders it on the
// first pass; on a slow network the route fallback shows after INITIAL_ROUTE_WAIT_MS.
const INITIAL_ROUTE_WAIT_MS = 150;

// I18nProvider sits outside the root error boundary so the boundary's own
// panel is localized; the boundary wraps the router and every provider under
// it, so a fault anywhere in the tree leaves a Reload control, never a blank
// root. Per-area boundaries live in App.tsx.
function render() {
  createRoot(container as HTMLElement).render(
    <StrictMode>
      <I18nProvider>
        <ShellErrorBoundary>
          <BrowserRouter basename={basename}>
            <RegisterProvider>
              <SessionProvider>
                <App />
              </SessionProvider>
            </RegisterProvider>
          </BrowserRouter>
        </ShellErrorBoundary>
      </I18nProvider>
    </StrictMode>,
  );
}

let rendered = false;
const renderOnce = () => {
  if (rendered) return;
  rendered = true;
  render();
};
const pathname = window.location.pathname.slice(basename.length) || "/";
void Promise.race([
  preloadInitialRoute(pathname),
  new Promise((resolve) => setTimeout(resolve, INITIAL_ROUTE_WAIT_MS)),
]).then(renderOnce);

// Install the shell-only service worker (ADR-0007 §1). No-op in dev.
registerServiceWorker();
