package main

import (
	"context"
	"fmt"
	"io"
)

func scan(ctx context.Context, args []string, stdout io.Writer) error {
	var opts cliHTTPOptions
	var libraryID string
	var root string
	var enqueue bool
	var runOne bool
	var backfill bool
	fs := commandFlagSet("beamfall scan", stdout)
	addHTTPFlags(fs, &opts)
	fs.StringVar(&libraryID, "library-id", "", "Library identifier")
	fs.StringVar(&root, "root", "", "Library root to scan")
	fs.BoolVar(&enqueue, "enqueue", false, "enqueue scan as a durable background job")
	fs.BoolVar(&runOne, "run-one", false, "run one queued scan job")
	fs.BoolVar(&backfill, "backfill", false, "re-run music hydration for audio lacking the hydration marker")
	if helped, err := parseCommandFlags(fs, args); err != nil || helped {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}
	if runOne && (libraryID != "" || root != "" || enqueue) {
		return fmt.Errorf("--run-one cannot be combined with scan request flags")
	}
	if !runOne && libraryID == "" {
		return fmt.Errorf("--library-id is required")
	}
	if !runOne && root == "" {
		return fmt.Errorf("--root is required")
	}

	if runOne {
		var resp struct {
			RunJob cliJobInfo `json:"runJob"`
		}
		if err := cliGraphQL(ctx, opts,
			"mutation RunJob { runJob { id status } }",
			map[string]any{},
			&resp,
		); err != nil {
			return err
		}
		if stdout != nil {
			_, _ = fmt.Fprintf(stdout, "job_id=%s job_status=%s\n", resp.RunJob.ID, resp.RunJob.Status)
		}
		return nil
	}
	if enqueue {
		// Historical flag retained for compatibility. Scan requests now always
		// enqueue through the running server to preserve the single-writer rule.
	}
	var resp struct {
		ScanLibrary cliJobInfo `json:"scanLibrary"`
	}
	if err := cliGraphQL(ctx, opts,
		"mutation ScanLibrary($libraryID: String!, $root: String!, $backfill: Boolean) { scanLibrary(libraryID: $libraryID, root: $root, backfill: $backfill) { id status } }",
		map[string]any{"libraryID": libraryID, "root": root, "backfill": backfill},
		&resp,
	); err != nil {
		return err
	}
	if stdout != nil {
		_, _ = fmt.Fprintf(stdout, "job_id=%s job_status=%s\n", resp.ScanLibrary.ID, resp.ScanLibrary.Status)
	}
	return nil
}

type cliJobInfo struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}
