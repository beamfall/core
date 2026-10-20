package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/beamfall/core/internal/config"
	"github.com/beamfall/core/internal/store"
)

// resetAdminPassword implements the ADR-0012 §6 filesystem-reset path: the
// operator must have filesystem access to the data directory to reset the admin
// password, so there is no network surface to attack. The server must not be
// running while this writes.
func resetAdminPassword(ctx context.Context, args []string, stdout io.Writer) error {
	cfg := config.Default()
	if value := os.Getenv("BEAMFALL_DATA_DIR"); value != "" {
		cfg.DataDir = value
	}
	var password string
	fs := commandFlagSet("beamfall reset-admin-password", stdout)
	fs.StringVar(&cfg.DataDir, "data-dir", cfg.DataDir, "data directory")
	fs.StringVar(&password, "password", "", "new admin password (omit to read from stdin)")
	if helped, err := parseCommandFlags(fs, args); err != nil || helped {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}
	if password == "" {
		// Read from stdin so callers can pipe a password without it appearing in
		// the process list (--password is convenient for scripting but exposes the
		// secret in ps output).
		fmt.Fprint(os.Stderr, "New admin password: ")
		line, err := bufio.NewReader(os.Stdin).ReadString('\n')
		if err != nil {
			return fmt.Errorf("reading password from stdin: %w", err)
		}
		password = strings.TrimRight(line, "\r\n")
	}
	if strings.TrimSpace(password) == "" {
		return fmt.Errorf("admin password must not be empty")
	}
	if err := refuseServerLock(cfg.DataDir); err != nil {
		return err
	}
	st, err := store.Open(ctx, cfg.DataDir, store.Options{})
	if err != nil {
		return err
	}
	defer st.Close()
	if err := st.SetAdminPassword(ctx, password, time.Now().UTC()); err != nil {
		return err
	}
	if stdout != nil {
		_, _ = fmt.Fprintln(stdout, "admin password updated")
	}
	return nil
}
