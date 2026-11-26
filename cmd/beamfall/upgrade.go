package main

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/beamfall/core/internal/app"
	backupcore "github.com/beamfall/core/internal/backup"
	"github.com/beamfall/core/internal/config"
)

// upgradeCommand implements `beamfall upgrade stage|apply|status` (ADR-0243 D1).
// It reuses upgradeVerifyKey only in tests; production defaults nil (pinned key).
func upgradeCommand(ctx context.Context, args []string, stdout io.Writer) error {
	return upgradeCommandWithKey(ctx, args, stdout, nil)
}

func upgradeCommandWithKey(ctx context.Context, args []string, stdout io.Writer, verifyKey ed25519.PublicKey) error {
	if stdout == nil {
		stdout = io.Discard
	}
	if len(args) == 0 {
		printUpgradeUsage(stdout)
		return errors.New("upgrade requires a subcommand: stage, apply, or status")
	}
	switch args[0] {
	case "-h", "--help", "help":
		printUpgradeUsage(stdout)
		return nil
	case "stage":
		return upgradeStageCommand(args[1:], stdout, verifyKey)
	case "apply":
		return upgradeApplyCommand(ctx, args[1:], stdout, verifyKey)
	case "status":
		return upgradeStatusCommand(args[1:], stdout)
	default:
		printUpgradeUsage(stdout)
		return fmt.Errorf("unknown upgrade subcommand %q (want stage, apply, or status)", args[0])
	}
}

func printUpgradeUsage(w io.Writer) {
	_, _ = io.WriteString(w, "usage: beamfall upgrade <stage|apply|status> [flags]\n")
	_, _ = io.WriteString(w, "  stage   Verify a signed release dist dir and stage it under --data-dir\n")
	_, _ = io.WriteString(w, "  apply   Apply the staged update to --binary (requires server stopped)\n")
	_, _ = io.WriteString(w, "  status  Inspect backup update gate and staged update state as JSON\n")
}

func upgradeStageCommand(args []string, stdout io.Writer, verifyKey ed25519.PublicKey) error {
	cfg := config.Default()
	if value := os.Getenv("BEAMFALL_DATA_DIR"); value != "" {
		cfg.DataDir = value
	}
	var distDir string
	fs := commandFlagSet("beamfall upgrade stage", stdout)
	fs.StringVar(&cfg.DataDir, "data-dir", cfg.DataDir, "data directory")
	fs.StringVar(&distDir, "dist", "", "signed release distribution directory")
	if helped, err := parseCommandFlags(fs, args); err != nil || helped {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}
	if strings.TrimSpace(cfg.DataDir) == "" || strings.TrimSpace(distDir) == "" {
		return errors.New("--data-dir and --dist are required")
	}
	releaseLock, err := acquireExclusiveServerLock(cfg.DataDir)
	if err != nil {
		return err
	}
	defer releaseLock()
	marker, err := app.StageUpdate(cfg.DataDir, distDir, verifyKey, time.Now().UTC())
	if err != nil {
		return err
	}
	return writeJSONLine(stdout, marker)
}

func upgradeApplyCommand(ctx context.Context, args []string, stdout io.Writer, verifyKey ed25519.PublicKey) error {
	cfg := config.Default()
	if value := os.Getenv("BEAMFALL_DATA_DIR"); value != "" {
		cfg.DataDir = value
	}
	var targetBinary string
	var allowUnverified bool
	fs := commandFlagSet("beamfall upgrade apply", stdout)
	fs.StringVar(&cfg.DataDir, "data-dir", cfg.DataDir, "data directory")
	fs.StringVar(&targetBinary, "binary", "", "target executable path to replace (defaults to current executable)")
	fs.BoolVar(&allowUnverified, "allow-unverified-upgrade", false, "apply even when the backup update gate is blocked (ADR-0243 recovery escape hatch)")
	if helped, err := parseCommandFlags(fs, args); err != nil || helped {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}
	if strings.TrimSpace(cfg.DataDir) == "" {
		return errors.New("--data-dir is required")
	}
	if strings.TrimSpace(targetBinary) == "" {
		exe, err := os.Executable()
		if err != nil {
			return fmt.Errorf("resolve current executable: %w", err)
		}
		targetBinary = exe
	}
	releaseLock, err := acquireExclusiveServerLock(cfg.DataDir)
	if err != nil {
		return err
	}
	defer releaseLock()
	marker, err := app.ApplyStagedUpdate(ctx, cfg.DataDir, targetBinary, allowUnverified, verifyKey, time.Now().UTC())
	if err != nil {
		return err
	}
	return writeJSONLine(stdout, marker)
}

func upgradeStatusCommand(args []string, stdout io.Writer) error {
	cfg := config.Default()
	if value := os.Getenv("BEAMFALL_DATA_DIR"); value != "" {
		cfg.DataDir = value
	}
	fs := commandFlagSet("beamfall upgrade status", stdout)
	fs.StringVar(&cfg.DataDir, "data-dir", cfg.DataDir, "data directory")
	if helped, err := parseCommandFlags(fs, args); err != nil || helped {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}
	if strings.TrimSpace(cfg.DataDir) == "" {
		return errors.New("--data-dir is required")
	}
	backupStatus, err := backupcore.LoadStatus(cfg.DataDir)
	if err != nil {
		return err
	}
	staged, stagedExists, err := app.LoadStagedUpdate(cfg.DataDir)
	if err != nil {
		return err
	}
	payload := struct {
		BackupStatus backupcore.Status       `json:"backupStatus"`
		GateClear    bool                    `json:"updateGateClear"`
		GatePolicy   string                  `json:"upgradeGate"`
		Staged       *app.StagedUpdateMarker `json:"stagedUpdate,omitempty"`
	}{
		BackupStatus: backupStatus,
		GateClear:    backupStatus.UpdateGateClear(),
		GatePolicy:   backupStatus.EffectiveUpgradeGate(),
	}
	if stagedExists {
		payload.Staged = &staged
	}
	return writeJSONLine(stdout, payload)
}

func writeJSONLine(w io.Writer, value any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(value)
}

func acquireExclusiveServerLock(dataDir string) (func(), error) {
	releaseLock, err := acquireServerLock(dataDir)
	if err != nil {
		if errors.Is(err, errServerLockHeld) {
			return nil, errors.New("server appears to be running; stop the server, then run this command again")
		}
		return nil, err
	}
	return releaseLock, nil
}
