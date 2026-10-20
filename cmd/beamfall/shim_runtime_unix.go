//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris || zos

package main

import (
	"fmt"
	"os"

	"github.com/beamfall/core/internal/ffmpeg"
	"github.com/beamfall/core/internal/integrationhost"
)

func runFFmpegShim(roPaths, rwPaths, argv []string) error {
	if err := applyRlimits(ffmpeg.DefaultRlimits()); err != nil {
		return fmt.Errorf("ffmpeg-shim: applying rlimits: %w", err)
	}
	if err := applyConfinement(roPaths, rwPaths, argv[0]); err != nil {
		return fmt.Errorf("ffmpeg-shim: applying filesystem confinement: %w", err)
	}
	if err := replaceProcess(argv[0], argv, os.Environ()); err != nil {
		return fmt.Errorf("ffmpeg-shim: exec %q: %w", argv[0], err)
	}
	return nil
}

func runWorkerShim(argv []string) error {
	if err := applyWorkerRlimits(integrationhost.DefaultWorkerRlimits()); err != nil {
		return err
	}
	if err := replaceProcess(argv[0], argv, []string{}); err != nil {
		return fmt.Errorf("worker-shim: exec %q: %w", argv[0], err)
	}
	return nil
}

// runLocalInferenceShim applies the complete ADR-0127 §1 profile and only then
// replaces this process with the inference binary. Both halves of the profile
// fail closed: a host without seccomp or without landlock refuses the launch
// instead of exec'ing an unconfined child. The exec target is the policy's
// resolved binary, so the process image is the inode the ruleset granted.
func runLocalInferenceShim(inputRoot, modelRoot, workRoot string, argv []string) error {
	policy, err := integrationhost.BuildLocalInferencePolicy(inputRoot, modelRoot, workRoot, argv[0])
	if err != nil {
		return err
	}
	if err := applyLocalInferenceSandbox(
		policy.InputRoot, policy.ModelRoot, policy.WorkRoot, policy.Binary,
		applyLocalInferenceNoNetwork, applyLocalInferenceConfinement,
	); err != nil {
		return err
	}
	confined := append([]string{policy.Binary}, argv[1:]...)
	if err := replaceProcess(policy.Binary, confined, []string{}); err != nil {
		return fmt.Errorf("local-inference-shim: exec %q: %w", policy.Binary, err)
	}
	return nil
}
