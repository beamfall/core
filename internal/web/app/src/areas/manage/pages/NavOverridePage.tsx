import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  EmptyLine,
  ErrorLine,
  ManageSection,
  ManageTable,
  StatusBadge,
} from "@/components/beamfall/workbench";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Switch } from "@/components/ui/switch";
import { TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { displayString } from "@/lib/display";
import {
  fetchManifest,
  fetchNavOverride,
  fetchNavOverrideHistory,
  resetNavOverride,
  restoreNavOverride,
  setNavOverride,
} from "@/lib/graphql";
import type {
  Layout,
  NavigationManifest,
  NavigationNode,
  NavOverrideHistoryInfo,
  NavOverrideInfo,
  SectionOverrideInfo,
} from "@/lib/graphql/types";
import { useT } from "@/lib/i18n";
import { formatDateTime } from "../lib/schedule";

// Navigation/Layout override editor (WEB-5J / ADR-0061 §Layout editor /
// ADR-0001 / ADR-0035): the manager surface for SERVER-LEVEL Navigation
// Manifest + Layout overrides. Edit node visibility, labels, and order;
// override the theme accent token; compose per-Layout Section visibility and
// order; and an AUDITED history with restore (HARD invariant #6 — every change
// is snapshotted and recoverable; Reset reverts to plugin/library defaults,
// never destroys plugin data). The override blob is a read-modify-write patch
// against the live manifest; the manifest itself supplies the node/Layout
// catalog. Labels are domain-agnostic (HARD invariant #1).

const DEFAULT_ACCENT = "#2F7DFF";
const EMPTY_OVERRIDE: NavOverrideInfo = {
  hiddenNodeIDs: [],
  nodeOrder: [],
  nodeLabelOverrides: {},
  themeAccentColor: "",
  layoutSectionOverrides: {},
};

// Order the manifest nodes by the override's nodeOrder: listed IDs first (in
// order), then any remaining nodes in their manifest order.
function orderedNodes(nodes: NavigationNode[], order: string[]): NavigationNode[] {
  if (order.length === 0) return nodes;
  const byID = new Map(nodes.map((n) => [n.id, n]));
  const out: NavigationNode[] = [];
  const seen = new Set<string>();
  for (const id of order) {
    const node = byID.get(id);
    if (node && !seen.has(id)) {
      out.push(node);
      seen.add(id);
    }
  }
  for (const node of nodes) {
    if (!seen.has(node.id)) out.push(node);
  }
  return out;
}

// Move the element at `index` by `delta` (clamped), returning a new array.
function moved<T>(list: T[], index: number, delta: number): T[] {
  const next = index + delta;
  if (next < 0 || next >= list.length) return list;
  const copy = [...list];
  const [item] = copy.splice(index, 1);
  copy.splice(next, 0, item);
  return copy;
}

export function NavOverridePage() {
  const t = useT();
  const [manifest, setManifest] = useState<NavigationManifest | null>(null);
  const [override, setOverride] = useState<NavOverrideInfo>(EMPTY_OVERRIDE);
  const [history, setHistory] = useState<NavOverrideHistoryInfo[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");

  const load = useCallback(async () => {
    setError("");
    setLoading(true);
    try {
      const [doc, ov, hist] = await Promise.all([
        fetchManifest(),
        fetchNavOverride(),
        fetchNavOverrideHistory(),
      ]);
      setManifest(doc);
      setOverride(ov);
      setHistory(hist);
    } catch {
      setError(t("navOverrideUnavailable"));
    } finally {
      setLoading(false);
    }
  }, [t]);

  useEffect(() => {
    void load();
  }, [load]);

  // Apply a patch and refresh local state from the server's response, then
  // re-pull history so a new snapshot row appears. Patches are read-modify-
  // write over `override`, so one is in flight at a time: a second click
  // before the first response lands would be computed from stale state and
  // silently undo the first.
  const patchInFlight = useRef(false);
  const apply = useCallback(
    async (patch: Parameters<typeof setNavOverride>[0]) => {
      if (patchInFlight.current) return;
      patchInFlight.current = true;
      setError("");
      try {
        setOverride(await setNavOverride(patch));
        setHistory(await fetchNavOverrideHistory());
      } catch {
        setError(t("navOverrideUnavailable"));
      } finally {
        patchInFlight.current = false;
      }
    },
    [t],
  );

  const onReset = useCallback(async () => {
    setError("");
    try {
      setOverride(await resetNavOverride());
      setHistory(await fetchNavOverrideHistory());
    } catch {
      setError(t("navOverrideUnavailable"));
    }
  }, [t]);

  const onRestore = useCallback(
    async (id: number) => {
      setError("");
      try {
        setOverride(await restoreNavOverride(id));
        setHistory(await fetchNavOverrideHistory());
      } catch {
        setError(t("navOverrideUnavailable"));
      }
    },
    [t],
  );

  const isOverridden = useMemo(
    () =>
      override.hiddenNodeIDs.length > 0 ||
      override.nodeOrder.length > 0 ||
      Object.keys(override.nodeLabelOverrides).length > 0 ||
      override.themeAccentColor !== "" ||
      Object.keys(override.layoutSectionOverrides).length > 0,
    [override],
  );

  return (
    <div className="space-y-4">
      <ManageSection
        title={t("navOverrideHeading")}
        lead={t("navOverrideLead")}
        actions={
          <div className="flex items-center gap-2">
            {isOverridden && <StatusBadge state="amber">{t("navOverrideActive")}</StatusBadge>}
            <Button type="button" variant="outline" size="sm" onClick={load}>
              {t("refresh")}
            </Button>
          </div>
        }
      >
        <NodesPanel
          nodes={manifest?.navigation ?? []}
          override={override}
          loading={loading}
          onApply={apply}
        />
        <ErrorLine message={error} />
      </ManageSection>

      <ThemePanel override={override} onApply={apply} />

      <SectionsPanel layouts={manifest?.layouts ?? []} override={override} onApply={apply} />

      <HistoryPanel
        history={history}
        canReset={isOverridden}
        onReset={onReset}
        onRestore={onRestore}
      />
    </div>
  );
}

function NodesPanel({
  nodes,
  override,
  loading,
  onApply,
}: {
  nodes: NavigationNode[];
  override: NavOverrideInfo;
  loading: boolean;
  onApply: (patch: Parameters<typeof setNavOverride>[0]) => Promise<void>;
}) {
  const t = useT();
  const [editing, setEditing] = useState<string | null>(null);
  const [editLabel, setEditLabel] = useState("");

  const hidden = useMemo(() => new Set(override.hiddenNodeIDs), [override.hiddenNodeIDs]);
  const ordered = useMemo(
    () => orderedNodes(nodes, override.nodeOrder),
    [nodes, override.nodeOrder],
  );

  const toggleHidden = (id: string) => {
    const next = new Set(hidden);
    if (next.has(id)) next.delete(id);
    else next.add(id);
    return onApply({ hiddenNodeIDs: [...next] });
  };

  const move = (index: number, delta: number) => {
    const next = moved(ordered, index, delta).map((n) => n.id);
    return onApply({ nodeOrder: next });
  };

  const beginEdit = (node: NavigationNode) => {
    setEditing(node.id);
    setEditLabel(override.nodeLabelOverrides[node.id]?.en ?? "");
  };

  const saveLabel = (id: string) => {
    const trimmed = editLabel.trim();
    const next = { ...override.nodeLabelOverrides };
    if (trimmed) next[id] = { ...(next[id] ?? {}), en: trimmed };
    else delete next[id];
    setEditing(null);
    return onApply({ nodeLabelOverrides: next });
  };

  if (!loading && ordered.length === 0) {
    return <EmptyLine message={t("navOverrideNoNodes")} />;
  }

  return (
    <div className="space-y-1">
      {ordered.map((node, index) => {
        const isHidden = hidden.has(node.id);
        const labelOverride = override.nodeLabelOverrides[node.id]?.en;
        return editing === node.id ? (
          <div key={node.id} className="flex flex-wrap items-end gap-2 py-1">
            <Input
              aria-label={t("navOverrideLabelFor")}
              value={editLabel}
              autoComplete="off"
              spellCheck={false}
              placeholder={displayString(node.title)}
              onChange={(e) => setEditLabel(e.target.value)}
              className="w-56"
            />
            <Button type="button" size="sm" onClick={() => saveLabel(node.id)}>
              {t("navOverrideSaveLabel")}
            </Button>
            <Button type="button" size="sm" variant="outline" onClick={() => setEditing(null)}>
              {t("cancel")}
            </Button>
          </div>
        ) : (
          <div
            key={node.id}
            className="flex items-center gap-3 rounded-md px-2 py-1.5 hover:bg-muted/50"
          >
            <Switch
              checked={!isHidden}
              onCheckedChange={() => void toggleHidden(node.id)}
              aria-label={`${t("navOverrideShow")} ${displayString(node.title)}`}
            />
            <span className="min-w-0 flex-1 text-sm">
              <span className={isHidden ? "text-muted-foreground line-through" : ""}>
                {labelOverride ?? displayString(node.title)}
              </span>
              <span className="ml-2 text-xs text-muted-foreground">{node.id}</span>
              {labelOverride && (
                <span className="ml-2 text-xs text-muted-foreground">
                  ({t("navOverrideRelabeled")})
                </span>
              )}
            </span>
            <span className="flex shrink-0 gap-1.5">
              <Button
                type="button"
                size="sm"
                variant="ghost"
                aria-label={t("navOverrideMoveUp")}
                disabled={index === 0}
                onClick={() => void move(index, -1)}
              >
                ↑
              </Button>
              <Button
                type="button"
                size="sm"
                variant="ghost"
                aria-label={t("navOverrideMoveDown")}
                disabled={index === ordered.length - 1}
                onClick={() => void move(index, 1)}
              >
                ↓
              </Button>
              <Button type="button" size="sm" variant="ghost" onClick={() => beginEdit(node)}>
                {t("navOverrideRelabel")}
              </Button>
            </span>
          </div>
        );
      })}
    </div>
  );
}

function ThemePanel({
  override,
  onApply,
}: {
  override: NavOverrideInfo;
  onApply: (patch: Parameters<typeof setNavOverride>[0]) => Promise<void>;
}) {
  const t = useT();
  const [accent, setAccent] = useState(override.themeAccentColor);

  // Keep the field in sync when the override changes underneath (load/restore).
  useEffect(() => setAccent(override.themeAccentColor), [override.themeAccentColor]);

  const effective = override.themeAccentColor || DEFAULT_ACCENT;
  const valid = /^#[0-9a-fA-F]{6}$/.test(accent.trim());

  return (
    <ManageSection
      title={t("navOverrideThemeHeading")}
      lead={t("navOverrideThemeLead")}
      actions={
        override.themeAccentColor ? (
          <StatusBadge state="green">{t("navOverrideCustomAccent")}</StatusBadge>
        ) : undefined
      }
    >
      <div className="flex flex-wrap items-end gap-3">
        <span
          className="h-7 w-7 shrink-0 rounded-md border"
          style={{ background: effective }}
          aria-hidden
        />
        <div className="grow space-y-1">
          <Label htmlFor="nav-accent">{t("navOverrideAccentLabel")}</Label>
          <Input
            id="nav-accent"
            value={accent}
            autoComplete="off"
            spellCheck={false}
            placeholder={DEFAULT_ACCENT}
            onChange={(e) => setAccent(e.target.value)}
            className="w-40"
          />
        </div>
        <Button
          type="button"
          disabled={!valid || accent.trim() === override.themeAccentColor}
          onClick={() => void onApply({ themeAccentColor: accent.trim() })}
        >
          {t("navOverrideApplyAccent")}
        </Button>
        <Button
          type="button"
          variant="outline"
          size="sm"
          disabled={!override.themeAccentColor}
          onClick={() => void onApply({ themeAccentColor: "" })}
        >
          {t("navOverrideUseDefault")}
        </Button>
      </div>
    </ManageSection>
  );
}

function SectionsPanel({
  layouts,
  override,
  onApply,
}: {
  layouts: Layout[];
  override: NavOverrideInfo;
  onApply: (patch: Parameters<typeof setNavOverride>[0]) => Promise<void>;
}) {
  const t = useT();
  const [layoutID, setLayoutID] = useState("");

  // Default the selector to the first Layout once the manifest arrives.
  useEffect(() => {
    if (!layoutID && layouts.length > 0) setLayoutID(layouts[0].id);
  }, [layouts, layoutID]);

  const layout = layouts.find((l) => l.id === layoutID);
  const sectionOverride: SectionOverrideInfo = override.layoutSectionOverrides[layoutID] ?? {
    hiddenSectionIDs: [],
    sectionOrder: [],
  };
  const hidden = new Set(sectionOverride.hiddenSectionIDs);

  // Order the Layout's sections by the override's per-Layout sectionOrder.
  const ordered = useMemo(() => {
    const sections = layout?.sections ?? [];
    const order = sectionOverride.sectionOrder;
    if (order.length === 0) return sections;
    const byID = new Map(sections.map((s) => [s.id, s]));
    const out: typeof sections = [];
    const seen = new Set<string>();
    for (const id of order) {
      const s = byID.get(id);
      if (s && !seen.has(id)) {
        out.push(s);
        seen.add(id);
      }
    }
    for (const s of sections) if (!seen.has(s.id)) out.push(s);
    return out;
  }, [layout, sectionOverride.sectionOrder]);

  const patchLayout = (next: SectionOverrideInfo) => {
    const map = { ...override.layoutSectionOverrides };
    if (next.hiddenSectionIDs.length === 0 && next.sectionOrder.length === 0) {
      delete map[layoutID];
    } else {
      map[layoutID] = next;
    }
    return onApply({ layoutSectionOverrides: map });
  };

  const toggleHidden = (id: string) => {
    const next = new Set(hidden);
    if (next.has(id)) next.delete(id);
    else next.add(id);
    return patchLayout({ ...sectionOverride, hiddenSectionIDs: [...next] });
  };

  const move = (index: number, delta: number) => {
    const order = moved(ordered, index, delta).map((s) => s.id);
    return patchLayout({ ...sectionOverride, sectionOrder: order });
  };

  return (
    <ManageSection
      title={t("navOverrideSectionsHeading")}
      lead={t("navOverrideSectionsLead")}
      actions={
        layouts.length > 0 ? (
          <Select value={layoutID} onValueChange={setLayoutID}>
            <SelectTrigger className="w-48" aria-label={t("navOverrideLayout")}>
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {layouts.map((l) => (
                <SelectItem key={l.id} value={l.id}>
                  {l.id}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        ) : undefined
      }
    >
      {ordered.length === 0 ? (
        <EmptyLine message={t("navOverrideNoSections")} />
      ) : (
        <div className="space-y-1">
          {ordered.map((section, index) => {
            const isHidden = hidden.has(section.id);
            return (
              <div
                key={section.id}
                className="flex items-center gap-3 rounded-md px-2 py-1.5 hover:bg-muted/50"
              >
                <Switch
                  checked={!isHidden}
                  onCheckedChange={() => void toggleHidden(section.id)}
                  aria-label={`${t("navOverrideShow")} ${displayString(section.title)}`}
                />
                <span className="min-w-0 flex-1 text-sm">
                  <span className={isHidden ? "text-muted-foreground line-through" : ""}>
                    {displayString(section.title) || section.id}
                  </span>
                  <span className="ml-2 text-xs text-muted-foreground">{section.id}</span>
                </span>
                <span className="flex shrink-0 gap-1.5">
                  <Button
                    type="button"
                    size="sm"
                    variant="ghost"
                    aria-label={t("navOverrideMoveUp")}
                    disabled={index === 0}
                    onClick={() => void move(index, -1)}
                  >
                    ↑
                  </Button>
                  <Button
                    type="button"
                    size="sm"
                    variant="ghost"
                    aria-label={t("navOverrideMoveDown")}
                    disabled={index === ordered.length - 1}
                    onClick={() => void move(index, 1)}
                  >
                    ↓
                  </Button>
                </span>
              </div>
            );
          })}
        </div>
      )}
    </ManageSection>
  );
}

// Summarize an override snapshot for the audit table — counts, not the full
// blob, so the row stays scannable.
function summarize(t: ReturnType<typeof useT>, data: NavOverrideInfo): string {
  const parts: string[] = [];
  if (data.hiddenNodeIDs.length > 0)
    parts.push(`${data.hiddenNodeIDs.length} ${t("navOverrideHiddenCount")}`);
  if (data.nodeOrder.length > 0) parts.push(t("navOverrideReordered"));
  const relabels = Object.keys(data.nodeLabelOverrides).length;
  if (relabels > 0) parts.push(`${relabels} ${t("navOverrideRelabelCount")}`);
  if (data.themeAccentColor) parts.push(t("navOverrideCustomAccent"));
  const layouts = Object.keys(data.layoutSectionOverrides).length;
  if (layouts > 0) parts.push(`${layouts} ${t("navOverrideLayoutCount")}`);
  return parts.length > 0 ? parts.join(" · ") : t("navOverrideDefaults");
}

function HistoryPanel({
  history,
  canReset,
  onReset,
  onRestore,
}: {
  history: NavOverrideHistoryInfo[];
  canReset: boolean;
  onReset: () => Promise<void>;
  onRestore: (id: number) => Promise<void>;
}) {
  const t = useT();
  return (
    <ManageSection
      title={t("navOverrideHistoryHeading")}
      lead={t("navOverrideHistoryLead")}
      actions={
        <Button
          type="button"
          variant="outline"
          size="sm"
          disabled={!canReset}
          onClick={() => void onReset()}
        >
          {t("navOverrideReset")}
        </Button>
      }
    >
      {history.length === 0 ? (
        <EmptyLine message={t("navOverrideNoHistory")} />
      ) : (
        <ManageTable>
          <TableHeader>
            <TableRow>
              <TableHead>{t("navOverrideChangedAt")}</TableHead>
              <TableHead>{t("navOverrideSummary")}</TableHead>
              <TableHead className="text-right">{t("navOverrideRestore")}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {history.map((entry) => (
              <TableRow key={entry.id}>
                <TableCell className="manage-cell-num">{formatDateTime(entry.createdAt)}</TableCell>
                <TableCell className="manage-cell-primary">{summarize(t, entry.data)}</TableCell>
                <TableCell className="text-right">
                  <Button
                    type="button"
                    variant="outline"
                    size="sm"
                    onClick={() => void onRestore(entry.id)}
                  >
                    {t("navOverrideRestore")}
                  </Button>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </ManageTable>
      )}
    </ManageSection>
  );
}
