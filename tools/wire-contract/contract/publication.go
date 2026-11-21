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

// GraphQLErrorsContract publishes the shape of one GraphQL errors[] entry and
// the closed extensions.code vocabulary (ARCHITECTURE §7, CORE-0053).
type GraphQLErrorsContract struct {
	Error WireShape `json:"error"`
	Codes []string  `json:"codes"`
	// Reasons is the closed extensions.reason vocabulary (CORE-1551).
	Reasons []string `json:"reasons,omitempty"`
	Notes   []string `json:"notes,omitempty"`
}

type GraphQLResponseContract struct {
	Extensions WireShape `json:"extensions"`
	Notes      []string  `json:"notes"`
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
		"adjustment requests a pitch shift (pitchShiftSemitones, whole semitones) and a tempo factor (tempo; 0 or omitted is 1) inside the ranges serverInfo.playbackAdjustments advertises; out-of-range values are rejected. It applies only to the main item of an audio Media when a transcode is admitted: the response echoes adjustment only when applied, and an absent response adjustment means the stream plays unadjusted. An adjusted stream runs on its own clock (stream seconds = song seconds / tempo) while timeline and reported progress stay in song seconds. Changing the adjustment mid-play is a new playback info call: report progress first, and the new stream starts at the segment holding timeline.positionSeconds.",
		"streams[].gapless declares the stream's encoder delay and end padding in samples at sampleRate, for gapless playback of consecutive items. A client trims encoderDelaySamples from the start and encoderPaddingSamples from the end of the decoded audio, exactly once. On a live transcode the stream's container trims nothing, so the client applies the trim. On direct play the source's own signalling (an MP4 edit list, an Opus pre-skip, a LAME header) declares the same samples, so a client whose decoder already honours it must not trim again. It is present only when the server knows the exact values: on direct play when the source file declares them, and on a live audio transcode started from the item's beginning. Absent means play the stream untrimmed.",
		"capabilityProfile.audioCodecs[].maxSampleRate (Hz) and maxBitDepth (bits per sample) are the hi-res ceilings the device renders without resampling or truncating; 0 or omitted means no ceiling. A source audio stream above either ceiling is not direct-played or passed through: the server transcodes it. Each ceiling is checked only when the server recorded the source's value, so a source with an unrecorded sample rate or bit depth is never refused on that axis.",
		"audioQuality describes the selected audio track of the primary stream (streams[0]): codec, sampleRate and bitDepth are the source track as the server probed it (sampleRate and bitDepth are omitted when unrecorded), and lossless reports a lossless source codec. delivery is the closed vocabulary direct|transcode; reflection publishes it as a generic string, so the vocabulary is enumerated here and growing it is a deliberate wire-contract change. direct means streams[0] carries the source audio bit-for-bit (direct play, or a live transcode copying a passthrough track); transcode means the audio is re-encoded or comes from a rendition the server does not vouch for. Lossless direct play is lossless with delivery direct. audioQuality is omitted when the item is unavailable or has no audio, and next.audioQuality mirrors it for the declared next item.",
		"Access schedule ended: a request to /api/playback-info or /api/preview-info refused only because the session's managed Profile is outside its access schedule (and holds no approved extension) answers HTTP 403 with the header X-Beamfall-Denial-Reason: ACCESS_SCHEDULE_ENDED and the body {\"error\":\"access schedule ended\",\"reason\":\"ACCESS_SCHEDULE_ENDED\"}. The Stream Token media transports (/api/media/direct/, /api/media/hls/, /api/media/subtitle/, /api/media/sidecar/, /api/media/assets/) re-resolve the session on every request and answer the same HTTP 403 and header, with a plain-text body, so a player stops mid-playback and shows that access has ended instead of retrying. The reason carries no schedule detail and is sent only after the credential or Stream Token verified. Every other refusal keeps its status: 401 invalid session or invalid stream token, 410 stream ended. An absent header means the refusal has no finer reason; clients treat an unknown value as absent. GraphQL uses the same value as extensions.reason.",
	}
}

