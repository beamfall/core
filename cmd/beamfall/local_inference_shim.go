package main

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
)

func localInferenceShim(_ context.Context, args []string) error {
	inputRoot, modelRoot, workRoot, argv, err := parseLocalInferenceShimArgs(args)
	if err != nil {
		return err
	}
	return runLocalInferenceShim(inputRoot, modelRoot, workRoot, argv)
}

func applyLocalInferenceSandbox(
	inputRoot, modelRoot, workRoot, binary string,
	applyNoNetwork func() error,
	applyConfinement func([]string, []string, string) error,
) error {
	if err := applyNoNetwork(); err != nil {
		return fmt.Errorf("local-inference-shim: applying no-network profile: %w", err)
	}
	if err := applyConfinement([]string{inputRoot, modelRoot}, []string{workRoot}, binary); err != nil {
		return fmt.Errorf("local-inference-shim: applying filesystem confinement: %w", err)
	}
	return nil
}

func parseLocalInferenceShimArgs(args []string) (inputRoot, modelRoot, workRoot string, argv []string, err error) {
	for i := 0; i < len(args); {
		switch args[i] {
		case "--input-ro":
			if i+1 >= len(args) {
				return "", "", "", nil, errors.New("local-inference-shim: --input-ro requires a path")
			}
			inputRoot = args[i+1]
			i += 2
		case "--model-ro":
			if i+1 >= len(args) {
				return "", "", "", nil, errors.New("local-inference-shim: --model-ro requires a path")
			}
			modelRoot = args[i+1]
			i += 2
		case "--work-rw":
			if i+1 >= len(args) {
				return "", "", "", nil, errors.New("local-inference-shim: --work-rw requires a path")
			}
			workRoot = args[i+1]
			i += 2
		case "--":
			argv = args[i+1:]
			i = len(args)
		default:
			return "", "", "", nil, fmt.Errorf("local-inference-shim: unexpected argument %q", args[i])
		}
	}
	if inputRoot == "" || modelRoot == "" || workRoot == "" {
		return "", "", "", nil, errors.New("local-inference-shim: input, model, and work roots are required")
	}
	if len(argv) == 0 {
		return "", "", "", nil, errors.New("local-inference-shim: missing command after --")
	}
	for _, p := range []string{inputRoot, modelRoot, workRoot, argv[0]} {
		if !filepath.IsAbs(p) {
			return "", "", "", nil, fmt.Errorf("local-inference-shim: path must be absolute: %q", p)
		}
	}
	return inputRoot, modelRoot, workRoot, argv, nil
}
