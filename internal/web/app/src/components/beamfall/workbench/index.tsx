import { Inbox, type LucideProps } from "lucide-react";
import type { ComponentType, ReactNode } from "react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
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

export { PathPickerField } from "./PathPickerField";

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

// Manage status badge: data-state selects the tone in manage.css.
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
    <Badge variant="secondary" data-state={state} className={cn("manage-badge", className)}>
      {children}
    </Badge>
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

export function EmptyState({
  message,
  icon: Icon = Inbox,
}: {
  message: StringKey;
  icon?: ComponentType<LucideProps>;
}) {
  const t = useT();
  return (
    <div className="console-card">
      <div className="console-empty">
        <Icon className="console-empty-icon" aria-hidden />
        <span className="console-empty-line">{t(message)}</span>
      </div>
    </div>
  );
}

export function ErrorState({ message }: { message: StringKey }) {
  const t = useT();
  return (
    <div className="console-card">
      <p role="alert" className="console-empty console-empty-line text-[var(--red-500)]">
        {t(message)}
      </p>
    </div>
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
  className,
  children,
}: {
  id: string;
  label: ReactNode;
  className?: string;
  children: ReactNode;
}) {
  return (
    <div className={cn("space-y-1", className)}>
      <Label htmlFor={id}>{label}</Label>
      {children}
    </div>
  );
}

export function Fieldset({
  title,
  help,
  hideTitle = false,
  children,
}: {
  title: StringKey;
  help?: StringKey;
  /** Keep the legend for assistive tech only, when the section heading already says it. */
  hideTitle?: boolean;
  children: ReactNode;
}) {
  const t = useT();
  if (hideTitle && !help) {
    return (
      <fieldset className="console-fieldset">
        <legend className="sr-only">{t(title)}</legend>
        <div className="console-fieldset-body">{children}</div>
      </fieldset>
    );
  }
  return (
    <fieldset className="console-fieldset">
      <div className="console-fieldset-head">
        <legend className="console-fieldset-title">{t(title)}</legend>
        {help && <p className="console-fieldset-help">{t(help)}</p>}
      </div>
      <div className="console-fieldset-body">{children}</div>
    </fieldset>
  );
}

export function ControlRow({
  label,
  help,
  control,
}: {
  label: StringKey;
  help?: StringKey;
  control: ReactNode;
}) {
  const t = useT();
  return (
    <div className="console-control-row">
      <div className="min-w-0">
        <div className="console-control-label">{t(label)}</div>
        {help && <p className="console-control-help">{t(help)}</p>}
      </div>
      <div className="flex shrink-0 items-center gap-2">{control}</div>
    </div>
  );
}

export function ManageSection({
  title,
  lead,
  icon: Icon,
  actions,
  children,
}: {
  title: string;
  lead?: string;
  /** A generic glyph for the house-style icon tile beside the title. */
  icon?: ComponentType<LucideProps>;
  actions?: ReactNode;
  children: ReactNode;
}) {
  return (
    <Card className="manage-section">
      <CardHeader className="manage-section-header flex flex-row items-start justify-between gap-2">
        <div className="flex min-w-0 items-start gap-3">
          {Icon && (
            <span className="house-icon-tile" aria-hidden>
              <Icon className="size-[18px]" />
            </span>
          )}
          <div className="min-w-0 space-y-0.5">
            <CardTitle role="heading" aria-level={2} className="manage-section-title">
              {title}
            </CardTitle>
            {lead && <CardDescription className="manage-section-lead">{lead}</CardDescription>}
          </div>
        </div>
        {actions && <div className="flex shrink-0 items-center gap-2">{actions}</div>}
      </CardHeader>
      {children ? <CardContent className="manage-section-body">{children}</CardContent> : null}
    </Card>
  );
}
