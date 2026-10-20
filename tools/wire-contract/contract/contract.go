package contract

import (
	"sort"

	"github.com/beamfall/core/internal/graph"
	"github.com/beamfall/core/internal/server"
)

type Snapshot struct {
	SnapshotVersion string                    `json:"snapshotVersion"`
	ContractVersion string                    `json:"contractVersion"`
	Fields          []graph.SchemaField       `json:"fields"`
	Nested          []graph.NestedSchemaField `json:"nested"`
	V1              server.V1Contract         `json:"v1"`
	Management      ManagementManifest        `json:"managementManifest"`
	Manifest        ManifestContract          `json:"manifest"`
	PlaybackInfo    PlaybackInfoContract      `json:"playbackInfo"`
	Compatibility   CompatibilityDecisions    `json:"compatibility"`
	KnownDrift      []KnownDrift              `json:"knownDrift"`
}

func Build(version string) Snapshot {
	return BuildWithKnownDrift(version, nil)
}

func BuildWithKnownDrift(version string, drift []KnownDrift) Snapshot {
	graphContract := graph.BuildWireContract(version)
	return Snapshot{
		SnapshotVersion: graphContract.SnapshotVersion,
		ContractVersion: graphContract.ContractVersion,
		Fields:          graphContract.Fields,
		Nested:          graphContract.Nested,
		V1:              server.BuildV1Contract(),
		Management:      buildManagementManifest(),
		Manifest:        buildManifestContract(),
		PlaybackInfo:    buildPlaybackInfoContract(),
		Compatibility:   compatibilityDecisions(),
		KnownDrift:      append([]KnownDrift(nil), drift...),
	}
}

// InventoryField is one producer field in the published field-inventory: the
// top-level wire field a client may consume, with its effect/tier and the
// nested member (child) names the Core emits under it. Clients drive their
// producer-field completeness check (XCONTRACT-10, ADR-0122 Amendment A §6) off
// this list — asserting every producer field is either consumed by a client
// model or on an explicit per-client ignore-list.
type InventoryField struct {
	// Field is the lowercased top-level field name (matches Snapshot.Fields).
	Field string `json:"field"`
	// Effect is "read" or "write" (mirrors graph.SchemaField.Effect).
	Effect string `json:"effect"`
	// Tier is the required authorization tier (mirrors graph.SchemaField.Tier).
	Tier string `json:"tier"`
	// Children are the direct nested member names the Core emits under this
	// field, sorted; empty for scalar result fields. Lets a client's
	// completeness check reason at member granularity, not just field.
	Children []string `json:"children,omitempty"`
}

// FieldInventory is the flat, deterministic producer-field manifest derived from
// the wire contract: every dispatchable top-level field with effect/tier and its
// direct nested member names. It is the single Core-published truth every client
// diffs its consumed+ignored field sets against, so no client can silently drop a
// producer field (the AUDIT-ANDROIDUI-17 failure mode) without the drift showing
// up as an unaccounted field.
type FieldInventory struct {
	SnapshotVersion string           `json:"snapshotVersion"`
	Fields          []InventoryField `json:"fields"`
}

// BuildFieldInventory projects the unversioned wire contract into the flat
// producer-field inventory. It joins the flat field manifest (effect/tier) with
// the reflected nested shapes (child names) on the lowercased field name, so the
// inventory can never diverge from what execute() dispatches and emits.
func BuildFieldInventory() FieldInventory {
	snapshot := Build("")

	childrenByField := make(map[string][]string, len(snapshot.Nested))
	for _, nested := range snapshot.Nested {
		names := make([]string, 0, len(nested.Children))
		for _, child := range nested.Children {
			names = append(names, child.Name)
		}
		sort.Strings(names)
		childrenByField[nested.Field] = names
	}

	fields := make([]InventoryField, 0, len(snapshot.Fields))
	for _, field := range snapshot.Fields {
		fields = append(fields, InventoryField{
			Field:    field.Field,
			Effect:   field.Effect,
			Tier:     field.Tier,
			Children: childrenByField[field.Field],
		})
	}
	sort.Slice(fields, func(i, j int) bool { return fields[i].Field < fields[j].Field })

	return FieldInventory{
		SnapshotVersion: snapshot.SnapshotVersion,
		Fields:          fields,
	}
}
