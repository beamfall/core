import { Slot } from "@radix-ui/react-slot";
import { clsx } from "clsx";
import type { ComponentProps, ReactNode } from "react";
import beamfallLogo from "@/assets/beamfall-logo.png";
import { CinemaBackdrop } from "@/components/beamfall/CinemaBackdrop";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { useT } from "@/lib/i18n";
import { cn } from "@/lib/utils";

// The surface primitives (CORE-1118): the house look the first-run setup
// wizard set (CORE-1114) — one card on the cinema backdrop, h-11 fields with a
// 10px radius, hairline rows — shared by every full-page surface outside the
// app chrome (setup, enroll, promote, the admin unlock gates). Pages compose
// these instead of carrying their own copies of the class strings.

// The brand wordmark asset the app header uses (chrome.css .bf-wordmark), so
// full-page surfaces carry the same mark instead of typed text.
export function Wordmark({ label }: { label: string }) {
  return (
    <span
      role="img"
      aria-label={label}
      className="h-3.5 w-[104px] shrink-0 bg-current [mask:url(/web/beamfall-wordmark.svg)_center/contain_no-repeat]"
    />
  );
}

const SHELL_WIDTH = { narrow: "max-w-[440px]", wide: "max-w-[560px]" } as const;

// Full-bleed cinema page: the static backdrop glow, the brand bar (with an
// optional suffix such as "Server setup") and one top-aligned column. The
// column sits at a fixed offset from the top so its card never shifts as the
// content grows.
export function PageShell({
  suffix,
  width = "narrow",
  children,
}: {
  suffix?: ReactNode;
  width?: keyof typeof SHELL_WIDTH;
  children: ReactNode;
}) {
  const t = useT();
  return (
    <main className="theme-cinema relative isolate flex min-h-screen flex-col overflow-hidden bg-background text-foreground">
      <CinemaBackdrop />
      <header className="flex items-center gap-2.5 px-5 py-4 sm:px-8 sm:py-6">
        <img src={beamfallLogo} alt="" width={28} height={28} className="shrink-0" />
        <Wordmark label={t("brand")} />
        {suffix && <span className="hidden text-sm text-muted-foreground sm:inline">{suffix}</span>}
      </header>
      <div className="flex flex-1 justify-center px-4 pb-12 pt-2 sm:px-8 sm:pt-[8vh]">
        <div className={cn("w-full", SHELL_WIDTH[width])}>{children}</div>
      </div>
    </main>
  );
}

// The one card a PageShell holds. `asChild` lets a <form> be the card.
export function GlassCard({
  asChild = false,
  className,
  ...props
}: ComponentProps<"div"> & { asChild?: boolean }) {
  const Comp = asChild ? Slot : "div";
  return (
    <Comp
      data-slot="glass-card"
      className={cn(
        "animate-in fade-in motion-reduce:animate-none rounded-[14px] border border-white/10 bg-card p-5 shadow-[0_24px_60px_-28px_rgba(0,0,0,0.7)] duration-200 sm:p-8",
        className,
      )}
      {...props}
    />
  );
}

// The card's title and lead. A `focusTarget` heading takes programmatic focus
// (tabIndex -1, no outline) on steps that have no text field to focus.
export function SurfaceHeading({
  id,
  title,
  lead,
  focusTarget = false,
}: {
  id?: string;
  title: string;
  lead: ReactNode;
  focusTarget?: boolean;
}) {
  return (
    <div className="mb-6">
      <h1
        id={id}
        data-step-heading={focusTarget || undefined}
        tabIndex={focusTarget ? -1 : undefined}
        className="m-0 text-2xl font-semibold leading-tight tracking-tight text-balance outline-none"
      >
        {title}
      </h1>
      <p className="mb-0 mt-2 text-[15px] leading-relaxed text-muted-foreground">{lead}</p>
    </div>
  );
}

const FIELD_LABEL = "mb-2 block text-dense font-medium text-foreground/80";
// The field look shared by inputs and the buttons that sit beside them.
const FIELD_LOOK = "h-11 rounded-[10px] border-white/10 bg-white/[0.04]";

export function FieldLabel({ className, ...props }: ComponentProps<typeof Label>) {
  return <Label className={cn(FIELD_LABEL, className)} {...props} />;
}

export function FieldLegend({ className, ...props }: ComponentProps<"legend">) {
  return <legend className={cn(FIELD_LABEL, "p-0", className)} {...props} />;
}

export function FieldInput({ className, ...props }: ComponentProps<typeof Input>) {
  return <Input className={cn(FIELD_LOOK, "text-[15px]", className)} {...props} />;
}

// The hint or error line under a field. Joined with clsx, not cn: tailwind-merge
// reads the custom `text-dense` size as a colour and would drop it in favour of
// `text-muted-foreground`.
export function FieldHint({ className, ...props }: ComponentProps<"p">) {
  return (
    <p className={clsx("mb-0 mt-1.5 text-dense text-muted-foreground", className)} {...props} />
  );
}

// An outline button with the field look, for controls inside a field row
// (show password, Browse…).
export function FieldButton({ className, ...props }: ComponentProps<typeof Button>) {
  return (
    <Button
      type="button"
      variant="outline"
      className={cn(FIELD_LOOK, "shrink-0 px-4", className)}
      {...props}
    />
  );
}

// The card's main action and its quieter companion (Back, Cancel).
export function PrimaryAction({ className, ...props }: ComponentProps<typeof Button>) {
  return (
    <Button
      className={cn("h-11 rounded-[10px] px-6 text-[15px] font-semibold", className)}
      {...props}
    />
  );
}

export function SecondaryAction({ className, ...props }: ComponentProps<typeof Button>) {
  return (
    <Button
      type="button"
      variant="ghost"
      className={cn("h-11 rounded-[10px] px-5", className)}
      {...props}
    />
  );
}

// Options sit directly on the card as rows split by hairlines: one surface.
export function HairlineRows({ className, ...props }: ComponentProps<"div">) {
  return <div className={cn(HAIRLINE_ROWS, className)} {...props} />;
}

export function HairlineList({ className, ...props }: ComponentProps<"ul">) {
  return <ul className={cn("m-0 list-none p-0", HAIRLINE_ROWS, className)} {...props} />;
}

const HAIRLINE_ROWS = "divide-y divide-white/10 border-y border-white/10";

// A titled block below a hairline, inside the card. `asChild` keeps the
// caller's element (a <section> with its own heading).
export function SurfaceSection({
  asChild = false,
  className,
  ...props
}: ComponentProps<"div"> & { asChild?: boolean }) {
  const Comp = asChild ? Slot : "div";
  return <Comp className={cn("mt-6 border-t border-white/10 pt-5", className)} {...props} />;
}

// One choice row on the card: a <label> wrapping a native radio (OptionRadio)
// and its text, so the whole row is the hit target.
export function OptionRow({ className, ...props }: ComponentProps<"label">) {
  return (
    <label className={cn("flex cursor-pointer items-start gap-3 py-3.5", className)} {...props} />
  );
}

export function OptionRadio({ className, ...props }: Omit<ComponentProps<"input">, "type">) {
  return (
    <input
      type="radio"
      className={cn("mt-1 size-4 shrink-0 cursor-pointer accent-[var(--accent)]", className)}
      {...props}
    />
  );
}

// The inset panel inside a card (the folder picker).
export function SurfaceInset({ className, ...props }: ComponentProps<"div">) {
  return (
    <div
      className={cn(
        "overflow-hidden rounded-[10px] border border-white/10 bg-white/[0.03]",
        className,
      )}
      {...props}
    />
  );
}
