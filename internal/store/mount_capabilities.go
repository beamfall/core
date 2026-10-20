package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// CapabilityStatus is the observed support state of one mount capability.
type CapabilityStatus string

const (
	CapabilityUnknown     CapabilityStatus = "unknown"
	CapabilitySupported   CapabilityStatus = "supported"
	CapabilityUnsupported CapabilityStatus = "unsupported"
)

// MountCapabilities records only behavior observed on the target mount.
type MountCapabilities struct {
	Mount                  string           `json:"mount"`
	Hardlinks              CapabilityStatus `json:"hardlinks"`
	AtomicRename           CapabilityStatus `json:"atomicRename"`
	CaseSensitive          CapabilityStatus `json:"caseSensitive"`
	PermissionPreservation CapabilityStatus `json:"permissionPreservation"`
	StableFileIdentity     CapabilityStatus `json:"stableFileIdentity"`
	ProbedAt               time.Time        `json:"probedAt"`
}

type mountCapabilityCache struct {
	Mounts map[string]mountCapabilityEntry `json:"mounts"`
}

type mountCapabilityEntry struct {
	Capabilities MountCapabilities `json:"capabilities"`
	IdentityPath string            `json:"identityPath"`
	Identity     string            `json:"identity"`
}

type mountCapabilityProbe struct {
	mu        sync.Mutex
	cachePath string
	loaded    bool
	cache     mountCapabilityCache
	seen      map[string]bool
	now       func() time.Time
	run       func(string) (MountCapabilities, error)
}

func newMountCapabilityProbe(cachePath string) *mountCapabilityProbe {
	return &mountCapabilityProbe{
		cachePath: cachePath,
		seen:      map[string]bool{},
		now:       time.Now,
		run:       probeMountCapabilities,
	}
}

// MountCapabilities returns the cached observation for path's mount, probing
// when no observation exists. Stable file identity remains unknown until a
// later Store instance can compare the same directory after a restart.
func (s *Store) MountCapabilities(path string) (MountCapabilities, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return MountCapabilities{}, err
	}
	return s.mountCapabilityProbe.probe(path, false)
}

// ReprobeMountCapabilities explicitly replaces the cached observation.
func (s *Store) ReprobeMountCapabilities(path string) (MountCapabilities, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return MountCapabilities{}, err
	}
	return s.mountCapabilityProbe.probe(path, true)
}

