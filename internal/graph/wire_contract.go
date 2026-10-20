package graph

// RenderingCapabilityProfile is the client-renderer half of the ADR-0063
// Client Contract. Capability presence bits are authoritative;
// VocabularyVersion is release/conformance metadata only.
type RenderingCapabilityProfile struct {
	VocabularyVersion string   `json:"vocabularyVersion"`
	Capabilities      []string `json:"capabilities"`
}

// ClientContractWireProjection pins the additive ADR-0138 renderingProfile
// JSON shape independently of the compatibility service's internal model.
type ClientContractWireProjection struct {
	RenderingProfile RenderingCapabilityProfile `json:"renderingProfile"`
}

// Versioned wire-contract snapshot.
//
// The Beamfall wire is a hand-rolled, depth-1 GraphQL dispatcher with no SDL —
// the schema is whatever queryFields/mutationFields/adminFields dispatch and
// whatever the result structs serialize. Two deterministic, drift-gated
// projections of that hand-rolled schema already exist:
//
//   - SchemaManifest() (H23-A) — every top-level field with effect + tier.
//   - NestedSchema()   (H23-B) — the reflected nested result shape per field.
//
// WireContract composes both into a single, versioned snapshot document: the
// pinned wire contract a release ships, so a client built against release vX can
// diff the exact field set and result shapes it was compiled against. It is a
// READ-ONLY aggregator over the two existing exported projections — it shapes no
// wire, adds no field, and changes no dispatch or result type. Holding the
// version string fixed, its JSON encoding is byte-identical on re-run (both
// component slices are already sorted by field name; there are no maps, clocks,
// or randomness at the top level), so it is publishable as a deterministic,
// re-derivable release artifact and drift-gated like the manifests it wraps.

// WireContractSnapshotVersion is the format version of the snapshot DOCUMENT
// itself (not the Beamfall release it describes). Bump it only when the shape of
// WireContract changes — adding/removing/renaming a top-level field of the
// snapshot — so a consumer can tell a format change from a contract change.
const WireContractSnapshotVersion = "1"

// WireContract is the versioned wire-contract snapshot: the consolidated,
// deterministic projection of the hand-rolled dispatcher's schema.
type WireContract struct {
	// SnapshotVersion is the format version of this snapshot document
	// (WireContractSnapshotVersion), distinct from the release it describes.
	SnapshotVersion string `json:"snapshotVersion"`
	// ContractVersion is the Beamfall release version this contract belongs to
	// (e.g. "v1.2.3"), supplied by the caller. Empty for an unversioned/dev
	// snapshot — the form the drift gate pins, so the committed snapshot stays
	// byte-stable across commits that don't cut a release.
	ContractVersion string `json:"contractVersion"`
	// Fields is the flat top-level field manifest (effect + tier), sorted by name.
	Fields []SchemaField `json:"fields"`
	// Nested is the reflected nested result shape per field, sorted by name.
	Nested []NestedSchemaField `json:"nested"`
}

// BuildWireContract returns the versioned wire-contract snapshot for the given
// release version. version is stamped verbatim into ContractVersion ("" for an
// unversioned/dev snapshot); every other field is derived deterministically from
// the live dispatcher registries via SchemaManifest()/NestedSchema(), so for a
// fixed version the result's JSON encoding is byte-identical on re-run.
func BuildWireContract(version string) WireContract {
	return WireContract{
		SnapshotVersion: WireContractSnapshotVersion,
		ContractVersion: version,
		Fields:          SchemaManifest(),
		Nested:          NestedSchema(),
	}
}
