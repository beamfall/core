package plugin

import "sort"

// RenderingCapabilityVocabularyVersion identifies the first closed vocabulary
// authorized by ADR-0138. It is diagnostic/CI metadata only; runtime
// negotiation is exclusively set intersection over capability IDs.
const RenderingCapabilityVocabularyVersion = "1.0.0"

const (
	RenderingAccentStyle                = "accent-style"
	RenderingActionExecute              = "action-execute"
	RenderingCertificationFilter        = "certification-filter"
	RenderingConfigDate                 = "config-date"
	RenderingConfigFlag                 = "config-flag"
	RenderingConfigNumber               = "config-number"
	RenderingConfigText                 = "config-text"
	RenderingConfigURL                  = "config-url"
	RenderingEntityCategory             = "entity-category"
	RenderingEntityContainer            = "entity-container"
	RenderingEntityPerson               = "entity-person"
	RenderingFamilyScopePick            = "familyscope-pick"
	RenderingFieldDate                  = "field-date"
	RenderingFieldFlag                  = "field-flag"
	RenderingFieldNumber                = "field-number"
	RenderingFieldText                  = "field-text"
	RenderingFieldURL                   = "field-url"
	RenderingImporterConfigure          = "importer-configure"
	RenderingLayoutBrowse               = "layout-browse"
	RenderingLayoutDetail               = "layout-detail"
	RenderingLayoutShapeCollection      = "layout-shape-collection"
	RenderingLayoutShapeContainer       = "layout-shape-container"
	RenderingLayoutShapeItem            = "layout-shape-item"
	RenderingLayoutShapeOrderedChildren = "layout-shape-ordered-children"
	RenderingLayoutShapePerson          = "layout-shape-person"
	RenderingMediaClassSubdivide        = "mediaclass-subdivide"
	RenderingPermissionReview           = "permission-review"
	RenderingPlaybackAirPlay            = "playback-airplay"
	RenderingPlaybackDirectPlay         = "playback-direct-play"
	RenderingPlaybackResume             = "playback-resume"
	RenderingSectionFilterBar           = "section-filter-bar"
	RenderingSectionGrid                = "section-grid"
	RenderingSectionHero                = "section-hero"
	RenderingSectionInfo                = "section-info"
	RenderingSectionList                = "section-list"
	RenderingSectionPlaybackAffordances = "section-playback-affordances"
	RenderingSectionShelf               = "section-shelf"
	RenderingSidecarInteractive         = "sidecar-interactive"
)

var renderingCapabilityVocabulary = []string{
	RenderingAccentStyle,
	RenderingActionExecute,
	RenderingCertificationFilter,
	RenderingConfigDate,
	RenderingConfigFlag,
	RenderingConfigNumber,
	RenderingConfigText,
	RenderingConfigURL,
	RenderingEntityCategory,
	RenderingEntityContainer,
	RenderingEntityPerson,
	RenderingFamilyScopePick,
	RenderingFieldDate,
	RenderingFieldFlag,
	RenderingFieldNumber,
	RenderingFieldText,
	RenderingFieldURL,
	RenderingImporterConfigure,
	RenderingLayoutBrowse,
	RenderingLayoutDetail,
	RenderingLayoutShapeCollection,
	RenderingLayoutShapeContainer,
	RenderingLayoutShapeItem,
	RenderingLayoutShapeOrderedChildren,
	RenderingLayoutShapePerson,
	RenderingMediaClassSubdivide,
	RenderingPermissionReview,
	RenderingPlaybackAirPlay,
	RenderingPlaybackDirectPlay,
	RenderingPlaybackResume,
	RenderingSectionFilterBar,
	RenderingSectionGrid,
	RenderingSectionHero,
	RenderingSectionInfo,
	RenderingSectionList,
	RenderingSectionPlaybackAffordances,
	RenderingSectionShelf,
	RenderingSidecarInteractive,
}

// RenderingCapabilities returns a sorted defensive copy of the core-owned
// Tier-1 vocabulary. Plugins and clients may select from it but cannot extend
// it.
func RenderingCapabilities() []string {
	return append([]string(nil), renderingCapabilityVocabulary...)
}

