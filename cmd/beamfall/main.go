package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, nil)))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	// Restore the default signal disposition as soon as the first INT/TERM has
	// been delivered, so a second one terminates the process immediately
	// instead of being swallowed while the orderly shutdown is still running.
	// Without this an operator's only escape from a stalled shutdown is KILL.
	go func() {
		<-ctx.Done()
		stop()
	}()
	if err := run(ctx, os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	return runWithOutput(ctx, args, os.Stdout)
}

func runWithOutput(ctx context.Context, args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return serve(ctx, nil)
	}

	switch args[0] {
	case "-h", "--help", "help":
		return helpCommand(stdout)
	case "--version", "version":
		return versionCommand(stdout)
	case "apply-config":
		return applyConfig(ctx, args[1:], os.Stdin, stdout)
	case "backup":
		return backup(ctx, args[1:], stdout)
	case "capabilities":
		return capabilitiesCommand(args[1:], stdout)
	case "export-config":
		return exportConfig(ctx, args[1:], stdout)
	case "ffmpeg-shim":
		// Internal FFmpeg hardening re-exec target; not an
		// operator command — dispatched by internal/ffmpeg, omitted from help.
		return ffmpegShim(ctx, args[1:])
	case "worker-shim":
		// Internal integration worker hardening re-exec target;
		// not an operator command — dispatched by internal/integrationhost, omitted from help.
		return workerShim(ctx, args[1:])
	case "local-inference-shim":
		// Internal local inference hardening re-exec target;
		// not an operator command — dispatched by internal/integrationhost, omitted from help.
		return localInferenceShim(ctx, args[1:])
	case "release":
		return releaseCommand(ctx, args[1:], stdout)
	case "reset-admin-password":
		return resetAdminPassword(ctx, args[1:], stdout)
	case "restore":
		return restore(ctx, args[1:], stdout)
	case "scan":
		return scan(ctx, args[1:], stdout)
	case "serve":
		return serve(ctx, args[1:])
	case "token":
		return tokenCommand(ctx, args[1:], stdout)
	default:
		return fmt.Errorf("unknown command %q\n\n%s", args[0], helpText)
	}
}

const helpText = `Usage: beamfall [command]

Commands:
  serve                  run the Beamfall Core server (default)
  capabilities [--version]
                         print runtime capabilities, or build metadata with --version
  apply-config           apply a console config document from stdin
  export-config          export a console config document
  backup                 request a server-side backup
  release                release helper commands
  reset-admin-password   set the local admin password while the server is stopped
  restore                restore a backup while the server is stopped
  scan                   queue a library scan
  token                  manage local API tokens
  version                print build metadata
`

func helpCommand(stdout io.Writer) error {
	_, err := fmt.Fprint(stdout, helpText)
	return err
}

func commandFlagSet(name string, stdout io.Writer) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	if stdout == nil {
		stdout = io.Discard
	}
	fs.SetOutput(stdout)
	return fs
}

func parseCommandFlags(fs *flag.FlagSet, args []string) (bool, error) {
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return true, nil
		}
		return false, err
	}
	return false, nil
}

func helpArg(args []string) bool {
	return len(args) == 1 && (args[0] == "-h" || args[0] == "--help" || args[0] == "help")
}