func (p *mountCapabilityProbe) probe(path string, reprobe bool) (MountCapabilities, error) {
	canonical, err := canonicalExistingPath(path)
	if err != nil {
		return MountCapabilities{}, err
	}
	info, err := os.Stat(canonical)
	if err != nil {
		return MountCapabilities{}, err
	}
	if !info.IsDir() {
		return MountCapabilities{}, ValidationError("mount probe path is not a folder")
	}
	key, mount, err := mountProbeLocation(canonical, info)
	if err != nil {
		return MountCapabilities{}, err
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.load(); err != nil {
		return MountCapabilities{}, err
	}
	if entry, ok := p.cache.Mounts[key]; ok && !reprobe {
		if !p.seen[key] {
			entry.Capabilities.StableFileIdentity = compareCachedFileIdentity(entry)
			p.cache.Mounts[key] = entry
			if err := p.save(); err != nil {
				return MountCapabilities{}, err
			}
		}
		p.seen[key] = true
		return entry.Capabilities, nil
	}

	capabilities, err := p.run(canonical)
	if err != nil {
		return MountCapabilities{}, err
	}
	capabilities.Mount = mount
	capabilities.StableFileIdentity = CapabilityUnknown
	capabilities.ProbedAt = p.now().UTC()
	identity, _ := mountProbeFileIdentity(info)
	p.cache.Mounts[key] = mountCapabilityEntry{
		Capabilities: capabilities,
		IdentityPath: canonical,
		Identity:     identity,
	}
	p.seen[key] = true
	if err := p.save(); err != nil {
		return MountCapabilities{}, err
	}
	return capabilities, nil
}

func (p *mountCapabilityProbe) load() error {
	if p.loaded {
		return nil
	}
	p.loaded = true
	p.cache.Mounts = map[string]mountCapabilityEntry{}
	raw, err := os.ReadFile(p.cachePath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, &p.cache)
}

func (p *mountCapabilityProbe) save() error {
	if err := os.MkdirAll(filepath.Dir(p.cachePath), 0o700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(p.cache, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	tmp := p.cachePath + ".tmp"
	if err := writeFileSync0600(tmp, raw); err != nil {
		return err
	}
	if err := os.Rename(tmp, p.cachePath); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return syncDir(filepath.Dir(p.cachePath))
}

func compareCachedFileIdentity(entry mountCapabilityEntry) CapabilityStatus {
	if entry.Identity == "" || entry.IdentityPath == "" {
		return CapabilityUnknown
	}
	info, err := os.Stat(entry.IdentityPath)
	if err != nil {
		return CapabilityUnknown
	}
	identity, ok := mountProbeFileIdentity(info)
	if !ok {
		return CapabilityUnknown
	}
	if identity == entry.Identity {
		return CapabilitySupported
	}
	return CapabilityUnsupported
}

func probeMountCapabilities(path string) (MountCapabilities, error) {
	result := MountCapabilities{
		Hardlinks:              CapabilityUnknown,
		AtomicRename:           CapabilityUnknown,
		CaseSensitive:          CapabilityUnknown,
		PermissionPreservation: CapabilityUnknown,
		StableFileIdentity:     CapabilityUnknown,
	}
	dir, err := os.MkdirTemp(path, ".beamfall-mount-probe-*")
	if err != nil {
		return result, nil
	}
	defer os.RemoveAll(dir)

	result.Hardlinks = probeHardlinks(dir)
	result.AtomicRename = probeAtomicRename(dir)
	result.CaseSensitive = probeCaseSensitivity(dir)
	result.PermissionPreservation = probePermissionPreservation(dir)
	return result, nil
}

func probeHardlinks(dir string) CapabilityStatus {
	source := filepath.Join(dir, "hardlink-source")
	target := filepath.Join(dir, "hardlink-target")
	if err := os.WriteFile(source, []byte("beamfall"), 0o600); err != nil {
		return CapabilityUnknown
	}
	if err := os.Link(source, target); err != nil {
		return unsupportedOrUnknown(err)
	}
	sourceInfo, sourceErr := os.Stat(source)
	targetInfo, targetErr := os.Stat(target)
	if sourceErr != nil || targetErr != nil {
		return CapabilityUnknown
	}
	if !os.SameFile(sourceInfo, targetInfo) {
		return CapabilityUnsupported
	}
	return CapabilitySupported
}

func probeAtomicRename(dir string) CapabilityStatus {
	source := filepath.Join(dir, "rename-source")
	target := filepath.Join(dir, "rename-target")
	if err := os.WriteFile(source, []byte("beamfall"), 0o600); err != nil {
		return CapabilityUnknown
	}
	before, err := os.Stat(source)
	if err != nil {
		return CapabilityUnknown
	}
	if err := os.Rename(source, target); err != nil {
		return unsupportedOrUnknown(err)
	}
	after, err := os.Stat(target)
	if err != nil || !os.SameFile(before, after) {
		return CapabilityUnsupported
	}
	return CapabilitySupported
}

func probeCaseSensitivity(dir string) CapabilityStatus {
	source := filepath.Join(dir, "case-Probe")
	alternate := filepath.Join(dir, "CASE-pROBE")
	if err := os.WriteFile(source, []byte("beamfall"), 0o600); err != nil {
		return CapabilityUnknown
	}
	info, err := os.Stat(alternate)
	if errors.Is(err, os.ErrNotExist) {
		return CapabilitySupported
	}
	if err != nil {
		return CapabilityUnknown
	}
	sourceInfo, err := os.Stat(source)
	if err != nil {
		return CapabilityUnknown
	}
	if os.SameFile(sourceInfo, info) {
		return CapabilityUnsupported
	}
	return CapabilityUnknown
}

func probePermissionPreservation(dir string) CapabilityStatus {
	path := filepath.Join(dir, "permissions")
	renamed := filepath.Join(dir, "permissions-renamed")
	if err := os.WriteFile(path, []byte("beamfall"), 0o600); err != nil {
		return CapabilityUnknown
	}
	if err := os.Chmod(path, 0o400); err != nil {
		return unsupportedOrUnknown(err)
	}
	if err := os.Rename(path, renamed); err != nil {
		return unsupportedOrUnknown(err)
	}
	info, err := os.Stat(renamed)
	if err != nil {
		return CapabilityUnknown
	}
	if info.Mode().Perm() == 0o400 {
		return CapabilitySupported
	}
	return CapabilityUnsupported
}

func unsupportedOrUnknown(err error) CapabilityStatus {
	if mountProbeUnsupported(err) {
		return CapabilityUnsupported
	}
	return CapabilityUnknown
}

func mountProbeDeviceKey(device uint64) string {
	return fmt.Sprintf("device:%d", device)
}
