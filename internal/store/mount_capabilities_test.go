package store

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestMountCapabilityProbeLocalFixture(t *testing.T) {
	mount := t.TempDir()
	dataDir := t.TempDir()
	first, err := Open(context.Background(), dataDir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	got, err := first.MountCapabilities(mount)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("local fixture capabilities: %+v", got)
	for name, status := range map[string]CapabilityStatus{
		"hardlinks":               got.Hardlinks,
		"atomic rename":           got.AtomicRename,
		"case sensitivity":        got.CaseSensitive,
		"permission preservation": got.PermissionPreservation,
	} {
		if status == CapabilityUnknown {
			t.Errorf("%s = unknown on writable local fixture", name)
		}
	}
	if got.StableFileIdentity != CapabilityUnknown {
		t.Fatalf("first stable identity = %q, want unknown", got.StableFileIdentity)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	restarted, err := Open(context.Background(), dataDir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	afterRestart, err := restarted.MountCapabilities(mount)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("local fixture after restart: %+v", afterRestart)
	if afterRestart.StableFileIdentity != CapabilitySupported {
		t.Fatalf("stable identity after restart = %q, want supported", afterRestart.StableFileIdentity)
	}
}

func TestMountCapabilityProbeCachesPerMountAndReprobesExplicitly(t *testing.T) {
	mount := t.TempDir()
	probe := newMountCapabilityProbe(filepath.Join(t.TempDir(), "mount-capabilities.json"))
	calls := 0
	probe.run = func(string) (MountCapabilities, error) {
		calls++
		return MountCapabilities{
			Hardlinks:              CapabilitySupported,
			AtomicRename:           CapabilitySupported,
			CaseSensitive:          CapabilitySupported,
			PermissionPreservation: CapabilitySupported,
		}, nil
	}
	now := time.Unix(1, 0)
	probe.now = func() time.Time {
		now = now.Add(time.Second)
		return now
	}

	first, err := probe.probe(mount, false)
	if err != nil {
		t.Fatal(err)
	}
	otherPath := filepath.Join(mount, "other")
	if err := os.Mkdir(otherPath, 0o700); err != nil {
		t.Fatal(err)
	}
	cached, err := probe.probe(otherPath, false)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || !cached.ProbedAt.Equal(first.ProbedAt) {
		t.Fatalf("cached probe calls = %d, times %v and %v", calls, first.ProbedAt, cached.ProbedAt)
	}
	reprobed, err := probe.probe(otherPath, true)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 || !reprobed.ProbedAt.After(cached.ProbedAt) {
		t.Fatalf("explicit re-probe calls = %d, times %v and %v", calls, cached.ProbedAt, reprobed.ProbedAt)
	}
}

func TestMountCapabilityProbeDetectsUnstableIdentityAcrossRestart(t *testing.T) {
	parent := t.TempDir()
	mount := filepath.Join(parent, "mount")
	if err := os.Mkdir(mount, 0o700); err != nil {
		t.Fatal(err)
	}
	cache := filepath.Join(t.TempDir(), "mount-capabilities.json")
	if _, err := newMountCapabilityProbe(cache).probe(mount, false); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(mount, mount+"-old"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(mount, 0o700); err != nil {
		t.Fatal(err)
	}

	got, err := newMountCapabilityProbe(cache).probe(mount, false)
	if err != nil {
		t.Fatal(err)
	}
	if got.StableFileIdentity != CapabilityUnsupported {
		t.Fatalf("stable identity after replacement = %q, want unsupported", got.StableFileIdentity)
	}
}

func TestMountCapabilityProbePreservesUnknownRemoteResults(t *testing.T) {
	mount := t.TempDir()
	probe := newMountCapabilityProbe(filepath.Join(t.TempDir(), "mount-capabilities.json"))
	probe.run = func(string) (MountCapabilities, error) {
		return MountCapabilities{
			Hardlinks:              CapabilityUnknown,
			AtomicRename:           CapabilityUnknown,
			CaseSensitive:          CapabilityUnknown,
			PermissionPreservation: CapabilityUnknown,
		}, nil
	}

	got, err := probe.probe(mount, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("remote fixture capabilities: %+v", got)
	if got.Hardlinks != CapabilityUnknown ||
		got.AtomicRename != CapabilityUnknown ||
		got.CaseSensitive != CapabilityUnknown ||
		got.PermissionPreservation != CapabilityUnknown ||
		got.StableFileIdentity != CapabilityUnknown {
		t.Fatalf("unknown remote fixture was promoted: %+v", got)
	}
}

func TestStoreMountCapabilitiesRejectsFiles(t *testing.T) {
	st, err := Open(context.Background(), t.TempDir(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	file := filepath.Join(t.TempDir(), "media")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := st.MountCapabilities(file); err == nil {
		t.Fatal("MountCapabilities accepted a file")
	}
}
