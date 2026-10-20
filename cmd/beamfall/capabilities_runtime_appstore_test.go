//go:build beamfall_appstore

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/beamfall/core/internal/store"
)

func TestAppStoreCapabilitiesCommandIgnoresPersistedEnables(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("BEAMFALL_DATA_DIR", dataDir)

	st, err := store.Open(context.Background(), dataDir, store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"conversion", "rename"} {
		if err := st.SetCapabilityEnabled(context.Background(), id, true, time.Now().UTC()); err != nil {
			t.Fatal(err)
		}
	}
	st.Close()

	var out bytes.Buffer
	if err := capabilitiesCommand(nil, &out); err != nil {
		t.Fatal(err)
	}
	var report struct {
		Capabilities     []string `json:"capabilities"`
		CapabilityStates []struct {
			ID string `json:"id"`
		} `json:"capabilityStates"`
	}
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatalf("decode capabilities output: %v; body=%s", err, out.String())
	}
	if len(report.Capabilities) != 0 {
		t.Fatalf("App Store capabilities = %v, want none", report.Capabilities)
	}
	if len(report.CapabilityStates) != 0 {
		t.Fatalf("App Store capabilityStates = %v, want none", report.CapabilityStates)
	}
}
