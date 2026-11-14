import { Blocks, Clapperboard, Film, type LucideIcon, Tv } from "lucide-react";
import { displayString } from "@/lib/display";
import type { DisplayString, PluginSummary } from "@/lib/graphql/types";
import { libraryIcon } from "@/lib/libraryIcons";
import type { StringKey } from "@/strings";

// The media types a new Library can be (CORE-1156). One list for every surface
// that creates a Library (first-run setup, Console > Libraries), built from the
// plugins the server reports: one type per plugin, labelled with the plugin's
// own display name, except that a plugin with family scopes offers one type
// per scope. The server does not publish family scopes yet, so the scopes of
// the one first-party plugin that declares them are mirrored here as presets;
// a plugin that does publish `familyScopes` is read from the server instead.

/** A plugin family scope (ADR-0028 §1), as a plugin declares it. */
export interface FamilyScopeSummary {
  value: string;
  displayName: DisplayString;
}

/** The plugin list as this module reads it; `familyScopes` once the server publishes it. */
export type LibraryTypePlugin = PluginSummary & { familyScopes?: FamilyScopeSummary[] | null };

export interface LibraryTypeOption {
  /** Stable option id: the plugin id, or `pluginID/scope` for a scope. */
  key: string;
  pluginID: string;
  /** The family scope this type creates the Library with, or null. */
  familyScope: string | null;
  /** What the type is called. */
  label: string;
  /** The plugin behind it (the same as `label` for a plugin without scopes). */
  pluginLabel: string;
  icon: LucideIcon;
}

interface ScopePreset {
  value: string;
  label: StringKey;
  icon: LucideIcon;
}

// Mirror of the Film & TV plugin's declared family scopes (plugin-filmtv
// bundle.go FamilyScopes), in the order setup has always offered them.
const SCOPE_PRESETS: Record<string, readonly ScopePreset[]> = {
  filmtv: [
    { value: "films", label: "setupTypeFilms", icon: Film },
    { value: "tvshows", label: "setupTypeTVShows", icon: Tv },
    { value: "mixed", label: "setupTypeMixed", icon: Clapperboard },
  ],
};

// Which Library icon (the CORE-1200 vocabulary) stands for each first-party
// plugin. Presentation only: a plugin missing here gets a generic symbol.
const PLUGIN_ICON_NAMES: Record<string, string> = {
  music: "note",
  podcasts: "broadcast",
  photos: "camera",
  audiobook: "headphones",
  familyarchive: "people",
  karaoke: "mic",
};

/** The symbol for a plugin, for lists of plugins and the types they offer. */
export function pluginIcon(pluginID: string): LucideIcon {
  if (pluginID in SCOPE_PRESETS) return Clapperboard;
  return libraryIcon(PLUGIN_ICON_NAMES[pluginID]) ?? Blocks;
}

/** Every media type the given plugins offer, in plugin order. */
export function libraryTypeOptions(
  plugins: readonly LibraryTypePlugin[],
  t: (key: StringKey) => string,
  locale: string,
): LibraryTypeOption[] {
  return plugins.flatMap((plugin): LibraryTypeOption[] => {
    const pluginLabel = displayString(plugin.displayName, locale) || plugin.id;
    const icon = pluginIcon(plugin.id);
    const served = plugin.familyScopes ?? [];
    if (served.length > 0) {
      return served.map((scope) => ({
        key: `${plugin.id}/${scope.value}`,
        pluginID: plugin.id,
        familyScope: scope.value,
        label: displayString(scope.displayName, locale) || scope.value,
        pluginLabel,
        icon: presetFor(plugin.id, scope.value)?.icon ?? icon,
      }));
    }
    const presets = SCOPE_PRESETS[plugin.id];
    if (presets) {
      return presets.map((preset) => ({
        key: `${plugin.id}/${preset.value}`,
        pluginID: plugin.id,
        familyScope: preset.value,
        label: t(preset.label),
        pluginLabel,
        icon: preset.icon,
      }));
    }
    return [
      {
        key: plugin.id,
        pluginID: plugin.id,
        familyScope: null,
        label: pluginLabel,
        pluginLabel,
        icon,
      },
    ];
  });
}

function presetFor(pluginID: string, scope: string): ScopePreset | undefined {
  return SCOPE_PRESETS[pluginID]?.find((preset) => preset.value === scope);
}
