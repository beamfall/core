package contract

import (
	"fmt"
	"sort"

	"github.com/beamfall/core/internal/server"
)

// ValidateListBounds rejects every array-valued /v1 response field that lacks
// an explicit cursor-page or finite-result bound in the exported contract.
func ValidateListBounds(snapshot Snapshot) error {
	var unbounded []string
	for _, route := range snapshot.V1.Routes {
		if route.Response == nil {
			continue
		}
		bounds := make(map[string]server.V1ListBound, len(route.ListBounds))
		for _, bound := range route.ListBounds {
			bounds[bound.Field] = bound
		}
		for _, field := range arrayFields(route.Response.Shape, "") {
			bound, ok := bounds[field]
			if !ok || bound.MaxItems < 1 {
				unbounded = append(unbounded, route.OperationID+"."+field)
				continue
			}
			if bound.CursorParameter != "" && (bound.LimitParameter == "" || bound.NextCursorField == "") {
				unbounded = append(unbounded, route.OperationID+"."+field)
				continue
			}
			if bound.CursorParameter != "" && !hasStringField(route.Response.Shape, bound.NextCursorField) {
				unbounded = append(unbounded, route.OperationID+"."+field)
			}
		}
	}
	if len(unbounded) == 0 {
		return nil
	}
	sort.Strings(unbounded)
	return fmt.Errorf("unbounded /v1 list fields: %v", unbounded)
}

func arrayFields(shape server.V1Shape, prefix string) []string {
	var fields []string
	if shape.Type == "array" {
		fields = append(fields, prefix)
		if shape.Element != nil {
			fields = append(fields, arrayFields(*shape.Element, prefix)...)
		}
		return fields
	}
	for _, field := range shape.Fields {
		path := field.Name
		if prefix != "" {
			path = prefix + "." + path
		}
		fields = append(fields, arrayFields(field.Shape, path)...)
	}
	return fields
}

func hasStringField(shape server.V1Shape, name string) bool {
	for _, field := range shape.Fields {
		if field.Name == name && field.Shape.Type == "string" {
			return true
		}
	}
	return false
}