// UsedRenderingCapabilities derives the closed capability set a Bundle uses.
// Authors do not maintain a parallel "used" declaration.
func UsedRenderingCapabilities(bundle Bundle) []string {
	used := make(map[string]struct{})
	addWhenPopulated(used, RenderingAccentStyle, bundle.Accent != "")
	addWhenPopulated(used, RenderingActionExecute, len(bundle.Actions) != 0)
	addWhenPopulated(used, RenderingCertificationFilter, len(bundle.CertificationScale) != 0)
	addWhenPopulated(used, RenderingFamilyScopePick, len(bundle.FamilyScopes) != 0)
	addWhenPopulated(used, RenderingImporterConfigure, len(bundle.Importers) != 0)
	addWhenPopulated(used, RenderingMediaClassSubdivide, len(bundle.MediaClassLabels) != 0)
	addWhenPopulated(used, RenderingPermissionReview, len(bundle.Permissions) != 0)
	addWhenPopulated(used, RenderingSidecarInteractive, len(bundle.InteractiveSidecars) != 0)

	for _, field := range bundle.ConfigFields {
		addConfigFieldCapability(used, field.Type)
	}
	for _, action := range bundle.Actions {
		for _, field := range action.InputFields {
			addConfigFieldCapability(used, field.Type)
		}
	}
	for _, field := range bundle.MediaFields {
		addTypedFieldCapability(used, field.Type)
	}
	for _, kind := range bundle.EntityKinds {
		addEntityCapability(used, kind.Archetype)
		for _, field := range kind.Fields {
			addTypedFieldCapability(used, field.Type)
		}
	}
	for _, layout := range bundle.Layouts {
		addLayoutRoleCapability(used, layout.Role)
		addLayoutShapeCapability(used, layout.Archetype)
		for _, section := range layout.Sections {
			addSectionCapability(used, section.Kind)
			for _, affordance := range section.PlaybackAffordances {
				addPlaybackCapability(used, affordance)
			}
		}
	}

	out := make([]string, 0, len(used))
	for capability := range used {
		out = append(out, capability)
	}
	sort.Strings(out)
	return out
}

func addWhenPopulated(used map[string]struct{}, capability string, populated bool) {
	if populated {
		used[capability] = struct{}{}
	}
}

func addConfigFieldCapability(used map[string]struct{}, kind string) {
	capabilities := map[string]string{
		"date": RenderingConfigDate, "flag": RenderingConfigFlag, "number": RenderingConfigNumber,
		"text": RenderingConfigText, "url": RenderingConfigURL,
	}
	addKnownDiscriminator(used, capabilities, kind)
}

func addTypedFieldCapability(used map[string]struct{}, kind string) {
	capabilities := map[string]string{
		"date": RenderingFieldDate, "flag": RenderingFieldFlag, "number": RenderingFieldNumber,
		"text": RenderingFieldText, "url": RenderingFieldURL,
	}
	addKnownDiscriminator(used, capabilities, kind)
}

func addEntityCapability(used map[string]struct{}, archetype string) {
	addKnownDiscriminator(used, map[string]string{
		"category":  RenderingEntityCategory,
		"container": RenderingEntityContainer,
		"person":    RenderingEntityPerson,
	}, archetype)
}

func addLayoutRoleCapability(used map[string]struct{}, role string) {
	addKnownDiscriminator(used, map[string]string{
		"browse": RenderingLayoutBrowse,
		"detail": RenderingLayoutDetail,
	}, role)
}

func addLayoutShapeCapability(used map[string]struct{}, archetype string) {
	addKnownDiscriminator(used, map[string]string{
		"collection":       RenderingLayoutShapeCollection,
		"container":        RenderingLayoutShapeContainer,
		"item":             RenderingLayoutShapeItem,
		"ordered-children": RenderingLayoutShapeOrderedChildren,
		"person":           RenderingLayoutShapePerson,
	}, archetype)
}

func addSectionCapability(used map[string]struct{}, kind string) {
	addKnownDiscriminator(used, map[string]string{
		"filter-bar":           RenderingSectionFilterBar,
		"grid":                 RenderingSectionGrid,
		"hero":                 RenderingSectionHero,
		"info":                 RenderingSectionInfo,
		"list":                 RenderingSectionList,
		"playback-affordances": RenderingSectionPlaybackAffordances,
		"shelf":                RenderingSectionShelf,
	}, kind)
}

func addPlaybackCapability(used map[string]struct{}, affordance string) {
	addKnownDiscriminator(used, map[string]string{
		"airplay":     RenderingPlaybackAirPlay,
		"direct-play": RenderingPlaybackDirectPlay,
		"resume":      RenderingPlaybackResume,
	}, affordance)
}

func addKnownDiscriminator(used map[string]struct{}, capabilities map[string]string, value string) {
	if capability, ok := capabilities[value]; ok {
		used[capability] = struct{}{}
	}
}

// KnownRenderingCapability reports closed-vocabulary membership.
func KnownRenderingCapability(capability string) bool {
	i := sort.SearchStrings(renderingCapabilityVocabulary, capability)
	return i < len(renderingCapabilityVocabulary) && renderingCapabilityVocabulary[i] == capability
}