func buildGraphQLErrorsContract() GraphQLErrorsContract {
	codes := make([]string, 0, len(graph.ErrorCodes()))
	for _, code := range graph.ErrorCodes() {
		codes = append(codes, string(code))
	}
	reasons := make([]string, 0, len(graph.ErrorReasons()))
	for _, reason := range graph.ErrorReasons() {
		reasons = append(reasons, string(reason))
	}
	return GraphQLErrorsContract{
		Error:   shapeOf(reflect.TypeOf(graph.GraphQLErrorWire{})),
		Codes:   codes,
		Reasons: reasons,
		Notes: []string{
			"Every entry in a GraphQL response's errors array carries extensions.code from the closed vocabulary in codes. Clients branch on the code, never on the message text; growing the vocabulary is a deliberate wire-contract change.",
			"Denial of existence (ARCHITECTURE §6): content outside the caller's Library or Reveal scope answers NOT_FOUND, exactly as content that does not exist. FORBIDDEN means a role, tier, session-kind or Reveal gate refused an authenticated caller; it never confirms that hidden content exists.",
			"UNAUTHENTICATED means no valid session or device credential; THROTTLED means attempt limiting refused the request and it may be retried later; INVALID means the request or an argument is malformed; CONFLICT means the target's current state does not allow the operation; INTERNAL means a server-side failure; its message is generic (the failed field name followed by \"unavailable\") and the cause stays in the server log.",
			"extensions.reason, when present, is one of the closed vocabulary in reasons and refines extensions.code where a client must act differently on refusals that share a code. REVEAL_PIN_NOT_SET (FORBIDDEN): the step-up needs a reveal PIN and the Profile has none, so entering one cannot help. REVEAL_PIN_REQUIRED (FORBIDDEN): the request carried no reveal PIN. REVEAL_PIN_INCORRECT (FORBIDDEN): the reveal PIN does not match. myEngagement and deleteMyData use these. Absent means the error has no finer reason; clients must not infer one from the message text, and must treat an unknown reason as absent.",
			"PROFILE_MISMATCH (CONFLICT): a Play Queue write named, in its optional expectedProfileID argument, a Profile the session is not bound to when the write applies (queueAdd, queueRemove, queueMove, queueClear, stationStart). Nothing was written; the client drops the write instead of retrying it.",
			"PROFILE_MISMATCH (CONFLICT) also answers setContinueWatchingHidden when its optional expectedProfileID names a Profile the session is not bound to. Nothing was written; the client drops the write instead of retrying it.",
			"ACCESS_SCHEDULE_ENDED (FORBIDDEN, HTTP 403): the request was refused only because the session's managed Profile is outside its access schedule and holds no approved extension, whether at the request gate or when a later field of the same operation re-resolves the session. The session itself is still valid: the client keeps it and shows that access has ended for now, instead of signing out or re-pairing. The message is \"access schedule ended\" and carries no schedule detail. Every other credential failure is unchanged: HTTP 401, UNAUTHENTICATED, \"invalid session\". REST playback and media transports carry the same value in the X-Beamfall-Denial-Reason header (see playbackInfo notes).",
		},
	}
}

func buildGraphQLResponseContract() GraphQLResponseContract {
	extensions := shapeOf(reflect.TypeOf(graph.GraphQLResponseExtensions{}))
	extensions.Optional = true
	return GraphQLResponseContract{
		Extensions: extensions,
		Notes: []string{
			"extensions.queueAdds lists committed queueAdd mutations in execution order. Each receipt names the top-level responseKey (the alias, or queueAdd), the target mediaID, and its zero-based position in the full visible queue at commit, even outside the first page. A muted target omits position.",
			"data[responseKey] retains the first queue page. If a later mutation fails, the response preserves earlier queueAdd pages and receipts, sets the failed response key to null, and identifies it in errors[].path. Refused adds have no receipt.",
		},
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
