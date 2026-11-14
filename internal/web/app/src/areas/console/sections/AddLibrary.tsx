import { Loader2, Plus, X } from "lucide-react";
import { type FormEvent, useId, useRef, useState } from "react";
import { Link } from "react-router";
import { PathPickerField } from "@/components/beamfall/workbench";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Skeleton } from "@/components/ui/skeleton";
import { createLibrary } from "@/lib/graphql";
import type { PluginSummary } from "@/lib/graphql/types";
import { useDisplayLocale, useT } from "@/lib/i18n";
import { libraryTypeOptions } from "@/lib/libraryTypes";
import { requestManifestRefresh } from "@/lib/manifestRefresh";
import { cn } from "@/lib/utils";
import { MutationFailure } from "./DeletedLibraries";
import { CARD } from "./libraryStyles";

// Add a library (CORE-1156): what kind of media first, then a name and the
// folders, created together in one createLibrary call. The server creates the
// library and its folders as one write, so a refused folder leaves nothing
// half-made and the form keeps what the owner typed.

interface FolderField {
  key: number;
  path: string;
}

export function AddLibrary({
  plugins,
  pluginsState,
  onCreated,
  onCancel,
}: {
  plugins: PluginSummary[];
  pluginsState: "loading" | "failed" | "ready";
  onCreated: (library: { id: string; title: string; roots: string[] }) => Promise<void>;
  onCancel?: () => void;
}) {
  const t = useT();
  const locale = useDisplayLocale();
  const ids = useId();
  const options = libraryTypeOptions(plugins, t, locale);
  const [typeKey, setTypeKey] = useState("");
  const [title, setTitle] = useState("");
  const [folders, setFolders] = useState<FolderField[]>([{ key: 0, path: "" }]);
  const nextKey = useRef(1);
  const [typeError, setTypeError] = useState(false);
  const [folderError, setFolderError] = useState(false);
  const [busy, setBusy] = useState(false);
  // null: the server has not refused. Otherwise its reason, which may be empty.
  const [failure, setFailure] = useState<string | null>(null);
  const typeGroupRef = useRef<HTMLFieldSetElement>(null);

  const selected = options.find((option) => option.key === typeKey);
  const folderID = (index: number) => `${ids}-folder-${index}`;

  function setFolderPath(key: number, path: string) {
    setFolders((list) => list.map((item) => (item.key === key ? { ...item, path } : item)));
    if (path.trim()) setFolderError(false);
  }

  function addFolderField() {
    const key = nextKey.current++;
    setFolders((list) => [...list, { key, path: "" }]);
    queueMicrotask(() => document.getElementById(folderID(folders.length))?.focus());
  }

  function removeFolderField(key: number, index: number) {
    setFolders((list) => list.filter((item) => item.key !== key));
    queueMicrotask(() => document.getElementById(folderID(index - 1))?.focus());
  }

  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    if (busy) return;
    const roots = [...new Set(folders.map((item) => item.path.trim()).filter(Boolean))];
    setTypeError(!selected);
    setFolderError(roots.length === 0);
    if (!selected) {
      typeGroupRef.current?.querySelector<HTMLInputElement>("input[type=radio]")?.focus();
      return;
    }
    if (roots.length === 0) {
      document.getElementById(folderID(0))?.focus();
      return;
    }
    const name = title.trim() || selected.label;
    setBusy(true);
    setFailure(null);
    try {
      const created = await createLibrary(
        name,
        selected.pluginID,
        selected.familyScope ?? undefined,
        roots,
      );
      requestManifestRefresh();
      await onCreated({ id: created.id, title: created.title || name, roots });
    } catch (err) {
      setFailure(err instanceof Error ? err.message : "");
    } finally {
      setBusy(false);
    }
  }

  const typeErrorID = `${ids}-type-error`;
  const nameHintID = `${ids}-name-hint`;
  const folderHintID = `${ids}-folder-hint`;
  const folderErrorID = `${ids}-folder-error`;

  return (
    <form
      onSubmit={onSubmit}
      noValidate
      aria-labelledby={`${ids}-heading`}
      aria-busy={busy || undefined}
      className={cn(CARD, "space-y-5 p-4")}
    >
      <div>
        <h3 id={`${ids}-heading`} className="m-0 text-base font-semibold">
          {t("taskAddLibrary")}
        </h3>
        <p className="m-0 text-xs text-muted-foreground">{t("taskAddLibraryLead")}</p>
      </div>

      <fieldset
        ref={typeGroupRef}
        aria-describedby={typeError ? typeErrorID : undefined}
        className="m-0 min-w-0 space-y-2 border-0 p-0"
        disabled={busy}
      >
        <legend className="mb-2 p-0 text-sm font-medium">{t("libraryKind")}</legend>
        {pluginsState === "loading" ? (
          <Skeleton className="h-20 w-full" />
        ) : options.length === 0 ? (
          <p className="m-0 text-sm text-muted-foreground">
            {pluginsState === "failed" ? t("libraryKindsFailed") : t("libraryKindsNone")}{" "}
            {pluginsState !== "failed" && (
              <Link
                to="/console/plugins"
                className="text-primary underline-offset-2 hover:underline"
              >
                {t("openPlugins")}
              </Link>
            )}
          </p>
        ) : (
          <div className="grid grid-cols-2 gap-2 sm:grid-cols-3 lg:grid-cols-4">
            {options.map((option) => {
              const Icon = option.icon;
              const checked = option.key === typeKey;
              return (
                <label
                  key={option.key}
                  className={cn(
                    "relative flex cursor-pointer items-center gap-2.5 rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-surface)] px-3 py-2.5 transition-colors hover:bg-[var(--bg-raised)]",
                    "has-[:focus-visible]:ring-2 has-[:focus-visible]:ring-ring",
                    checked && "border-primary/60 bg-primary/10 hover:bg-primary/15",
                  )}
                >
                  <input
                    type="radio"
                    name={`${ids}-type`}
                    value={option.key}
                    checked={checked}
                    onChange={() => {
                      setTypeKey(option.key);
                      setTypeError(false);
                    }}
                    aria-invalid={typeError || undefined}
                    className="sr-only"
                  />
                  <Icon
                    aria-hidden
                    className={cn(
                      "size-5 shrink-0",
                      checked ? "text-primary" : "text-muted-foreground",
                    )}
                  />
                  <span className="min-w-0">
                    <span className="block break-words text-sm font-medium">{option.label}</span>
                    {option.pluginLabel !== option.label && (
                      <span className="block break-words text-[11px] text-muted-foreground">
                        {option.pluginLabel}
                      </span>
                    )}
                  </span>
                </label>
              );
            })}
          </div>
        )}
        {typeError && (
          <p id={typeErrorID} className="m-0 text-xs text-destructive">
            {t("libraryKindRequired")}
          </p>
        )}
      </fieldset>

      <div className="space-y-1.5">
        <Label htmlFor={`${ids}-title`}>{t("libraryName")}</Label>
        <Input
          id={`${ids}-title`}
          value={title}
          disabled={busy}
          autoComplete="off"
          placeholder={selected?.label}
          aria-describedby={selected ? nameHintID : undefined}
          onChange={(e) => setTitle(e.target.value)}
          className="sm:max-w-sm"
        />
        {selected && (
          <p id={nameHintID} className="m-0 text-xs text-muted-foreground">
            {t("libraryNameHint", { name: selected.label })}
          </p>
        )}
      </div>

      <div className="space-y-2">
        {folders.map((folder, index) => (
          <div key={folder.key} className="flex items-end gap-2">
            <PathPickerField
              id={folderID(index)}
              label={
                folders.length > 1
                  ? t("libraryFolderNumbered", { number: index + 1 })
                  : t("libraryFolder")
              }
              role="libraryRoot"
              value={folder.path}
              placeholder="/media"
              disabled={busy}
              describedBy={index === 0 ? cn(folderHintID, folderError && folderErrorID) : undefined}
              invalid={index === 0 && folderError}
              onChange={(path) => setFolderPath(folder.key, path)}
              className="min-w-0 flex-1"
              inputClassName="min-w-0 flex-1 font-mono"
            />
            {index > 0 && (
              <Button
                type="button"
                variant="ghost"
                size="sm"
                disabled={busy}
                onClick={() => removeFolderField(folder.key, index)}
                aria-label={t("removeFolderField", { number: index + 1 })}
                title={t("removeFolderField", { number: index + 1 })}
              >
                <X aria-hidden className="size-4" />
              </Button>
            )}
          </div>
        ))}
        <p id={folderHintID} className="m-0 text-xs text-muted-foreground">
          {t("libraryFolderHint")}
        </p>
        {folderError && (
          <p id={folderErrorID} className="m-0 text-xs text-destructive">
            {t("libraryFolderRequired")}
          </p>
        )}
        <Button type="button" variant="ghost" size="sm" disabled={busy} onClick={addFolderField}>
          <Plus className="console-nav-icon" aria-hidden />
          {t("addAnotherFolder")}
        </Button>
      </div>

      <MutationFailure title={t("couldNotCreateLibrary")} reason={failure} />

      <div className="flex flex-wrap justify-end gap-2 border-t border-white/[0.07] pt-4">
        {onCancel && (
          <Button type="button" variant="ghost" onClick={onCancel} disabled={busy}>
            {t("cancel")}
          </Button>
        )}
        <Button type="submit" aria-disabled={busy || undefined}>
          {busy ? (
            <Loader2 className="console-nav-icon animate-spin" aria-hidden />
          ) : (
            <Plus className="console-nav-icon" aria-hidden />
          )}
          {busy ? t("createLibraryBusy") : t("createLibrary")}
        </Button>
      </div>
    </form>
  );
}
