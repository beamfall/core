// Package encderiv builds a derivative of Media in an Encrypted Library
// without its plaintext leaving the protected plane (ADR-0235, amending
// ADR-0045 §2.2).
//
// A job serves the decrypted source to the transcoder over a loopback-only,
// per-job HTTP endpoint guarded by a 256-bit bearer token, reads the
// transcoder's fragmented MP4 from a pipe, and encrypts it under an ephemeral
// per-job key into a staging file the moment it arrives. The staging file is
// owner-only and created exclusively; it is removed and the key zeroed on
// success, failure and cancellation alike. A segment index is built from the
// fragments as they stream, and Result.Open returns the final layout (the
// stream with the index placed before its first fragment) for the caller's
// single re-encryption into its own at-rest format.
//
// The endpoint and the job stop the moment authorization ends: the caller's
// revocation check is polled throughout, and a revoked or expired token refuses
// new requests and aborts responses mid-body. A source that fails to decrypt or
// ends early fails the job even when the transcoder itself exits cleanly.
package encderiv
