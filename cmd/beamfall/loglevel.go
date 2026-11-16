package main

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/beamfall/core/internal/config"
)

// logLevelFileName is the file under the data directory that SIGHUP re-reads
// to change the operator log level without a restart (CORE-1545).
const logLevelFileName = "log-level"

// logLevelReloader applies <data dir>/log-level to the operator logger's level.
type logLevelReloader struct {
	level  *slog.LevelVar
	base   slog.Level
	path   string
	logger *slog.Logger
}

func newLogLevelReloader(level *slog.LevelVar, dataDir string, logger *slog.Logger) *logLevelReloader {
	return &logLevelReloader{
		level:  level,
		base:   level.Level(),
		path:   filepath.Join(dataDir, logLevelFileName),
		logger: logger,
	}
}

// reload reads the level file once. A missing file restores the start-up level;
// an unreadable file or invalid value keeps the current level.
func (r *logLevelReloader) reload() {
	data, err := os.ReadFile(r.path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		r.set(r.base, "configured")
	case err != nil:
		r.logger.Warn("log level file unreadable; level unchanged", "path", r.path, "error", err, "level", r.level.Level())
	default:
		level, err := config.ParseLogLevel(string(data))
		if err != nil {
			r.logger.Warn("log level file invalid; level unchanged", "path", r.path, "error", err, "level", r.level.Level())
			return
		}
		r.set(level, "file")
	}
}

func (r *logLevelReloader) set(level slog.Level, source string) {
	old := r.level.Level()
	if old == level {
		return
	}
	// Log before applying so the record survives a change to Error.
	r.logger.Warn("log level changed", "from", old, "to", level, "source", source)
	r.level.Set(level)
}

// watch reloads the level on every SIGHUP until ctx is done. Registering for
// SIGHUP also stops it terminating the server.
func (r *logLevelReloader) watch(ctx context.Context) {
	hup := make(chan os.Signal, 1)
	signal.Notify(hup, syscall.SIGHUP)
	go func() {
		defer signal.Stop(hup)
		for {
			select {
			case <-ctx.Done():
				return
			case <-hup:
				r.reload()
			}
		}
	}()
}
