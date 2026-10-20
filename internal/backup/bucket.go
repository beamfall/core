// Package backup is the zero-knowledge off-site backup core primitive (ADR-0115
// §1/§2/§4/§5): client-side-encrypt any Library's bytes into ciphertext before
// they leave the box, replicate to an operator-owned S3-compatible target, and
// restore from it. The unwrap secret is a BackupKey — a Recovery-Key-shaped
// credential minted locally and never escrowed (ADR-0045/0031 lineage).
//
// AGPL firewall: this package is domain-agnostic and
// vendor-neutral. No Beamfall account, entitlement, billing, or destination code
// lives here. The BucketClient interface is the only S3 dependency; concrete
// implementations (aws-sdk-go, Backblaze, Cloudflare R2, MinIO, …) are wired in
// by the composition root in the proprietary clients/cloud.
package backup

import (
	"context"
	"io"
)

// BucketClient is the vendor-neutral S3-compatible storage seam. Any operator
// bucket — Backblaze B2, Cloudflare R2, AWS S3/Glacier, MinIO, or a local
// implementation for testing — satisfies this interface.
//
// The backup package stores ONLY opaque ciphertext through this seam; no plaintext
// ever crosses it (ADR-0115 §1: "Beamfall is blind by construction").
type BucketClient interface {
	// Put streams r to the bucket under key. key is an opaque string (no
	// assumptions about path separators or encoding). Overwrites any existing
	// object at the same key; replication semantics are the bucket's responsibility.
	Put(ctx context.Context, key string, r io.Reader) error

	// Get fetches the object at key. The caller must close the returned
	// ReadCloser when done. A missing key is an error (the caller should treat it
	// as ErrNotFound via errors.Is; concrete implementations may wrap a sentinel).
	Get(ctx context.Context, key string) (io.ReadCloser, error)

	// Delete removes the object at key. A missing key is not an error.
	Delete(ctx context.Context, key string) error

	// List returns all object keys whose names start with prefix. An empty
	// prefix lists all objects. The list is unsorted.
	List(ctx context.Context, prefix string) ([]string, error)
}
