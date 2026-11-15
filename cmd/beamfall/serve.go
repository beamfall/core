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
	slog.SetDefault(newOperatorLogger(os.Stderr, cfg.LogLevel))
	// Enable FFmpeg invocation hardening: point the spawn seam at
	// this server binary so every ffmpeg/ffprobe run re-execs through the
	// ffmpeg-shim subcommand (rlimits + Linux landlock confinement). Only the
	// real serve entrypoint sets this; test harnesses that call app.New leave it
	// empty so a test binary never re-execs itself.
	cfg.FFmpegHardeningShimPath = ffmpeg.DefaultShimPath()
	cfg.IntegrationWorkerHardeningShimPath = integrationhost.DefaultShimPath()
	beamfall, release, err := startServer(ctx, cfg)
	if err != nil {
		return err
	}
	defer release()
	return beamfall.Run(ctx)
}

// startServer takes the data directory's server lock, which offline commands
// probe before writing, and builds the server. The caller runs the server and
// then calls release.
func startServer(ctx context.Context, cfg config.Config) (*app.App, func(), error) {
	if err := applyStagedRestore(ctx, cfg.DataDir, slog.Default()); err != nil {
		return nil, nil, err
	}
	release, err := lockServeDataDir(cfg.DataDir)
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

func newOperatorLogger(w io.Writer, level slog.Level) *slog.Logger {
	// The handler also keeps the most recent lines, redacted, for the console
	// server-log read (CORE-1716); stderr output is unchanged.
	return slog.New(logtail.NewTextHandler(w, logtail.NewRing(logtail.DefaultCapacity), &slog.HandlerOptions{Level: level}))
}
