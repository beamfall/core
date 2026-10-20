//go:build !beamfall_appstore

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/beamfall/core/internal/store"
)

func TestRunCapabilitiesCommandReportsDefaultSet(t *testing.T) {
	var out bytes.Buffer
	if err := capabilitiesCommand(nil, &out); err != nil {
		t.Fatal(err)
	}
	var report struct {
		Commit       string   `json:"commit"`
		BuildTags    []string `json:"buildTags"`
		OfferURL     string   `json:"correspondingSourceOfferURL"`
		Capabilities []string `json:"capabilities"`
	}
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.Commit != "" || report.BuildTags != nil || report.OfferURL != "" {
		t.Fatalf("default report leaked version metadata: %#v", report)
	}
	if got, want := strings.Join(report.Capabilities, ","), "conversion,rename"; got != want {
		t.Fatalf("capabilities = %q, want %q", got, want)
	}
}

// TestCapabilitiesCommandReflectsPersistedFlip proves `beamfall capabilities`
// reflects the operator's persisted Workflow toggle (ADR-0124 §5: a flipped
// capability is immediately visible). It persists a conversion-disabled override
// into the store, then runs the CLI against the same data dir and asserts the
// flip is reflected in both the flat list and the per-capability state.
func TestCapabilitiesCommandReflectsPersistedFlip(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("BEAMFALL_DATA_DIR", dataDir)

	st, err := store.Open(context.Background(), dataDir, store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetCapabilityEnabled(context.Background(), "conversion", false, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	st.Close()

	var out bytes.Buffer
	if err := capabilitiesCommand(nil, &out); err != nil {
		t.Fatal(err)
	}
	var report struct {
		Capabilities     []string `json:"capabilities"`
		CapabilityStates []struct {
			ID      string `json:"id"`
			Enabled bool   `json:"enabled"`
		} `json:"capabilityStates"`
	}
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatalf("decode capabilities output: %v; body=%s", err, out.String())
	}

	for _, id := range report.Capabilities {
		if id == "conversion" {
			t.Fatalf("capabilities still lists conversion after persisted disable: %v", report.Capabilities)
		}
	}
	var found bool
	for _, s := range report.CapabilityStates {
		if s.ID == "conversion" {
			found = true
			if s.Enabled {
				t.Fatal("capabilityStates conversion enabled = true after persisted flip, want false")
			}
		}
	}
	if !found {
		t.Fatal("capabilityStates omitted conversion entirely")
	}
	// rename remained at its enabled default — the flip targeted only conversion.
	var renameEnabled bool
	for _, id := range report.Capabilities {
		if id == "rename" {
			renameEnabled = true
		}
	}
	if !renameEnabled {
		t.Fatal("rename missing from capabilities — flip leaked beyond conversion")
	}
}
