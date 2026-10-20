package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
)

func main() {
	var cfg Config
	var timingsPath string
	var measureOnly bool
	flag.StringVar(&cfg.Tier, "tier", "10k", "fixture tier: 10k, 100k, or 1m")
	flag.StringVar(&cfg.Profile, "profile", "perf-realistic", "fixture profile: perf-realistic or messy-correctness")
	flag.Uint64Var(&cfg.Seed, "seed", 171, "deterministic fixture seed")
	flag.StringVar(&cfg.Output, "output", "", "output corpus directory (required)")
	flag.StringVar(&timingsPath, "timings", "", "write raw timing JSON outside the corpus")
	flag.BoolVar(&measureOnly, "measure-only", false, "measure an existing matching corpus without regenerating it")
	flag.Parse()

	if err := cfg.Validate(); err != nil {
		fatal(err)
	}

	if !measureOnly {
		manifest, err := Generate(cfg)
		if err != nil {
			fatal(err)
		}
		printJSON(manifest)
	}
	if timingsPath != "" || measureOnly {
		if timingsPath == "" {
			fatal(fmt.Errorf("--timings is required with --measure-only"))
		}
		timings, err := Measure(cfg)
		if err != nil {
			fatal(err)
		}
		if err := writeJSON(timingsPath, timings); err != nil {
			fatal(err)
		}
		printJSON(timings)
	}
}

func printJSON(value any) {
	data, err := json.Marshal(value)
	if err != nil {
		fatal(err)
	}
	fmt.Println(string(data))
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "scale-fixture:", err)
	os.Exit(2)
}
