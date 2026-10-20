//go:build !unix

package main

func runWorkerShimGuardian() error { return errUnsupportedShimHardening }

func runWorkerShimWithGuardian(argv []string, _ int) error { return runWorkerShim(argv) }
