package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"

	"github.com/beamfall/core/internal/app"
	"github.com/beamfall/core/internal/config"
	"github.com/beamfall/core/internal/ffmpeg"
	"github.com/beamfall/core/internal/integrationhost"
	"github.com/beamfall/core/internal/logtail"
)

func serve(ctx context.Context, args []string) error {
	cfg, err := config.LoadWithOutput(args, nil, os.Stdout)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if err := ffmpeg.EnableHLSDescendantReaping(); err != nil {
		return fmt.Errorf("enable HLS descendant reaping: %w", err)
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	level := new(slog.LevelVar)
	level.Set(cfg.LogLevel)
	slog.SetDefault(newOperatorLogger(os.Stderr, level))
	newLogLevelReloader(level, cfg.DataDir, slog.Default()).watch(ctx)
	// Enable FFmpeg invocation hardening: point the spawn seam at
	// this server binary so every ffmpeg/ffprobe run re-execs through the
	// ffmpeg-shim subcommand (rlimits + Linux landlock confinement). Only the
	// real serve entrypoint sets this; test harnesses that call app.New leave it
	// empty so a test binary never re-execs itself. The hardware probes take the
	// same shim, so they certify the confinement a transcode will run under.
	cfg.FFmpegHardeningShimPath = ffmpeg.DefaultShimPath()
	ffmpeg.SetProbeHardeningShim(cfg.FFmpegHardeningShimPath)
	cfg.IntegrationWorkerHardeningShimPath = integrationhost.DefaultShimPath()
	beamfall, release, err := startServer(ctx, cfg)
	if err != nil {
		return err
	}
	defer release()
	return beamfall.Run(ctx)
}

// startServer takes the data directory's server lock, which offline commands
// probe before writing, applies a staged restore under it, and builds the
// server. The caller runs the server and then calls release.
func startServer(ctx context.Context, cfg config.Config) (*app.App, func(), error) {
	release, err := lockServeDataDir(cfg.DataDir)
	if err != nil {
		return nil, nil, err
	}
	release, err = applyStagedRestore(ctx, cfg.DataDir, slog.Default(), release)
	if err != nil {
		return nil, nil, err
	}
	beamfall, err := app.New(ctx, cfg, slog.Default(), firstPartyBundles(), firstPartyHooks())
	if err != nil {
		release()
		return nil, nil, err
	}
	return beamfall, release, nil
}

func newOperatorLogger(w io.Writer, level slog.Leveler) *slog.Logger {
	// The handler also keeps the most recent lines, redacted, for the console
	// server-log read (CORE-1716); stderr output is unchanged.
	return slog.New(logtail.NewTextHandler(w, logtail.NewRing(logtail.DefaultCapacity), &slog.HandlerOptions{Level: level}))
}
