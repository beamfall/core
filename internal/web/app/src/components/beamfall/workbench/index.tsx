import { FolderOpen, Inbox, type LucideProps, Plus } from "lucide-react";
import { type ComponentType, type ReactNode, useState } from "react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
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
import { getBeamfallHost } from "@/lib/beamfallHost";
import { addPathPickerRoot, fetchPathPicker } from "@/lib/graphql";
import type { PathPickerEntryInfo, PathPickerInfo, PathPickerRole } from "@/lib/graphql/types";
import { useT } from "@/lib/i18n";
import { cn } from "@/lib/utils";
import type { StringKey } from "@/strings";
import "./workbench.css";

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

function hostDistributionClass(): string | undefined {
  const host = getBeamfallHost();
  if (host?.clientClass === "ipad-manager-app-store") return "appStore";
  if (host?.clientClass === "apple-manager-macos-direct") return "directDistribution";
  return undefined;
}

export function PathPickerField({
  id,
  label,
  role,
  value,
  onChange,
  libraryID,
  placeholder,
  disabled,
  className,
  inputClassName,
  suppressAppStoreDownload = false,
}: {
  id: string;
  label: ReactNode;
  role: PathPickerRole;
  value: string;
  onChange: (value: string) => void;
  libraryID?: string;
  placeholder?: string;
  disabled?: boolean;
  className?: string;
  inputClassName?: string;
  suppressAppStoreDownload?: boolean;
}) {
  const t = useT();
  const distributionClass = hostDistributionClass();
  const suppressed =
    suppressAppStoreDownload && role === "downloadDestination" && distributionClass === "appStore";
  const [open, setOpen] = useState(false);
  const [picker, setPicker] = useState<PathPickerInfo | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  if (suppressed) return null;

  async function load(path = value.trim()) {
    setBusy(true);
    setError("");
    try {
      setPicker(
        await fetchPathPicker({
          role,
          path: path || undefined,
          libraryID,
          distributionClass,
          depth: 1,
        }),
      );
    } catch (err) {
      setError(err instanceof Error ? err.message : t("pathPickerUnavailable"));
    } finally {
      setBusy(false);
    }
  }

  async function onAddAllowedRoot() {
    const path = value.trim();
    if (!path) return;
    setBusy(true);
    setError("");
    try {
      const root = await addPathPickerRoot(path);
      onChange(root.path);
      await load(root.path);
    } catch (err) {
      setError(err instanceof Error ? err.message : t("pathPickerUnavailable"));
    } finally {
      setBusy(false);
    }
  }

  const roleAvailable = !picker || picker.roles.includes(role);

  return (
    <div className={cn("space-y-1", className)}>
      <Label htmlFor={id}>{label}</Label>
      <div className="flex flex-wrap items-center gap-2">
        <Input
          id={id}
          value={value}
          autoComplete="off"
          spellCheck={false}
          placeholder={placeholder}
          disabled={disabled}
          onChange={(e) => onChange(e.target.value)}
          className={inputClassName}
        />
        <Button
          type="button"
          size="sm"
          variant="outline"
          disabled={disabled || busy}
          onClick={() => {
            const next = !open;
            setOpen(next);
            if (next) void load();
          }}
        >
          <FolderOpen className="console-nav-icon" aria-hidden />
          {t("browse")}
        </Button>
        {open && roleAvailable && (
          <Button
            type="button"
            size="sm"
            variant="outline"
            disabled={disabled || busy || !value.trim()}
            onClick={() => void onAddAllowedRoot()}
          >
            <Plus className="console-nav-icon" aria-hidden />
            {t("addPathPickerRoot")}
          </Button>
        )}
      </div>
      {open && (
        <div className="rounded-md border bg-muted/20 p-2 text-sm">
          {busy && <p className="text-muted-foreground">{t("pathPickerLoading")}</p>}
          {!busy && !roleAvailable && (
            <p className="text-muted-foreground">{t("pathPickerUnavailable")}</p>
          )}
          {!busy && roleAvailable && picker && (
            <div className="space-y-2">
              {picker.roots.length > 0 && (
                <PathPickerButtonList
                  label={t("pathPickerRoots")}
                  entries={picker.roots.map((root) => ({
                    name: root.name,
                    path: root.path,
                  }))}
                  onPick={(path) => {
                    onChange(path);
                    void load(path);
                  }}
                />
              )}
              <PathPickerEntries
                entries={picker.entries}
                onPick={(path) => {
                  onChange(path);
                  void load(path);
                }}
              />
            </div>
          )}
          {error && (
            <p role="alert" className="mt-2 text-destructive">
              {error}
            </p>
          )}
        </div>
      )}
    </div>
  );
}

function PathPickerButtonList({
  label,
  entries,
  onPick,
}: {
  label: string;
  entries: { name: string; path: string }[];
  onPick: (path: string) => void;
}) {
  return (
    <div className="space-y-1">
      <p className="text-xs font-medium text-muted-foreground">{label}</p>
      <div className="grid gap-1">
        {entries.map((entry) => (
          <button
            key={entry.path}
            type="button"
            className="rounded border bg-background px-2 py-1 text-left hover:bg-muted"
            onClick={() => onPick(entry.path)}
          >
            <span className="font-medium">{entry.name}</span>
            <span className="block break-all text-xs text-muted-foreground">{entry.path}</span>
          </button>
        ))}
      </div>
    </div>
  );
}

function PathPickerEntries({
  entries,
  onPick,
}: {
  entries: PathPickerEntryInfo[];
  onPick: (path: string) => void;
}) {
  const t = useT();
  if (entries.length === 0) {
    return <p className="text-muted-foreground">{t("pathPickerEmpty")}</p>;
  }
  return (
    <PathPickerButtonList
      label={t("pathPickerEntries")}
      entries={entries.map((entry) => ({
        name: entry.name,
        path: entry.path,
      }))}
      onPick={onPick}
    />
  );
}

export function Fieldset({
  title,
  help,
  children,
}: {
  title: StringKey;
  help?: StringKey;
  children: ReactNode;
}) {
  const t = useT();
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
  actions,
  children,
}: {
  title: string;
  lead?: string;
  actions?: ReactNode;
  children: ReactNode;
}) {
  return (
    <Card className="manage-section">
      <CardHeader className="manage-section-header flex flex-row items-start justify-between gap-2">
        <div className="space-y-0.5">
          <CardTitle className="manage-section-title">{title}</CardTitle>
          {lead && <CardDescription className="manage-section-lead">{lead}</CardDescription>}
        </div>
        {actions && <div className="flex items-center gap-2">{actions}</div>}
      </CardHeader>
      <CardContent className="manage-section-body">{children}</CardContent>
    </Card>
  );
}
