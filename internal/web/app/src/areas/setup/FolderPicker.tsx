import { ChevronRight, Folder, X } from "lucide-react";
import { Fragment, useEffect, useRef } from "react";
import { Button } from "@/components/ui/button";
import type { FolderEntryInfo } from "@/lib/graphql/types";
import { cn } from "@/lib/utils";

// Setup folder picker (WEB-6): a directories-only browse of the server
// filesystem for the first-run wizard's root field. It renders one directory at
// a time — a breadcrumb to navigate up, the subfolders to descend into, and a
// footer that commits the current folder. Data comes from the manager-gated
// browseServerFolders query; this component is presentational and drives
// navigation through the onOpenFolder callback.
//
// Keyboard: each folder that loads moves focus to its first subfolder (or to
// "Use this folder" when it has none), so Enter walks down the tree; Escape
// closes the picker and the host returns focus to its Browse button.

interface Crumb {
  label: string;
  path: string;
}

// Build breadcrumb segments from an absolute path. "/media/films" →
// [{/, /}, {media, /media}, {films, /media/films}]. The leading root is always
// present so the operator can climb back to the top of the filesystem.
export function breadcrumbs(path: string): Crumb[] {
  const crumbs: Crumb[] = [{ label: "/", path: "/" }];
  const parts = path.split("/").filter(Boolean);
  let acc = "";
  for (const part of parts) {
    acc += "/" + part;
    crumbs.push({ label: part, path: acc });
  }
  return crumbs;
}

interface FolderPickerProps {
  node: FolderEntryInfo;
  busy: boolean;
  error: string | null;
  onOpenFolder: (path: string) => void;
  onUse: () => void;
  onClose: () => void;
  labels: { use: string; loading: string; empty: string; close: string };
}

export function FolderPicker({
  node,
  busy,
  error,
  onOpenFolder,
  onUse,
  onClose,
  labels,
}: FolderPickerProps) {
  const crumbs = breadcrumbs(node.path);
  const folders = (node.children ?? []).filter((c) => c.kind === "folder");
  const listRef = useRef<HTMLDivElement>(null);
  const commitRef = useRef<HTMLButtonElement>(null);

  useEffect(() => {
    if (busy) return;
    const first = listRef.current?.querySelector<HTMLButtonElement>("button");
    (first ?? commitRef.current)?.focus();
  }, [node.path, busy]);

  return (
    <div
      className="mt-2 overflow-hidden rounded-[10px] border border-white/10 bg-white/[0.03]"
      onKeyDown={(e) => {
        if (e.key !== "Escape") return;
        e.preventDefault();
        e.stopPropagation();
        onClose();
      }}
    >
      {/* Breadcrumb */}
      <div className="flex items-center gap-1 border-b border-white/10 py-1.5 pl-3 pr-1.5 text-dense text-muted-foreground">
        <div className="flex min-w-0 flex-1 flex-wrap items-center gap-0.5">
          {crumbs.map((crumb, i) => {
            const isCurrent = i === crumbs.length - 1;
            return (
              <Fragment key={crumb.path}>
                {i > 0 && <ChevronRight aria-hidden className="size-3.5 shrink-0 opacity-50" />}
                {isCurrent ? (
                  <span className="truncate px-1 font-medium text-foreground" title={crumb.label}>
                    {crumb.label}
                  </span>
                ) : (
                  <Button
                    type="button"
                    variant="ghost"
                    size="sm"
                    className="px-1.5 font-normal text-muted-foreground hover:text-foreground"
                    onClick={() => onOpenFolder(crumb.path)}
                  >
                    {crumb.label}
                  </Button>
                )}
              </Fragment>
            );
          })}
        </div>
        <Button
          type="button"
          variant="ghost"
          size="icon-sm"
          onClick={onClose}
          aria-label={labels.close}
          className="text-muted-foreground hover:text-foreground"
        >
          <X aria-hidden />
        </Button>
      </div>

      {/* Directory list */}
      <div ref={listRef} className="max-h-56 overflow-y-auto py-1" aria-busy={busy}>
        {busy ? (
          <p className="m-0 px-3 py-2.5 text-dense text-muted-foreground">{labels.loading}</p>
        ) : folders.length === 0 ? (
          <p
            role={error ? "alert" : undefined}
            className={cn(
              "m-0 px-3 py-2.5 text-dense leading-snug",
              error ? "text-(--state-danger)" : "text-muted-foreground",
            )}
          >
            {error ?? labels.empty}
          </p>
        ) : (
          folders.map((folder) => (
            <Button
              key={folder.path}
              type="button"
              variant="ghost"
              size="lg"
              onClick={() => onOpenFolder(folder.path)}
              className="w-full justify-start gap-2.5 rounded-none px-3 font-normal has-[>svg]:px-3"
            >
              <Folder aria-hidden className="size-4 shrink-0 text-muted-foreground" />
              <span className="truncate">{folder.name}</span>
              <ChevronRight
                aria-hidden
                className="ml-auto size-4 shrink-0 text-muted-foreground/60"
              />
            </Button>
          ))
        )}
        {error && !busy && folders.length > 0 && (
          <p role="alert" className="m-0 px-3 py-2 text-dense leading-snug text-(--state-danger)">
            {error}
          </p>
        )}
      </div>

      {/* Footer: current path + commit */}
      <div className="flex items-center gap-3 border-t border-white/10 py-2 pl-3 pr-2">
        <span
          className="min-w-0 truncate font-mono text-dense text-muted-foreground"
          title={node.path}
        >
          {node.path}
        </span>
        <Button
          type="button"
          size="sm"
          ref={commitRef}
          className="ml-auto shrink-0 rounded-[10px] px-4"
          onClick={onUse}
          disabled={busy}
        >
          {labels.use}
        </Button>
      </div>
    </div>
  );
}
