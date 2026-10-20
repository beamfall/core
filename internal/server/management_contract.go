package server

import "net/http"

// ManagementRouteContract is the producer-owned first-party management route
// registry consumed by both wire-contract publishers. Route-specific request,
// response, authorization, limits, and errors live beside the server route so
// a new transport cannot silently bypass publication.
type ManagementRouteContract struct {
	Method        string                           `json:"method"`
	Pattern       string                           `json:"pattern"`
	Note          string                           `json:"note"`
	Authorization string                           `json:"authorization,omitempty"`
	Request       *ManagementRouteRequestContract  `json:"request,omitempty"`
	Success       *ManagementRouteResponseContract `json:"success,omitempty"`
	Errors        []ManagementRouteErrorContract   `json:"errors,omitempty"`
}

type ManagementRouteRequestContract struct {
	ContentType     string                    `json:"contentType"`
	MaxBodyBytes    int64                     `json:"maxBodyBytes,omitempty"`
	PathFields      []ManagementFieldContract `json:"pathFields,omitempty"`
	MultipartFields []ManagementFieldContract `json:"multipartFields,omitempty"`
}

type ManagementRouteResponseContract struct {
	Status      int                       `json:"status"`
	ContentType string                    `json:"contentType"`
	Fields      []ManagementFieldContract `json:"fields"`
}

type ManagementFieldContract struct {
	Name                 string   `json:"name"`
	Type                 string   `json:"type"`
	Required             bool     `json:"required"`
	MinimumInteger       *int64   `json:"minimumInteger,omitempty"`
	MaxBytes             int64    `json:"maxBytes,omitempty"`
	MaxPixels            int      `json:"maxPixels,omitempty"`
	AcceptedContentTypes []string `json:"acceptedContentTypes,omitempty"`
}

type ManagementRouteErrorContract struct {
	Status   int      `json:"status"`
	Messages []string `json:"messages"`
}

var artworkYearMinimum int64

var managementRouteContracts = []ManagementRouteContract{
	{Method: http.MethodPost, Pattern: "/api/setup", Note: "First-run setup bootstrap (admin password, first Library, owner Profile)"},
	{
		Method:        http.MethodPost,
		Pattern:       createMediaArtworkRoutePattern,
		Note:          "Create Media from an unmatched-review record and pin uploaded poster artwork",
		Authorization: "manager-admin-session",
		Request: &ManagementRouteRequestContract{
			ContentType:  "multipart/form-data",
			MaxBodyBytes: maxArtworkUploadBody,
			PathFields: []ManagementFieldContract{
				{Name: "recordID", Type: "string", Required: true},
			},
			MultipartFields: []ManagementFieldContract{
				{Name: "libraryID", Type: "string", Required: true},
				{Name: "title", Type: "string", Required: true},
				{Name: "year", Type: "integer", MinimumInteger: &artworkYearMinimum},
				{
					Name:                 "artwork",
					Type:                 "binary",
					Required:             true,
					MaxBytes:             maxArtworkUploadBytes,
					MaxPixels:            maxArtworkPixels,
					AcceptedContentTypes: []string{"image/jpeg", "image/png"},
				},
			},
		},
		Success: &ManagementRouteResponseContract{
			Status:      http.StatusCreated,
			ContentType: "application/json",
			Fields: []ManagementFieldContract{
				{Name: "mediaID", Type: "string", Required: true},
			},
		},
		Errors: []ManagementRouteErrorContract{
			{
				Status: http.StatusBadRequest,
				Messages: []string{
					"recordID, libraryID, and title are required",
					"artwork is required",
					"could not read artwork",
					"invalid multipart body",
				},
			},
			{Status: http.StatusUnauthorized, Messages: []string{"invalid session"}},
			{Status: http.StatusForbidden, Messages: []string{"manager role required"}},
			{Status: http.StatusConflict, Messages: []string{"unmatched record was resolved by a different request"}},
			{Status: http.StatusRequestEntityTooLarge, Messages: []string{"artwork is too large"}},
			{
				Status: http.StatusUnsupportedMediaType,
				Messages: []string{
					"Content-Type must be multipart/form-data",
					"artwork must be a valid JPEG or PNG image",
				},
			},
			{Status: http.StatusInternalServerError, Messages: []string{"could not create Media with artwork"}},
			{
				Status: http.StatusServiceUnavailable,
				Messages: []string{
					"artwork upload unavailable",
					"privileged audit unavailable",
				},
			},
		},
	},
	{Method: http.MethodPost, Pattern: "/api/web/enroll", Note: "Enroll a web device session"},
	{Method: http.MethodPost, Pattern: "/api/web/logout", Note: "Logout a web device session"},
	{Method: http.MethodPost, Pattern: "/api/web/promote", Note: "Promote a web session to Admin Session"},
	{Method: http.MethodPost, Pattern: "/api/beam/offers", Note: "Create a Beam offer for device pairing"},
	{Method: http.MethodPost, Pattern: "/api/beam/claims", Note: "Claim a Beam offer (device pairing)"},
	{Method: http.MethodPost, Pattern: "/api/pairing-codes", Note: "Generate a pairing code for device enrollment"},
	{Method: http.MethodPost, Pattern: "/api/enroll", Note: "Enroll a native device"},
	{Method: http.MethodPost, Pattern: "/api/group/calibrate", Note: "Calibrate group watch sync"},
	{Method: http.MethodGet, Pattern: "/api/manifest", Note: "Navigation Manifest: server-side layout and section configuration"},
	{Method: http.MethodGet, Pattern: "/api/events", Note: "SSE event fanout: real-time server events for Console/Manage UIs"},
}

// ManagementRouteContracts returns a detached copy of the server-owned
// first-party management registry.
func ManagementRouteContracts() []ManagementRouteContract {
	contracts := make([]ManagementRouteContract, len(managementRouteContracts))
	for i, route := range managementRouteContracts {
		contracts[i] = route
		if route.Request != nil {
			request := *route.Request
			request.PathFields = cloneManagementFields(route.Request.PathFields)
			request.MultipartFields = cloneManagementFields(route.Request.MultipartFields)
			contracts[i].Request = &request
		}
		if route.Success != nil {
			success := *route.Success
			success.Fields = cloneManagementFields(route.Success.Fields)
			contracts[i].Success = &success
		}
		contracts[i].Errors = make([]ManagementRouteErrorContract, len(route.Errors))
		for errorIndex, routeError := range route.Errors {
			contracts[i].Errors[errorIndex] = routeError
			contracts[i].Errors[errorIndex].Messages = append([]string(nil), routeError.Messages...)
		}
	}
	return contracts
}

func cloneManagementFields(fields []ManagementFieldContract) []ManagementFieldContract {
	cloned := make([]ManagementFieldContract, len(fields))
	for i, field := range fields {
		cloned[i] = field
		cloned[i].AcceptedContentTypes = append([]string(nil), field.AcceptedContentTypes...)
	}
	return cloned
}
