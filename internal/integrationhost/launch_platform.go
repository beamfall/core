package integrationhost

import "errors"

// ErrWorkerShimRequired prevents a worker from launching without the guardian
// and rlimit seam that owns complete-tree containment.
var ErrWorkerShimRequired = errors.New("integrationhost: worker hardening shim is required")

// ErrWorkerPlatformUnsupported rejects Integration Worker launch where the
// current fd-based worker transport is not implemented.
var ErrWorkerPlatformUnsupported = errors.New("integrationhost: worker launch is unsupported on this platform")

// ErrWorkerSandboxRequired means the host has no supported least-privilege OS
// boundary. Native workers are refused rather than launched with raw host
// filesystem or network access.
var ErrWorkerSandboxRequired = errors.New("integrationhost: worker OS sandbox is required")
