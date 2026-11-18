import { ChevronDown, type LucideProps } from "lucide-react";
import { type ComponentType, type ReactNode, useEffect, useState } from "react";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Label } from "@/components/ui/label";
import { Skeleton } from "@/components/ui/skeleton";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { useT } from "@/lib/i18n";
import { cn } from "@/lib/utils";
import type { StringKey } from "@/strings";
import "./workbench.css";

import { HelpTip } from "./HelpTip";

export { HelpTip };
export { PathPickerField } from "./PathPickerField";
export {
  formatMinuteOfDay,
  minuteOfDayPresets,
  type Preset,
  PresetSelect,
  presetOptions,
  TimeOfDaySelect,
} from "./PresetSelect";

export type StatusTone = "neutral" | "green" | "amber" | "red" | "cyan" | "iris";

const TONE_CLASS: Record<StatusTone, string> = {
  neutral: "",
  green: "is-green",
  amber: "is-amber",
  red: "is-red",
  cyan: "is-cyan",
  iris: "is-iris",
};

export function StatusChip({
  tone = "neutral",
  dot = true,
  children,
}: {
  tone?: StatusTone;
  dot?: boolean;
  children: ReactNode;
}) {
  return (
    <span className={cn("status-chip", TONE_CLASS[tone])}>
      {dot && <span className="status-chip-dot" aria-hidden />}
      {children}
    </span>
  );
}

export type BadgeState = "neutral" | "green" | "amber" | "red" | "cyan" | "restricted" | "revealed";

const BADGE_TONE: Record<BadgeState, StatusTone> = {
  neutral: "neutral",
  green: "green",
  amber: "amber",
  red: "red",
  cyan: "cyan",
  restricted: "iris",
  revealed: "iris",
};

// Manage status: the same dot-and-text idiom as StatusChip, so Console and
// Manage read status one way. data-state stays as the stable hook.
export function StatusBadge({
  state = "neutral",
  className,
  children,
}: {
  state?: BadgeState;
  className?: string;
  children: ReactNode;
}) {
  return (
    <span
      data-slot="status-badge"
      data-state={state}
      className={cn("status-chip", TONE_CLASS[BADGE_TONE[state]], className)}
    >
      <span className="status-chip-dot" aria-hidden />
      {children}
    </span>
  );
}

export function DataTable({
  columns,
  children,
}: {
  columns: { key: string; label?: ReactNode; numeric?: boolean }[];
  children: ReactNode;
}) {
  return (
    <div className="console-table">
      <Table>
        <TableHeader>
          <TableRow>
            {columns.map((col) => (
              <TableHead key={col.key} className={col.numeric ? "text-right" : undefined}>
                {col.label}
              </TableHead>
            ))}
          </TableRow>
        </TableHeader>
        <TableBody>{children}</TableBody>
      </Table>
    </div>
  );
}

export function TableSkeletonRows({ columns, rows = 4 }: { columns: number; rows?: number }) {
  return (
    <>
      {Array.from({ length: rows }).map((_, r) => (
        <TableRow key={r} aria-hidden className="pointer-events-none">
          {Array.from({ length: columns }).map((_, c) => (
            <TableCell key={c}>
              <Skeleton className="h-4 w-[60%]" />
            </TableCell>
          ))}
        </TableRow>
      ))}
    </>
  );
}

export function ManageTable({ className, children }: { className?: string; children: ReactNode }) {
  return <Table className={cn("manage-table", className)}>{children}</Table>;
}

// An empty collection is a quiet sentence where the content would be, not a
// boxed illustration: the section heading already says what is missing.
export function EmptyState({ message }: { message: StringKey }) {
  const t = useT();
  return <p className="console-empty console-empty-line">{t(message)}</p>;
}

export function ErrorState({ message }: { message: StringKey }) {
  const t = useT();
  return (
    <p role="alert" className="console-empty console-empty-line text-[var(--state-danger)]">
      {t(message)}
    </p>
  );
}

export function ErrorLine({ message }: { message: string }) {
  if (!message) return null;
  return (
    <p role="alert" className="mt-2 text-sm text-destructive">
      {message}
    </p>
  );
}

export function EmptyLine({ message }: { message: string }) {
  return <p className="manage-empty">{message}</p>;
}

export function ManageInlineForm({
  children,
  className,
}: {
  children: ReactNode;
  className?: string;
}) {
  return <div className={cn("flex flex-wrap items-end gap-3", className)}>{children}</div>;
}

export function ManageField({
  id,
  label,
  tip,
  setting,
  className,
  children,
}: {
  id: string;
  label: ReactNode;
  /** A (?) explanation for a setting whose name alone is not enough (CORE-2445). */
  tip?: StringKey;
  /** The settings-search anchor for this field (see lib/settingsIndex). */
  setting?: string;
  className?: string;
  children: ReactNode;
}) {
  return (
    <div className={cn("space-y-1", className)} data-setting={setting}>
      {tip && typeof label === "string" ? (
        <span className="bf-label-with-tip">
          <Label htmlFor={id}>{label}</Label>
          <HelpTip label={label} text={tip} />
        </span>
      ) : (
        <Label htmlFor={id}>{label}</Label>
      )}
      {children}
    </div>
  );
}

