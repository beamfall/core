package server

import (
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"time"

	"github.com/beamfall/core/internal/reveal"
	"github.com/beamfall/core/internal/store"
)

// V1Contract is the stable /v1 REST + SSE contract projection.
type V1Contract struct {
	Routes []V1RouteContract `json:"routes"`
	SSE    V1SSEContract     `json:"sse"`
}

type V1RouteContract struct {
	OperationID string `json:"operationID"`
	Method      string `json:"method"`
	Pattern     string `json:"pattern"`
	// Scope is the API scope a scoped developer key must hold. It is empty
	// for a route that does not accept scoped keys.
	Scope store.APIScope `json:"scope,omitempty"`
	// Credentials lists the credential kinds the route accepts.
	Credentials []V1Credential      `json:"credentials"`
	RevealClass reveal.SurfaceClass `json:"revealClass"`
	Request     *V1MessageContract  `json:"request,omitempty"`
	Response    *V1MessageContract  `json:"response,omitempty"`
	ListBounds  []V1ListBound       `json:"listBounds,omitempty"`
}

// V1Credential names a credential kind a /v1 route accepts.
type V1Credential string

const (
	// V1CredentialScopedKey is an operator-minted scoped developer key
	// (Bearer bfk_…) that holds the route's Scope.
	V1CredentialScopedKey V1Credential = "scopedKey"
	// V1CredentialDeviceSession is an enrolled device Session: the credential
	// a client app holds after pairing. The request acts as the Session's
	// Profile, with its reveal state and library grants.
	V1CredentialDeviceSession V1Credential = "deviceSession"
)

var (
	v1KeyOnly     = []V1Credential{V1CredentialScopedKey}
	v1KeyOrDevice = []V1Credential{V1CredentialScopedKey, V1CredentialDeviceSession}
	v1DeviceOnly  = []V1Credential{V1CredentialDeviceSession}
)

// V1ListBound records how an array-valued response field is kept bounded.
// Cursor fields identify list endpoints; MaxItems covers finite command
// results whose complete response is intentionally returned in one message.
type V1ListBound struct {
	Field           string `json:"field"`
	MaxItems        int    `json:"maxItems"`
	CursorParameter string `json:"cursorParameter,omitempty"`
	LimitParameter  string `json:"limitParameter,omitempty"`
	NextCursorField string `json:"nextCursorField,omitempty"`
}

type V1MessageContract struct {
	ContentType string        `json:"contentType"`
	Shape       V1Shape       `json:"shape"`
	Fields      []V1FormField `json:"fields,omitempty"`
}

type V1FormField struct {
	Name     string `json:"name"`
	Required bool   `json:"required"`
	Type     string `json:"type"`
}

type V1Shape struct {
	Type    string            `json:"type"`
	Fields  []V1FieldContract `json:"fields,omitempty"`
	Element *V1Shape          `json:"element,omitempty"`
}

type V1FieldContract struct {
	Name      string  `json:"name"`
	Shape     V1Shape `json:"shape"`
	OmitEmpty bool    `json:"omitempty,omitempty"`
}

type V1SSEContract struct {
	Path        string            `json:"path"`
	AliasOf     string            `json:"aliasOf"`
	ContentType string            `json:"contentType"`
	Resume      []string          `json:"resume"`
	Frame       []V1SSEFrameField `json:"frame"`
	Data        V1MessageContract `json:"data"`
}

type V1SSEFrameField struct {
	Line string `json:"line"`
	Type string `json:"type"`
}

