import { ChevronRight, Folder, X } from "lucide-react";
import { Fragment } from "react";
import { Button } from "@/components/ui/button";
import type { FolderEntryInfo } from "@/lib/graphql/types";
import { cn } from "@/lib/utils";

// Setup folder picker (WEB-6): a directories-only browse of the server
// filesystem for the first-run wizard's root field. It renders one directory at
// a time — a breadcrumb to navigate up, the subfolders to descend into, and a
// footer that commits the current folder. Data comes from the manager-gated
// browseServerFolders query; this component is presentational and drives
// navigation through the onOpenFolder callback.

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

  return (
    <div className="mt-2 overflow-hidden rounded-xl border border-white/10 bg-white/[0.03]">
      {/* Breadcrumb */}
      <div className="flex items-center gap-1 border-b border-white/10 py-1.5 pl-3 pr-1.5 text-[13px] text-muted-foreground">
        <div className="flex min-w-0 flex-1 flex-wrap items-center gap-0.5">
          {crumbs.map((crumb, i) => {
            const isCurrent = i === crumbs.length - 1;
            return (
              <Fragment key={crumb.path}>
                {i > 0 && <ChevronRight aria-hidden className="size-3.5 shrink-0 opacity-50" />}
                {isCurrent ? (
                  <span className="truncate px-1 font-medium text-foreground">{crumb.label}</span>
                ) : (
                  <button
                    type="button"
                    className="rounded-md px-1 py-0.5 transition-colors duration-150 hover:text-foreground focus-visible:outline-2 focus-visible:outline-primary"
                    onClick={() => onOpenFolder(crumb.path)}
                  >
                    {crumb.label}
                  </button>
                )}
              </Fragment>
            );
          })}
        </div>
        <button
          type="button"
          onClick={onClose}
          aria-label={labels.close}
          className="grid size-8 shrink-0 place-items-center rounded-lg text-muted-foreground transition-colors duration-150 hover:bg-white/[0.06] hover:text-foreground focus-visible:outline-2 focus-visible:outline-primary"
        >
          <X aria-hidden className="size-4" />
        </button>
      </div>

      {/* Directory list */}
      <div className="max-h-56 overflow-y-auto py-1" aria-busy={busy}>
        {busy ? (
          <p className="m-0 px-3 py-2.5 text-[13px] text-muted-foreground">{labels.loading}</p>
        ) : folders.length === 0 ? (
          <p
            className={cn(
              "m-0 px-3 py-2.5 text-[13px]",
              error ? "text-destructive" : "text-muted-foreground",
            )}
          >
            {error ?? labels.empty}
          </p>
        ) : (
          folders.map((folder) => (
            <button
              key={folder.path}
              type="button"
              onClick={() => onOpenFolder(folder.path)}
              className="flex w-full items-center gap-2.5 px-3 py-2 text-left text-sm transition-colors duration-150 hover:bg-white/[0.06] focus-visible:bg-white/[0.06] focus-visible:outline-none"
            >
              <Folder aria-hidden className="size-4 shrink-0 text-muted-foreground" />
              <span className="truncate">{folder.name}</span>
              <ChevronRight
                aria-hidden
                className="ml-auto size-4 shrink-0 text-muted-foreground/60"
              />
            </button>
          ))
        )}
        {error && !busy && folders.length > 0 && (
          <p className="m-0 px-3 py-2 text-[13px] text-destructive">{error}</p>
        )}
      </div>

      {/* Footer: current path + commit */}
      <div className="flex items-center gap-3 border-t border-white/10 py-2 pl-3 pr-2">
        <span className="min-w-0 truncate font-mono text-[13px] text-muted-foreground">
          {node.path}
        </span>
        <Button
          type="button"
          size="sm"
          className="ml-auto h-9 shrink-0 rounded-lg px-4"
          onClick={onUse}
          disabled={busy}
        >
          {labels.use}
        </Button>
      </div>
    </div>
  );
}
