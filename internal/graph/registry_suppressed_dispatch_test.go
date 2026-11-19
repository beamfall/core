//go:build !beamfall_appstore

package graph

import "github.com/beamfall/core/internal/capabilities"

var suppressedDispatchFields = map[string]bool{}

func init() {
	if len(capabilities.WorkflowIDs()) == 0 {
		suppressedDispatchFields["enqueueconvertjob"] = true
	}
}
