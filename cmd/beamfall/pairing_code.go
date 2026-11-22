package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"time"

	"github.com/beamfall/core/internal/config"
	"github.com/beamfall/core/internal/pairing"
	"github.com/beamfall/core/internal/store"
)

// pairingCodeCommand mints a one-time owner pairing code on the host
// (CORE-1106). Like reset-admin-password it needs filesystem access to the data
// directory, so it adds no network surface. It refuses to run while a server
// holds the data directory, because opening the store runs startup maintenance
// that a live server owns. The code lives in the database and only a running
// server redeems it, so the printed step starts the server first (CORE-1734).
func pairingCodeCommand(ctx context.Context, args []string, stdout io.Writer) error {
	cfg := config.Default()
	if value := os.Getenv("BEAMFALL_DATA_DIR"); value != "" {
		cfg.DataDir = value
	}
	if value := os.Getenv("BEAMFALL_ADDR"); value != "" {
		cfg.Addr = value
	}
	role := string(store.DeviceRoleManager)
	fs := commandFlagSet("beamfall pairing-code", stdout)
	fs.StringVar(&cfg.DataDir, "data-dir", cfg.DataDir, "data directory")
	fs.StringVar(&cfg.Addr, "addr", cfg.Addr, "the server's listen address, for the printed URL")
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
	// Store log lines (migrations, "store opened") go to stderr at WARN and
	// above only, so the code and its instruction stand alone (CORE-1609).
	quiet := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	st, err := store.Open(ctx, cfg.DataDir, store.Options{Logger: quiet, RequireExisting: true})
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
		interfaceAddrs, _ := net.InterfaceAddrs()
		_, _ = fmt.Fprintf(stdout, "%s\nStart the server, then open %s and enter this code %s.\n",
			code.Code, pairingWebURL(cfg.Addr, interfaceAddrs), pairingExpiry(code.ExpiresAt, time.Now()))
	}
	return nil
}

// pairingWebURL is the /web/ address to open for the configured listen address.
// A wildcard listener is reached on the machine's first LAN IPv4 address, since
// the code is usually entered on another device; loopback is the fallback.
func pairingWebURL(listenAddr string, interfaceAddrs []net.Addr) string {
	host, port, err := net.SplitHostPort(listenAddr)
	if err != nil {
		return "http://127.0.0.1:2326/web/"
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
		for _, addr := range interfaceAddrs {
			ipNet, ok := addr.(*net.IPNet)
			if !ok {
				continue
			}
			if ip := ipNet.IP.To4(); ip != nil && !ip.IsLoopback() && !ip.IsLinkLocalUnicast() && !ip.IsUnspecified() {
				host = ip.String()
				break
			}
		}
	}
	return "http://" + net.JoinHostPort(host, port) + "/web/"
}

// pairingExpiry phrases the code's RFC 3339 expiry for a person: a relative
// duration and the local wall-clock time.
func pairingExpiry(expiresAt string, now time.Time) string {
	at, err := time.Parse(time.RFC3339, expiresAt)
	if err != nil {
		return "before it expires at " + expiresAt
	}
	minutes := int(at.Sub(now).Round(time.Minute) / time.Minute)
	unit := "minutes"
	if minutes == 1 {
		unit = "minute"
	}
	return fmt.Sprintf("within %d %s (it expires at %s)", minutes, unit, at.Local().Format("15:04"))
}
