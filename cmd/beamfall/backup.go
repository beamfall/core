package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	backupcore "github.com/beamfall/core/internal/backup"
	"github.com/beamfall/core/internal/config"
	"github.com/beamfall/core/internal/store"
)

var (
	restoreBackupFn        = store.RestoreBackup
	renameRestoreDataDir   = os.Rename
	syncRestoreDataDirRoot = syncRestoreDataDirParent
)

func backup(ctx context.Context, args []string, stdout io.Writer) error {
	if len(args) > 0 && args[0] == "inspect" {
		return backupInspect(args[1:], stdout)
	}

	var opts cliHTTPOptions
	var dest string
	fs := commandFlagSet("beamfall backup", stdout)
	addHTTPFlags(fs, &opts)
	fs.StringVar(&dest, "dest", "", "backup destination directory")
	if helped, err := parseCommandFlags(fs, args); err != nil || helped {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}
	if dest == "" {
		return fmt.Errorf("--dest is required")
	}
	var resp struct {
		Backup struct {
			Manifest string `json:"manifest"`
		} `json:"backup"`
	}
	if err := cliGraphQL(ctx, opts,
		"mutation Backup($dest: String!) { backup(dest: $dest) { manifest } }",
		map[string]any{"dest": dest},
		&resp,
	); err != nil {
		return err
	}
	if stdout != nil {
		_, _ = fmt.Fprintf(stdout, "%s\n", resp.Backup.Manifest)
	}
	return nil
}

func backupInspect(args []string, stdout io.Writer) error {
	keyText, args, err := extractHiddenKey(args)
	if err != nil {
		return err
	}

	var inputPath, outputPath string
	fs := commandFlagSet("beamfall backup inspect (debug-only)", stdout)
	fs.StringVar(&inputPath, "input", "", "encrypted backup input path")
	fs.StringVar(&outputPath, "output", "", "decrypted output path")
	if helped, err := parseCommandFlags(fs, args); err != nil || helped {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}
	if strings.TrimSpace(inputPath) == "" {
		return fmt.Errorf("--input is required")
	}
	if strings.TrimSpace(outputPath) == "" {
		return fmt.Errorf("--output is required")
	}
	if strings.TrimSpace(keyText) == "" {
		return fmt.Errorf("--key is required")
	}

	key, err := backupcore.ParseBackupKey(keyText)
	if err != nil {
		return fmt.Errorf("invalid --key")
	}
	//nolint:gosec // debug-only CLI intentionally reads the caller-selected backup path.
	src, err := os.Open(inputPath)
	if err != nil {
		return err
	}
	defer src.Close()

	//nolint:gosec // debug-only CLI intentionally creates the caller-selected output path.
	dst, err := os.OpenFile(outputPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	complete := false
	defer func() {
		_ = dst.Close()
		if !complete {
			_ = os.Remove(outputPath)
		}
	}()

	info, err := backupcore.InspectEnvelope(key, src, dst)
	if err != nil {
		return err
	}
	if err := dst.Sync(); err != nil {
		return err
	}
	if err := dst.Close(); err != nil {
		return err
	}
	complete = true

	if stdout == nil {
		return nil
	}
	return json.NewEncoder(stdout).Encode(struct {
		Version                       int    `json:"version"`
		RecipientKeyFingerprint       string `json:"recipient_key_fingerprint"`
		EphemeralSenderKeyFingerprint string `json:"ephemeral_sender_key_fingerprint"`
	}{
		Version:                       info.Version,
		RecipientKeyFingerprint:       info.RecipientKeyFingerprint,
		EphemeralSenderKeyFingerprint: info.EphemeralSenderPublicKey.Fingerprint(),
	})
}

func extractHiddenKey(args []string) (string, []string, error) {
	filtered := make([]string, 0, len(args))
	var key string
	keySet := false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			filtered = append(filtered, args[i:]...)
			break
		}
		if arg == "--key" {
			if keySet {
				return "", nil, fmt.Errorf("--key may only be specified once")
			}
			if i+1 >= len(args) {
				return "", nil, fmt.Errorf("--key requires a value")
			}
			key = args[i+1]
			keySet = true
			i++
			continue
		}
		if strings.HasPrefix(arg, "--key=") {
			if keySet {
				return "", nil, fmt.Errorf("--key may only be specified once")
			}
			key = strings.TrimPrefix(arg, "--key=")
			keySet = true
			continue
		}
		filtered = append(filtered, arg)
	}
	return key, filtered, nil
}