var v1RouteContracts = []V1RouteContract{
	{OperationID: "listLibraries", Method: http.MethodGet, Pattern: "/v1/libraries", Scope: store.ScopeReadCatalog, Credentials: v1KeyOrDevice, RevealClass: reveal.ContentJSON, Response: jsonMessage[v1LibrariesResponse](), ListBounds: []V1ListBound{cursorListBound("libraries")}},
	{OperationID: "listLibraryMedia", Method: http.MethodGet, Pattern: "/v1/libraries/{libraryID}/media", Scope: store.ScopeReadCatalog, Credentials: v1KeyOrDevice, RevealClass: reveal.ContentJSON, Response: jsonMessage[v1LibraryMediaResponse](), ListBounds: []V1ListBound{cursorListBound("media")}},
	{OperationID: "getMedia", Method: http.MethodGet, Pattern: "/v1/media/{mediaID}", Scope: store.ScopeReadCatalog, Credentials: v1KeyOrDevice, RevealClass: reveal.ContentJSON, Response: jsonMessage[v1MediaDetail](), ListBounds: []V1ListBound{{Field: "externalIDs", MaxItems: v1ExternalIDLimit}}},
	{OperationID: "lookupMediaByExternalID", Method: http.MethodGet, Pattern: "/v1/media", Scope: store.ScopeReadCatalog, Credentials: v1KeyOrDevice, RevealClass: reveal.ContentJSON, Response: jsonMessage[v1MediaLookupResponse](), ListBounds: []V1ListBound{{Field: "media", MaxItems: v1ExternalIDLookupLimit}, {Field: "media.externalIDs", MaxItems: v1ExternalIDLimit}}},
	{OperationID: "getPlaybackInfo", Method: http.MethodPost, Pattern: "/v1/media/{mediaID}/playback-info", Credentials: v1DeviceOnly, RevealClass: reveal.ContentJSON, Request: jsonMessage[v1PlaybackInfoRequest](), Response: jsonMessage[v1PlaybackInfoResponse](), ListBounds: []V1ListBound{
		{Field: "streams", MaxItems: v1PlaybackStreamLimit},
		{Field: "streams.renditions", MaxItems: v1PlaybackRenditionLimit},
		{Field: "captionTracks", MaxItems: v1PlaybackCaptionTrackLimit},
	}},
	{OperationID: "reportProgress", Method: http.MethodPut, Pattern: "/v1/media/{mediaID}/progress", Credentials: v1DeviceOnly, RevealClass: reveal.ContentJSON, Request: jsonMessage[v1ProgressRequest](), Response: jsonMessage[v1ProgressResponse]()},
	{OperationID: "listSessions", Method: http.MethodGet, Pattern: "/v1/sessions", Scope: store.ScopeReadSessions, Credentials: v1KeyOnly, RevealClass: reveal.ContentJSON, Response: jsonMessage[v1SessionsResponse](), ListBounds: []V1ListBound{cursorListBound("sessions")}},
	{OperationID: "playbackControl", Method: http.MethodPost, Pattern: "/v1/playback-control", Scope: store.ScopePlaybackControl, Credentials: v1KeyOnly, RevealClass: reveal.ContentJSON, Request: jsonMessage[v1PlaybackControlRequest](), Response: jsonMessage[v1PlaybackControlResponse]()},
	{OperationID: "scanLibrary", Method: http.MethodPost, Pattern: "/v1/scans", Scope: store.ScopeIngest, Credentials: v1KeyOnly, RevealClass: reveal.ContentJSON, Request: jsonMessage[v1ScanRequest](), Response: jsonMessage[v1ScanResponse](), ListBounds: []V1ListBound{{Field: "jobIDs", MaxItems: v1ScanJobLimit}}},
	{OperationID: "getBackupStatus", Method: http.MethodGet, Pattern: "/v1/backups/{libraryID}/status", Scope: store.ScopeBackup, Credentials: v1KeyOnly, RevealClass: reveal.ContentJSON, Response: jsonMessage[v1BackupStatusResponse]()},
	{OperationID: "backupMedia", Method: http.MethodPost, Pattern: "/v1/backups/media", Scope: store.ScopeBackup, Credentials: v1KeyOnly, RevealClass: reveal.ContentJSON, Request: multipartMessage(
		V1FormField{Name: "libraryID", Required: true, Type: "string"},
		V1FormField{Name: "file", Required: true, Type: "file"},
		V1FormField{Name: "title", Type: "string"},
		V1FormField{Name: "class", Type: "string"},
		V1FormField{Name: "container", Type: "string"},
		V1FormField{Name: "codec", Type: "string"},
	), Response: jsonMessage[v1BackupMediaResponse]()},
	{OperationID: "subscribeEvents", Method: http.MethodGet, Pattern: "/v1/events", Scope: store.ScopeEventsSubscribe, Credentials: v1KeyOnly, RevealClass: reveal.EventFanout},
}

func cursorListBound(field string) V1ListBound {
	return V1ListBound{
		Field:           field,
		MaxItems:        v1PageMaxLimit,
		CursorParameter: "cursor",
		LimitParameter:  "limit",
		NextCursorField: "nextCursor",
	}
}

func BuildV1Contract() V1Contract {
	routes := make([]V1RouteContract, len(v1RouteContracts))
	copy(routes, v1RouteContracts)
	return V1Contract{
		Routes: routes,
		SSE: V1SSEContract{
			Path:        "/v1/events",
			AliasOf:     "/api/events",
			ContentType: "text/event-stream",
			Resume:      []string{"Last-Event-ID", "after"},
			Frame: []V1SSEFrameField{
				{Line: "id", Type: "int64"},
				{Line: "event", Type: "string"},
				{Line: "data", Type: "json"},
			},
			Data: *jsonMessage[sseEventEnvelope](),
		},
	}
}

func jsonMessage[T any]() *V1MessageContract {
	var zero T
	return &V1MessageContract{ContentType: "application/json", Shape: shapeOf(reflect.TypeOf(zero))}
}

func multipartMessage(fields ...V1FormField) *V1MessageContract {
	return &V1MessageContract{ContentType: "multipart/form-data", Shape: V1Shape{Type: "form"}, Fields: fields}
}

func shapeOf(t reflect.Type) V1Shape {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t == reflect.TypeOf(time.Time{}) {
		return V1Shape{Type: "timestamp"}
	}
	if t == reflect.TypeOf(json.RawMessage{}) {
		return V1Shape{Type: "json"}
	}
	switch t.Kind() {
	case reflect.Struct:
		fields := make([]V1FieldContract, 0, t.NumField())
		for i := 0; i < t.NumField(); i++ {
			field := t.Field(i)
			if !field.IsExported() {
				continue
			}
			name, omitEmpty, ok := jsonFieldName(field)
			if !ok {
				continue
			}
			fields = append(fields, V1FieldContract{Name: name, Shape: shapeOf(field.Type), OmitEmpty: omitEmpty})
		}
		return V1Shape{Type: "object", Fields: fields}
	case reflect.Slice, reflect.Array:
		elem := shapeOf(t.Elem())
		return V1Shape{Type: "array", Element: &elem}
	case reflect.Bool:
		return V1Shape{Type: "boolean"}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return V1Shape{Type: "integer"}
	default:
		return V1Shape{Type: "string"}
	}
}

func jsonFieldName(field reflect.StructField) (string, bool, bool) {
	tag := field.Tag.Get("json")
	if tag == "-" {
		return "", false, false
	}
	parts := strings.Split(tag, ",")
	name := parts[0]
	if name == "" {
		name = field.Name
	}
	for _, part := range parts[1:] {
		if part == "omitempty" {
			return name, true, true
		}
	}
	return name, false, true
}
