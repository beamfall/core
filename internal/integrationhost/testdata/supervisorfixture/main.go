package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/beamfall/core/internal/integrationhost"
)

type noGrants struct{}

func (noGrants) Grants(context.Context, string) ([]integrationhost.Grant, error) {
	return nil, nil
}

func main() {
	worker := flag.String("worker", "", "worker artifact path")
	shim := flag.String("shim", "", "beamfall worker-shim path")
	scratch := flag.String("scratch", "", "test-only worker scratch root")
	pidFile := flag.String("pid-file", "", "test-only worker pid file")
	resistant := flag.Bool("term-resistant", false, "test-only resistant descendant")
	flag.Parse()
	if *worker == "" || *shim == "" {
		fmt.Fprintln(os.Stderr, "worker and shim are required")
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	host := integrationhost.NewHost("tree-harness", integrationhost.Deps{}, noGrants{}, nil)
	digest, err := integrationhost.HashFile(*worker)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	supervisor := integrationhost.NewSupervisor(
		"tree-harness",
		integrationhost.Artifact{
			PluginID: "tree-harness", Path: *worker, VerifiedSHA256: digest,
			Args:          []string{"--tree-mode", "hang", "--pid-file", *pidFile, "--hold-file", *pidFile + ".release", fmt.Sprintf("--term-resistant=%t", *resistant)},
			WritablePaths: []string{*scratch},
		},
		host,
		nil,
		nil,
	)
	supervisor.SetHardeningShim(*shim)
	if err := supervisor.Run(ctx); err != nil && ctx.Err() == nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