func restore(ctx context.Context, args []string, stdout io.Writer) error {
	keyText, args, err := extractHiddenKey(args)
	if err != nil {
		return err
	}
	cfg := config.Default()
	if value := os.Getenv("BEAMFALL_DATA_DIR"); value != "" {
		cfg.DataDir = value
	}
	var manifest, rollbackManifest string
	var replaceIdentity bool
	fs := commandFlagSet("beamfall restore", stdout)
	fs.StringVar(&manifest, "manifest", "", "encrypted Backup Set or legacy manifest path")
	fs.StringVar(&rollbackManifest, "rollback-manifest", "", "plaintext pre-migration manifest v2")
	fs.StringVar(&cfg.DataDir, "data-dir", cfg.DataDir, "data directory")
	fs.BoolVar(&replaceIdentity, "replace-identity", false, "replace divergent secrets/TLS identity")
	if helped, err := parseCommandFlags(fs, args); err != nil || helped {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}
	if strings.TrimSpace(manifest) == "" && strings.TrimSpace(rollbackManifest) == "" {
		return fmt.Errorf("--manifest or --rollback-manifest is required")
	}
	if strings.TrimSpace(manifest) != "" && strings.TrimSpace(rollbackManifest) != "" {
		return fmt.Errorf("--manifest and --rollback-manifest are mutually exclusive")
	}
	if strings.TrimSpace(cfg.DataDir) == "" {
		return fmt.Errorf("--data-dir is required")
	}
	if err := refuseServerLock(cfg.DataDir); err != nil {
		return err
	}
	if rollbackManifest != "" {
		previous, err := store.RollbackMigrationSnapshot(ctx, rollbackManifest, cfg.DataDir)
		if err != nil {
			return err
		}
		if stdout != nil {
			_, _ = fmt.Fprintf(stdout, "rolled back databases in %s (replaced databases at %s)\n", cfg.DataDir, previous)
		}
		return nil
	}
	var decryptedSetDir string
	if filepath.Base(manifest) == "set.bbk" {
		if strings.TrimSpace(keyText) == "" {
			return fmt.Errorf("--key is required for an encrypted Backup Set")
		}
		key, err := backupcore.ParseBackupKey(keyText)
		keyText = ""
		if err != nil {
			return fmt.Errorf("invalid --key")
		}
		decryptedSetDir, err = os.MkdirTemp("", "beamfall-restore-decrypted-")
		if err != nil {
			return err
		}
		if err := os.Remove(decryptedSetDir); err != nil {
			return err
		}
		defer os.RemoveAll(decryptedSetDir)
		if _, err := backupcore.OpenDirectory(ctx, key, manifest, decryptedSetDir); err != nil {
			return err
		}
		manifest = filepath.Join(decryptedSetDir, "manifest.json")
	}
	if err := store.VerifyBackupManifest(ctx, manifest); err != nil {
		return err
	}
	if err := store.PreflightRestoreIdentity(ctx, manifest, cfg.DataDir, replaceIdentity); err != nil {
		return err
	}
	snapshot, err := snapshotRestoreDataDir(cfg.DataDir)
	if err != nil {
		return err
	}
	if err := restoreBackupFn(ctx, manifest, cfg.DataDir); err != nil {
		return rollbackFailedRestoreDataDir(cfg.DataDir, snapshot, err)
	}
	if stdout != nil {
		if snapshot == "" {
			_, _ = fmt.Fprintf(stdout, "restored %s\n", cfg.DataDir)
		} else {
			_, _ = fmt.Fprintf(stdout, "restored %s (previous data at %s)\n", cfg.DataDir, snapshot)
		}
	}
	return nil
}

func snapshotRestoreDataDir(dataDir string) (string, error) {
	_, err := os.ReadDir(dataDir)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	snapshot := dataDir + ".pre-restore-" + time.Now().UTC().Format("20060102T150405.000000000Z")
	if err := renameRestoreDataDir(dataDir, snapshot); err != nil {
		return "", err
	}
	if err := syncRestoreDataDirRoot(dataDir); err != nil {
		if rollbackErr := renameRestoreDataDir(snapshot, dataDir); rollbackErr != nil {
			return "", fmt.Errorf(
				"snapshot restore data directory sync failed: %w; original data remains at %s; rollback to %s failed: %v",
				err,
				snapshot,
				dataDir,
				rollbackErr,
			)
		}
		if rollbackSyncErr := syncRestoreDataDirRoot(dataDir); rollbackSyncErr != nil {
			return "", fmt.Errorf(
				"snapshot restore data directory sync failed: %w; original data was restored at %s, but syncing the rollback failed: %v",
				err,
				dataDir,
				rollbackSyncErr,
			)
		}
		return "", fmt.Errorf("snapshot restore data directory sync failed and was rolled back to %s: %w", dataDir, err)
	}
	return snapshot, nil
}

