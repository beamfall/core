// Command printidentity prints the link-time build identity, for
// TestReleaseIdentityLinkTime (CORE-1096).
package main

import (
	"fmt"

	"github.com/beamfall/core/internal/capabilities"
)

func main() {
	report := capabilities.Current()
	fmt.Printf("%s\n%s\n", report.Version, report.Commit)
}
