package contract

import (
	"net/http"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/beamfall/core/internal/graph"
	"github.com/beamfall/core/internal/playback"
	"github.com/beamfall/core/internal/server"
	"github.com/beamfall/core/internal/streamtoken"
)

type ManagementManifest struct {
	SchemaVersion     string                           `json:"schemaVersion"`
	GraphQLOperations []ManagementOperation            `json:"graphqlOperations"`
	ManagementRoutes  []server.ManagementRouteContract `json:"managementRoutes"`
}

type ManagementOperation struct {
	Field  string `json:"field"`
	Effect string `json:"effect"`
}

type ManifestContract struct {
	Method   string    `json:"method"`
	Pattern  string    `json:"pattern"`
	Response WireShape `json:"response"`
	Notes    []string  `json:"notes,omitempty"`
}

type PlaybackInfoContract struct {
	Method   string    `json:"method"`
	Pattern  string    `json:"pattern"`
	Request  WireShape `json:"request"`
	Response WireShape `json:"response"`
	// Notes carry the semantic rules a reflected shape cannot express —
	// precedence between a server-stated value and a client-local resolver,
	// field independence, and closed string vocabularies. They ship inside every
	// published wire-contract artifact, so a consumer pinning a version gets the
	// rules with the shape rather than having to read Core's source comments.
	Notes       []string  `json:"notes,omitempty"`
	StreamToken WireShape `json:"streamToken"`
}

type WireShape struct {
	Type     string      `json:"type"`
	Fields   []WireField `json:"fields,omitempty"`
	Element  *WireShape  `json:"element,omitempty"`
	Optional bool        `json:"optional,omitempty"`
}

type WireField struct {
	Name  string    `json:"name"`
	Shape WireShape `json:"shape"`
}

type CompatibilityDecisions struct {
	Versioning         string `json:"versioning"`
	IncompatibleChange string `json:"incompatibleChange"`
	ConsumerPinRule    string `json:"consumerPinRule"`
}

type KnownDrift struct {
	Ticket   string `json:"ticket"`
	Deadline string `json:"deadline"`
	Reason   string `json:"reason"`
}

func buildManagementManifest() ManagementManifest {
	ops := make([]ManagementOperation, 0)
	for _, field := range graph.SchemaManifest() {
		if field.Tier == "admin" {
			ops = append(ops, ManagementOperation{Field: field.Field, Effect: field.Effect})
		}
	}
	sort.Slice(ops, func(i, j int) bool { return ops[i].Field < ops[j].Field })
	return ManagementManifest{
		SchemaVersion:     "1",
		GraphQLOperations: ops,
		ManagementRoutes:  server.ManagementRouteContracts(),
	}
}

func buildManifestContract() ManifestContract {
	return ManifestContract{
		Method:   http.MethodGet,
		Pattern:  "/api/manifest",
		Response: shapeOf(reflect.TypeOf(server.ManifestResponse{})),
		Notes: []string{
			"systemSurfaceDisclosure.metadataAllowed is server-authoritative for the Navigation Manifest metadata returned by REST /api/manifest and GraphQL navigationManifest. It is true only when that metadata is visible to every Profile with Reveal off. Missing fields, unknown snapshot versions, and false mean clients must publish no content-specific system metadata.",
		},
	}
}

func buildPlaybackInfoContract() PlaybackInfoContract {
	return PlaybackInfoContract{
		Method:      http.MethodPost,
		Pattern:     "/api/playback-info",
		Request:     shapeOf(reflect.TypeOf(playback.InfoRequest{})),
		Response:    shapeOf(reflect.TypeOf(server.PlaybackInfoResponse{})),
		Notes:       playbackInfoNotes(),
		StreamToken: shapeOf(reflect.TypeOf(streamtoken.Grant{})),
	}
}

// playbackInfoNotes states the ADR-0194 §3/§6 subtitle rules in the wire spec
// itself rather than in Go comments, which is where that ADR requires them.
func playbackInfoNotes() []string {
	return []string{
		"selectedSubtitleTrack is server-authoritative at playback START only. The client-local resolver is the sole authority for mid-session track switches and for offline playback: once the viewer switches tracks the server value is superseded and is not re-applied. An old server that omits the field means the client local-resolves from the start; an old client ignores it.",
		"selectedSubtitleTrack.track is the format-independent language+kind+role identity. It is NOT the request-side selectedSubtitleIndex ffprobe stream index, which is unstable across renditions. roles is a SET — forced/sdh/visualImpaired/commentary co-occur and have no precedence order.",
		"selectedSubtitleTrack.track.kind is the closed vocabulary muxed|sidecar. Reflection publishes it as a generic string, so the vocabulary is enumerated here; growing it is a deliberate wire-contract change.",
		"subtitlesUnavailableReason is free text and is INDEPENDENT of decision. Subtitles being unavailable never degrades the whole-media decision verdict (direct-play|direct-stream|precomputed-rendition|transcode|unavailable). Absent/empty means subtitles are not known to be unavailable.",
		"systemSurfaceDisclosure.metadataAllowed is server-authoritative for the response Media. It is true only when the Media metadata is visible to every Profile with Reveal off. Missing fields, unknown snapshot versions, and false mean clients must use generic system-surface metadata.",
	}
}

func compatibilityDecisions() CompatibilityDecisions {
	return CompatibilityDecisions{
		Versioning:         "semantic-versioning",
		IncompatibleChange: "publish a new contract version; never overwrite an existing artifact",
		ConsumerPinRule:    "consumer pins must remain on the published major; minor-version lag is allowed",
	}
}

func shapeOf(t reflect.Type) WireShape {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t == reflect.TypeOf(time.Time{}) {
		return WireShape{Type: "timestamp"}
	}
	switch t.Kind() {
	case reflect.Struct:
		fields := make([]WireField, 0, t.NumField())
		for i := 0; i < t.NumField(); i++ {
			field := t.Field(i)
			if !field.IsExported() {
				continue
			}
			if field.Anonymous && field.Tag.Get("json") == "" {
				embedded := shapeOf(field.Type)
				fields = append(fields, embedded.Fields...)
				continue
			}
			name, optional, ok := jsonField(field)
			if !ok {
				continue
			}
			child := shapeOf(field.Type)
			child.Optional = optional
			fields = append(fields, WireField{Name: name, Shape: child})
		}
		return WireShape{Type: "object", Fields: fields}
	case reflect.Slice, reflect.Array:
		element := shapeOf(t.Elem())
		return WireShape{Type: "array", Element: &element}
	case reflect.Bool:
		return WireShape{Type: "boolean"}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return WireShape{Type: "integer"}
	case reflect.Float32, reflect.Float64:
		return WireShape{Type: "number"}
	default:
		return WireShape{Type: "string"}
	}
}

func jsonField(field reflect.StructField) (string, bool, bool) {
	tag := field.Tag.Get("json")
	if tag == "-" {
		return "", false, false
	}
	parts := strings.Split(tag, ",")
	name := parts[0]
	if name == "" {
		name = field.Name
	}
	optional := false
	for _, option := range parts[1:] {
		optional = optional || option == "omitempty"
	}
	return name, optional, true
}
