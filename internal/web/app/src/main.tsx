import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { BrowserRouter } from "react-router";
import "./styles/global.css";
import { ShellErrorBoundary } from "@/components/chrome/ErrorBoundary";
import { I18nProvider } from "@/lib/i18n";
import { registerServiceWorker } from "@/lib/pwa";
import { RegisterProvider } from "@/lib/register";
import { SessionProvider } from "@/lib/session";
import { App } from "./App";

const container = document.getElementById("root");
if (!container) throw new Error("missing #root");

// BASE_URL is "/web/" (the cutover mount; vite base) — the router basename
// tracks it so client routing works under whichever mount point it is built for.
const basename = import.meta.env.BASE_URL.replace(/\/$/, "");

// I18nProvider sits outside the root error boundary so the boundary's own
// panel is localized; the boundary wraps the router and every provider under
// it, so a fault anywhere in the tree leaves a Reload control, never a blank
// root. Per-area boundaries live in App.tsx.
createRoot(container).render(
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

// Install the shell-only service worker (ADR-0007 §1). No-op in dev.
registerServiceWorker();
