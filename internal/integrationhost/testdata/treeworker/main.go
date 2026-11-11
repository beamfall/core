// Command treeworker is the process-tree fixture for internal/integrationhost
// tests: it starts a descendant, publishes both pids, and then exits, fails or
// hangs on demand so the supervisor's tree retirement can be observed.
//
// It deliberately imports nothing from the host (and so nothing that links
// internal/store and its sqlite driver): under the worker's 1 GiB address-space
// limit the sqlite initializer cannot start, and the tree tests need no Host
// API handshake.
package main

import (
	"flag"
	"os"
)

func main() {
	treeMode := flag.String("tree-mode", "", "process-tree mode: success, failure or hang")
	pidFile := flag.String("pid-file", "", "process-tree pid file")
	holdFile := flag.String("hold-file", "", "process-tree release file")
	descendant := flag.Bool("descendant", false, "run as a descendant")
	termResistant := flag.Bool("term-resistant", false, "ignore TERM in descendant mode")
	readyFile := flag.String("ready-file", "", "descendant readiness file")
	// The supervisor always appends the Host API socket; the tree fixture never
	// dials it.
	flag.String("host-api-socket", "", "Host API unix-domain socket path (unused)")
	flag.Parse()
	if *descendant {
		runDescendant(*termResistant, *readyFile)
		return
	}
	if *treeMode != "" {
		runProcessTree(*treeMode, *pidFile, *holdFile, *termResistant)
		return
	}
	os.Exit(2)
}
