import { AlertCircle } from "lucide-react";
import type { ReactNode, RefObject } from "react";
import beamfallLogo from "@/assets/beamfall-logo.png";
import { CinemaBackdrop } from "@/components/beamfall/CinemaBackdrop";
import { useT } from "@/lib/i18n";

// The house form look (CORE-1114 setup wizard), shared by the sign-in pages.
export const AUTH_FIELD = "h-11 rounded-[10px] border-white/10 bg-white/[0.04] text-[15px]";
export const AUTH_FIELD_LABEL = "mb-2 block text-[13px] font-medium text-foreground/80";

// Full-bleed cinema page with the brand bar and one card. The card sits at a
// fixed offset from the top so it never shifts as its content grows.
export function AuthShell({ children }: { children: ReactNode }) {
  const t = useT();
  return (
    <main className="theme-cinema relative isolate flex min-h-screen flex-col bg-background text-foreground">
      <CinemaBackdrop />
      <header className="flex items-center gap-2.5 px-5 py-4 sm:px-8 sm:py-6">
        <img src={beamfallLogo} alt="" width={28} height={28} className="shrink-0" />
        <span className="text-base font-semibold tracking-tight">{t("brand")}</span>
      </header>
      <div className="flex flex-1 justify-center px-4 pb-12 pt-2 sm:px-8 sm:pt-[8vh]">
        <div className="animate-in fade-in w-full max-w-[440px] self-start rounded-[14px] border border-white/10 bg-card p-6 shadow-[0_24px_60px_-28px_rgba(0,0,0,0.7)] duration-200 sm:p-8">
          {children}
        </div>
      </div>
    </main>
  );
}

export function AuthHeading({ title, lead }: { title: string; lead: string }) {
  return (
    <div className="mb-6">
      <h1 className="m-0 text-2xl font-semibold leading-tight tracking-tight">{title}</h1>
      <p className="mb-0 mt-2 text-[15px] leading-relaxed text-muted-foreground">{lead}</p>
    </div>
  );
}

// The status line and the focusable error alert under each form.
export function FormFeedback({
  status,
  error,
  errorID,
  errorRef,
}: {
  status?: string | null;
  error: string | null;
  errorID: string;
  errorRef: RefObject<HTMLParagraphElement | null>;
}) {
  return (
    <>
      {status && (
        <p role="status" className="m-0 text-center text-sm text-muted-foreground">
          {status}
        </p>
      )}
      {error && (
        <p
          ref={errorRef}
          id={errorID}
          role="alert"
          tabIndex={-1}
          className="m-0 flex items-start gap-2 rounded-[10px] border border-(--state-danger)/30 bg-(--state-danger)/10 px-3.5 py-2.5 text-sm text-(--state-danger) outline-none"
        >
          <AlertCircle aria-hidden className="mt-0.5 size-4 shrink-0" />
          {error}
        </p>
      )}
    </>
  );
}
