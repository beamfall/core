package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ResolveDataDir selects one path for startup to use for the catalog, server
// lock, secrets and default scratch. Configs assembled outside Load call it
// before opening the app.
func (cfg *Config) ResolveDataDir() error {
	if cfg.DataDir == "" {
		return errors.New("data-dir is required")
	}
	if cfg.DataDir == cfg.resolvedDataDir {
		return nil
	}
	resolved, err := evalDataDir(cfg.DataDir)
	if err != nil {
		return fmt.Errorf("resolve data-dir: %w", err)
	}
	if !filepath.IsAbs(resolved) {
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("resolve data-dir: %w", err)
		}
		cwd, err = filepath.EvalSymlinks(cwd)
		if err != nil {
			return fmt.Errorf("resolve data-dir: %w", err)
		}
		resolved = filepath.Join(cwd, resolved)
	}
	legacy, err := filepath.Abs(cfg.DataDir)
	if err != nil {
		return fmt.Errorf("resolve data-dir: %w", err)
	}
	if err := checkLegacyDataDir(legacy, resolved); err != nil {
		return err
	}
	if !cfg.TranscodeScratchDirExplicit && cfg.TranscodeScratchDir == filepath.Join(cfg.DataDir, "transcode") {
		cfg.TranscodeScratchDir = filepath.Join(resolved, "transcode")
	}
	cfg.DataDir = resolved
	cfg.resolvedDataDir = resolved
	return nil
}

// evalDataDir also handles first startup, when some trailing components may
// need to be created. Leave creation to the caller after the recovery check.
func evalDataDir(path string) (string, error) {
	resolved, err := filepath.EvalSymlinks(path)
	if !errors.Is(err, os.ErrNotExist) {
		return resolved, err
	}
	path = strings.TrimRight(path, string(filepath.Separator))
	parent, name := filepath.Split(path)
	if name == "" || name == "." || name == ".." {
		return "", err
	}
	// A dangling symlink is an existing entry, not part of a missing suffix.
	if _, statErr := os.Lstat(path); !errors.Is(statErr, os.ErrNotExist) {
		return "", err
	}
	if parent == "" {
		parent = "."
	}
	resolved, err = evalDataDir(parent)
	if err != nil {
		return "", err
	}
	return filepath.Join(resolved, name), nil
}

func checkLegacyDataDir(legacy, resolved string) error {
	if legacy == resolved {
		return nil
	}
	// Older versions cleaned DataDir before joining the catalog and scratch
	// paths. A symlink followed by .. can put those files in a different tree.
	oldRoot, err := filepath.EvalSymlinks(legacy)
	if errors.Is(err, os.ErrNotExist) || err == nil && oldRoot == resolved {
		return nil
	}
	if err != nil {
		return fmt.Errorf("check previous data-dir %q: %w", legacy, err)
	}
	for _, name := range []string{"db", "transcode", "stream-token.key"} {
		_, err := os.Lstat(filepath.Join(oldRoot, name)) // #nosec G703 -- operator-configured directory, fixed entry names
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return fmt.Errorf("check previous data-dir %q: %w", oldRoot, err)
		}
		return fmt.Errorf("data-dir resolves to %q, but previous startup files exist in %q; use %q as data-dir to recover the existing installation", resolved, oldRoot, oldRoot)
	}
	return nil
}