export function Fieldset({
  title,
  help,
  tip,
  setting,
  hideTitle = false,
  children,
}: {
  title: StringKey;
  help?: StringKey;
  /** A (?) explanation beside the title (CORE-2445). */
  tip?: StringKey;
  /** The settings-search anchor for this group (see lib/settingsIndex). */
  setting?: string;
  /** Keep the legend for assistive tech only, when the section heading already says it. */
  hideTitle?: boolean;
  children: ReactNode;
}) {
  const t = useT();
  if (hideTitle && !help) {
    return (
      <fieldset className="console-fieldset" data-setting={setting}>
        <legend className="sr-only">{t(title)}</legend>
        <div className="console-fieldset-body">{children}</div>
      </fieldset>
    );
  }
  return (
    <fieldset className="console-fieldset" data-setting={setting}>
      <div className="console-fieldset-head">
        <legend className="console-fieldset-title">
          {t(title)}
          {tip && <HelpTip label={t(title)} text={tip} />}
        </legend>
        {help && <p className="console-fieldset-help">{t(help)}</p>}
      </div>
      <div className="console-fieldset-body">{children}</div>
    </fieldset>
  );
}

export function ControlRow({
  label,
  help,
  tip,
  setting,
  control,
}: {
  label: StringKey;
  help?: StringKey;
  /** A (?) explanation beside the label (CORE-2445). */
  tip?: StringKey;
  /** The settings-search anchor for this row (see lib/settingsIndex). */
  setting?: string;
  control: ReactNode;
}) {
  const t = useT();
  return (
    <div className="console-control-row" data-setting={setting}>
      <div className="min-w-0">
        <div className="console-control-label">
          {t(label)}
          {tip && <HelpTip label={t(label)} text={tip} />}
        </div>
        {help && <p className="console-control-help">{t(help)}</p>}
      </div>
      <div className="flex shrink-0 items-center gap-2">{control}</div>
    </div>
  );
}

// A page's opening heading (CORE-1125): an icon badge, the page's one h1 and a
// one-line lead saying what the page is for.
export function IconHeading({
  icon: Icon,
  title,
  lead,
}: {
  icon: ComponentType<LucideProps>;
  title: string;
  lead?: string;
}) {
  return (
    <div className="icon-heading">
      <span className="icon-heading-badge">
        <Icon className="icon-heading-icon" aria-hidden />
      </span>
      <div className="min-w-0">
        <h1 className="icon-heading-title">{title}</h1>
        {lead && <p className="icon-heading-lead">{lead}</p>}
      </div>
    </div>
  );
}

// A card whose body folds away behind its heading (CORE-1123): a native
// <details>, so it opens from the keyboard, and a closed body stays mounted
// and keeps anything typed into it. The summary holds only the heading and
// lead; actions sit at the top of the body, never inside the summary. A
// `flush` card is a fold inside another card: no border or fill of its own.
// `defaultOpen` turning true later (say, a problem found after load) opens it.
export function CollapsibleCard({
  title,
  lead,
  actions,
  level = 2,
  defaultOpen = false,
  flush = false,
  className,
  children,
}: {
  title: string;
  lead?: string;
  actions?: ReactNode;
  level?: 2 | 3 | 4;
  defaultOpen?: boolean;
  flush?: boolean;
  className?: string;
  children: ReactNode;
}) {
  const [open, setOpen] = useState(defaultOpen);
  useEffect(() => {
    if (defaultOpen) setOpen(true);
  }, [defaultOpen]);
  const Heading = (["h2", "h3", "h4"] as const)[level - 2];
  return (
    <details
      className={cn("collapsible-card", flush && "is-flush", className)}
      open={open}
      onToggle={(event) => setOpen(event.currentTarget.open)}
    >
      <summary className="collapsible-card-summary">
        <span className="collapsible-card-text">
          <Heading className="collapsible-card-title">{title}</Heading>
          {lead && <span className="collapsible-card-lead">{lead}</span>}
        </span>
        <ChevronDown className="collapsible-card-chevron" aria-hidden />
      </summary>
      <div className="collapsible-card-body">
        {actions && <div className="collapsible-card-actions">{actions}</div>}
        {children}
      </div>
    </details>
  );
}

export function ManageSection({
  title,
  lead,
  actions,
  collapsible = false,
  defaultOpen = false,
  children,
}: {
  /** Left out on a page's first card, whose title and lead are the page heading's. */
  title?: string;
  lead?: string;
  actions?: ReactNode;
  /** Folds the body behind the title; closed unless defaultOpen. Needs a title. */
  collapsible?: boolean;
  defaultOpen?: boolean;
  children: ReactNode;
}) {
  if (collapsible && title) {
    return (
      <CollapsibleCard
        className="manage-section-collapsible"
        title={title}
        lead={lead}
        actions={actions}
        defaultOpen={defaultOpen}
      >
        {children}
      </CollapsibleCard>
    );
  }
  return (
    <Card className="manage-section">
      {(title || lead || actions) && (
        <CardHeader className="manage-section-header flex flex-row items-start justify-between gap-2">
          <div className="min-w-0 space-y-1">
            {title && (
              <CardTitle role="heading" aria-level={2} className="manage-section-title">
                {title}
              </CardTitle>
            )}
            {lead && <CardDescription className="manage-section-lead">{lead}</CardDescription>}
          </div>
          {actions && <div className="flex shrink-0 items-center gap-2">{actions}</div>}
        </CardHeader>
      )}
      {children ? <CardContent className="manage-section-body">{children}</CardContent> : null}
    </Card>
  );
}
