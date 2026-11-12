package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/beamfall/core/internal/config"
	"github.com/beamfall/core/internal/pairing"
	"github.com/beamfall/core/internal/store"
)

// pairingCodeCommand mints a one-time owner pairing code on the host
// (CORE-1106). Like reset-admin-password it needs filesystem access to the data
// directory and refuses while the server runs, so it adds no network surface.
func pairingCodeCommand(ctx context.Context, args []string, stdout io.Writer) error {
	cfg := config.Default()
	if value := os.Getenv("BEAMFALL_DATA_DIR"); value != "" {
		cfg.DataDir = value
	}
	role := string(store.DeviceRoleManager)
	fs := commandFlagSet("beamfall pairing-code", stdout)
	fs.StringVar(&cfg.DataDir, "data-dir", cfg.DataDir, "data directory")
	fs.StringVar(&role, "role", role, "device role for the code: manager or viewer")
	if helped, err := parseCommandFlags(fs, args); err != nil || helped {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}
	if role != string(store.DeviceRoleManager) && role != string(store.DeviceRoleViewer) {
		return fmt.Errorf("--role must be manager or viewer, not %q", role)
	}
	if err := refuseServerLock(cfg.DataDir); err != nil {
		return err
	}
	st, err := store.Open(ctx, cfg.DataDir, store.Options{})
	if err != nil {
		return err
	}
	defer st.Close()
	code, err := pairing.NewService(st, nil).CreateOwnerCode(ctx, role)
	if errors.Is(err, pairing.ErrNoOwner) {
		return errors.New("no owner profile yet; finish first-run setup instead")
	}
	if err != nil {
		return err
	}
	if stdout != nil {
		_, _ = fmt.Fprintf(stdout, "%s\nStart the server, open http://<server>:8800/web/ and enter this code. It expires at %s.\n", code.Code, code.ExpiresAt)
	}
	return nil
}
