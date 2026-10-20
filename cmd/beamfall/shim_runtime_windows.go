//go:build windows

package main

import "errors"

var errUnsupportedShimHardening = errors.New("shim hardening unsupported on windows")

// Windows has no process-image replacement or equivalent confinement profile.
// Refuse every artifact before inspecting or launching it.
func runFFmpegShim(_, _, _ []string) error { return errUnsupportedShimHardening }

func runWorkerShim(_ []string) error { return errUnsupportedShimHardening }

func runLocalInferenceShim(_, _, _ string, _ []string) error {
	return errUnsupportedShimHardening
}
