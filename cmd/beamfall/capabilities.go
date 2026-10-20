package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"

	"github.com/beamfall/core/internal/capabilities"
	"github.com/beamfall/core/internal/config"
	"github.com/beamfall/core/internal/store"
)

func capabilitiesCommand(args []string, stdout io.Writer) error {
	if helpArg(args) {
		_, err := fmt.Fprint(stdout, capabilitiesHelp)
		return err
	}
	if len(args) > 1 || len(args) == 1 && args[0] != "--version" {
		return fmt.Errorf("%s", capabilitiesHelp)
	}
	if len(args) == 1 {
		return versionCommand(stdout)
	}
	// Reflect the *runtime* capability state: apply the operator's
	// persisted Workflow toggle overrides so a flipped capability is immediately
	// visible. The normal store is opened best-effort — on a box with no data dir
	// yet (or a running server holding the lock) the command still reports the
	// default state rather than failing.
	report := capabilities.CurrentWithOverrides(workflowOverrides())
	report.Version = ""
	report.Commit = ""
	report.BuildTags = nil
	report.CorrespondingSourceOfferURL = ""
	return writeCapabilitiesReport(stdout, report)
}

const capabilitiesHelp = `Usage: beamfall capabilities [--version]

Print runtime capabilities. With --version, print build metadata instead.
`

// workflowOverrides reads the operator's persisted Workflow capability toggles
// from the store. It is best-effort: any error (no data dir, store locked by a
// running server, capability set unsupported) yields nil overrides so the
// command falls back to the default runtime state.
func workflowOverrides() map[string]bool {
	cfg := config.Default()
	if value := os.Getenv("BEAMFALL_DATA_DIR"); value != "" {
		cfg.DataDir = value
	}
	if _, err := os.Stat(cfg.DataDir); err != nil {
		return nil
	}
	ctx := context.Background()
	st, err := store.Open(ctx, cfg.DataDir, store.Options{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		return nil
	}
	defer st.Close()
	overrides, err := st.CapabilityStates(ctx)
	if err != nil {
		return nil
	}
	return overrides
}

func versionCommand(stdout io.Writer) error {
	report := capabilities.Current()
	report.Capabilities = nil
	report.CapabilityStates = nil
	report.CapabilityManifest = nil
	return writeCapabilitiesReport(stdout, report)
}

func writeCapabilitiesReport(stdout io.Writer, report capabilities.Report) error {
	if err := report.Validate(); err != nil {
		return err
	}
	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(report)
}
