import { Fragment } from "react";
import { Button } from "@/components/ui/button";
import type { FolderEntryInfo } from "@/lib/graphql/types";

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
    <div className="mt-2 overflow-hidden rounded-lg border bg-card">
      {/* Breadcrumb */}
      <div className="flex flex-wrap items-center gap-1 border-b px-2.5 py-2 text-xs text-muted-foreground">
        {crumbs.map((crumb, i) => {
          const isCurrent = i === crumbs.length - 1;
          return (
            <Fragment key={crumb.path}>
              {i > 0 && <span className="opacity-50">›</span>}
              {isCurrent ? (
                <span className="text-foreground">{crumb.label}</span>
              ) : (
                <button
                  type="button"
                  className="text-primary hover:underline"
                  onClick={() => onOpenFolder(crumb.path)}
                >
                  {crumb.label}
                </button>
              )}
            </Fragment>
          );
        })}
        <button
          type="button"
          onClick={onClose}
          aria-label={labels.close}
          className="ml-auto text-muted-foreground hover:text-foreground"
        >
          ✕
        </button>
      </div>

      {/* Directory list */}
      <div className="max-h-44 overflow-y-auto" aria-busy={busy}>
        {busy ? (
          <p className="px-3 py-2.5 text-[13px] text-muted-foreground">{labels.loading}</p>
        ) : folders.length === 0 ? (
          <p className="px-3 py-2.5 text-[13px] text-muted-foreground">{error ?? labels.empty}</p>
        ) : (
          folders.map((folder) => (
            <button
              key={folder.path}
              type="button"
              onClick={() => onOpenFolder(folder.path)}
              className="flex w-full items-center gap-2 border-b px-2.5 py-2 text-left text-[13px] last:border-b-0 hover:bg-muted"
            >
              <span className="opacity-70">📁</span>
              <span className="truncate">{folder.name}</span>
              <span className="ml-auto opacity-40">›</span>
            </button>
          ))
        )}
        {error && !busy && folders.length > 0 && (
          <p className="px-3 py-2 text-[12px] text-destructive">{error}</p>
        )}
      </div>

      {/* Footer: current path + commit */}
      <div className="flex items-center gap-2.5 border-t bg-muted/40 px-2.5 py-2">
        <span className="truncate font-mono text-xs text-muted-foreground">{node.path}</span>
        <Button type="button" size="sm" className="ml-auto" onClick={onUse} disabled={busy}>
          {labels.use}
        </Button>
      </div>
    </div>
  );
}
