package main

import (
	"context"
	"errors"
	"flag"
	"io"
	"log/slog"
	"os"

	"github.com/beamfall/core/internal/app"
	"github.com/beamfall/core/internal/config"
	"github.com/beamfall/core/internal/ffmpeg"
	"github.com/beamfall/core/internal/integrationhost"
)

func serve(ctx context.Context, args []string) error {
	cfg, err := config.LoadWithOutput(args, nil, os.Stdout)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	slog.SetDefault(newOperatorLogger(os.Stderr, cfg.LogLevel))
	// Enable FFmpeg invocation hardening: point the spawn seam at
	// this server binary so every ffmpeg/ffprobe run re-execs through the
	// ffmpeg-shim subcommand (rlimits + Linux landlock confinement). Only the
	// real serve entrypoint sets this; test harnesses that call app.New leave it
	// empty so a test binary never re-execs itself.
	cfg.FFmpegHardeningShimPath = ffmpeg.DefaultShimPath()
	cfg.IntegrationWorkerHardeningShimPath = integrationhost.DefaultShimPath()
	beamfall, err := app.New(ctx, cfg, slog.Default(), firstPartyBundles(), firstPartyHooks())
	if err != nil {
		return err
	}
	return beamfall.Run(ctx)
}

func newOperatorLogger(w io.Writer, level slog.Level) *slog.Logger {
	return slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: level}))
}
