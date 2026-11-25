import {
  ChevronDown,
  ChevronUp,
  Copy,
  GripVertical,
  Pencil,
  Plus,
  Sparkles,
  Trash2,
} from "lucide-react";
import { type KeyboardEvent, useEffect, useState } from "react";
import { Button } from "@/components/ui/button";
import { Switch } from "@/components/ui/switch";
import type {
  HomeConfigInfo,
  HomeFilterSchemaInfo,
  HomeShelf,
  SmartRuleGroup,
} from "@/lib/graphql/types";
import {
  fetchHomeConfig,
  fetchHomeFilterSchema,
  resetDefaultHomeTemplate,
  resetHomeConfig,
  saveDefaultHomeTemplate,
  saveHomeConfig,
} from "@/lib/graphql/viewer";
import { useT } from "@/lib/i18n";
import { HomeAddShelfDialog } from "./HomeAddShelfDialog";
import { HomeShelfInspector } from "./HomeShelfInspector";

interface HomeEditorProps {
  /**
   * Called after a save or reset lands, so the caller can re-read the Home it
   * shows. Use the in-epoch manifest refresh, not a session reload, which
   * would unmount the player.
   */
  onSaved: () => void;
  onClose: () => void;
}

const EMPTY_SCHEMA: HomeFilterSchemaInfo = {
  libraries: [],
  fields: [],
  pinnables: { collections: [], smartCollections: [], savedFilters: [] },
};

function countRules(group?: SmartRuleGroup): number {
  if (!group) return 0;
  let count = 0;
  for (const cond of group.conditions) {
    if (cond.group) count += countRules(cond.group);
    else count += 1;
  }
  return count;
}

function summarizeShelf(shelf: HomeShelf, schema: HomeFilterSchemaInfo): string {
  const parts: string[] = [];
  const libs = shelf.source.libraryIDs ?? [];
  if (libs.length === 0) {
    parts.push("All Home libraries");
  } else {
    const names = libs.map((id) => schema.libraries.find((l) => l.id === id)?.title ?? id);
    parts.push(names.join(", "));
  }
  const ruleCount = countRules(shelf.source.rules);
  if (ruleCount > 0) {
    parts.push(`${ruleCount} ${ruleCount === 1 ? "rule" : "rules"}`);
  }
  if (shelf.source.sort?.by) {
    parts.push(`Sort: ${shelf.source.sort.by}`);
  }
  parts.push(`${shelf.display.style} · Max ${shelf.display.limit}`);
  return parts.join(" · ");
}

