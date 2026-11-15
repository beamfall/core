// Package apidocs embeds the developer API's OpenAPI document so a running
// server serves the exact bytes the contract checks validate (CORE-1714).
package apidocs

import (
	"bytes"
	_ "embed"
)

//go:embed openapi.json
var openAPI []byte

// OpenAPI returns a copy of the embedded docs/api/openapi.json.
func OpenAPI() []byte { return bytes.Clone(openAPI) }