func rollbackFailedRestoreDataDir(dataDir, snapshot string, restoreErr error) error {
	if snapshot == "" {
		// #nosec G703 -- dataDir is operator-supplied CLI/env input and is
		// inspected only to report where a failed restore was preserved.
		_, err := os.Lstat(dataDir)
		if err == nil {
			return fmt.Errorf("restore failed: %w; no original data directory existed; failed restore remains at %s", restoreErr, dataDir)
		}
		if os.IsNotExist(err) {
			return fmt.Errorf("restore failed: %w; no original or failed restore data directory exists at %s", restoreErr, dataDir)
		}
		return fmt.Errorf("restore failed: %w; no original data directory existed; failed restore recovery location %s could not be inspected: %v", restoreErr, dataDir, err)
	}

	failedRestore := ""
	// #nosec G703 -- every writer of cfg.DataDir in this package is
	// operator-supplied CLI/env input (a -data-dir flag or BEAMFALL_DATA_DIR);
	// no archive or manifest content reaches it. Archive-derived paths are
	// confined separately in internal/store (cleanOperationalRelativePath /
	// confinedOperationalPath / pathWithinRoot). Metadata-only Lstat.
	if _, err := os.Lstat(dataDir); err == nil {
		failedRestore, err = nextFailedRestorePath(dataDir)
		if err != nil {
			return fmt.Errorf(
				"restore failed: %w; original data remains at %s; failed restore remains at %s because no recovery path could be reserved: %v",
				restoreErr,
				snapshot,
				dataDir,
				err,
			)
		}
		if err := renameRestoreDataDir(dataDir, failedRestore); err != nil {
			return fmt.Errorf(
				"restore failed: %w; original data remains at %s; failed restore remains at %s because rollback staging failed: %v",
				restoreErr,
				snapshot,
				dataDir,
				err,
			)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf(
			"restore failed: %w; original data remains at %s; failed restore recovery location %s could not be inspected: %v",
			restoreErr,
			snapshot,
			dataDir,
			err,
		)
	}

	if err := renameRestoreDataDir(snapshot, dataDir); err != nil {
		if failedRestore == "" {
			return fmt.Errorf(
				"restore failed: %w; original data remains at %s; no failed restore directory was created; rollback to %s failed: %v",
				restoreErr,
				snapshot,
				dataDir,
				err,
			)
		}
		return fmt.Errorf(
			"restore failed: %w; original data remains at %s; failed restore is preserved at %s; rollback failed: %v",
			restoreErr,
			snapshot,
			failedRestore,
			err,
		)
	}
	if err := syncRestoreDataDirRoot(dataDir); err != nil {
		if failedRestore == "" {
			return fmt.Errorf(
				"restore failed: %w; original data was restored at %s and no failed restore directory was created, but syncing the rollback failed: %v",
				restoreErr,
				dataDir,
				err,
			)
		}
		return fmt.Errorf(
			"restore failed: %w; original data was restored at %s and failed restore is preserved at %s, but syncing the rollback failed: %v",
			restoreErr,
			dataDir,
			failedRestore,
			err,
		)
	}
	if failedRestore == "" {
		return fmt.Errorf("restore failed: %w; original data restored at %s; no failed restore directory was created", restoreErr, dataDir)
	}
	return fmt.Errorf(
		"restore failed: %w; original data restored at %s; failed restore preserved at %s",
		restoreErr,
		dataDir,
		failedRestore,
	)
}

func nextFailedRestorePath(dataDir string) (string, error) {
	base := dataDir + ".failed-restore-" + time.Now().UTC().Format("20060102T150405.000000000Z")
	for suffix := 0; ; suffix++ {
		candidate := base
		if suffix != 0 {
			candidate = fmt.Sprintf("%s-%d", base, suffix)
		}
		// #nosec G703 -- candidate is dataDir plus a literal ".failed-restore-"
		// timestamp suffix. Every writer of cfg.DataDir in this package is
		// operator-supplied CLI/env input; no archive or manifest content
		// reaches it, and the suffix adds none. Metadata-only Lstat.
		if _, err := os.Lstat(candidate); os.IsNotExist(err) {
			return candidate, nil
		} else if err != nil {
			return "", err
		}
	}
}

func syncRestoreDataDirParent(dataDir string) error {
	// #nosec G703 -- every writer of cfg.DataDir in this package is
	// operator-supplied CLI/env input (a -data-dir flag or BEAMFALL_DATA_DIR);
	// no archive or manifest content reaches it. The directory is opened solely
	// to fsync the rename; nothing is read from it.
	parent, err := os.Open(filepath.Dir(dataDir))
	if err != nil {
		return err
	}
	if err := parent.Sync(); err != nil {
		_ = parent.Close()
		return err
	}
	return parent.Close()
}