export function HomeEditor({ onSaved, onClose }: HomeEditorProps) {
  const t = useT();
  const [templateMode, setTemplateMode] = useState(false);
  const [config, setConfig] = useState<HomeConfigInfo | null>(null);
  const [schema, setSchema] = useState<HomeFilterSchemaInfo>(EMPTY_SCHEMA);
  const [dragging, setDragging] = useState<string | null>(null);
  const [editingShelfID, setEditingShelfID] = useState<string | null>(null);
  const [showAddDialog, setShowAddDialog] = useState(false);
  const [saving, setSaving] = useState(false);
  const [failed, setFailed] = useState(false);

  useEffect(() => {
    const controller = new AbortController();
    setConfig(null);
    setFailed(false);
    Promise.all([
      fetchHomeConfig(templateMode, controller.signal),
      fetchHomeFilterSchema(undefined, controller.signal).catch(() => EMPTY_SCHEMA),
    ]).then(
      ([cfg, sch]) => {
        if (controller.signal.aborted) return;
        setConfig(cfg);
        setSchema(sch);
      },
      () => {
        if (!controller.signal.aborted) setFailed(true);
      },
    );
    return () => controller.abort();
  }, [templateMode]);

  const shelves = config?.shelves ?? null;

  function updateShelves(next: HomeShelf[]) {
    if (!config) return;
    setConfig({ ...config, shelves: next });
  }

  function move(from: number, to: number) {
    if (!shelves || saving || to < 0 || to >= shelves.length || from === to) return;
    const next = [...shelves];
    const [shelf] = next.splice(from, 1);
    next.splice(to, 0, shelf);
    updateShelves(next);
  }

  function toggle(id: string, visible: boolean) {
    if (!shelves || saving) return;
    updateShelves(shelves.map((s) => (s.id === id ? { ...s, hidden: !visible } : s)));
  }

  function duplicateShelf(shelf: HomeShelf) {
    if (!shelves || saving) return;
    const copy: HomeShelf = {
      ...structuredClone(shelf),
      id: "hs_" + Math.random().toString(16).slice(2, 14),
      title: `${shelf.title} (Copy)`,
    };
    const idx = shelves.findIndex((s) => s.id === shelf.id);
    const next = [...shelves];
    next.splice(idx + 1, 0, copy);
    updateShelves(next);
  }

  function removeShelf(id: string) {
    if (!shelves || saving) return;
    updateShelves(shelves.filter((s) => s.id !== id));
  }

  async function commit(write: () => Promise<unknown>) {
    setSaving(true);
    setFailed(false);
    try {
      await write();
      onSaved();
      onClose();
    } catch {
      setFailed(true);
    } finally {
      setSaving(false);
    }
  }

  function save() {
    if (!config) return;
    const payload = { version: 1, hero: config.hero, shelves: config.shelves };
    void commit(() => (templateMode ? saveDefaultHomeTemplate(payload) : saveHomeConfig(payload)));
  }

  function reset() {
    void commit(() => (templateMode ? resetDefaultHomeTemplate() : resetHomeConfig()));
  }

  function onRowKeyDown(event: KeyboardEvent, index: number) {
    if (!event.altKey) return;
    if (event.key === "ArrowUp") {
      event.preventDefault();
      move(index, index - 1);
    } else if (event.key === "ArrowDown") {
      event.preventDefault();
      move(index, index + 1);
    }
  }

  const editingShelf =
    editingShelfID === "__hero__" && config
      ? {
          id: "__hero__",
          title: t("homeHeroTitle"),
          hidden: false,
          hideWhenEmpty: false,
          display: { style: "shelf" as const, artworkSlot: "landscape" as const, limit: 4 },
          source: config.hero.source ?? { type: "builtin" as const, builtinKind: "recently-added" },
        }
      : (shelves?.find((s) => s.id === editingShelfID) ?? null);

  return (
    <section aria-labelledby="home-editor-title" className="flex max-w-3xl flex-col gap-4">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div>
          <h2 id="home-editor-title" className="text-xl font-semibold">
            {t("homeStudioTitle")}
          </h2>
          <p className="text-sm text-[var(--text-secondary)]">{t("homeStudioHelp")}</p>
        </div>

        {config?.canEditTemplate && (
          <div className="inline-flex rounded-md border border-[var(--border)] p-0.5 text-xs">
            <button
              type="button"
              onClick={() => setTemplateMode(false)}
              className={
                "rounded px-3 py-1.5 font-medium transition-colors " +
                (!templateMode
                  ? "bg-primary text-primary-foreground"
                  : "text-[var(--text-secondary)] hover:text-foreground")
              }
            >
              {t("homeScopeMine")}
            </button>
            <button
              type="button"
              onClick={() => setTemplateMode(true)}
              className={
                "rounded px-3 py-1.5 font-medium transition-colors " +
                (templateMode
                  ? "bg-primary text-primary-foreground"
                  : "text-[var(--text-secondary)] hover:text-foreground")
              }
            >
              {t("homeScopeTemplate")}
            </button>
          </div>
        )}
      </div>

      {failed && (
        <p role="alert" className="text-sm text-destructive">
          {t("homeEditFailed")}
        </p>
      )}

      {config === null ? (
        !failed && <p aria-busy="true">{t("loading")}</p>
      ) : (
        <>
          {/* Hero Banner Control */}
          <div className="flex flex-wrap items-center justify-between gap-3 rounded-lg border border-[var(--border)] bg-muted/15 px-4 py-3">
            <div className="flex items-center gap-3">
              <Sparkles className="size-4 text-primary" aria-hidden />
              <div>
                <div className="text-sm font-medium">{t("homeHeroTitle")}</div>
                <div className="text-xs text-[var(--text-secondary)]">
                  {!config.hero.enabled
                    ? "Hidden on Home"
                    : config.hero.mode === "shelf"
                      ? t("homeHeroModeShelf")
                      : t("homeHeroModeAuto")}
                </div>
              </div>
            </div>
            <div className="flex flex-wrap items-center gap-2">
              {config.hero.enabled && (
                <>
                  <select
                    aria-label="Hero mode"
                    value={config.hero.mode}
                    disabled={saving}
                    onChange={(e) => {
                      const mode = e.target.value as "auto" | "shelf";
                      setConfig({
                        ...config,
                        hero: {
                          ...config.hero,
                          mode,
                          source:
                            mode === "shelf"
                              ? (config.hero.source ?? {
                                  type: "builtin",
                                  builtinKind: "recently-added",
                                })
                              : config.hero.source,
                        },
                      });
                    }}
                    className="h-8 rounded border border-[var(--border)] bg-background px-2 text-xs"
                  >
                    <option value="auto">{t("homeHeroModeAuto")}</option>
                    <option value="shelf">{t("homeHeroModeShelf")}</option>
                  </select>
                  {config.hero.mode === "shelf" && (
                    <Button
                      type="button"
                      variant="outline"
                      size="sm"
                      disabled={saving}
                      onClick={() => setEditingShelfID("__hero__")}
                    >
                      <Pencil className="mr-1 size-3.5" aria-hidden />
                      {t("homeHeroEditSource")}
                    </Button>
                  )}
                </>
              )}
              <Switch
                checked={config.hero.enabled}
                disabled={saving}
                aria-label={t("homeHeroShow")}
                onCheckedChange={(enabled) =>
                  setConfig({
                    ...config,
                    hero: { ...config.hero, enabled },
                  })
                }
              />
            </div>
          </div>

          {/* Shelf List */}
          <ul className="flex flex-col gap-2">
            {config.shelves.map((shelf, index) => {
              const name = shelf.title || shelf.id;
              return (
                <li
                  key={shelf.id}
                  data-section-id={shelf.id}
                  className={
                    "flex items-center gap-3 rounded-lg border border-[var(--border)] px-3 py-2.5" +
                    (shelf.hidden ? " opacity-60" : "") +
                    (dragging === shelf.id ? " ring-2 ring-primary" : "")
                  }
                  draggable={!saving}
                  onDragStart={(event) => {
                    setDragging(shelf.id);
                    event.dataTransfer?.setData("text/plain", shelf.id);
                    if (event.dataTransfer) event.dataTransfer.effectAllowed = "move";
                  }}
                  onDragOver={(event) => {
                    if (dragging) event.preventDefault();
                  }}
                  onDrop={(event) => {
                    event.preventDefault();
                    if (dragging)
                      move(
                        config.shelves.findIndex((s) => s.id === dragging),
                        index,
                      );
                    setDragging(null);
                  }}
                  onDragEnd={() => setDragging(null)}
                  onKeyDown={(event) => onRowKeyDown(event, index)}
                >
                  <GripVertical aria-hidden className="size-4 shrink-0 cursor-grab" />
                  <div className="min-w-0 flex-1">
                    <div className="flex items-center gap-2">
                      <span className="truncate font-medium">{name}</span>
                      <span className="rounded bg-muted px-1.5 py-0.5 text-[10px] uppercase tracking-wide text-[var(--text-secondary)]">
                        {shelf.source.type}
                      </span>
                    </div>
                    <p className="truncate text-xs text-[var(--text-secondary)]">
                      {summarizeShelf(shelf, schema)}
                    </p>
                  </div>
                  <Switch
                    checked={!shelf.hidden}
                    disabled={saving}
                    aria-label={t("homeEditVisible", { name })}
                    onCheckedChange={(visible) => toggle(shelf.id, visible)}
                  />
                  <Button
                    type="button"
                    variant="ghost"
                    size="icon-sm"
                    aria-label={t("homeEditShelf", { name })}
                    disabled={saving}
                    onClick={() => setEditingShelfID(shelf.id)}
                  >
                    <Pencil aria-hidden />
                  </Button>
                  <Button
                    type="button"
                    variant="ghost"
                    size="icon-sm"
                    aria-label={t("homeDuplicateShelf", { name })}
                    disabled={saving}
                    onClick={() => duplicateShelf(shelf)}
                  >
                    <Copy aria-hidden />
                  </Button>
                  <Button
                    type="button"
                    variant="ghost"
                    size="icon-sm"
                    aria-label={t("homeDeleteShelf", { name })}
                    disabled={saving}
                    onClick={() => removeShelf(shelf.id)}
                  >
                    <Trash2 aria-hidden />
                  </Button>
                  <Button
                    type="button"
                    variant="ghost"
                    size="icon-sm"
                    aria-label={t("homeEditMoveUp", { name })}
                    disabled={saving || index === 0}
                    onClick={() => move(index, index - 1)}
                  >
                    <ChevronUp aria-hidden />
                  </Button>
                  <Button
                    type="button"
                    variant="ghost"
                    size="icon-sm"
                    aria-label={t("homeEditMoveDown", { name })}
                    disabled={saving || index === config.shelves.length - 1}
                    onClick={() => move(index, index + 1)}
                  >
                    <ChevronDown aria-hidden />
                  </Button>
                </li>
              );
            })}
          </ul>
        </>
      )}

      <div className="flex flex-wrap items-center gap-2">
        <Button
          type="button"
          variant="outline"
          disabled={saving || config === null}
          onClick={() => setShowAddDialog(true)}
        >
          <Plus className="mr-1.5 size-4" aria-hidden />
          {t("homeAddShelf")}
        </Button>
        <Button disabled={saving || config === null} onClick={save}>
          {t("homeEditSave")}
        </Button>
        <Button variant="outline" disabled={saving} onClick={onClose}>
          {t("homeEditCancel")}
        </Button>
        <Button variant="ghost" className="ml-auto" disabled={saving} onClick={reset}>
          {t("homeEditReset")}
        </Button>
      </div>

      {showAddDialog && config && (
        <HomeAddShelfDialog
          schema={schema}
          onClose={() => setShowAddDialog(false)}
          onAddShelves={(added, openInspector) => {
            updateShelves([...config.shelves, ...added]);
            setShowAddDialog(false);
            if (openInspector && added[0]) {
              setEditingShelfID(added[0].id);
            }
          }}
        />
      )}

      {editingShelf && config && (
        <HomeShelfInspector
          shelf={editingShelf}
          schema={schema}
          onCancel={() => setEditingShelfID(null)}
          onApply={(updated) => {
            if (updated.id === "__hero__") {
              setConfig({
                ...config,
                hero: { ...config.hero, mode: "shelf", source: updated.source },
              });
            } else {
              updateShelves(config.shelves.map((s) => (s.id === updated.id ? updated : s)));
            }
            setEditingShelfID(null);
          }}
        />
      )}
    </section>
  );
}
