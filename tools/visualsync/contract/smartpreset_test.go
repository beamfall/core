package contract

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSmartPresetValidationRejectsUnknowns(t *testing.T) {
	valid := []byte(`{
		"contractVersion":"smart-preset.v1",
		"id":"liquid-optics-calm",
		"visualId":"liquid-optics",
		"defaults":{"param0":0.2,"param1":0.4,"param2":0.6},
		"requiredInputs":[{"input":"audioFrame","fallback":"use-envelope"}],
		"intensityBounds":{"min":0.1,"max":0.8},
		"cueResponses":{"groove":[{"target":"param0","curve":{"kind":"constant","value":0.5}}]},
		"palette":{"mode":"accent"},
		"motion":{"mode":"calm"},
		"camera":{"mode":"locked"},
		"overlay":{"allowed":true,"blend":"additive"},
		"reducedMotion":{"mode":"static","fallback":"dark-room"},
		"performance":{"minTier":"Lite","maxPasses":1,"textureBudgetMiB":16}
	}`)
	if _, err := DecodeSmartPresetManifest(valid); err != nil {
		t.Fatalf("valid manifest rejected: %v", err)
	}
	cases := map[string]string{
		"unknown field": strings.Replace(string(valid), `"performance"`, `"surprise":true,"performance"`, 1),
		"unknown cue":   strings.Replace(string(valid), `"groove"`, `"solo"`, 1),
		"expression":    strings.Replace(string(valid), `"value":0.5`, `"value":"param0 + 1"`, 1),
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodeSmartPresetManifest([]byte(body)); err == nil {
				t.Fatal("invalid manifest accepted")
			}
		})
	}
}

func TestSmartPresetCurveFixture(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "contracts", "visualsync", "smartpreset", "v1", "curve-fixtures.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Version string `json:"version"`
		Cases   []struct {
			Name  string     `json:"name"`
			Curve Curve      `json:"curve"`
			Input CurveInput `json:"input"`
			Want  float64    `json:"want"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	if fixture.Version != SmartPresetContractVersion {
		t.Fatalf("fixture version = %q, want %q", fixture.Version, SmartPresetContractVersion)
	}
	seen := map[string]bool{}
	for _, tc := range fixture.Cases {
		t.Run(tc.Name, func(t *testing.T) {
			got, err := EvaluateCurve(tc.Curve, tc.Input)
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.Want {
				t.Fatalf("EvaluateCurve = %v, want %v", got, tc.Want)
			}
			seen[tc.Curve.Kind] = true
		})
	}
	for _, kind := range SmartPresetCurveKinds {
		if !seen[kind] {
			t.Fatalf("fixture missing curve kind %q", kind)
		}
	}
}
