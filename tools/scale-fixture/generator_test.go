package main

import (
	"crypto/sha256"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func TestSameTupleProducesBitIdenticalCorpus(t *testing.T) {
	first := filepath.Join(t.TempDir(), "first")
	second := filepath.Join(t.TempDir(), "second")
	base := Config{Tier: "10k", Profile: "messy-correctness", Seed: 8675309, Count: 96}

	one := base
	one.Output = first
	manifestOne, err := Generate(one)
	if err != nil {
		t.Fatal(err)
	}
	two := base
	two.Output = second
	manifestTwo, err := Generate(two)
	if err != nil {
		t.Fatal(err)
	}
	if manifestOne != manifestTwo {
		t.Fatalf("manifests differ:\n%+v\n%+v", manifestOne, manifestTwo)
	}
	if got, want := treeDigest(t, first), treeDigest(t, second); got != want {
		t.Fatalf("corpus digest differs: %s != %s", got, want)
	}
}

func TestProfilesKeepAdversityBehindMessyFlag(t *testing.T) {
	perf := Config{Tier: "10k", Profile: "perf-realistic", Seed: 1, Output: filepath.Join(t.TempDir(), "perf"), Count: 128}
	perfManifest, err := Generate(perf)
	if err != nil {
		t.Fatal(err)
	}
	if perfManifest.Stats.BrokenFiles != 0 || perfManifest.Stats.AdversarialNames != 0 {
		t.Fatalf("perf profile contains messy cases: %+v", perfManifest.Stats)
	}

	messy := perf
	messy.Profile = "messy-correctness"
	messy.Output = filepath.Join(t.TempDir(), "messy")
	messyManifest, err := Generate(messy)
	if err != nil {
		t.Fatal(err)
	}
	if messyManifest.Stats.BrokenFiles == 0 || messyManifest.Stats.AdversarialNames == 0 || messyManifest.Stats.ZeroByteFiles == 0 {
		t.Fatalf("messy profile lacks required adversity: %+v", messyManifest.Stats)
	}
}

func TestMeasureValidatesTupleAndEmitsAllTimings(t *testing.T) {
	cfg := Config{Tier: "10k", Profile: "perf-realistic", Seed: 42, Output: filepath.Join(t.TempDir(), "corpus"), Count: 40}
	if _, err := Generate(cfg); err != nil {
		t.Fatal(err)
	}
	timings, err := Measure(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if timings.FileCount != 40 || timings.ScanNanoseconds <= 0 || timings.SearchNanoseconds <= 0 || timings.ThumbnailNanoseconds <= 0 || timings.ThumbnailProbes == 0 {
		t.Fatalf("incomplete timings: %+v", timings)
	}
	wrong := cfg
	wrong.Seed++
	if _, err := Measure(wrong); err == nil {
		t.Fatal("Measure accepted a cache with the wrong seed")
	}
}

func TestGenerateRefusesNonEmptyTarget(t *testing.T) {
	output := t.TempDir()
	if err := os.WriteFile(filepath.Join(output, "keep"), []byte("user data"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Generate(Config{Tier: "10k", Profile: "perf-realistic", Seed: 1, Output: output, Count: 1})
	if err == nil || !strings.Contains(err.Error(), "not empty") {
		t.Fatalf("expected non-empty refusal, got %v", err)
	}
}

func treeDigest(t *testing.T, root string) string {
	t.Helper()
	var paths []string
	if err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			paths = append(paths, path)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	sort.Strings(paths)
	hash := sha256.New()
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(hash, "%s\x00%x\n", filepath.ToSlash(rel), sha256.Sum256(data))
	}
	return fmt.Sprintf("%x", hash.Sum(nil))
}
