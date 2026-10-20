package plugin

import sdk "github.com/beamfall/plugin-sdk"

type (
	ID                   = sdk.ID
	Bundle               = sdk.Bundle
	LocaleString         = sdk.LocaleString
	LibraryDefaults      = sdk.LibraryDefaults
	Engagement           = sdk.Engagement
	IdentificationPolicy = sdk.IdentificationPolicy
	EntityKind           = sdk.EntityKind
	FieldSpec            = sdk.FieldSpec
	ConfigFieldSpec      = sdk.ConfigFieldSpec
	ConfigOption         = sdk.ConfigOption
	PermissionSpec       = sdk.PermissionSpec
	ActionSpec           = sdk.ActionSpec
	RelationSpec         = sdk.RelationSpec
	LayoutSpec           = sdk.LayoutSpec
	SectionSpec          = sdk.SectionSpec
	FilterOption         = sdk.FilterOption
	SourceSpec           = sdk.SourceSpec
	EntityFilter         = sdk.EntityFilter
	MediaFieldRoute      = sdk.MediaFieldRoute
	ScraperSpec          = sdk.ScraperSpec
	EntityMap            = sdk.EntityMap
	LabelMapSpec         = sdk.LabelMapSpec
	LoginSpec            = sdk.LoginSpec
	ImporterSpec         = sdk.ImporterSpec
	FamilyScopeSpec      = sdk.FamilyScopeSpec
	Candidate            = sdk.Candidate
	ParsedMedia          = sdk.ParsedMedia
	MatchScorer          = sdk.MatchScorer
)

func ArtworkSlots() []string                 { return sdk.ArtworkSlots() }
func Archetypes() []string                   { return sdk.Archetypes() }
func LayoutRoles() []string                  { return sdk.LayoutRoles() }
func LayoutArchetypes() []string             { return sdk.LayoutArchetypes() }
func SectionKinds() []string                 { return sdk.SectionKinds() }
func PlaybackAffordances() []string          { return sdk.PlaybackAffordances() }
func SourceKinds() []string                  { return sdk.SourceKinds() }
func AvailabilityValues() []string           { return sdk.AvailabilityValues() }
func WatchedByAnyProfileValues() []string    { return sdk.WatchedByAnyProfileValues() }
func ValuedSourceKinds() map[string][]string { return sdk.ValuedSourceKinds() }
func FieldTypes() []string                   { return sdk.FieldTypes() }
func AuthStyleForms() []string               { return sdk.AuthStyleForms() }
func TransformKinds() []string               { return sdk.TransformKinds() }
func FilterableSourceKinds() []string        { return sdk.FilterableSourceKinds() }
func PermissionKinds() []string              { return sdk.PermissionKinds() }

const (
	IdentificationPolicyQueue = sdk.IdentificationPolicyQueue
	MediaFieldKeyPrefix       = sdk.MediaFieldKeyPrefix
	MediaKindName             = sdk.MediaKindName
	FeatureAudioStation       = sdk.FeatureAudioStation
)

func L(value string) LocaleString { return sdk.L(value) }

func LocalFileScraperKind(kind string) bool { return sdk.LocalFileScraperKind(kind) }

func ClassifyMediaFieldKey(key string, mediaFields map[string]string) (MediaFieldRoute, error) {
	return sdk.ClassifyMediaFieldKey(key, mediaFields)
}
