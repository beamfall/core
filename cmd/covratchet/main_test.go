package main

import (
	"errors"
	"io"
	"os"
	"strings"
	"testing"
)

func TestMeasureCoverageFailsWhenGoTestFails(t *testing.T) {
	measured, err := measureCoverageWith(func(string) ([]byte, error) {
		return []byte("ok  \tgithub.com/beamfall/core/internal/foo\t0.01s\tcoverage: 75.0% of statements\n"), errors.New("exit status 1")
	})
	if err == nil {
		t.Fatal("measureCoverageWith returned nil error for failing go test")
	}
	if measured.Packages["github.com/beamfall/core/internal/foo"] != 75.0 {
		t.Fatalf("measured coverage = %#v, want parsed coverage retained", measured)
	}
}

func TestMeasureCoverageReportsPackageTestFailure(t *testing.T) {
	_, err := measureCoverageWith(func(string) ([]byte, error) {
		return []byte("--- FAIL: TestBroken (0.00s)\n" + strings.Repeat("diagnostic noise\n", 40) + "FAIL\tgithub.com/beamfall/core/internal/foo\t0.01s\n"), errors.New("exit status 1")
	})
	if err == nil {
		t.Fatal("measureCoverageWith returned nil error for failing go test")
	}
	for _, want := range []string{
		"go test -coverprofile ./... failed",
		"--- FAIL: TestBroken",
		"FAIL\tgithub.com/beamfall/core/internal/foo",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error = %q, want %q", err, want)
		}
	}
}

func TestCheckFailsWhenFlooredPackageIsNotMeasured(t *testing.T) {
	floors := &floorFile{
		SchemaVersion: 1,
		Floors: map[string]float64{
			"github.com/beamfall/core/internal/foo": 80.0,
		},
	}
	if got := check(&measurement{Packages: map[string]float64{}}, floors); got == 0 {
		t.Fatal("check passed with a configured floor that was not measured")
	}
}

func TestCheckReportsCoverageFloorRegressionWithPackageAttribution(t *testing.T) {
	floors := &floorFile{
		SchemaVersion: 1,
		Floors: map[string]float64{
			"github.com/beamfall/core/internal/foo": 80.0,
		},
	}
	measured := &measurement{Packages: map[string]float64{"github.com/beamfall/core/internal/foo": 77.0}}

	var got int
	out := captureStdout(t, func() {
		got = check(measured, floors)
	})
	if got == 0 {
		t.Fatal("check passed with coverage below floor")
	}
	for _, want := range []string{
		"FAIL  internal/foo",
		"measured=77.0% floor=80.0%",
		"(Δ=-3.0%)",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("output = %q, want %q", out, want)
		}
	}
}

func TestCheckPassesMeasuredPackageAtFloor(t *testing.T) {
	floors := &floorFile{
		SchemaVersion: 1,
		Floors: map[string]float64{
			"github.com/beamfall/core/internal/foo": 80.0,
		},
	}
	measured := &measurement{Packages: map[string]float64{"github.com/beamfall/core/internal/foo": 80.0}}
	if got := check(measured, floors); got != 0 {
		t.Fatalf("check exit = %d, want 0", got)
	}
}

func TestCheckPassesAggregateAtFloor(t *testing.T) {
	aggregate := 70.0
	floors := &floorFile{SchemaVersion: 1, Aggregate: &aggregate, Floors: map[string]float64{}}
	measured := &measurement{Aggregate: 70.0, Packages: map[string]float64{}}
	if got := check(measured, floors); got != 0 {
		t.Fatalf("check exit = %d, want 0", got)
	}
}

func TestCheckReportsLargeUnlistedPackages(t *testing.T) {
	floors := &floorFile{SchemaVersion: 1, Floors: map[string]float64{}}
	measured := &measurement{
		Packages: map[string]float64{"github.com/beamfall/core/internal/large": 75},
		Lines:    map[string]int{"github.com/beamfall/core/internal/large": 501},
	}
	out := captureStdout(t, func() {
		if got := check(measured, floors); got != 0 {
			t.Fatalf("check exit = %d, want 0", got)
		}
	})
	if !strings.Contains(out, "INFO  internal/large") || !strings.Contains(out, "source_lines=501 (unlisted; threshold=500)") {
		t.Fatalf("output = %q, want large unlisted package report", out)
	}
}

func TestParsePackageLinesSumsFileExtents(t *testing.T) {
	tmp, err := os.CreateTemp("", "covratchet-lines-*.out")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.WriteString("mode: set\ngithub.com/beamfall/core/internal/foo/a.go:1.1,20.1 3 1\ngithub.com/beamfall/core/internal/foo/a.go:4.1,10.1 1 0\ngithub.com/beamfall/core/internal/foo/b.go:1.1,30.1 2 1\n"); err != nil {
		t.Fatal(err)
	}
	if err := tmp.Close(); err != nil {
		t.Fatal(err)
	}
	_, got, err := parseCoverageProfile(tmp.Name())
	if err != nil {
		t.Fatal(err)
	}
	if got["github.com/beamfall/core/internal/foo"] != 50 {
		t.Fatalf("package lines = %d, want 50", got["github.com/beamfall/core/internal/foo"])
	}
}

func TestParseAggregateCoverage(t *testing.T) {
	tmp, err := os.CreateTemp("", "covratchet-test-*.out")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.WriteString("mode: set\nx.go:1.1,2.1 3 1\ny.go:1.1,2.1 1 0\n"); err != nil {
		t.Fatal(err)
	}
	if err := tmp.Close(); err != nil {
		t.Fatal(err)
	}
	got, _, err := parseCoverageProfile(tmp.Name())
	if err != nil {
		t.Fatal(err)
	}
	if got != 75.0 {
		t.Fatalf("aggregate = %.1f, want 75.0", got)
	}
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	defer func() { os.Stdout = old }()

	fn()

	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}
